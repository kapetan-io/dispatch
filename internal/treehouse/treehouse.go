// Package treehouse leases worktrees from a repo's pool and reports what a tree holds.
package treehouse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	// RepoDir is the clone whose pool holds the tree. Trees fills it in.
	RepoDir string `json:"-"`
}

// Pool is a repo's pool of leased worktrees.
type Pool interface {
	// Trees lists the pool of the clone at repoDir.
	Trees(ctx context.Context, repoDir string) ([]Tree, error)
	// Lease takes an available tree for ticket, or fails when none is available.
	Lease(ctx context.Context, repoDir, ticket string) (Tree, error)
	// Return gives the tree at path back to the pool, discarding anything left
	// in it. It refuses, and leaves the tree as it was, when holder does not
	// hold the lease, so a tree leased to someone else since the caller looked
	// is left alone.
	Return(ctx context.Context, repoDir, path, holder string) error
}

// ErrNoPath is the failure of a lease that treehouse reported as successful
// but whose output named no tree.
var ErrNoPath = errors.New("lease succeeded but treehouse JSON had no path")

// unreadable is a status whose output could not be parsed.
type unreadable struct{ error }

func (u unreadable) Unwrap() error { return u.error }

// StatusError says what failed in reading the pool of repoDir, for the caller
// to report. A status treehouse printed but dispatch could not parse reads
// differently from one treehouse failed to produce.
func StatusError(repoDir string, err error) error {
	if errors.As(err, &unreadable{}) {
		return fmt.Errorf("while reading treehouse status in %s: %w", repoDir, errors.Unwrap(err))
	}
	return fmt.Errorf("while running treehouse status in %s: %w", repoDir, err)
}

// CLI is the Pool that runs the installed treehouse command.
type CLI struct{ bin string }

// NewCLI returns a Pool that runs bin.
func NewCLI(bin string) *CLI { return &CLI{bin: bin} }

func (c *CLI) Trees(ctx context.Context, repoDir string) ([]Tree, error) {
	cmd := c.command(ctx, repoDir, "status", "--json")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var trees []Tree
	if err := json.Unmarshal(out, &trees); err != nil {
		return nil, unreadable{err}
	}
	for i := range trees {
		trees[i].RepoDir = repoDir
	}
	return trees, nil
}

// Lease fails with treehouse's own stderr as the error text, empty when it
// printed none, because the launcher reports that text.
func (c *CLI) Lease(ctx context.Context, repoDir, ticket string) (Tree, error) {
	var stderr bytes.Buffer
	cmd := c.command(ctx, repoDir, "get", "--lease", "--lease-holder", ticket, "--json")
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return Tree{}, errors.New(strings.TrimSpace(stderr.String()))
	}
	var tree Tree
	if err := json.Unmarshal(out, &tree); err != nil || tree.Path == "" {
		return Tree{}, fmt.Errorf("%w: %s", ErrNoPath, strings.TrimSpace(string(out)))
	}
	tree.Status, tree.LeaseHolder, tree.RepoDir = "leased", ticket, repoDir
	return tree, nil
}

func (c *CLI) Return(ctx context.Context, repoDir, path, holder string) error {
	out, err := c.command(ctx, repoDir, "return", path, "--if-lease-holder", holder, "--force").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (c *CLI) command(ctx context.Context, repoDir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Dir = repoDir
	return cmd
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
