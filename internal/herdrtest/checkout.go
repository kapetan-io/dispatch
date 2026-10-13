package herdrtest

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// NewCheckout makes a git repo with one commit and a linked worktree of it, and
// returns both paths. herdr opens a workspace on a worktree of a repo.
func NewCheckout(t testing.TB) (repo, tree string) {
	t.Helper()
	root := t.TempDir()
	repo, tree = filepath.Join(root, "repo"), filepath.Join(root, "tree")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", repo},
		{"-C", repo, "-c", "user.name=drift", "-c", "user.email=drift@example.com", "commit", "-q", "--allow-empty", "-m", "init"},
		{"-C", repo, "worktree", "add", "-q", "--detach", tree},
	} {
		out, err := exec.Command("git", args...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	// herdr reports paths with symlinks resolved, and macOS temp dirs are links.
	resolved, err := filepath.EvalSymlinks(tree)
	require.NoError(t, err)
	repo, err = filepath.EvalSymlinks(repo)
	require.NoError(t, err)
	return repo, resolved
}
