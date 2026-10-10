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

// Unfinished says what returning the tree at path would destroy: modified or
// staged tracked files, or commits reachable from HEAD that no remote has. Other local
// branches are not counted, since the pool's trees share one repository and
// treehouse returns a tree by detaching HEAD (gitvcs in treehouse v3.1.2), which
// leaves every branch in place. Untracked files are not counted, since Untracked
// and RemoveUntracked deal with them. The result is empty when the tree has no
// tracked changes and its HEAD is pushed. A squash-merged branch whose remote
// copy was pruned counts as unpushed.
func Unfinished(ctx context.Context, path string) (string, error) {
	status, err := git(ctx, path, "status", "--porcelain", "--untracked-files=no")
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

// Untracked lists the files in the tree at path that git does not track and
// .gitignore does not cover. It only reads.
func Untracked(ctx context.Context, path string) ([]string, error) {
	out, err := git(ctx, path, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, name := range strings.Split(out, "\x00") {
		if name != "" {
			files = append(files, name)
		}
	}
	return files, nil
}

// RemoveUntracked deletes the untracked files and directories in the tree at
// path. Ignored files stay, so build output is left alone.
func RemoveUntracked(ctx context.Context, path string) error {
	cmd := exec.CommandContext(ctx, "git", "clean", "-f", "-d")
	cmd.Dir = path
	cmd.Env = gitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("while removing untracked files in %s: %w: %s", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// git runs a read-only git command in dir.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("while running git %s in %s: %w", strings.Join(args, " "), dir, err)
	}
	return string(out), nil
}

// gitEnv is the environment for git commands that act on one tree. It drops
// the variables that tell git where a repository is, because a dispatcher
// started from a git hook inherits them and they would point git, and the
// delete in RemoveUntracked, at another repository. It also takes no optional
// locks, so checking a tree can't collide with a worker's own git commands.
func gitEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		switch name, _, _ := strings.Cut(kv, "="); name {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
			"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR", "GIT_PREFIX":
		default:
			env = append(env, kv)
		}
	}
	return append(env, "GIT_OPTIONAL_LOCKS=0")
}
