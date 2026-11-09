// Package launchedbranch keeps the branch dispatch launched each ticket's worker on. Linear
// reports a branch name of its own that can later differ from the pushed branch (it truncates
// long names), so the world reads this record before Linear's name.
package launchedbranch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kapetan-io/dispatch/internal/treekey"
)

func dir(stateDir string) string { return filepath.Join(stateDir, "launched-branches") }

// Write records branch as the one ticket's worker was launched on, replacing an earlier record.
func Write(stateDir, ticket, branch string) error {
	if err := os.MkdirAll(dir(stateDir), 0o755); err != nil {
		return fmt.Errorf("while recording the branch of %s: %w", ticket, err)
	}
	tmp, err := os.CreateTemp(dir(stateDir), "tmp-*")
	if err != nil {
		return fmt.Errorf("while recording the branch of %s: %w", ticket, err)
	}
	_, werr := tmp.WriteString(branch)
	if cerr := tmp.Close(); werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("while recording the branch of %s: %w", ticket, errors.Join(werr, cerr))
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir(stateDir), treekey.Hash(ticket))); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("while recording the branch of %s: %w", ticket, err)
	}
	return nil
}

// Of is the branch ticket's worker was launched on, and false when no launch recorded one.
func Of(stateDir, ticket string) (string, bool) {
	if stateDir == "" {
		return "", false
	}
	b, err := os.ReadFile(filepath.Join(dir(stateDir), treekey.Hash(ticket)))
	branch := strings.TrimSpace(string(b))
	return branch, err == nil && branch != ""
}
