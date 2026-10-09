// Package herdr reads the herdr workspaces and agents that host workers.
package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Snapshot is herdr's view of every workspace and agent at one moment.
type Snapshot struct {
	Workspaces []Workspace
	Agents     []Agent
}

// Workspace is a herdr workspace and the checkout it shows.
type Workspace struct {
	ID           string
	Label        string
	CheckoutPath string
}

// Agent is a terminal agent such as a Claude worker.
type Agent struct {
	Kind        string
	Status      string
	PaneID      string
	WorkspaceID string
}

// Read takes a snapshot by running `<bin> workspace list` and `<bin> agent list`.
func Read(ctx context.Context, bin string) (Snapshot, error) {
	var workspaces struct {
		Result struct {
			Workspaces []struct {
				WorkspaceID string `json:"workspace_id"`
				Label       string `json:"label"`
				Worktree    *struct {
					CheckoutPath string `json:"checkout_path"`
				} `json:"worktree"`
			} `json:"workspaces"`
		} `json:"result"`
	}
	if err := run(ctx, bin, &workspaces, "workspace", "list"); err != nil {
		return Snapshot{}, err
	}
	var agents struct {
		Result struct {
			Agents []struct {
				Agent       string `json:"agent"`
				Status      string `json:"agent_status"`
				PaneID      string `json:"pane_id"`
				WorkspaceID string `json:"workspace_id"`
			} `json:"agents"`
		} `json:"result"`
	}
	if err := run(ctx, bin, &agents, "agent", "list"); err != nil {
		return Snapshot{}, err
	}
	var s Snapshot
	for _, w := range workspaces.Result.Workspaces {
		ws := Workspace{ID: w.WorkspaceID, Label: w.Label}
		if w.Worktree != nil {
			ws.CheckoutPath = w.Worktree.CheckoutPath
		}
		s.Workspaces = append(s.Workspaces, ws)
	}
	for _, a := range agents.Result.Agents {
		s.Agents = append(s.Agents, Agent{Kind: a.Agent, Status: a.Status, PaneID: a.PaneID, WorkspaceID: a.WorkspaceID})
	}
	return s, nil
}

// Prompt types text into the agent in pane and submits it. herdr refuses a
// blocked agent before sending any input, so a question on screen is never
// answered by accident.
func Prompt(ctx context.Context, bin, pane, text string) error {
	out, err := exec.CommandContext(ctx, bin, "agent", "prompt", pane, text).CombinedOutput()
	if err != nil {
		return fmt.Errorf("while prompting herdr pane %s: %w: %s", pane, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func run(ctx context.Context, bin string, out any, args ...string) error {
	b, err := exec.CommandContext(ctx, bin, args...).Output()
	if err != nil {
		return fmt.Errorf("while running herdr %v: %w", args, err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("while reading herdr %v output: %w", args, err)
	}
	return nil
}
