//go:build drift

package launcher_test

// Package exception: drift test that runs dispatch against a real Claude, which no fake stands in for.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
	"github.com/kapetan-io/dispatch/internal/journal"
	"github.com/kapetan-io/dispatch/internal/lineartest"
	"github.com/kapetan-io/dispatch/internal/loop"
	"github.com/kapetan-io/dispatch/internal/notifytest"
	"github.com/kapetan-io/dispatch/internal/pullrequesttest"
	"github.com/kapetan-io/dispatch/internal/sessionlog"
	"github.com/kapetan-io/dispatch/internal/world"
)

// TestRealClaudeTakesDispatchsKickoffAndSlashNudgesAsCommandsSentByDispatch launches a worker
// on a real Claude in a throwaway herdr session, types a single-line and a multi-line slash
// nudge into it, and reads the session log the running dispatch wrote from the plugin's
// UserPromptSubmit hook. The fakes hand the hook the same string dispatch recorded, so only a
// real Claude shows that the text the hook receives matches it. A missing herdr, treehouse, claude or
// login fails the test. It spends a few short Claude turns.
// Run with: go test -tags drift ./internal/launcher/
func TestRealClaudeTakesDispatchsKickoffAndSlashNudgesAsCommandsSentByDispatch(t *testing.T) {
	t.Cleanup(func() {
		left, err := herdrtest.Leftovers(os.Getpid())
		require.NoError(t, err)
		assert.Empty(t, left)
	})
	_, err := exec.LookPath("treehouse")
	require.NoError(t, err)
	_, err = exec.LookPath("claude")
	require.NoError(t, err)

	// herdr reports paths with symlinks resolved, and macOS temp dirs are links.
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	repo := filepath.Join(root, "repo")
	require.NoError(t, os.Mkdir(repo, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "treehouse.toml"), []byte("max_trees = 1\n"), 0o644))
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "-c", "user.name=drift", "-c", "user.email=drift@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	// macOS caps a socket path at about 100 bytes, which the test's temp dir can pass.
	state, err := os.MkdirTemp("/tmp", "dsp")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(state) })
	config := filepath.Join(root, "config.toml")
	require.NoError(t, os.WriteFile(config, []byte(fmt.Sprintf("[repos.drift]\npath = %q\n\n[history]\ndir = %q\n", repo, filepath.Join(state, "events"))), 0o644))

	// The plugin's hooks run `dispatch` from the PATH the worker's shell has, which the herdr
	// server hands down from this process, so the build that reads the temp state goes first.
	bin := t.TempDir()
	out, err := exec.Command("go", "build", "-o", filepath.Join(bin, "dispatch"), "github.com/kapetan-io/dispatch/internal/herdrtest/driftdispatch").CombinedOutput()
	t.Log(string(out))
	require.NoError(t, err)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DISPATCH_DRIFT_CONFIG", config)
	t.Setenv("DISPATCH_DRIFT_STATE", state)
	// The loop and the launch share this pool.
	t.Setenv("TREEHOUSE_ROOT", filepath.Join(root, "pool"))
	session := herdrtest.StartSession(t)
	cli := session.CLI()

	calls := &journal.Journal{}
	// The hooks and the launch report to a running dispatch, so one runs over the same state.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- dispatch.Run(ctx, []string{"run"}, dispatch.Options{
			ConfigPath: config, StateDir: state, Herdr: cli, LinearKey: lineartest.Key,
			Linear: lineartest.New(nil).Connect, PullRequests: pullrequesttest.New(calls), Alerts: notifytest.New(calls), Stdout: os.Stderr, Stderr: os.Stderr,
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
	plugin, err := filepath.Abs("../..")
	require.NoError(t, err)

	// A skill that answers in a word keeps the three turns short.
	const kickoff = "/dispatch:principles"
	cmd := exec.Command(filepath.Join(plugin, "bin", "dispatch-launch"), "ENG-1", "drift/eng-1", "--kickoff", kickoff)
	cmd.Dir = repo
	cmd.Env = append(session.Env(t), "TREEHOUSE_ROOT="+filepath.Join(root, "pool"))
	stdout, err := cmd.Output()
	t.Log(string(stdout))
	require.NoError(t, err)
	var launched struct {
		Status   string `json:"status"`
		Worktree string `json:"worktree"`
		Pane     string `json:"pane"`
	}
	require.NoError(t, json.Unmarshal(stdout, &launched))
	require.Equal(t, "ok", launched.Status)

	worker := func() world.Worker {
		snap, err := cli.Read(context.Background())
		require.NoError(t, err)
		for _, a := range snap.Agents {
			if a.PaneID == launched.Pane {
				return world.Worker{PaneID: a.PaneID, TreePath: launched.Worktree, Session: a.Session}
			}
		}
		require.Fail(t, "herdr shows no agent in the worker's pane")
		return world.Worker{}
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
			screen, _ := session.Output(context.Background(), "pane", "read", launched.Pane)
			t.Logf("agent %+v\nscreen:\n%s", worker(), screen)
		})
		require.Eventually(t, func() bool {
			snap, err := cli.Read(context.Background())
			require.NoError(t, err)
			log, err := sessionlog.Read(state, launched.Worktree, worker().Session)
			if err != nil || len(log.Turns) < wantTurns {
				return false
			}
			for _, a := range snap.Agents {
				if a.PaneID == launched.Pane {
					return a.Status == "idle" || a.Status == "done"
				}
			}
			return false
		}, 3*time.Minute, time.Second)
	}
	settled(1)

	nudge := func(text string) {
		loop.Typing(state).Note(launched.Worktree, text)
		require.NoError(t, cli.Prompt(context.Background(), launched.Pane, text))
	}
	nudge("/dispatch:principles")
	settled(2)
	nudge("/dispatch:principles\nand then reply with the word done")
	settled(3)

	log, err := sessionlog.Read(state, launched.Worktree, worker().Session)
	require.NoError(t, err)
	require.Len(t, log.Commands, 3)
	for _, c := range log.Commands {
		assert.Equal(t, "dispatch:principles", c.Name)
		assert.Equal(t, sessionlog.ByDispatch, c.By)
	}
	assert.Equal(t, "and then reply with the word done", log.Commands[2].Args)

	// The run's history belongs in the temp state, not the folder a real restart resumes from.
	segments, err := os.ReadDir(filepath.Join(state, "events"))
	require.NoError(t, err)
	assert.NotEmpty(t, segments)
}
