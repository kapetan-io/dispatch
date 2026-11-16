//go:build drift

package launcher_test

// Package exception: drift test that runs dispatch against a real Claude, which no fake stands in for.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/harness"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
	"github.com/kapetan-io/dispatch/internal/journal"
	"github.com/kapetan-io/dispatch/internal/lineartest"
	"github.com/kapetan-io/dispatch/internal/loop"
	"github.com/kapetan-io/dispatch/internal/pullrequesttest"
	"github.com/kapetan-io/dispatch/internal/sessionlog"
	"github.com/kapetan-io/dispatch/internal/world"
)

// TestRealClaudeTakesDispatchsKickoffAndSlashNudgesAsCommandsSentByDispatch has a running
// `dispatch run` launch a worker on a real Claude in a throwaway herdr session, tells it a
// single-line and a multi-line slash nudge, and reads the session log the running dispatch
// wrote from the plugin's UserPromptSubmit hook. The fakes hand the hook the same string dispatch recorded, so only a
// real Claude shows that the text the hook receives matches it. A missing herdr, treehouse, claude or
// login fails the test. It spends a few short Claude turns.
// Run with: go test -tags drift ./internal/launcher/
func TestRealClaudeTakesDispatchsKickoffAndSlashNudgesAsCommandsSentByDispatch(t *testing.T) {
	realClaudeTakesDispatchsCommands(t, "tui")
}

// TestRealClaudeUnderTheAppHostTakesDispatchsKickoffAndSlashNudgesAsCommandsSentByDispatch is the
// same run with host = "app", over a scratch herdr-core. It also checks that the Worker label dispatch put
// on the terminal is the session id in Claude's transcript, and that the worker's Stop reached its
// Stop record, which only happens when the hook found the terminal through HC_TERMINAL_ID.
func TestRealClaudeUnderTheAppHostTakesDispatchsKickoffAndSlashNudgesAsCommandsSentByDispatch(t *testing.T) {
	realClaudeTakesDispatchsCommands(t, "app")
}

// TestRealClaudeUnderRexTakesDispatchsKickoffAndSlashNudgesAsCommandsSentByDispatch is the same
// run with [plugins.rex], over a scratch Rex server. It also checks that the session id in the
// worker's label is the one in Claude's transcript, and that the worker's Stop reached its Stop
// record, which only happens when the hook found the block through REX_SESSION and REX_BLOCK.
func TestRealClaudeUnderRexTakesDispatchsKickoffAndSlashNudgesAsCommandsSentByDispatch(t *testing.T) {
	realClaudeTakesDispatchsCommands(t, "rex")
}

