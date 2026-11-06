//go:build drift

package herdr_test

// Package exception: this package wraps the rex tool, so every test here is an adapter test of it.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/harness"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
)

const rexSessionID = "6f0c1e2a-0000-4000-8000-0000000000a1"

// rexRig is the Rex adapter over a scratch server, with the stand-in Claude on the workers' PATH.
type rexRig struct {
	stateDir string
	server   *herdrtest.RexServer
	rex      *herdr.Rex
	worker   herdr.Worker
	repo     string
}

// newRexRig starts a scratch server whose environment is serverEnv over the clean process
// environment, with the stand-in Claude first on PATH.
func newRexRig(t *testing.T, serverEnv ...string) *rexRig {
	t.Helper()
	env := herdrtest.CleanEnv().With("PATH", herdrtest.StandInClaude(t)+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, kv := range serverEnv {
		name, value, _ := strings.Cut(kv, "=")
		env = env.With(name, value)
	}
	server := herdrtest.StartRexServer(t, env)
	plugin, err := filepath.Abs("../..")
	require.NoError(t, err)
	repo, tree := herdrtest.NewShortCheckout(t)
	stateDir := t.TempDir()
	rex := server.Rex(stateDir).WithKinds("claude").WithDispatchBin(dispatchBinary(t)).WithPace(100 * time.Millisecond)
	space, err := rex.OpenWorkspace(context.Background(), herdr.NewWorkspace{Repo: repo, Path: tree})
	require.NoError(t, err)
	program := harness.ClaudeCode{}.Program(harness.Start{Model: "sonnet", PluginRoot: plugin, Session: rexSessionID})
	return &rexRig{stateDir: stateDir, server: server, rex: rex, repo: repo, worker: herdr.Worker{
		Workspace: space, Dir: tree, Label: "ENG-1 title", Name: "eng-1", PluginRoot: plugin, Program: program,
	}}
}

func (r *rexRig) agent(t *testing.T) (herdr.Agent, bool) {
	t.Helper()
	snap, err := r.rex.Read(context.Background())
	require.NoError(t, err)
	if len(snap.Agents) == 0 {
		return herdr.Agent{}, false
	}
	return snap.Agents[0], true
}

func (r *rexRig) awaitStatus(t *testing.T, want string) {
	t.Helper()
	require.Eventually(t, func() bool {
		a, ok := r.agent(t)
		return ok && a.Status == want
	}, 20*time.Second, 100*time.Millisecond)
}

// launched starts the worker and gives it its kickoff, as a launch does.
func (r *rexRig) launched(t *testing.T) herdr.Started {
	t.Helper()
	started, err := r.rex.StartWorker(context.Background(), r.worker)
	require.NoError(t, err)
	require.NoError(t, r.rex.Kickoff(context.Background(), started.Pane, "/dispatch:build ENG-1", r.worker.Program.Startup))
	return started
}

func TestRexStartsAWorkerAnswersItsDialogsAndTypesTheKickoff(t *testing.T) {
	r := newRexRig(t)

	started := r.launched(t)

	snap, err := r.rex.Read(context.Background())
	require.NoError(t, err)
	require.Len(t, snap.Workspaces, 1)
	assert.Equal(t, r.worker.Workspace, snap.Workspaces[0].ID)
	assert.Equal(t, r.worker.Dir, snap.Workspaces[0].CheckoutPath)
	require.Len(t, snap.Tabs, 1)
	assert.Equal(t, "ENG-1 title", snap.Tabs[0].Label)
	require.Len(t, snap.Agents, 1)
	assert.Equal(t, herdr.Agent{Kind: "claude", Status: "working", PaneID: started.Pane, TabID: snap.Tabs[0].ID, WorkspaceID: r.worker.Workspace, Session: rexSessionID}, snap.Agents[0])
	var recorded struct {
		Args []string `json:"args"`
	}
	b, err := os.ReadFile(filepath.Join(r.worker.Dir, "standin-claude.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &recorded))
	assert.Contains(t, recorded.Args, "--session-id")
	assert.Contains(t, recorded.Args, rexSessionID)
}

func TestRexRefusesASecondWorkerOnTheTreeAndClosesNothing(t *testing.T) {
	r := newRexRig(t)
	r.launched(t)

	_, err := r.rex.StartWorker(context.Background(), r.worker)

	require.ErrorIs(t, err, herdr.ErrSessionRunning)
	snap, err := r.rex.Read(context.Background())
	require.NoError(t, err)
	assert.Len(t, snap.Panes, 1)
	r.awaitStatus(t, "working")
}

func TestRexRefusesASecondWorkerOnATreeReachedThroughASymlink(t *testing.T) {
	r := newRexRig(t)
	r.launched(t)
	link := filepath.Join(filepath.Dir(r.worker.Dir), "link")
	require.NoError(t, os.Symlink(r.worker.Dir, link))
	second := r.worker
	second.Dir = link

	_, err := r.rex.StartWorker(context.Background(), second)

	require.ErrorIs(t, err, herdr.ErrSessionRunning)
	snap, err := r.rex.Read(context.Background())
	require.NoError(t, err)
	assert.Len(t, snap.Panes, 1)
}

func TestRexLeavesOneWorkerWhenTwoStartsRace(t *testing.T) {
	r := newRexRig(t)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A second adapter shares the state directory, as a hand-run dispatch launch does.
			_, errs[i] = r.server.Rex(r.stateDir).WithKinds("claude").WithDispatchBin(dispatchBinary(t)).StartWorker(context.Background(), r.worker)
		}()
	}
	wg.Wait()

	snap, err := r.rex.Read(context.Background())
	require.NoError(t, err)
	assert.Len(t, snap.Panes, 1)
	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
			continue
		}
		assert.ErrorIs(t, err, herdr.ErrSessionRunning)
	}
	assert.Equal(t, 1, succeeded)
}

