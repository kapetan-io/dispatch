//go:build drift

package launcher_test

// Package exception: drift test that runs a launch inside `dispatch run` against the real herdr and treehouse.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
	"github.com/kapetan-io/dispatch/internal/journal"
	"github.com/kapetan-io/dispatch/internal/lineartest"
	"github.com/kapetan-io/dispatch/internal/loop"
	"github.com/kapetan-io/dispatch/internal/pullrequesttest"
	"github.com/kapetan-io/dispatch/internal/treehouse"
)

// shippedRules is the [rules] table the shipped rules read.
const shippedRules = "[rules]\nworker_model = \"sonnet\"\ndesign_model = \"opus\"\nlaunch_failure_limit = 2\nnudge_limit = 2\nblocked_renotify_minutes = 15\n"

// TestDispatchRunLaunchesAWorkerAgainstRealHerdrAndTreehouse moves a local ticket In Progress
// under a running `dispatch run` with the shipped rules, which launches its worker in a
// throwaway herdr session and a temp treehouse pool, with the stand-in Claude in place of
// Claude. A missing herdr or treehouse fails the test.
// Run with: go test -tags drift ./internal/launcher/
func TestDispatchRunLaunchesAWorkerAgainstRealHerdrAndTreehouse(t *testing.T) {
	t.Cleanup(func() {
		left, err := herdrtest.Leftovers(os.Getpid())
		require.NoError(t, err)
		assert.Empty(t, left)
	})
	_, err := exec.LookPath("treehouse")
	require.NoError(t, err)

	// herdr reports paths with symlinks resolved, and macOS temp dirs are links.
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	repo := driftRepo(t, root)
	bin := builtDispatch(t)
	plugin, err := filepath.Abs("../..")
	require.NoError(t, err)
	// The launch finds claude on dispatch's own PATH, so the stand-in goes first there.
	t.Setenv("PATH", bin+string(os.PathListSeparator)+herdrtest.StandInClaude(t)+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TREEHOUSE_ROOT", filepath.Join(root, "pool"))
	session := herdrtest.StartSession(t)

	trace := filepath.Join(root, "trace.log")
	opts := startDriftRun(t, root, session.CLI().WithKinds("claude").WithTrace(trace).WithDispatchBin(filepath.Join(bin, "dispatch")),
		fmt.Sprintf("[repos.drift]\npath = %q\n\n[plugins.herdr]\nplugin_root = %q\n\n[plugins.local]\n\n%s", repo, plugin, shippedRules))

	worker := launchLocalTicket(t, opts, repo)

	assert.Equal(t, "/dispatch:build LOC-1", worker.kickoff)
	snap, err := session.CLI().Read(context.Background())
	require.NoError(t, err)
	var shown bool
	for _, w := range snap.Workspaces {
		if w.ID == worker.workspace {
			shown = true
			assert.Equal(t, worker.tree, w.CheckoutPath)
		}
	}
	assert.True(t, shown)
	require.Len(t, snap.Agents, 1)
	assert.Equal(t, worker.workspace, snap.Agents[0].WorkspaceID)
	assert.Equal(t, "working", snap.Agents[0].Status)

	// The worker began with the plugin root the config names, and Claude's flags.
	raw, err := os.ReadFile(filepath.Join(worker.tree, "standin-claude.json"))
	require.NoError(t, err)
	var started struct {
		Args        []string `json:"args"`
		PluginRoot  string   `json:"plugin_root"`
		Autonomous  string   `json:"autonomous_worker"`
		DispatchBin string   `json:"dispatch_bin"`
	}
	require.NoError(t, json.Unmarshal(raw, &started))
	assert.Equal(t, plugin, started.PluginRoot)
	assert.Equal(t, "1", started.Autonomous)
	assert.Equal(t, filepath.Join(bin, "dispatch"), started.DispatchBin)
	assert.Contains(t, started.Args, "--plugin-dir")
	assert.Contains(t, started.Args, plugin)

	traced, err := os.ReadFile(trace)
	require.NoError(t, err)
	assert.Contains(t, string(traced), "typing")
}

// TestDispatchRunLaunchesAWorkerInHerdrCoreUnderTheAppHost is the same launch with
// host = "app", over a scratch herdr-core, so the workers are terminals the Herdr app shows.
func TestDispatchRunLaunchesAWorkerInHerdrCoreUnderTheAppHost(t *testing.T) {
	t.Cleanup(func() {
		left, err := herdrtest.CoreLeftovers(os.Getpid())
		require.NoError(t, err)
		assert.Empty(t, left)
	})
	_, err := exec.LookPath("treehouse")
	require.NoError(t, err)

	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	repo := driftRepo(t, root)
	bin := builtDispatch(t)
	plugin, err := filepath.Abs("../..")
	require.NoError(t, err)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+herdrtest.StandInClaude(t)+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TREEHOUSE_ROOT", filepath.Join(root, "pool"))
	core := herdrtest.StartCoreSession(t)

	// The scratch core's adapter runs /usr/bin/true as the app, so a failed launch that closes its
	// workspace never reaches the maintainer's app window.
	opts := startDriftRun(t, root, core.Core().WithKinds("claude").WithDispatchBin(filepath.Join(bin, "dispatch")),
		fmt.Sprintf("[repos.drift]\npath = %q\n\n[plugins.herdr]\nplugin_root = %q\nhost = \"app\"\n\n[plugins.local]\n\n%s", repo, plugin, shippedRules))

	worker := launchLocalTicket(t, opts, repo)

	snap, err := core.Core().WithKinds("claude").Read(context.Background())
	require.NoError(t, err)
	require.Len(t, snap.Workspaces, 1)
	assert.Equal(t, worker.tree, snap.Workspaces[0].CheckoutPath)
	assert.Equal(t, worker.workspace, snap.Workspaces[0].ID)
	require.Len(t, snap.Agents, 1)
	assert.Equal(t, worker.workspace, snap.Agents[0].WorkspaceID)
	assert.Equal(t, "working", snap.Agents[0].Status)
	raw, err := os.ReadFile(filepath.Join(worker.tree, "standin-claude.json"))
	require.NoError(t, err)
	var started struct {
		Args       []string `json:"args"`
		TerminalID string   `json:"hc_terminal_id"`
	}
	require.NoError(t, json.Unmarshal(raw, &started))
	assert.Equal(t, snap.Agents[0].PaneID, started.TerminalID)
	assert.Contains(t, started.Args, "--session-id")
	assert.Equal(t, snap.Agents[0].Session, started.Args[slices.Index(started.Args, "--session-id")+1])
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	require.NoError(t, exec.Command("git", append([]string{"-C", dir}, args...)...).Run())
}

// driftRepo makes the clone under root that the [repos.drift] table names, with a pool of one tree.
func driftRepo(t *testing.T, root string) string {
	t.Helper()
	repo := filepath.Join(root, "repo")
	require.NoError(t, os.Mkdir(repo, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "treehouse.toml"), []byte("max_trees = 1\n"), 0o644))
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "-c", "user.name=drift", "-c", "user.email=drift@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	return repo
}

