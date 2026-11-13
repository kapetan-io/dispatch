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
	"slices"
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

// SessionIn is the one session in workspace: the first agent there. A snapshot holds
// only agents of the kinds its reader was built with, so an agent dispatch can't run,
// such as a Pi a person opened, is never the session.
func (s Snapshot) SessionIn(workspace string) (Agent, bool) {
	for _, a := range s.Agents {
		if a.WorkspaceID == workspace {
			return a, true
		}
	}
	return Agent{}, false
}

// Tab is a herdr tab and the label it shows. Under the Herdr app the label is empty.
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

// Workspace is a herdr workspace and the checkout it shows. Under the Herdr app the label is empty.
type Workspace struct {
	ID           string
	Label        string
	CheckoutPath string
}

// Agent is a terminal agent such as a worker's session.
type Agent struct {
	// Kind is the agent kind herdr detected, such as "claude".
	Kind        string
	Status      string
	PaneID      string
	TabID       string
	WorkspaceID string
	// Session is the id of the session in the pane, which the harness also puts
	// in the reports its hooks send. For a launched worker it is the id the launch chose.
	Session string
}

// Multiplexer is the port to the multiplexer that hosts the workers.
type Multiplexer interface {
	// Multiplexer is what messages call this product: "herdr" for the herdr TUI and for
	// herdr-core with the Herdr app, "Rex" for Rex.
	Name() string
	// Read takes a snapshot of every workspace, tab, pane and agent.
	Read(ctx context.Context) (Snapshot, error)
	// AgentAt returns the agent in pane, asking herdr about that pane alone. A pane
	// with no agent is not an error, because hooks run in panes that hold none. The CLI
	// adapter does not filter it by WithKinds, and the caller decides whether it runs the
	// agent's kind. Core returns only a worker it started that runs one of its kinds.
	AgentAt(ctx context.Context, pane string) (Agent, bool, error)
	// RenameWorkspace sets a workspace's label. Core names the space in the Herdr app window, if one is open.
	RenameWorkspace(ctx context.Context, id, label string) error
	// RenameTab sets a tab's label. Core names the tab in the Herdr app window, if one is open.
	RenameTab(ctx context.Context, id, label string) error
	// CloseWorkspace closes a workspace and removes its tabs, panes and agents,
	// which ends any worker running there. Core also closes the space in the app window when
	// no terminal is left in it.
	CloseWorkspace(ctx context.Context, id string) error
	// CloseTab closes a tab and removes its panes and agents.
	CloseTab(ctx context.Context, id string) error
	// OpenWorkspace returns the workspace showing the checkout, opening one
	// nested under the repo when none shows it yet. Core opens nothing: it derives the id,
	// and the workspace shows once a worker starts in it.
	OpenWorkspace(ctx context.Context, w NewWorkspace) (workspaceID string, err error)
	// StartWorker gives the workspace a new tab holding a running agent of the
	// worker's program, answering nothing yet. The workspace's agent-less tabs are
	// closed. A failure after the tab came up still returns its pane, so the
	// caller can tell a missing tab from a program that would not start. It fails with
	// ErrSessionRunning, starting nothing, when a session already runs on the tree, and with
	// *TerminalWithoutAgent when a terminal dispatch started for the tree runs no Claude.
	StartWorker(ctx context.Context, w Worker) (Started, error)
	// Kickoff types text into the program in pane once its input box is ready,
	// answering the startup dialogs the Startup lists first, and returns once the
	// multiplexer shows the agent running it. It fails with ErrNeverStarted or
	// ErrKickoffUnconfirmed when the agent never runs it, and with *ProgramExited (which is also an
	// ErrKickoffUnconfirmed) when the multiplexer shows the program finished first.
	Kickoff(ctx context.Context, pane, text string, startup Startup) error
}

// Stream is a multiplexer that reports, as it happens, each pane whose agent may have changed:
// its status moved, its program exited, or its tab closed.
type Stream interface {
	// Changes calls changed with the pane of each change, in the order the multiplexer made them,
	// until ctx ends or the stream drops. It returns nil only when ctx ended; any other return is a
	// drop and says why. It never starts the multiplexer. Once it is live it has named each pane of a
	// worker, so a change made before it went live is not lost. When listing those panes fails it names
	// none, and the next full read covers them. changed may block, which
	// holds the stream. A pane may hold no worker, and a pane may be named twice.
	Changes(ctx context.Context, changed func(pane string)) error
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
	// FreshLease is set when this launch took the tree's lease for the ticket, so a
	// session left on the tree belongs to an earlier ticket and is never resumed.
	FreshLease bool
	// Program is what herdr runs in the tab.
	Program Program
}