func TestRexTypesOnlyIntoAWorkerThatReportsAStatusItCanDescribe(t *testing.T) {
	r := newRexRig(t)
	started, err := r.rex.StartWorker(context.Background(), r.worker)
	require.NoError(t, err)
	// Claude at its trust dialog reports no status, and a Prompt there would press Enter on "No, exit".
	require.Eventually(t, func() bool { a, ok := r.agent(t); return ok && a.Status == "unknown" }, 20*time.Second, 100*time.Millisecond)

	err = r.rex.Prompt(context.Background(), started.Pane, "hello")

	require.ErrorIs(t, err, herdr.ErrStatusUnknown)
	assert.True(t, herdr.NothingTyped(err))
	require.NoError(t, r.rex.Kickoff(context.Background(), started.Pane, "/dispatch:build ENG-1", r.worker.Program.Startup))

	require.NoError(t, r.rex.Prompt(context.Background(), started.Pane, "!blocked"))
	r.awaitStatus(t, "blocked")
	err = r.rex.Prompt(context.Background(), started.Pane, "hello")
	require.ErrorIs(t, err, herdr.ErrAgentBlocked)
	assert.True(t, herdr.NothingTyped(err))
}

func TestRexPromptSubmitsToAnIdleAndADoneWorker(t *testing.T) {
	r := newRexRig(t)
	started := r.launched(t)

	require.NoError(t, r.rex.Prompt(context.Background(), started.Pane, "!idle"))
	r.awaitStatus(t, "idle")
	require.NoError(t, r.rex.Prompt(context.Background(), started.Pane, "!done"))
	r.awaitStatus(t, "done")
	require.NoError(t, r.rex.Prompt(context.Background(), started.Pane, "hello"))
	r.awaitStatus(t, "working")
}