// builtDispatch builds the real dispatch and returns its directory. The worker's claude shim runs it
// as DISPATCH_BIN, because the dispatch that launches the worker here is the test binary.
func builtDispatch(t *testing.T) string {
	t.Helper()
	bin, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, exec.Command("go", "build", "-o", filepath.Join(bin, "dispatch"), "github.com/kapetan-io/dispatch/cmd/dispatch").Run())
	return bin
}

// startDriftRun starts `dispatch run` in this process over a scratch state directory and a config
// holding settings, the shipped rules and a history under the state directory. Linear and GitHub
// are fakes, so a launch reaches neither, and its ticket is a local one. It returns the options
// every other dispatch command of the test runs with.
func startDriftRun(t *testing.T, root string, host herdr.Multiplexer, settings string) dispatch.Options {
	t.Helper()
	// macOS caps a socket path at about 100 bytes, which the test's temp dir can pass.
	state, err := os.MkdirTemp("/tmp", "dsp")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(state) })
	shipped, err := filepath.Abs("../../rules")
	require.NoError(t, err)
	rules := filepath.Join(root, "rules")
	require.NoError(t, os.CopyFS(rules, os.DirFS(shipped)))
	config := filepath.Join(root, "config.toml")
	require.NoError(t, os.WriteFile(config, []byte(fmt.Sprintf("[dispatch]\nrules = %q\n\n[history]\ndir = %q\n\n%s", rules, filepath.Join(state, "events"), settings)), 0o644))

	opts := dispatch.Options{ConfigPath: config, StateDir: state, Multiplexer: host}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		run := opts
		run.LinearKey, run.Linear, run.PullRequests = lineartest.Key, lineartest.New(nil).Connect, pullrequesttest.New(&journal.Journal{})
		run.Stdout, run.Stderr = os.Stderr, os.Stderr
		done <- dispatch.Run(ctx, []string{"run"}, run)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	require.Eventually(t, func() bool {
		resp, err := loop.Ask(ctx, state, loop.Request{Op: loop.OpIdle})
		return err == nil && !resp.Busy
	}, 30*time.Second, 50*time.Millisecond)
	return opts
}

