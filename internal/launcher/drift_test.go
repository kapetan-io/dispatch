//go:build drift

package launcher_test

// Package exception: drift test that runs the shipped launcher against the real herdr and treehouse.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdrtest"
)

// TestShippedWrapperLaunchesAWorkerAgainstRealHerdrAndTreehouse runs
// bin/dispatch-launch, the way the plugin ships it, to launch a worker in a
// throwaway herdr session and a temp treehouse pool, with the stand-in Claude
// in place of Claude. A missing herdr or treehouse fails the test.
// Run with: go test -tags drift ./internal/launcher/
func TestShippedWrapperLaunchesAWorkerAgainstRealHerdrAndTreehouse(t *testing.T) {
	t.Cleanup(func() {
		left, err := herdrtest.Leftovers(os.Getpid())
		require.NoError(t, err)
		assert.Empty(t, left)
	})
	session := herdrtest.StartSession(t)
	_, err := exec.LookPath("treehouse")
	require.NoError(t, err)

	// herdr reports paths with symlinks resolved, and macOS temp dirs are links.
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	repo := filepath.Join(root, "repo")
	require.NoError(t, os.Mkdir(repo, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "treehouse.toml"), []byte("max_trees = 1\n"), 0o644))
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "-c", "user.name=drift", "-c", "user.email=drift@example.com", "commit", "-q", "--allow-empty", "-m", "init")

	// Symlinks resolved, because the worker's DISPATCH_BIN comes from os.Executable and the test compares it to this path.
	bin, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	build := exec.Command("go", "build", "-o", filepath.Join(bin, "dispatch"), "github.com/kapetan-io/dispatch/cmd/dispatch")
	out, err := build.CombinedOutput()
	require.NoError(t, err, string(out))
	plugin, err := filepath.Abs("../..")
	require.NoError(t, err)

	trace := filepath.Join(root, "trace.log")
	cmd := exec.Command(filepath.Join(plugin, "bin", "dispatch-launch"), "ENG-1", "drift/eng-1", "--kickoff", "/dispatch:design ENG-1")
	cmd.Dir = repo
	cmd.Env = append(session.Env(t),
		// The config and state defaults land under HOME, so the launch never writes the maintainer's.
		"HOME="+filepath.Join(root, "home"),
		"PATH="+bin+string(os.PathListSeparator)+herdrtest.StandInClaude(t)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"TREEHOUSE_ROOT="+filepath.Join(root, "pool"),
		"DISPATCH_PLUGIN_ROOT=",
		"DISPATCH_LAUNCH_TRACE="+trace,
	)
	stdout, err := cmd.Output()
	require.NoError(t, err, string(stdout))

	var result struct {
		Status    string `json:"status"`
		Worktree  string `json:"worktree"`
		Workspace string `json:"workspace"`
		Pane      string `json:"pane"`
		Agent     string `json:"agent"`
		Kickoff   string `json:"kickoff"`
	}
	require.NoError(t, json.Unmarshal(stdout, &result))
	assert.Equal(t, "ok", result.Status)
	assert.Equal(t, "eng-1", result.Agent)
	assert.Equal(t, "/dispatch:design ENG-1", result.Kickoff)

	snap, err := session.CLI().Read(context.Background())
	require.NoError(t, err)
	var shown bool
	for _, w := range snap.Workspaces {
		if w.ID == result.Workspace {
			shown = true
			assert.Equal(t, result.Worktree, w.CheckoutPath)
		}
	}
	assert.True(t, shown)
	require.Len(t, snap.Agents, 1)
	assert.Equal(t, result.Pane, snap.Agents[0].PaneID)
	assert.Equal(t, "working", snap.Agents[0].Status)

	// The worker began with the plugin root the wrapper names, and Claude's
	// flags.
	raw, err := os.ReadFile(filepath.Join(result.Worktree, "standin-claude.json"))
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

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	require.NoError(t, err, string(out))
}