// Program is what herdr runs in a worker's new tab, as a harness describes it.
type Program struct {
	// Name is how messages name the program, such as "Claude".
	Name string
	// AgentKind is herdr's --kind for the agent.
	AgentKind string
	// Argv follows the `--` of `herdr agent start`, before the session flag StartArgv adds.
	Argv []string
	// PathFirst lists directories put before the inherited PATH.
	PathFirst []string
	// Env holds the program's own tab entries, beside the ones herdr stamps for
	// every program. Claude Code sets none.
	Env map[string]string
	// Startup is every screen the program shows before it takes input.
	Startup Startup
	// Session is the id the program is to use for its session. Empty when the
	// harness can't choose one.
	Session string
	// SessionFlag starts the program on Session (Claude: "--session-id"). An
	// adapter appends SessionFlag and Session to Argv, which holds neither.
	SessionFlag string
	// ResumeFlag continues an earlier session instead (Claude: "--resume"). Core
	// uses it through ResumeArgv after a core restart. Empty when the program
	// can't resume.
	ResumeFlag string
}

// StartArgv is Argv followed by the flag that starts the program on its chosen
// session, when it has one.
func (p Program) StartArgv() []string {
	argv := slices.Clone(p.Argv)
	if p.Session != "" && p.SessionFlag != "" {
		argv = append(argv, p.SessionFlag, p.Session)
	}
	return argv
}

// ResumeArgv is Argv followed by the flag that continues session, or false when the
// program can't resume.
func (p Program) ResumeArgv(session string) ([]string, bool) {
	if session == "" || p.ResumeFlag == "" {
		return nil, false
	}
	return append(slices.Clone(p.Argv), p.ResumeFlag, session), true
}

// Startup describes the screens a program shows on its way to an input box.
// Kickoff checks each screen against Answers (the first that fully matches, so
// put the more specific Answer first), then Unrun (unless Started follows it),
// then Ready, and types the
// kickoff only after Ready matches.
type Startup struct {
	Answers []Answer
	// Unrun matches the launch command still sitting at the shell prompt.
	Unrun *regexp.Regexp
	// Started matches output only a running program prints. Some versions leave the
	// launch command on screen above it, so Unrun alone would misread them.
	Started *regexp.Regexp
	// Ready matches the screen once the program takes input.
	Ready *regexp.Regexp
}

// afterCommand returns the screen below the last launch command on it, which is
// the whole screen when none shows. A reused pane can hold an earlier run's
// command above the current one.
func (s Startup) afterCommand(screen string) (rest string, shown bool) {
	if s.Unrun == nil {
		return screen, false
	}
	locs := s.Unrun.FindAllStringIndex(screen, -1)
	if len(locs) == 0 {
		return screen, false
	}
	return screen[locs[len(locs)-1][1]:], true
}

// commandUnrun reports whether screen shows the launch command with no Started
// output after it.
func (s Startup) commandUnrun(screen string) bool {
	rest, shown := s.afterCommand(screen)
	return shown && (s.Started == nil || !s.Started.MatchString(rest))
}

// ready reports whether screen shows the program's input box. The launch
// command's own prompt is not one, so with Started the box must follow it.
func (s Startup) ready(screen string) bool {
	if s.Ready == nil {
		return false
	}
	rest, shown := s.afterCommand(screen)
	if !shown || s.Started == nil {
		return s.Ready.MatchString(screen)
	}
	loc := s.Started.FindStringIndex(rest)
	return loc != nil && s.Ready.MatchString(rest[loc[1]:])
}

// Answer is one startup dialog. Every pattern in Shows must match the screen.
// A dialog seen again gets the next Try, in a cycle, because a way of answering
// may be lost.
type Answer struct {
	Shows []*regexp.Regexp
	Tries []Try
}

