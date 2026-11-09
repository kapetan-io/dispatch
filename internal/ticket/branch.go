package ticket

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kapetan-io/dispatch/internal/treekey"
)

func branchDir(stateDir string) string { return filepath.Join(stateDir, "ticket-branches") }

// WriteBranch records branch as the one id's work is on, replacing an earlier record. Each ticket
// has its own file, so the launcher and the running loop can write at once without losing each
// other's records.
func WriteBranch(stateDir, id, branch string) error {
	dir := branchDir(stateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("while recording the branch of %s: %w", id, err)
	}
	tmp, err := os.CreateTemp(dir, "tmp-*")
	if err != nil {
		return fmt.Errorf("while recording the branch of %s: %w", id, err)
	}
	_, werr := tmp.WriteString(branch)
	if cerr := tmp.Close(); werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("while recording the branch of %s: %w", id, errors.Join(werr, cerr))
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, treekey.Hash(id))); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("while recording the branch of %s: %w", id, err)
	}
	return nil
}

// RecordedBranch is the branch dispatch recorded for id, and false when it recorded none.
func RecordedBranch(stateDir, id string) (string, bool) {
	if stateDir == "" {
		return "", false
	}
	b, err := os.ReadFile(filepath.Join(branchDir(stateDir), treekey.Hash(id)))
	branch := strings.TrimSpace(string(b))
	return branch, err == nil && branch != ""
}