// dispatchOut runs one dispatch command against the running `dispatch run` and returns what it printed.
func dispatchOut(t *testing.T, opts dispatch.Options, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	opts.Stdout, opts.Stderr = &out, os.Stderr
	require.NoError(t, dispatch.Run(context.Background(), args, opts))
	return out.String()
}

// launchedWorker is what a launch inside `dispatch run` left behind.
type launchedWorker struct {
	// workspace is the one the launch receipt names.
	workspace string
	// tree and kickoff are the worker row's.
	tree, kickoff string
}

// launchLocalTicket files LOC-1 for the drift repo through the running dispatch and moves it In
// Progress, so the config's launch rule queues a launch at the tick. It returns once that launch
// ended with a worker running, on a tree treehouse shows leased to the ticket.
func launchLocalTicket(t *testing.T, opts dispatch.Options, repo string) launchedWorker {
	t.Helper()
	require.Equal(t, "LOC-1\n", dispatchOut(t, opts, "ticket", "create", "local", "--repo", "drift", "--title", "Drift launch"))
	dispatchOut(t, opts, "ticket", "move", "LOC-1", "In Progress")
	dispatchOut(t, opts, "tick")

	var worker launchedWorker
	for _, text := range strings.Split(dispatchOut(t, opts, "explain", "LOC-1", "--json"), "\n") {
		var line struct{ Type, Kind, State, Detail string }
		if json.Unmarshal([]byte(text), &line) != nil || line.Type != "receipt" || line.Kind != "launch" || line.State == "attempted" {
			continue
		}
		require.Equal(t, "done", line.State)
		found, ok := strings.CutPrefix(line.Detail, "ok: workspace ")
		require.True(t, ok)
		worker.workspace, _, _ = strings.Cut(found, ",")
	}
	require.NotEmpty(t, worker.workspace)

	// The rows are read through the running dispatch, the only process that may open dispatch.db.
	resp, err := loop.Ask(context.Background(), opts.StateDir, loop.Request{Op: loop.OpTicket, Call: &loop.TicketCall{Verb: loop.VerbTracked}})
	require.NoError(t, err)
	rows := resp.Rows.Of("LOC-1")
	require.Len(t, rows, 1)
	require.NotNil(t, rows[0].Row.Kickoff)
	worker.tree, worker.kickoff = rows[0].Row.Tree, rows[0].Row.Kickoff.Text

	trees, err := treehouse.NewCLI("treehouse").WithEnv(environment.Process()).Trees(context.Background(), repo)
	require.NoError(t, err)
	require.Len(t, trees, 1)
	assert.Equal(t, worker.tree, trees[0].Path)
	assert.Equal(t, "LOC-1", trees[0].LeaseHolder)
	return worker
}
