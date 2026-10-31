// Package herdr reads the herdr workspaces and agents that host workers and
// acts on them.
package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/environment"
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
	// Session is the id of the session in the pane, which Claude Code also puts
	// in its hook input.
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
	// OpenWorkspace returns the workspace showing the checkout, opening one
	// nested under the repo when none shows it yet.
	OpenWorkspace(ctx context.Context, w NewWorkspace) (workspaceID string, err error)
	// StartWorker gives the workspace a new tab holding a running agent of the
	// worker's program, answering nothing yet. The workspace's agent-less tabs are
	// closed. A failure after the tab came up still returns its pane, so the
	// caller can tell a missing tab from a program that would not start.
	StartWorker(ctx context.Context, w Worker) (Started, error)
	// Kickoff types text into the program in pane once its input box is ready,
	// answering the startup dialogs the Startup lists first, and returns once herdr
	// shows the agent running it. It fails with ErrNeverStarted or
	// ErrKickoffUnconfirmed when the agent never runs it.
	Kickoff(ctx context.Context, pane, text string, startup Startup) error
}

// NewWorkspace is a checkout to show in a workspace.
type NewWorkspace struct {
	// Repo is the repo's main checkout, which herdr nests the workspace under.
	Repo string
	// Path is the checkout the workspace shows.
	Path string
	// Label is what the workspace is called until the dispatcher renames it.
	Label string
}

// Worker is a program to start in a workspace.
type Worker struct {
	Workspace string
	// Dir is where the agent runs.
	Dir string
	// Label is the new tab's label.
	Label string
	// Name is the agent's name. When herdr already has an agent by that name, the
	// worker gets a suffix.
	Name string
	// PluginRoot is stamped as DISPATCH_PLUGIN_ROOT for every program, because
	// dispatch's skills read it. A program that loads the plugin by flag carries the
	// same root in Argv and PathFirst.
	PluginRoot string
	// Program is what herdr runs in the tab.
	Program Program
}

// Program is what herdr runs in a worker's new tab, as a harness describes it.
type Program struct {
	// Name is how messages name the program, such as "Claude".
	Name string
	// AgentKind is herdr's --kind for the agent.
	AgentKind string
	// Argv follows the `--` of `herdr agent start`.
	Argv []string
	// PathFirst lists directories put before the inherited PATH.
	PathFirst []string
	// Env holds the program's own tab entries, beside the ones herdr stamps for
	// every program. Claude Code sets none.
	Env map[string]string
	// Startup is every screen the program shows before it takes input.
	Startup Startup
}

// Startup describes the screens a program shows on its way to an input box.
// Kickoff checks each screen against Answers (the first that fully matches, so
// put the more specific Answer first), then Unrun, then Ready, and types the
// kickoff only after Ready matches.
type Startup struct {
	Answers []Answer
	// Unrun matches the launch command still sitting at the shell prompt.
	Unrun *regexp.Regexp
	// Ready matches the screen once the program takes input.
	Ready *regexp.Regexp
}

// Answer is one startup dialog. Every pattern in Shows must match the screen.
// A dialog seen again gets the next Try, in a cycle, because a way of answering
// may be lost.
type Answer struct {
	Shows []*regexp.Regexp
	Tries []Try
}

// Try is one way to answer a dialog, a list of `pane send-keys` calls that each
// send a list of keys together. {{"Down","Enter"}} is one call and
// {{"2"},{"Enter"}} is two.
type Try [][]string

// Started is a worker's pane and the name its agent got.
type Started struct {
	Pane  string
	Agent string
}

// Reasons Kickoff gives up.
var (
	// ErrKickoffUnconfirmed means the program was up but herdr never showed it running the kickoff.
	ErrKickoffUnconfirmed = errors.New("herdr never showed the kickoff running")
	// ErrNeverStarted means the command that starts the program still showed at the shell prompt after Enter was pressed on it.
	ErrNeverStarted = errors.New("the program never started, because its launch command still showed at the shell prompt after Enter was pressed on it")
	// ErrEnterNotSent means the launch command still showed at the shell prompt and herdr refused every Enter sent to it.
	ErrEnterNotSent = errors.New("the program never started, because its launch command still showed at the shell prompt and herdr could not send Enter to it")
)

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
	bin         string
	global      []string
	pace        time.Duration
	trace       string
	progress    io.Writer
	env         environment.Environment
	dispatchBin string
}

// NewCLI returns a Herdr that runs bin. Arguments in global come before every
// subcommand, such as "--session", "name" to act on a named session.
func NewCLI(bin string, global ...string) *CLI {
	return &CLI{bin: bin, global: global, pace: time.Second, progress: io.Discard}
}

// WithEnv runs herdr, and finds it, with env instead of the process's environment. Workers it
// starts get the PATH of env, ahead of their shims.
func (c *CLI) WithEnv(env environment.Environment) *CLI {
	c.env = env
	return c
}

// WithDispatchBin names the dispatch binary stamped as a started worker's
// DISPATCH_BIN, which its claude shim and skills run. Unset, they run the
// dispatch on the worker's PATH. A test that calls the adapter in-process must
// build one, because its own binary is not a dispatch.
func (c *CLI) WithDispatchBin(bin string) *CLI {
	c.dispatchBin = bin
	return c
}

// WithPace sets the unit of waiting between polls while a worker starts. The
// default is 1s.
func (c *CLI) WithPace(d time.Duration) *CLI {
	if d > 0 {
		c.pace = d
	}
	return c
}

// WithTrace appends every screen Kickoff reads to the file at path.
func (c *CLI) WithTrace(path string) *CLI {
	c.trace = path
	return c
}

// WithProgress sends a line to w for each wait and failure while a worker starts.
func (c *CLI) WithProgress(w io.Writer) *CLI {
	if w != nil {
		c.progress = w
	}
	return c
}

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
	return c.env.Command(ctx, c.bin, append(append([]string{}, c.global...), args...)...)
}

var _ Herdr = (*CLI)(nil)

// TabLabel is the label of a worker's tab, `<ticket> <title>`, or the ticket alone when it has no title.
func TabLabel(ticket, title string) string {
	if title == "" {
		return ticket
	}
	return ticket + " " + title
}