// Answer returns the next way to answer the dialog the screen shows, or false when the
// screen shows none. sightings counts, per Answer, how often it was seen and is updated
// here, so a dialog seen again gets its next Try.
func (s Startup) Answer(screen string, sightings []int) (Try, bool) {
	for i, answer := range s.Answers {
		if !answer.shows(screen) {
			continue
		}
		try := answer.Tries[sightings[i]%len(answer.Tries)]
		sightings[i]++
		return try, true
	}
	return nil, false
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

// How long Kickoff tries: it looks at the worker up to kickoffChecks times for its input box,
// then types the kickoff up to kickoffTries times.
const (
	kickoffChecks = 30
	kickoffTries  = 3
	// kickoffWaits is how many reads Core gives a typed kickoff to move the status.
	kickoffWaits = 15
)

// probeOf is the start of text, enough to find the typed kickoff on a screen that wraps it.
func probeOf(text string) string {
	if r := []rune(text); len(r) > 24 {
		return string(r[:24])
	}
	return text
}

// oneLine joins the lines of a screen so a pattern can match across them.
func oneLine(screen string) string { return strings.ReplaceAll(screen, "\n", " ") }

// Reasons Kickoff gives up.
var (
	// ErrKickoffUnconfirmed means the multiplexer never showed the program running the kickoff.
	ErrKickoffUnconfirmed = errors.New("the multiplexer never showed the kickoff running")
	// ErrNeverStarted means the command that starts the program still showed at the shell prompt after Enter was pressed on it.
	ErrNeverStarted = errors.New("the program never started, because its launch command still showed at the shell prompt after Enter was pressed on it")
	// ErrEnterNotSent means the launch command still showed at the shell prompt and the multiplexer refused every Enter sent to it.
	ErrEnterNotSent = errors.New("the program never started, because its launch command still showed at the shell prompt and the multiplexer could not send Enter to it")
)

// ProgramExited is Kickoff's failure when the multiplexer shows the worker's program finished
// before the kickoff ran. errors.Is(err, ErrKickoffUnconfirmed) is true for it.
type ProgramExited struct {
	// Exit is how the program ended in the multiplexer's words, such as "exit status 1", or ""
	// when it reports none.
	Exit string
	// Screen is the end of the program's screen on one line, or "" when it can't be read.
	Screen string
}

func (e *ProgramExited) Error() string {
	msg := "the program exited"
	if e.Exit != "" {
		msg += " (" + e.Exit + ")"
	}
	if e.Screen != "" {
		msg += ", and its screen ended with: " + e.Screen
	}
	return msg
}

func (e *ProgramExited) Unwrap() error { return ErrKickoffUnconfirmed }

var (
	// ErrStatusUnknown means Kickoff found a worker that reports no status, as Claude does at
	// a startup dialog.
	ErrStatusUnknown = errors.New("the worker reports no status, so nothing was typed")
	// ErrSessionRunning means StartWorker found a session on the tree and started nothing.
	ErrSessionRunning = errors.New("a session already runs on the tree")
)

// TerminalWithoutAgent means StartWorker found a live terminal started by dispatch for the
// tree, with no Claude running in the tree's space, and started nothing. The terminal may be
// stuck or may belong to a racing launch whose Claude is still loading, so dispatch leaves it
// for a person to remove.
type TerminalWithoutAgent struct{ Address string }

func (e *TerminalWithoutAgent) Error() string {
	return "terminal " + e.Address + " holds the tree but runs no Claude"
}

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

// CLI is the Multiplexer that runs the installed herdr command.
type CLI struct {
	workLog
	bin         string
	global      []string
	env         environment.Environment
	dispatchBin string
	kinds       []string
}

// NewCLI returns a Multiplexer that runs bin. Arguments in global come before every
// subcommand, such as "--session", "name" to act on a named session.
func NewCLI(bin string, global ...string) *CLI {
	return &CLI{bin: bin, global: global, workLog: newWorkLog()}
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

// WithKinds makes Read leave out agents of every other kind, so a session dispatch
// has no harness for is never a worker's session. Unset, Read keeps every kind. Core,
// unset, finds none.
func (c *CLI) WithKinds(kinds ...string) *CLI {
	c.kinds = kinds
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
		if c.kinds != nil && !slices.Contains(c.kinds, a.Agent) {
			continue
		}
		s.Agents = append(s.Agents, Agent{Kind: a.Agent, Status: a.Status, PaneID: a.PaneID, TabID: a.TabID, WorkspaceID: a.WorkspaceID, Session: a.Session.Value})
	}
	return s, nil
}

func (c *CLI) AgentAt(ctx context.Context, pane string) (Agent, bool, error) {
	cmd := c.command(ctx, "agent", "get", pane)
	// A herdr wrapped in a script leaves a child holding stdout after the timeout kills the script.
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		// herdr prints its refusal body on stderr.
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			var body refusalBody
			if json.Unmarshal(bytes.TrimSpace(exit.Stderr), &body) == nil && body.Error.Code == "agent_not_found" {
				return Agent{}, false, nil
			}
			return Agent{}, false, fmt.Errorf("while running herdr agent get %s: %w: %s", pane, err, bytes.TrimSpace(exit.Stderr))
		}
		return Agent{}, false, fmt.Errorf("while running herdr agent get %s: %w", pane, err)
	}
	var got struct {
		Result struct {
			Agent struct {
				Agent       string `json:"agent"`
				Status      string `json:"agent_status"`
				PaneID      string `json:"pane_id"`
				TabID       string `json:"tab_id"`
				WorkspaceID string `json:"workspace_id"`
				Session     struct {
					Value string `json:"value"`
				} `json:"agent_session"`
			} `json:"agent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		return Agent{}, false, fmt.Errorf("while reading herdr agent get %s output: %w", pane, err)
	}
	a := got.Result.Agent
	return Agent{Kind: a.Agent, Status: a.Status, PaneID: a.PaneID, TabID: a.TabID, WorkspaceID: a.WorkspaceID, Session: a.Session.Value}, true, nil
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

var _ Multiplexer = (*CLI)(nil)

func (c *CLI) Name() string { return "herdr" }

// TabLabel is the label of a worker's tab, `<ticket> <title>`, or the ticket alone when it has no title.
func TabLabel(ticket, title string) string {
	if title == "" {
		return ticket
	}
	return ticket + " " + title
}

// WorkspaceLabel is the label of a worker's workspace, `<emoji> <ticket>`.
func WorkspaceLabel(emoji, ticket string) string { return emoji + " " + ticket }