func realClaudeTakesDispatchsCommands(t *testing.T, host string) {
	app, rex := host == "app", host == "rex"
	t.Cleanup(func() {
		left, err := herdrtest.Leftovers(os.Getpid())
		require.NoError(t, err)
		assert.Empty(t, left)
		if app {
			leftCores, err := herdrtest.CoreLeftovers(os.Getpid())
			require.NoError(t, err)
			assert.Empty(t, leftCores)
		}
	})
	_, err := exec.LookPath("treehouse")
	require.NoError(t, err)
	_, err = exec.LookPath("claude")
	require.NoError(t, err)

	// herdr reports paths with symlinks resolved, and macOS temp dirs are links.
	base := t.TempDir()
	if rex {
		// The pool lives under root, and Rex's worker label holds a tree of at most 73 characters.
		base, err = os.MkdirTemp("/tmp", "rl")
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.RemoveAll(base) })
	}
	root, err := filepath.EvalSymlinks(base)
	require.NoError(t, err)
	repo := filepath.Join(root, "repo")
	require.NoError(t, os.Mkdir(repo, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "treehouse.toml"), []byte("max_trees = 1\n"), 0o644))
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "-c", "user.name=drift", "-c", "user.email=drift@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	plugin, err := filepath.Abs("../..")
	require.NoError(t, err)
	// A skill that answers in a word keeps the three turns short, so this rule stands in for the
	// shipped ones, whose kickoff would start a build.
	const kickoff = "/dispatch:principles"
	rules := filepath.Join(root, "rules")
	require.NoError(t, os.Mkdir(rules, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(rules, "dispatch.ts"), []byte(`import { whenever } from "dispatch";
whenever({ name: "launch", each: "ticket",
  if: (t, ctx) => t.status === "in_progress" && ctx.worker.of(t).state === "none",
  then: (t, ctx) => ctx.worker.queueLaunch(t, { kickoff: "`+kickoff+`", once: ["launch", t.id] }) });
`), 0o644))
	// macOS caps a socket path at about 100 bytes, which the test's temp dir can pass.
	state, err := os.MkdirTemp("/tmp", "dsp")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(state) })
	config := filepath.Join(root, "config.toml")
	settings := fmt.Sprintf("[dispatch]\nrules = %q\n\n[repos.drift]\npath = %q\n\n[history]\ndir = %q\n\n[plugins.local]\n", rules, repo, filepath.Join(state, "events"))
	switch {
	case app:
		settings += fmt.Sprintf("\n[plugins.herdr]\nplugin_root = %q\nhost = \"app\"\n", plugin)
	case rex:
		settings += fmt.Sprintf("\n[plugins.rex]\nplugin_root = %q\n", plugin)
	default:
		settings += fmt.Sprintf("\n[plugins.herdr]\nplugin_root = %q\n", plugin)
	}
	require.NoError(t, os.WriteFile(config, []byte(settings), 0o644))

	// The plugin's hooks run `dispatch` from the PATH the worker's shell has, which the herdr
	// server hands down from this process, so the build that reads the temp state goes first.
	// The worker's claude shim runs it too, as the DISPATCH_BIN each adapter below is given,
	// because the dispatch that launches the worker here is the test binary.
	bin := t.TempDir()
	out, err := exec.Command("go", "build", "-o", filepath.Join(bin, "dispatch"), "github.com/kapetan-io/dispatch/internal/herdrtest/driftdispatch").CombinedOutput()
	t.Log(string(out))
	require.NoError(t, err)
	// The launch under the app host closes the workspace when a launch fails, and that must
	// not reach the maintainer's app window.
	require.NoError(t, os.Symlink("/usr/bin/true", filepath.Join(bin, "herdr-app")))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DISPATCH_DRIFT_CONFIG", config)
	t.Setenv("DISPATCH_DRIFT_STATE", state)
	// The loop and the launch share this pool.
	t.Setenv("TREEHOUSE_ROOT", filepath.Join(root, "pool"))
	var cli herdr.Multiplexer
	var shimEnv string
	var core *herdrtest.CoreSession
	var screen func(pane string) string
	if app {
		// The scratch core's XDG data, state and cache directories would reach Claude and make its
		// installer re-point ~/.local/bin/claude into them, so driftdispatch's claude shim restores
		// the DISPATCH_DRIFT_REAL_* values. The runtime and config directories stay scratch, because
		// the hooks' hc finds its core through them.
		shimEnv = filepath.Join(root, "shim.env")
		t.Setenv("DISPATCH_DRIFT_SHIM_TRACE", shimEnv)
		t.Setenv("DISPATCH_DRIFT_RESTORE_XDG", "1")
		for _, name := range []string{"XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
			if value, ok := os.LookupEnv(name); ok {
				t.Setenv("DISPATCH_DRIFT_REAL_"+name, value)
			}
		}
		// A herdr TUI pane that started this test would let the Stop hook find the worker through
		// HERDR_PANE_ID and pass without HC_TERMINAL_ID.
		t.Setenv("HERDR_PANE_ID", "")
		t.Setenv("HERDR_SOCKET_PATH", "")
		core = herdrtest.StartCoreSession(t)
		cli = core.Core().WithKinds("claude").WithDispatchBin(filepath.Join(bin, "dispatch"))
		screen = func(pane string) string { return core.Run(t, "read", pane) }
	} else if rex {
		// The hooks' dispatch and the real claude come from this server's PATH, and a worker's blocks inherit it.
		server := herdrtest.StartRexServer(t, herdrtest.CleanEnv())
		t.Setenv("DISPATCH_DRIFT_REX_SOCKET", server.Socket)
		cli = server.Rex(state).WithKinds("claude").WithDispatchBin(filepath.Join(bin, "dispatch"))
		screen = func(pane string) string {
			session, block, _ := strings.Cut(pane, "/")
			args, _ := json.Marshal(map[string]any{"session_id": session, "block_id": block, "method": "format", "args": map[string]any{"format": "text", "trim": true}})
			out, _ := server.Output("api", "call", "-s", session, "block.method", string(args))
			return out
		}
	} else {
		session := herdrtest.StartSession(t)
		cli = session.CLI().WithDispatchBin(filepath.Join(bin, "dispatch"))
		screen = func(pane string) string {
			out, _ := session.Output(context.Background(), "pane", "read", pane)
			return string(out)
		}
	}

	calls := &journal.Journal{}
	// The hooks report to the running dispatch that launches the worker. On the herdr TUI a real
	// Claude in a fresh tree takes about 24 of the default 25 seconds to reach the kickoff, 15 of
	// them the adapter's wait for an idle agent, so the launch gets 2 minutes.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- dispatch.Run(ctx, []string{"run"}, dispatch.Options{
			ConfigPath: config, StateDir: state, Multiplexer: cli, LinearKey: lineartest.Key, LaunchTimeout: 2 * time.Minute,
			Linear: lineartest.New(nil).Connect, PullRequests: pullrequesttest.New(calls), Stdout: os.Stderr, Stderr: os.Stderr,
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
	opts := dispatch.Options{ConfigPath: config, StateDir: state, Multiplexer: cli}
	launched := launchLocalTicket(t, opts, repo)
	require.Equal(t, kickoff, launched.kickoff)

	worker := func() world.Worker {
		snap, err := cli.Read(context.Background())
		require.NoError(t, err)
		for _, a := range snap.Agents {
			if a.WorkspaceID == launched.workspace {
				return world.Worker{PaneID: a.PaneID, TreePath: launched.tree, Session: a.Session}
			}
		}
		require.Fail(t, "herdr shows no agent in the worker's workspace")
		return world.Worker{}
	}
	// The id herdr learns from Claude is the one dispatch chose and passed on the command line.
	if !app && !rex {
		require.Eventually(t, func() bool {
			id := worker().Session
			out, err := exec.Command("ps", "-axo", "args").Output()
			return err == nil && id != "" && strings.Contains(string(out), "--session-id "+id)
		}, 30*time.Second, 200*time.Millisecond)
	}
	settled := func(wantTurns int) {
		t.Helper()
		t.Cleanup(func() {
			if !t.Failed() {
				return
			}
			_ = filepath.WalkDir(state, func(path string, e os.DirEntry, err error) error {
				if err == nil && !e.IsDir() {
					b, _ := os.ReadFile(path)
					t.Logf("%s: %s", path, b)
				}
				return nil
			})
			t.Logf("agent %+v\nscreen:\n%s", worker(), screen(worker().PaneID))
		})
		require.Eventually(t, func() bool {
			snap, err := cli.Read(context.Background())
			require.NoError(t, err)
			log, err := sessionlog.Read(state, launched.tree, worker().Session)
			if err != nil || len(log.Turns) < wantTurns {
				return false
			}
			for _, a := range snap.Agents {
				if a.WorkspaceID == launched.workspace {
					return a.Status == "idle" || a.Status == "done"
				}
			}
			return false
		}, 3*time.Minute, time.Second)
	}
	settled(1)

	// Deliver finds the worker in Claude's session list by the id herdr reports, on every host.
	listed, err := filepath.Glob(filepath.Join(harness.SessionsDir(environment.Process()), "*.json"))
	require.NoError(t, err)
	var ids []string
	for _, file := range listed {
		raw, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var entry struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(raw, &entry) == nil {
			ids = append(ids, entry.SessionID)
		}
	}
	assert.Contains(t, ids, worker().Session)

	// `dispatch tell` is how production sends a nudge, so a real Claude's hook reports the raw text.
	dispatchOut(t, opts, "tell", "LOC-1", "/dispatch:principles")
	settled(2)
	dispatchOut(t, opts, "tell", "LOC-1", "/dispatch:principles\nand then reply with the word done")
	settled(3)

	log, err := sessionlog.Read(state, launched.tree, worker().Session)
	require.NoError(t, err)
	require.Len(t, log.Commands, 3)
	for _, c := range log.Commands {
		assert.Equal(t, "dispatch:principles", c.Name)
		assert.Equal(t, sessionlog.ByDispatch, c.By)
	}
	assert.Equal(t, "and then reply with the word done", log.Commands[2].Args)

	if rex {
		id := worker().Session
		require.NotEmpty(t, id)
		assert.Equal(t, []string{id}, transcriptSessions(t, launched.tree))
		assert.Eventually(t, func() bool { return endedATurn(opts) }, 10*time.Second, 100*time.Millisecond)
	}
	if app {
		// ps shows no --session-id for a core terminal, so the Worker label is compared with the
		// session id in the transcript Claude wrote for the worker's tree.
		id := worker().Session
		require.NotEmpty(t, id)
		assert.Equal(t, []string{id}, transcriptSessions(t, launched.tree))
		// settled already needed the hook's Stop report to reach a turn, so a hook that missed the
		// terminal through HC_TERMINAL_ID times out there. This reads the Stop record the same report wrote.
		assert.Eventually(t, func() bool { return endedATurn(opts) }, 10*time.Second, 100*time.Millisecond)
		// The hooks run in the shim's environment, so they reach the scratch core and not the maintainer's.
		raw, err := os.ReadFile(shimEnv)
		require.NoError(t, err)
		assert.Contains(t, string(raw), "HC_SOCKET="+core.Socket+"\n")
		assert.Contains(t, string(raw), "HC_SESSION="+core.Env.Get("HC_SESSION")+"\n")
	}

	// The run's history belongs in the temp state, not the folder a real restart resumes from.
	segments, err := os.ReadDir(filepath.Join(state, "events"))
	require.NoError(t, err)
	assert.NotEmpty(t, segments)
}

