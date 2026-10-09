// Package treehouse reads a repo's pool of leased worktrees.
package treehouse

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// Tree is one worktree in a repo's pool.
type Tree struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	// LeaseHolder is the ticket identifier for trees the launcher leased.
	LeaseHolder string `json:"lease_holder"`
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
	return trees, nil
}
