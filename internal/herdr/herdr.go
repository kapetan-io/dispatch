// Package herdr reads the herdr workspaces and agents that host workers and
// acts on them.
package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Snapshot is herdr's view of every workspace, tab, pane and agent at one moment.
type Snapshot struct {
	Workspaces []Workspace
	Tabs       []Tab
	Panes      []Pane
	Agents     []Agent
}

// Tab is a herdr tab and the label it shows.
type Tab struct {
	ID          string
	Label       string
	WorkspaceID string
}

// Pane is a terminal in a tab.
type Pane struct {
	ID          string
	TabID       string
	WorkspaceID string
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

// Herdr is the terminal workspace manager that hosts the workers.
type Herdr interface {
	// Read takes a snapshot of every workspace, tab, pane and agent.
	Read(ctx context.Context) (Snapshot, error)
	// Prompt types text into the agent in pane and submits it. herdr refuses a
	// blocked agent before sending any input, so a question on screen is never
	// answered by accident. The refusal satisfies errors.Is(err, ErrAgentBlocked).
	Prompt(ctx context.Context, pane, text string) error
	// RenameWorkspace sets a workspace's label.
	RenameWorkspace(ctx context.Context, id, label string) error
	// RenameTab sets a tab's label.
	RenameTab(ctx context.Context, id, label string) error
	// CloseWorkspace closes a workspace and removes its tabs, panes and agents,
	// which ends any worker running there.
	CloseWorkspace(ctx context.Context, id string) error
	// CloseTab closes a tab and removes its panes and agents.
	CloseTab(ctx context.Context, id string) error
}

// ErrAgentBlocked is the refusal of a prompt to an agent that waits on a
// question.
var ErrAgentBlocked = errors.New("agent is blocked on a question")

// Refusal is herdr declining a request. Error renders herdr's JSON error body,
// so a reported refusal reads as herdr printed it.
type Refusal struct {
	// Code is herdr's name for the refusal, such as agent_blocked or workspace_not_found.
	Code    string
	Message string
	// Command is herdr's name for the request it refused, such as cli:agent:prompt.
	Command string
}

type refusalBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	ID string `json:"id"`
}

func (r *Refusal) Error() string {
	var body refusalBody
	body.Error.Code, body.Error.Message, body.ID = r.Code, r.Message, r.Command
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(body)
	return strings.TrimSpace(buf.String())
}

// Is makes a blocked-agent refusal match ErrAgentBlocked, so callers can tell
// it from other refusals.
func (r *Refusal) Is(target error) bool {
	return target == ErrAgentBlocked && r.Code == "agent_blocked"
}

// CLI is the Herdr that runs the installed herdr command.
type CLI struct {
	bin    string
	global []string
}

// NewCLI returns a Herdr that runs bin. Arguments in global come before every
// subcommand, such as "--session", "name" to act on a named session.
func NewCLI(bin string, global ...string) *CLI { return &CLI{bin: bin, global: global} }

func (c *CLI) Read(ctx context.Context) (Snapshot, error) {
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
	if err := c.list(ctx, &workspaces, "workspace", "list"); err != nil {
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
	if err := c.list(ctx, &agents, "agent", "list"); err != nil {
		return Snapshot{}, err
	}
	var tabs struct {
		Result struct {
			Tabs []struct {
				TabID       string `json:"tab_id"`
				Label       string `json:"label"`
				WorkspaceID string `json:"workspace_id"`
			} `json:"tabs"`
		} `json:"result"`
	}
	// dispatch's own logic doesn't read tabs or panes, so a herdr without `tab
	// list` or `pane list` must not stop launches, nudges and teardown.
	_ = c.list(ctx, &tabs, "tab", "list")
	var panes struct {
		Result struct {
			Panes []struct {
				PaneID      string `json:"pane_id"`
				TabID       string `json:"tab_id"`
				WorkspaceID string `json:"workspace_id"`
			} `json:"panes"`
		} `json:"result"`
	}
	_ = c.list(ctx, &panes, "pane", "list")
	var s Snapshot
	for _, t := range tabs.Result.Tabs {
		s.Tabs = append(s.Tabs, Tab{ID: t.TabID, Label: t.Label, WorkspaceID: t.WorkspaceID})
	}
	for _, p := range panes.Result.Panes {
		s.Panes = append(s.Panes, Pane{ID: p.PaneID, TabID: p.TabID, WorkspaceID: p.WorkspaceID})
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

func (c *CLI) Prompt(ctx context.Context, pane, text string) error {
	return c.act(ctx, "agent", "prompt", pane, text)
}

func (c *CLI) RenameWorkspace(ctx context.Context, id, label string) error {
	return c.act(ctx, "workspace", "rename", id, label)
}

func (c *CLI) RenameTab(ctx context.Context, id, label string) error {
	return c.act(ctx, "tab", "rename", id, label)
}

func (c *CLI) CloseWorkspace(ctx context.Context, id string) error {
	return c.act(ctx, "workspace", "close", id)
}

func (c *CLI) CloseTab(ctx context.Context, id string) error {
	return c.act(ctx, "tab", "close", id)
}

// act runs a command that changes herdr. A refusal herdr explains comes back
// as a *Refusal, and any other failure carries what herdr printed.
func (c *CLI) act(ctx context.Context, args ...string) error {
	out, err := c.command(ctx, args...).CombinedOutput()
	if err == nil {
		return nil
	}
	var body refusalBody
	if json.Unmarshal(bytes.TrimSpace(out), &body) == nil && body.Error.Code != "" {
		return &Refusal{Code: body.Error.Code, Message: body.Error.Message, Command: body.ID}
	}
	return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
}

func (c *CLI) list(ctx context.Context, out any, args ...string) error {
	b, err := c.command(ctx, args...).Output()
	if err != nil {
		return fmt.Errorf("while running herdr %v: %w", args, err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("while reading herdr %v output: %w", args, err)
	}
	return nil
}

func (c *CLI) command(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, c.bin, append(append([]string{}, c.global...), args...)...)
}

var _ Herdr = (*CLI)(nil)
