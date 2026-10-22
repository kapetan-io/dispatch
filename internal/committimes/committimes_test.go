package committimes_test

// No exception needed: these tests run committimes' own command line through Run, which is that tool's surface.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kapetan-io/dispatch/internal/committimes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func commit(t *testing.T, dir, when, message string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", message}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_DATE="+when, "GIT_COMMITTER_DATE="+when,
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
}

func TestCommitTimes(t *testing.T) {
	dir := t.TempDir()
	out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput()
	require.NoError(t, err, string(out))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "old.txt"), []byte("1"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "new file.txt"), []byte("1"), 0o644))
	commit(t, dir, "2024-01-02T03:04:05Z", "first")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "new file.txt"), []byte("2"), 0o644))
	commit(t, dir, "2025-06-07T08:09:10Z", "second")

	var stdout, stderr bytes.Buffer
	code := committimes.Run(t.Context(), []string{"-dir", dir}, committimes.Options{Stdout: &stdout, Stderr: &stderr})
	require.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), "set the time of 2 files")

	old, err := os.Stat(filepath.Join(dir, "old.txt"))
	require.NoError(t, err)
	assert.True(t, old.ModTime().Equal(time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)))
	changed, err := os.Stat(filepath.Join(dir, "sub", "new file.txt"))
	require.NoError(t, err)
	assert.True(t, changed.ModTime().Equal(time.Date(2025, 6, 7, 8, 9, 10, 0, time.UTC)))
}

func TestCommitTimesOutsideRepository(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := committimes.Run(t.Context(), []string{"-dir", t.TempDir()}, committimes.Options{Stdout: &stdout, Stderr: &stderr})
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "committimes: git ls-files")
}
