// Package herdr reads the herdr workspaces and agents that host workers.
package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Snapshot is herdr's view of every workspace, tab and agent at one moment.
type Snapshot struct {
	Workspaces []Workspace
	Tabs       []Tab
	Agents     []Agent
}

// Tab is a herdr tab and the label it shows.
type Tab struct {
	ID    string
	Label string
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
	TabID       string
	WorkspaceID string
	// Session is the id of the Claude session in the pane, which Claude Code
	// also puts in its hook input.
	Session string
}

// Read takes a snapshot by running `<bin> workspace list`, `<bin> tab list` and
// `<bin> agent list`.
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
				TabID       string `json:"tab_id"`
				WorkspaceID string `json:"workspace_id"`
				Session     struct {
					Value string `json:"value"`
				} `json:"agent_session"`
			} `json:"agents"`
		} `json:"result"`
	}
	if err := run(ctx, bin, &agents, "agent", "list"); err != nil {
		return Snapshot{}, err
	}
	var tabs struct {
		Result struct {
			Tabs []struct {
				TabID string `json:"tab_id"`
				Label string `json:"label"`
			} `json:"tabs"`
		} `json:"result"`
	}
	// Tab labels are cosmetic, so a herdr without `tab list` must not stop launches, nudges and teardown.
	_ = run(ctx, bin, &tabs, "tab", "list")
	var s Snapshot
	for _, t := range tabs.Result.Tabs {
		s.Tabs = append(s.Tabs, Tab{ID: t.TabID, Label: t.Label})
	}
	for _, w := range workspaces.Result.Workspaces {
		ws := Workspace{ID: w.WorkspaceID, Label: w.Label}
		if w.Worktree != nil {
			ws.CheckoutPath = w.Worktree.CheckoutPath
		}
		s.Workspaces = append(s.Workspaces, ws)
	}
	for _, a := range agents.Result.Agents {
		s.Agents = append(s.Agents, Agent{Kind: a.Agent, Status: a.Status, PaneID: a.PaneID, TabID: a.TabID, WorkspaceID: a.WorkspaceID, Session: a.Session.Value})
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

// RenameWorkspace sets a workspace's label.
func RenameWorkspace(ctx context.Context, bin, workspace, label string) error {
	out, err := exec.CommandContext(ctx, bin, "workspace", "rename", workspace, label).CombinedOutput()
	if err != nil {
		return fmt.Errorf("while renaming herdr workspace %s: %w: %s", workspace, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RenameTab sets a tab's label.
func RenameTab(ctx context.Context, bin, tab, label string) error {
	out, err := exec.CommandContext(ctx, bin, "tab", "rename", tab, label).CombinedOutput()
	if err != nil {
		return fmt.Errorf("while renaming herdr tab %s: %w: %s", tab, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// CloseWorkspace closes a workspace and the panes in it, which ends any worker
// running there.
func CloseWorkspace(ctx context.Context, bin, workspace string) error {
	out, err := exec.CommandContext(ctx, bin, "workspace", "close", workspace).CombinedOutput()
	if err != nil {
		return fmt.Errorf("while closing herdr workspace %s: %w: %s", workspace, err, strings.TrimSpace(string(out)))
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
