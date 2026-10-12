//go:build drift

package treehouse_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/treehouse"
	"github.com/kapetan-io/dispatch/internal/treehousetest"
)

// TestRealTreehouseKeepsTheTreehouseContract runs the fake's scenarios against
// the installed treehouse, in a temp repo with a temp pool root, so no real
// pool is touched. A missing treehouse fails the test.
// Run with: go test -tags drift ./internal/treehouse/
func TestRealTreehouseKeepsTheTreehouseContract(t *testing.T) {
	bin, err := exec.LookPath("treehouse")
	require.NoError(t, err)

	for _, scenario := range treehousetest.Scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			root := t.TempDir()
			repo := filepath.Join(root, "repo")
			require.NoError(t, os.Mkdir(repo, 0o755))
			runGit(t, repo, "init", "-q", "-b", "main")
			runGit(t, repo, "-c", "user.name=drift", "-c", "user.email=drift@example.com", "commit", "-q", "--allow-empty", "-m", "init")
			t.Setenv("TREEHOUSE_ROOT", filepath.Join(root, "pool"))

			scenario.Run(t, treehousetest.Subject{
				Pool: treehouse.NewCLI(bin),
				Repo: repo,
				Provision: func(n int) {
					toml := fmt.Sprintf("max_trees = %d\n", n)
					require.NoError(t, os.WriteFile(filepath.Join(repo, "treehouse.toml"), []byte(toml), 0o644))
				},
			})
		})
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	require.NoError(t, err, string(out))
}