func TestRexRenamesTheWorkspaceEmojiAndTheTabTitleApart(t *testing.T) {
	r := newRexRig(t)
	r.launched(t)
	snap, err := r.rex.Read(context.Background())
	require.NoError(t, err)
	require.Len(t, snap.Tabs, 1)

	require.NoError(t, r.rex.RenameWorkspace(context.Background(), r.worker.Workspace, "🟢 ENG-1"))
	require.NoError(t, r.rex.RenameTab(context.Background(), snap.Tabs[0].ID, "ENG-1 new title"))

	snap, err = r.rex.Read(context.Background())
	require.NoError(t, err)
	require.Len(t, snap.Workspaces, 1)
	assert.Equal(t, "🟢 ENG-1", snap.Workspaces[0].Label)
	assert.Equal(t, "ENG-1 new title", snap.Tabs[0].Label)

	require.NoError(t, r.rex.RenameWorkspace(context.Background(), r.worker.Workspace, "🔴 ENG-1"))

	snap, err = r.rex.Read(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "🔴 ENG-1", snap.Workspaces[0].Label)
	assert.Equal(t, "ENG-1 new title", snap.Tabs[0].Label)
}

func TestRexCloseWorkspaceEndsTheWorkerKeepsTheRepoSessionAndTheUsersOwnTab(t *testing.T) {
	r := newRexRig(t)
	r.launched(t)
	sessions, err := r.server.Output("api", "call", "session.list")
	require.NoError(t, err)
	var listed struct {
		Sessions []struct {
			ID    string `json:"session_id"`
			Label string `json:"label"`
		} `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal([]byte(sessions), &listed))
	require.Len(t, listed.Sessions, 1)
	assert.Equal(t, filepath.Base(r.repo), listed.Sessions[0].Label)
	_, err = r.server.Output("api", "call", "-s", listed.Sessions[0].ID, "session.new_window", `{"window_label":"mine","layout":{"block":{"flavor":"com.superlogical.terminal.shell","label":"mine","options":{"shell":"none","command":["sleep","300"]}}}}`)
	require.NoError(t, err)

	require.NoError(t, r.rex.CloseWorkspace(context.Background(), r.worker.Workspace))

	snap, err := r.rex.Read(context.Background())
	require.NoError(t, err)
	assert.Empty(t, snap.Workspaces)
	assert.Empty(t, snap.Agents)
	view, err := r.server.Output("api", "call", "-s", listed.Sessions[0].ID, "session.view")
	require.NoError(t, err)
	assert.Contains(t, view, `"label":"mine"`)
	require.NoError(t, r.rex.CloseWorkspace(context.Background(), r.worker.Workspace))
}

func TestRexLeavesTheRepoSessionAfterTheLastWorkerCloses(t *testing.T) {
	r := newRexRig(t)
	r.launched(t)

	require.NoError(t, r.rex.CloseWorkspace(context.Background(), r.worker.Workspace))

	out, err := r.server.Output("api", "call", "session.list")
	require.NoError(t, err)
	assert.Contains(t, out, `"label":"`+filepath.Base(r.repo)+`"`)
}

func TestRexResumesTheWorkersConversationAfterAServerRestart(t *testing.T) {
	r := newRexRig(t)
	first := r.launched(t)

	r.server.Restart(t)

	snap, err := r.rex.Read(context.Background())
	require.NoError(t, err)
	require.Len(t, snap.Workspaces, 1)
	assert.Equal(t, r.worker.Dir, snap.Workspaces[0].CheckoutPath)
	assert.Empty(t, snap.Agents)
	resumed := r.worker
	resumed.Program = harness.ClaudeCode{}.Program(harness.Start{Model: "sonnet", PluginRoot: r.worker.PluginRoot, Session: "99999999-0000-4000-8000-000000000001"})
	second, err := r.rex.StartWorker(context.Background(), resumed)
	require.NoError(t, err)
	assert.NotEqual(t, first.Pane, second.Pane)
	snap, err = r.rex.Read(context.Background())
	require.NoError(t, err)
	assert.Len(t, snap.Panes, 1)
	assert.Equal(t, second.Pane, snap.Panes[0].ID)
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(filepath.Join(r.worker.Dir, "standin-claude.json"))
		if err != nil {
			return false
		}
		var recorded struct {
			Args []string `json:"args"`
		}
		return json.Unmarshal(b, &recorded) == nil && strings.Contains(strings.Join(recorded.Args, " "), "--resume "+rexSessionID)
	}, 20*time.Second, 100*time.Millisecond)
	agent, ok := r.agent(t)
	require.True(t, ok)
	assert.Equal(t, rexSessionID, agent.Session)
}

func TestRexStartsAFreshConversationAfterAServerRestartWhenTheLeaseIsFresh(t *testing.T) {
	r := newRexRig(t)
	r.launched(t)
	r.server.Restart(t)
	fresh := r.worker
	fresh.FreshLease = true
	fresh.Program = harness.ClaudeCode{}.Program(harness.Start{Model: "sonnet", PluginRoot: r.worker.PluginRoot, Session: "99999999-0000-4000-8000-000000000001"})

	_, err := r.rex.StartWorker(context.Background(), fresh)

	require.NoError(t, err)
	require.Eventually(t, func() bool {
		agent, ok := r.agent(t)
		return ok && agent.Session == "99999999-0000-4000-8000-000000000001"
	}, 20*time.Second, 100*time.Millisecond)
}

func TestRexReportsAStoppedServerAsNotRunningAndStartsNone(t *testing.T) {
	r := newRexRig(t)
	r.server.Stop(t)

	_, err := r.rex.Read(context.Background())

	require.ErrorIs(t, err, herdr.ErrRexNotRunning)
	_, statErr := os.Stat(r.server.Socket)
	assert.True(t, os.IsNotExist(statErr))
}

func TestRexRefusesATreeTooLongForTheWorkerLabel(t *testing.T) {
	r := newRexRig(t)
	long := r.worker
	long.Dir = "/" + strings.Repeat("a", 100)

	_, err := r.rex.StartWorker(context.Background(), long)

	require.ErrorContains(t, err, "at most 73")
}

func TestRexDeletesTheRepoSessionAfterTheLastWorkerClosesWhenAsked(t *testing.T) {
	r := newRexRig(t)
	r.rex.WithDeleteEmptySessions(true)
	r.launched(t)

	require.NoError(t, r.rex.CloseWorkspace(context.Background(), r.worker.Workspace))

	out, err := r.server.Output("api", "call", "session.list")
	require.NoError(t, err)
	assert.NotContains(t, out, `"label":"`+filepath.Base(r.repo)+`"`)
	require.NoError(t, r.rex.CloseWorkspace(context.Background(), r.worker.Workspace))
}

func TestRexStreamReportsAStoppedServerAsNotRunningAndStartsNone(t *testing.T) {
	r := newRexRig(t)
	r.server.Stop(t)

	err := r.rex.Changes(context.Background(), func(string) {})

	require.ErrorIs(t, err, herdr.ErrRexNotRunning)
	_, statErr := os.Stat(r.server.Socket)
	assert.True(t, os.IsNotExist(statErr))
}

func TestRexStreamNamesTheWorkerPaneWhenItsWorkspaceCloses(t *testing.T) {
	r := newRexRig(t)
	started := r.launched(t)
	var mu sync.Mutex
	var named []string
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = r.rex.Changes(ctx, func(pane string) {
			mu.Lock()
			defer mu.Unlock()
			named = append(named, pane)
		})
	}()
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(named) }
	require.Eventually(t, func() bool { return count() > 0 }, 20*time.Second, 50*time.Millisecond)
	live := count()

	require.NoError(t, r.rex.CloseWorkspace(context.Background(), r.worker.Workspace))

	require.Eventually(t, func() bool { return count() > live }, 20*time.Second, 50*time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, started.Pane, named[len(named)-1])
}