// endedATurn reports whether `dispatch turn` prints a turn of LOC-1's worker, which it does only
// from the Stop record and closing message of the worker's session.
func endedATurn(opts dispatch.Options) bool {
	var out bytes.Buffer
	opts.Stdout, opts.Stderr = &out, io.Discard
	return dispatch.Run(context.Background(), []string{"turn", "LOC-1"}, opts) == nil && strings.HasPrefix(out.String(), "LOC-1 turn ")
}

// transcriptSessions returns the sessionId recorded in each of Claude's transcripts for tree,
// asserting each matches its file name.
func transcriptSessions(t *testing.T, tree string) []string {
	t.Helper()
	home := os.Getenv("CLAUDE_CONFIG_DIR")
	if home == "" {
		user, err := os.UserHomeDir()
		require.NoError(t, err)
		home = filepath.Join(user, ".claude")
	}
	// Claude names a tree's transcript directory after its path with every non-alphanumeric character as "-".
	dir := filepath.Join(home, "projects", regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(tree, "-"))
	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	require.NoError(t, err)
	var ids []string
	for _, file := range files {
		raw, err := os.ReadFile(file)
		require.NoError(t, err)
		for _, line := range strings.Split(string(raw), "\n") {
			var entry struct {
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal([]byte(line), &entry) == nil && entry.SessionID != "" {
				assert.Equal(t, strings.TrimSuffix(filepath.Base(file), ".jsonl"), entry.SessionID)
				ids = append(ids, entry.SessionID)
				break
			}
		}
	}
	return ids
}
