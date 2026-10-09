// Package treehouse reads a repo's pool of leased worktrees.
package treehouse

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Tree is one worktree in a repo's pool.
type Tree struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	// LeaseHolder is the ticket identifier for trees the launcher leased.
	LeaseHolder string `json:"lease_holder"`
	// RepoDir is the clone whose pool holds the tree. Status fills it in.
	RepoDir string `json:"-"`
}

// Status runs `<bin> status --json` in repoDir and returns its trees.
func Status(ctx context.Context, bin, repoDir string) ([]Tree, error) {
	cmd := exec.CommandContext(ctx, bin, "status", "--json")
	cmd.Dir = repoDir
	b, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("while running treehouse status in %s: %w", repoDir, err)
	}
	var trees []Tree
	if err := json.Unmarshal(b, &trees); err != nil {
		return nil, fmt.Errorf("while reading treehouse status in %s: %w", repoDir, err)
	}
	for i := range trees {
		trees[i].RepoDir = repoDir
	}
	return trees, nil
}

// Return gives the tree at path back to repoDir's pool, discarding anything
// left in it. holder pins the call to that lease, so a tree leased to someone
// else since the caller looked is left alone.
func Return(ctx context.Context, bin, repoDir, path, holder string) error {
	cmd := exec.CommandContext(ctx, bin, "return", path, "--if-lease-holder", holder, "--force")
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("while returning %s to the pool: %w: %s", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Unfinished says what returning the tree at path would destroy: uncommitted
// changes, or commits that no remote has. It is empty when the tree is clean
// and every commit is pushed.
func Unfinished(ctx context.Context, path string) (string, error) {
	status, err := git(ctx, path, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	count, err := git(ctx, path, "rev-list", "--count", "HEAD", "--not", "--remotes")
	if err != nil {
		return "", err
	}
	unpushed, err := strconv.Atoi(strings.TrimSpace(count))
	if err != nil {
		return "", fmt.Errorf("while counting unpushed commits in %s: %w", path, err)
	}
	var found []string
	if strings.TrimSpace(status) != "" {
		found = append(found, "uncommitted changes")
	}
	switch {
	case unpushed == 1:
		found = append(found, "1 commit that no remote has")
	case unpushed > 1:
		found = append(found, fmt.Sprintf("%d commits that no remote has", unpushed))
	}
	return strings.Join(found, " and "), nil
}

// git runs a read-only git command in dir. It takes no optional locks, so
// checking a tree can't collide with a worker's own git commands.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("while running git %s in %s: %w", strings.Join(args, " "), dir, err)
	}
	return string(out), nil
}
