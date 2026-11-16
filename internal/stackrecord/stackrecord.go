// Package stackrecord reads and clears the keys an earlier dispatch kept under a branch in a repo
// clone's git config, which the first-start import copies into branch rows (internal/ticketbranch).
// Two keys held a stacked ticket's stack parent and fork point, both or neither, and one key named
// the ticket whose work the branch carried. Nothing else reads them, so the package goes when the
// import does.
package stackrecord

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/ticketbranch"
)

const (
	parentKey = "dispatchStackParent"
	forkKey   = "dispatchStackForkPoint"
	ticketKey = "dispatchTicket"
)

// Keys is what one repo's git config held under its branches.
type Keys struct {
	Whole map[string]ticketbranch.Record
	// Half maps a branch that has only one of the two stack record keys set to a value, to the key
	// that is set.
	Half map[string]string
	// Tickets maps a local branch to the ticket its ticket branch key names. A key whose branch is
	// gone is left out.
	Tickets map[string]string
	// Branches lists every branch with any of the keys, which the import unsets.
	Branches []string
}

// Read reads the keys of the clone at repoDir. A clone with none is an empty read, and only a read
// git itself fails is an error.
func Read(ctx context.Context, env environment.Environment, repoDir string) (Keys, error) {
	out, err := env.Command(ctx, "git", "-C", repoDir, "config", "--get-regexp", `^branch\..*\.dispatch(stack|ticket)`).Output()
	keys := Keys{Whole: map[string]ticketbranch.Record{}, Half: map[string]string{}, Tickets: map[string]string{}}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return keys, nil
		}
		return Keys{}, fmt.Errorf("read stack records in %s: %w", repoDir, err)
	}
	parents, forks, seen := map[string]string{}, map[string]string{}, map[string]bool{}
	for line := range strings.SplitSeq(string(out), "\n") {
		key, value, _ := strings.Cut(strings.TrimSpace(line), " ")
		// A branch name may hold dots, so only the last dot splits the variable name off.
		name, ok := strings.CutPrefix(key, "branch.")
		if !ok {
			continue
		}
		dot := strings.LastIndex(name, ".")
		if dot < 0 {
			continue
		}
		branch, variable := name[:dot], name[dot+1:]
		// git prints variable names lowercased.
		switch {
		case strings.EqualFold(variable, parentKey):
			parents[branch] = value
		case strings.EqualFold(variable, forkKey):
			forks[branch] = value
		case strings.EqualFold(variable, ticketKey) && value != "":
			keys.Tickets[branch] = value
		default:
			continue
		}
		if !seen[branch] {
			seen[branch] = true
			keys.Branches = append(keys.Branches, branch)
		}
	}
	for branch, parent := range parents {
		fork, ok := forks[branch]
		switch {
		case ok && parent != "" && fork != "":
			keys.Whole[branch] = ticketbranch.Record{Parent: parent, ForkPoint: fork}
		case ok:
			// Both keys are there and one is empty, so the other is the key that is set.
			keys.Half[branch] = parentKey
			if parent == "" {
				keys.Half[branch] = forkKey
			}
		default:
			keys.Half[branch] = parentKey
		}
	}
	for branch := range forks {
		if _, ok := parents[branch]; !ok {
			keys.Half[branch] = forkKey
		}
	}
	if len(keys.Tickets) == 0 {
		return keys, nil
	}
	local, err := LocalBranches(ctx, env, repoDir)
	if err != nil {
		return Keys{}, err
	}
	for branch := range keys.Tickets {
		if _, ok := local[branch]; !ok {
			delete(keys.Tickets, branch)
		}
	}
	return keys, nil
}

// LocalBranches maps each local branch of the clone at repoDir to the tree that has it checked
// out, empty when none does. A directory that is not a git repository has no branches.
func LocalBranches(ctx context.Context, env environment.Environment, repoDir string) (map[string]string, error) {
	out, err := env.Command(ctx, "git", "-C", repoDir, "for-each-ref", "--format=%(refname:short)%09%(worktreepath)", "refs/heads").Output()
	if err != nil && NotARepo(ctx, env, repoDir) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list branches in %s: %w", repoDir, err)
	}
	branches := map[string]string{}
	for line := range strings.SplitSeq(string(out), "\n") {
		branch, tree, _ := strings.Cut(strings.TrimSpace(line), "\t")
		if branch != "" {
			branches[branch] = tree
		}
	}
	return branches, nil
}

// NotARepo reports whether the directory exists and git says it is in no git repository, which is
// a fact about the directory and not a failed read. A directory that is gone, as a moved clone's
// is, is a failed read. git exits 128 also for a clone it refuses to read, as it does one another
// user owns, so only its message, in the C locale, tells the two apart.
func NotARepo(ctx context.Context, env environment.Environment, dir string) bool {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return false
	}
	cmd := env.Command(ctx, "git", "-C", dir, "rev-parse", "--git-dir")
	cmd.Env = append(cmd.Env, "LC_ALL=C")
	var said strings.Builder
	cmd.Stderr = &said
	var exit *exec.ExitError
	return errors.As(cmd.Run(), &exit) && exit.ExitCode() == 128 && strings.Contains(said.String(), "not a git repository")
}

// Clear removes every dispatch key of branch. Keys that are already gone are not an error.
func Clear(ctx context.Context, env environment.Environment, repoDir, branch string) error {
	var errs []error
	for _, key := range []string{parentKey, forkKey, ticketKey} {
		err := env.Command(ctx, "git", "-C", repoDir, "config", "--unset", "branch."+branch+"."+key).Run()
		var exit *exec.ExitError
		// Exit 5 is git's answer for a key that was never set.
		if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 5) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
