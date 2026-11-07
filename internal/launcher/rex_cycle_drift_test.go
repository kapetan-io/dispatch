//go:build drift

package launcher_test

// Package exception: drift test that runs dispatch's loop against the installed Rex, which no fake stands in for.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
	"github.com/kapetan-io/dispatch/internal/journal"
	"github.com/kapetan-io/dispatch/internal/lineartest"
	"github.com/kapetan-io/dispatch/internal/loop"
	"github.com/kapetan-io/dispatch/internal/pullrequesttest"
	"github.com/kapetan-io/dispatch/internal/ticket"
)

// rexCycle is a dispatch loop whose host is Rex, over a scratch Rex server, the stand-in Claude,
// a real treehouse pool and a Linear fake holding one In Progress ticket.
type rexCycle struct {
	server *herdrtest.RexServer
	rex    *herdr.Rex
	state  string
	config string
	root   string
	ctx    context.Context
}

// startRexCycle starts the loop, with the scratch server already stopped when stopped is set.
func startRexCycle(t *testing.T, stopped bool) *rexCycle {
	t.Helper()
	base, err := os.MkdirTemp("/tmp", "rc")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	root, err := filepath.EvalSymlinks(base)
	require.NoError(t, err)
	repo := filepath.Join(root, "repo")
	require.NoError(t, os.Mkdir(repo, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "treehouse.toml"), []byte("max_trees = 2\n"), 0o644))
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "-c", "user.name=drift", "-c", "user.email=drift@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	state, err := os.MkdirTemp("/tmp", "dsp")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(state) })
	// The claude shim in a real plugin root runs the dispatch named by DISPATCH_BIN, which here is this test binary.
	// This plugin root holds no shim, so the stand-in Claude starts directly.
	plugin := filepath.Join(root, "plugin")
	require.NoError(t, os.Mkdir(plugin, 0o755))
	shipped, err := filepath.Abs("../../rules")
	require.NoError(t, err)
	rules := filepath.Join(root, "rules")
	require.NoError(t, os.CopyFS(rules, os.DirFS(shipped)))
	config := filepath.Join(root, "config.toml")
	body := fmt.Sprintf("[repos.drift]\npath = %q\n\n[history]\ndir = %q\n\n[dispatch]\nrules = %q\n\n[plugins.rex]\nplugin_root = %q\n\n"+
		"[rules]\nworker_model = \"sonnet\"\ndesign_model = \"opus\"\nlaunch_failure_limit = 2\nnudge_limit = 2\nblocked_renotify_minutes = 15\n",
		repo, filepath.Join(state, "events"), rules, plugin)
	require.NoError(t, os.WriteFile(config, []byte(body), 0o644))
	t.Setenv("TREEHOUSE_ROOT", filepath.Join(root, "pool"))

	// The launch finds claude on dispatch's own PATH, so the stand-in goes first there as well as on the server's.
	path := herdrtest.StandInClaude(t) + string(os.PathListSeparator) + os.Getenv("PATH")
	t.Setenv("PATH", path)
	server := herdrtest.StartRexServer(t, herdrtest.CleanEnv().With("PATH", path))
	if stopped {
		server.Stop(t)
	}

	linear := lineartest.New(&journal.Journal{})
	linear.Seed(lineartest.Issue{Assignee: linear.Viewer().ID, Ticket: ticket.Ticket{
		ID: "uuid-ENG-1", Identifier: "ENG-1", Title: "Add the thing", Branch: "drift/eng-1", Status: "In Progress", Team: "ENG",
		Labels: []ticket.Label{{Name: "ai"}, {Name: "drift", Group: "repo"}},
	}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- dispatch.Run(ctx, []string{"run"}, dispatch.Options{
			ConfigPath: config, StateDir: state, RexSocket: server.Socket, LinearKey: lineartest.Key,
			Linear: linear.Connect, PullRequests: pullrequesttest.New(&journal.Journal{}), Stdout: os.Stderr, Stderr: os.Stderr,
		})
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	require.Eventually(t, func() bool {
		resp, err := loop.Ask(ctx, state, loop.Request{Op: loop.OpIdle})
		return err == nil && !resp.Busy
	}, 30*time.Second, 50*time.Millisecond)
	return &rexCycle{server: server, rex: server.Rex(state).WithKinds("claude"), state: state, config: config, root: root, ctx: ctx}
}

func (c *rexCycle) tick(t *testing.T) loop.Response {
	t.Helper()
	resp, err := loop.Ask(c.ctx, c.state, loop.Request{Op: loop.OpTick})
	require.NoError(t, err)
	return resp
}

// await runs cycles until the Rex snapshot satisfies ok.
func (c *rexCycle) await(t *testing.T, ok func(herdr.Snapshot) bool) herdr.Snapshot {
	t.Helper()
	var snap herdr.Snapshot
	require.Eventually(t, func() bool {
		c.tick(t)
		var err error
		snap, err = c.rex.Read(context.Background())
		require.NoError(t, err)
		return ok(snap)
	}, 60*time.Second, 500*time.Millisecond)
	return snap
}

func TestRexRunLaunchesAnInProgressTicketIntoATabAndFollowsItsState(t *testing.T) {
	c := startRexCycle(t, false)

	// The cycle launches the ticket's worker into the repo session, with the stand-in Claude in the
	// tree, and gives its window the workspace emoji, the ticket and the title.
	snap := c.await(t, func(snap herdr.Snapshot) bool {
		return len(snap.Agents) == 1 && snap.Agents[0].Status == "working" && len(snap.Workspaces) == 1 && snap.Workspaces[0].Label == "🟢 ENG-1"
	})

	require.Len(t, snap.Tabs, 1)
	assert.Equal(t, "ENG-1 Add the thing", snap.Tabs[0].Label)
	assert.Equal(t, "claude", snap.Agents[0].Kind)
	sessions, err := c.server.Output("api", "call", "session.list")
	require.NoError(t, err)
	assert.Contains(t, sessions, `"label":"repo"`)
	recorded, err := os.ReadFile(filepath.Join(snap.Workspaces[0].CheckoutPath, "standin-claude.json"))
	require.NoError(t, err)
	assert.Contains(t, string(recorded), "--session-id")

	// A worker that blocks on a question changes the window's emoji and leaves its title.
	c.server.Type(t, snap.Agents[0].PaneID, "!blocked")
	snap = c.await(t, func(snap herdr.Snapshot) bool {
		return len(snap.Workspaces) == 1 && snap.Workspaces[0].Label == "🔴 ENG-1"
	})

	require.Len(t, snap.Tabs, 1)
	assert.Equal(t, "ENG-1 Add the thing", snap.Tabs[0].Label)
	assert.Len(t, snap.Agents, 1)
}

func TestRexRunReportsAStoppedRexAndLaunchesNothing(t *testing.T) {
	c := startRexCycle(t, true)
	c.tick(t)

	var out bytes.Buffer
	err := dispatch.Run(c.ctx, []string{"explain", "ENG-1"}, dispatch.Options{ConfigPath: c.config, StateDir: c.state, RexSocket: c.server.Socket, Stdout: &out})

	require.NoError(t, err)
	assert.Contains(t, out.String(), "launch-worker")
	assert.Contains(t, out.String(), "herdr unread: Rex is not running")
	_, statErr := os.Stat(c.server.Socket)
	assert.True(t, os.IsNotExist(statErr))
	leased, err := filepath.Glob(filepath.Join(c.root, "pool", ".treehouse", "*", "[0-9]*"))
	require.NoError(t, err)
	assert.Empty(t, leased)
}
