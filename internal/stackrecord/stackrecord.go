// Package stackrecord reads, writes and clears the stack record: two keys in a repo clone's git
// config that name a stacked ticket's stack parent and fork point. It owns the key names and the
// both-or-neither rule, so the launcher that writes the record and the world that reads it agree.
package stackrecord

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/kapetan-io/dispatch/internal/environment"
)

const (
	parentKey = "dispatchStackParent"
	forkKey   = "dispatchStackForkPoint"
)

// Record is where a stacked ticket's branch started and from whose PR.
type Record struct {
	// Parent is the stack parent's ticket id.
	Parent string
	// ForkPoint is the full sha the branch started at.
	ForkPoint string
}

// Records is every stack record of one repo, read in one call.
type Records struct {
	Whole   map[string]Record `json:"whole,omitempty"`
	Partial map[string]bool   `json:"partial,omitempty"`
}

// Of is the record of branch. Partial is true when only one of its two keys is set, which
// callers treat as unknown. A branch with no keys is neither found nor partial.
func (r Records) Of(branch string) (rec Record, found, partial bool) {
	if r.Partial[branch] {
		return Record{}, false, true
	}
	rec, found = r.Whole[branch]
	return rec, found, false
}

// Read reads the records of the clone at repoDir. A clone with none is an empty read, and
// only a read git itself fails is an error.
func Read(ctx context.Context, env environment.Environment, repoDir string) (Records, error) {
	out, err := env.Command(ctx, "git", "-C", repoDir, "config", "--get-regexp", `^branch\..*\.dispatchstack`).Output()
	recs := Records{Whole: map[string]Record{}, Partial: map[string]bool{}}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return recs, nil
		}
		return Records{}, fmt.Errorf("read stack records in %s: %w", repoDir, err)
	}
	parents, forks := map[string]string{}, map[string]string{}
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
		}
	}
	for branch, parent := range parents {
		if fork, ok := forks[branch]; ok && parent != "" && fork != "" {
			recs.Whole[branch] = Record{Parent: parent, ForkPoint: fork}
		} else {
			recs.Partial[branch] = true
		}
	}
	for branch := range forks {
		if _, ok := parents[branch]; !ok {
			recs.Partial[branch] = true
		}
	}
	return recs, nil
}

// Write sets both keys of branch in the clone at repoDir. When the second key fails it tries to
// remove the first, which fails too while the clone's config is locked, so a half record can
// remain and reads as partial.
func Write(ctx context.Context, env environment.Environment, repoDir, branch string, rec Record) error {
	for i, kv := range [][2]string{{parentKey, rec.Parent}, {forkKey, rec.ForkPoint}} {
		out, err := env.Command(ctx, "git", "-C", repoDir, "config", "branch."+branch+"."+kv[0], kv[1]).CombinedOutput()
		if err != nil {
			if i > 0 {
				_ = Clear(context.WithoutCancel(ctx), env, repoDir, branch)
			}
			return fmt.Errorf("write stack record of %s: %w: %s", branch, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// Clear removes both keys of branch. Keys that are already gone are not an error.
func Clear(ctx context.Context, env environment.Environment, repoDir, branch string) error {
	var errs []error
	for _, key := range []string{parentKey, forkKey} {
		err := env.Command(ctx, "git", "-C", repoDir, "config", "--unset", "branch."+branch+"."+key).Run()
		var exit *exec.ExitError
		// Exit 5 is git's answer for a key that was never set.
		if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 5) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
