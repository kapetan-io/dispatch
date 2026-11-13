package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/treekey"
)

// Labels Core writes on every terminal it starts. The dispatch.* keys belong to dispatch and
// the herdr.* keys are the Herdr app's placement keys.
const (
	LabelTree    = "dispatch.tree"
	LabelSession = "dispatch.session"
	labelSpace   = "herdr.space"
	labelTab     = "herdr.tab"
)

// Core is the Herdr that drives herdr-core through the installed hc command, so the
// workers show in the Herdr app. It never stops or restarts herdr-core.
type Core struct {
	workLog
	hc          string
	app         string
	env         environment.Environment
	dispatchBin string
	kinds       []string
}

// NewCore returns a Herdr that runs hc.
func NewCore(hc string) *Core {
	return &Core{hc: hc, app: appCommand, workLog: newWorkLog()}
}

// WithApp runs the command at path to name and close spaces in the Herdr app window, instead
// of herdr-app.
func (c *Core) WithApp(path string) *Core {
	c.app = path
	return c
}

// WithEnv runs hc, and finds it, with env instead of the process's environment. Workers it
// starts get the PATH of env, behind their shims.
func (c *Core) WithEnv(env environment.Environment) *Core {
	c.env = env
	return c
}

// WithDispatchBin names the dispatch binary stamped as a started worker's DISPATCH_BIN.
func (c *Core) WithDispatchBin(bin string) *Core {
	c.dispatchBin = bin
	return c
}

// WithKinds names the programs a terminal may run to hold a session. A terminal running any
// other program is never an agent, so unset, Core finds none.
func (c *Core) WithKinds(kinds ...string) *Core {
	c.kinds = kinds
	return c
}

// WithPace sets the unit of waiting between polls while a worker starts. The default is 1s.
func (c *Core) WithPace(d time.Duration) *Core {
	if d > 0 {
		c.pace = d
	}
	return c
}

// WithTrace appends what Kickoff sees to the file at path, which is each status read, each screen and the program exiting.
func (c *Core) WithTrace(path string) *Core {
	c.trace = path
	return c
}

// WithProgress sends a line to w for each wait and failure while a worker starts.
func (c *Core) WithProgress(w io.Writer) *Core {
	if w != nil {
		c.progress = w
	}
	return c
}

var _ Herdr = (*Core)(nil)

// terminal is what herdr-core reports about one terminal.
type terminal struct {
	Address    string            `json:"address"`
	Name       string            `json:"name"`
	State      string            `json:"state"`
	Reason     string            `json:"reason"`
	FinishedMS uint64            `json:"finished_ms"`
	Labels     map[string]string `json:"labels"`
	Status     *terminalStatus   `json:"status"`
	Turn       uint64            `json:"turn"`
	Foreground *struct {
		Argv []string `json:"argv"`
	} `json:"foreground"`
}

type terminalStatus struct {
	State string `json:"state"`
	Kind  string `json:"kind"`
}

// resumable lists the reasons herdr-core gives a finished terminal that lost its program to
// the core or the machine going away. An exited, killed or signaled Claude crashed or was quit.
var resumable = []string{"server_stop", "stopped_by_restart", "holder_lost", "host_shutdown"}

func (t terminal) live() bool { return t.State == "live" }

// startedByDispatch reports whether the terminal carries the tree of a worker Core started.
func (t terminal) startedByDispatch() bool { return t.Labels[LabelTree] != "" }

// status is the five words the rest of dispatch reads. A terminal that reports no status,
// or reports anything but idle, done, working or blocked, is unknown.
func (t terminal) status() string {
	if t.Status == nil {
		return "unknown"
	}
	switch t.Status.State {
	case "idle", "done", "working", "blocked":
		return t.Status.State
	}
	return "unknown"
}

// runs reports whether the terminal's foreground program is one of kinds, by the base name of its argv[0].
func (c *Core) runs(t terminal) (string, bool) {
	if !t.live() || t.Foreground == nil || len(t.Foreground.Argv) == 0 {
		return "", false
	}
	kind := filepath.Base(t.Foreground.Argv[0])
	return kind, slices.Contains(c.kinds, kind)
}

func (c *Core) agent(t terminal) (Agent, bool) {
	kind, ok := c.runs(t)
	if !ok || !t.startedByDispatch() {
		return Agent{}, false
	}
	return Agent{Kind: kind, Status: t.status(), PaneID: t.Address, TabID: t.Labels[labelTab], WorkspaceID: t.Labels[labelSpace], Session: t.Labels[LabelSession]}, true
}

func (c *Core) Read(ctx context.Context) (Snapshot, error) {
	terminals, err := c.list(ctx, nil)
	if err != nil {
		return Snapshot{}, err
	}
	var s Snapshot
	names := c.names(ctx)
	spaces, tabs := map[string]bool{}, map[string]bool{}
	for _, t := range terminals {
		if !t.startedByDispatch() {
			continue
		}
		space, tab := t.Labels[labelSpace], t.Labels[labelTab]
		if !spaces[space] {
			spaces[space] = true
			s.Workspaces = append(s.Workspaces, Workspace{ID: space, Label: names[space], CheckoutPath: t.Labels[LabelTree]})
		}
		if !tabs[tab] {
			tabs[tab] = true
			s.Tabs = append(s.Tabs, Tab{ID: tab, Label: names[tab], WorkspaceID: space})
		}
		if !t.live() {
			continue
		}
		s.Panes = append(s.Panes, Pane{ID: t.Address, TabID: tab, WorkspaceID: space})
		if a, ok := c.agent(t); ok {
			s.Agents = append(s.Agents, a)
		}
	}
	return s, nil
}

func (c *Core) AgentAt(ctx context.Context, pane string) (Agent, bool, error) {
	t, err := c.get(ctx, pane)
	var refusal *Refusal
	if errors.As(err, &refusal) && refusal.Code == "agent_not_found" {
		return Agent{}, false, nil
	}
	if err != nil {
		return Agent{}, false, err
	}
	a, ok := c.agent(t)
	return a, ok, nil
}

// sendReply is what hc says of a send.
type sendReply struct {
	Started *bool  `json:"started"`
	Turn    uint64 `json:"turn"`
}

// send types text and presses Enter. A worker that is idle is sent to only if it is still
// idle, so a status that moved on is refused with nothing typed. Only the kickoff sends this way.
func (c *Core) send(ctx context.Context, pane, text, status string) (sendReply, error) {
	params := map[string]any{"terminal": pane, "text": text}
	if status == "idle" {
		params["if"] = "idle"
	}
	var out sendReply
	return out, c.call(ctx, "terminal.send", params, &out)
}

const (
	appCommand = "herdr-app"
	// appDocument is the hc document in which the Herdr app keeps its spaces and tabs. Space
	// is the app's word for a workspace.
	appDocument = "herdr-app"
)

// appTimeout bounds one herdr-app command, so a window that hangs cannot stall the dispatcher.
const appTimeout = 10 * time.Second

// names returns the name the Herdr app window holds for every space and tab, by id. A window
// that is closed, or a document that is empty or unreadable, gives none, so every label reads "".
func (c *Core) names(ctx context.Context) map[string]string {
	var doc struct {
		Value *struct {
			Spaces []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
				Tabs []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"tabs"`
			} `json:"spaces"`
		} `json:"value"`
	}
	if err := c.call(ctx, "doc.get", map[string]any{"name": appDocument}, &doc); err != nil {
		c.record("doc.get", err.Error())
		return nil
	}
	names := map[string]string{}
	if doc.Value == nil {
		return names
	}
	for _, space := range doc.Value.Spaces {
		names[space.ID] = space.Name
		for _, tab := range space.Tabs {
			names[tab.ID] = tab.Name
		}
	}
	return names
}

// RenameWorkspace and RenameTab name a space and a tab in the Herdr app window. They never
// fail, because with the window closed there is nothing to name: the next Read still shows
// the old name, and the loop asks again.
func (c *Core) RenameWorkspace(ctx context.Context, id, label string) error {
	c.runApp(ctx, "space.rename", id, "--name", label)
	return nil
}

func (c *Core) RenameTab(ctx context.Context, id, label string) error {
	c.runApp(ctx, "tab.rename", id, "--name", label)
	return nil
}

// runApp runs one herdr-app command, best effort. A failure goes to the trace.
func (c *Core) runApp(ctx context.Context, command, id string, args ...string) {
	ctx, cancel := context.WithTimeout(ctx, appTimeout)
	defer cancel()
	argv := append([]string{"do", command, "--id", id}, args...)
	cmd := c.env.Command(ctx, c.app, argv...)
	cmd.WaitDelay = time.Second // kills a child that outlives the timeout holding the output pipe
	if out, err := cmd.CombinedOutput(); err != nil {
		c.record(c.app+" "+strings.Join(argv, " "), fmt.Sprintf("%v: %s", err, bytes.TrimSpace(out)))
	}
}

// CloseWorkspace removes the space's workers. It also closes the space in the app window when
// no terminal is left in it, so a space the user is working in stays open.
func (c *Core) CloseWorkspace(ctx context.Context, id string) error {
	emptied, err := c.closeWorkers(ctx, map[string]string{labelSpace: id})
	if err == nil && emptied {
		// Without --confirmed the app asks first.
		c.runApp(ctx, "space.close", id, "--confirmed", "true")
	}
	return err
}

func (c *Core) CloseTab(ctx context.Context, id string) error {
	_, err := c.closeWorkers(ctx, map[string]string{labelTab: id})
	return err
}

// closeWorkers kills and removes every worker terminal with labels. A space or tab holding
// none has nothing to close, which is not an error: the ids are derived, so one can be
// asked for before its first terminal starts. Terminals the user opened there are left alone.
// It reports whether it removed a worker and left no terminal at all behind.
func (c *Core) closeWorkers(ctx context.Context, labels map[string]string) (emptied bool, err error) {
	terminals, err := c.list(ctx, labels)
	if err != nil {
		return false, err
	}
	var errs []error
	var removed, others int
	for _, t := range terminals {
		if !t.startedByDispatch() {
			others++
			continue
		}
		if t.live() {
			if err := c.call(ctx, "terminal.kill", map[string]any{"terminal": t.Address}, nil); err != nil && !gone(err) {
				errs = append(errs, err)
				continue
			}
		}
		if err := c.call(ctx, "terminal.rm", map[string]any{"terminal": t.Address}, nil); err != nil && !gone(err) {
			errs = append(errs, err)
			continue
		}
		removed++
	}
	return removed > 0 && others == 0, errors.Join(errs...)
}

// gone reports whether err says the terminal is no longer there.
func gone(err error) bool {
	var refusal *Refusal
	return errors.As(err, &refusal) && refusal.Code == "agent_not_found"
}

// spaceOf is the id of the app space that shows tree.
func spaceOf(tree string) string { return "sp" + treekey.Digest("dispatch-space", tree, 32) }

func (c *Core) OpenWorkspace(ctx context.Context, w NewWorkspace) (string, error) {
	return spaceOf(w.Path), nil
}

func (c *Core) StartWorker(ctx context.Context, w Worker) (Started, error) {
	space := spaceOf(w.Dir)
	existing, err := c.list(ctx, map[string]string{labelSpace: space})
	if err != nil {
		return Started{}, err
	}
	var held *TerminalWithoutAgent
	for _, t := range existing {
		if !t.live() {
			continue
		}
		if _, running := c.runs(t); running {
			return Started{}, ErrSessionRunning
		}
		// Dispatch never kills a live worker terminal, because a racing launch's Claude may
		// still be loading in it. A person clears the one that never loads. A Claude anywhere
		// in the space still wins over a held terminal, so this only records the first one.
		if t.startedByDispatch() && held == nil {
			held = &TerminalWithoutAgent{Address: t.Address}
		}
	}
	if held != nil {
		return Started{}, held
	}
	program := w.Program
	exe, err := c.find(program)
	if err != nil {
		return Started{}, err
	}
	// Only a Claude lost to the core or the machine resumes, and only for the ticket that
	// owned the tree: a launch that took the lease fresh never resumes an earlier ticket's
	// session. terminal.list promises no order, so the terminal that finished last decides.
	var previous string
	var latest uint64
	for _, t := range existing {
		if t.startedByDispatch() && !t.live() && t.FinishedMS >= latest {
			latest, previous = t.FinishedMS, ""
			if slices.Contains(resumable, t.Reason) && !w.FreshLease {
				previous = t.Labels[LabelSession]
			}
		}
	}
	for _, t := range existing {
		if t.startedByDispatch() && !t.live() {
			if err := c.call(ctx, "terminal.rm", map[string]any{"terminal": t.Address}, nil); err != nil && !gone(err) {
				return Started{}, err
			}
		}
	}

	env := map[string]string{
		"PATH":                 strings.Join(append(slices.Clone(program.PathFirst), c.env.Get("PATH")), ":"),
		"AUTONOMOUS_WORKER":    "1",
		"DISPATCH_PLUGIN_ROOT": w.PluginRoot,
	}
	if c.dispatchBin != "" {
		env["DISPATCH_BIN"] = c.dispatchBin
	}
	maps.Copy(env, program.Env)
	labels := map[string]string{
		LabelTree:  w.Dir,
		labelSpace: space,
		labelTab:   "tb" + treekey.Digest("dispatch-tab", w.Dir, 32),
	}
	argv, session := program.StartArgv(), program.Session
	if resume, ok := program.ResumeArgv(previous); ok {
		argv, session = resume, previous
	}
	if session != "" {
		labels[LabelSession] = session
	}
	name := "dw-" + treekey.Digest("dispatch-terminal", w.Dir, 24)
	var run terminal
	err = c.call(ctx, "terminal.run", map[string]any{
		"argv":      append([]string{exe}, argv...),
		"cwd":       w.Dir,
		"env":       env,
		"name":      name,
		"labels":    labels,
		"no_parent": true,
		"no_notify": true,
	}, &run)
	var refusal *Refusal
	if errors.As(err, &refusal) && refusal.Code == "name_taken" {
		return Started{}, ErrSessionRunning
	}
	if err != nil {
		return Started{}, err
	}
	return Started{Pane: run.Address, Agent: name}, nil
}

// find is the absolute path of the program's executable, looked up in its shim directories
// and then on PATH, because terminal.run takes no command that is not a path.
func (c *Core) find(p Program) (string, error) { return findExecutable(c.env, p) }

// findExecutable is the absolute path of the program's agent executable, looked up in its shim
// directories and then on PATH.
func findExecutable(env environment.Environment, p Program) (string, error) {
	dirs := append(slices.Clone(p.PathFirst), filepath.SplitList(env.Get("PATH"))...)
	for _, dir := range dirs {
		path := filepath.Join(dir, p.AgentKind)
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 && filepath.IsAbs(path) {
			return path, nil
		}
	}
	return "", fmt.Errorf("no %s executable in the worker's PATH", p.AgentKind)
}

func (c *Core) Kickoff(ctx context.Context, pane, text string, startup Startup) error {
	return c.workLog.kickoff(ctx, coreKickoff{c: c, pane: pane}, text, startup)
}

// coreKickoff gives the kickoff driver Core's four operations on one terminal.
type coreKickoff struct {
	c    *Core
	pane string
}

func (k coreKickoff) read(ctx context.Context) (observed, error) {
	t, err := k.c.get(ctx, k.pane)
	if gone(err) {
		return observed{}, nil
	}
	if err != nil {
		return observed{}, err
	}
	_, agent := k.c.runs(t)
	return observed{Alive: t.live(), Agent: agent, Status: t.status(), Turn: t.Turn}, nil
}

func (k coreKickoff) screen(ctx context.Context, history int) string {
	return k.c.screen(ctx, k.pane, history)
}

func (k coreKickoff) press(ctx context.Context, keys []string) {
	names := make([]string, len(keys))
	for i, key := range keys {
		names[i] = strings.ToLower(key)
	}
	_ = k.c.call(ctx, "terminal.keys", map[string]any{"terminal": k.pane, "keys": names}, nil)
}

func (k coreKickoff) send(ctx context.Context, text, status string) (sent, error) {
	res, err := k.c.send(ctx, k.pane, text, status)
	var refusal *Refusal
	switch {
	case errors.As(err, &refusal) && refusal.Code == "status_mismatch":
		return sent{Outcome: sendNotStarted}, nil
	case err != nil:
		return sent{}, err
	case res.Started != nil && *res.Started:
		return sent{Outcome: sendStarted}, nil
	}
	return sent{Outcome: sendUnknown, Turn: res.Turn}, nil
}

// screen is the visible screen plus lines of history above it, joined on one line, or ""
// when it can't be read. A startup dialog is read with no history, so one already answered
// and scrolled off is never answered again.
func (c *Core) screen(ctx context.Context, pane string, history int) string {
	var read struct {
		Text string `json:"text"`
	}
	params := map[string]any{"terminal": pane}
	if history > 0 {
		params["lines"] = history
	}
	if err := c.call(ctx, "terminal.read", params, &read); err != nil {
		return ""
	}
	return oneLine(read.Text)
}

func (c *Core) get(ctx context.Context, pane string) (terminal, error) {
	var t terminal
	return t, c.call(ctx, "terminal.get", map[string]any{"terminal": pane}, &t)
}

// list returns every terminal, finished ones included, that carries all of labels.
func (c *Core) list(ctx context.Context, labels map[string]string) ([]terminal, error) {
	params := map[string]any{"all": true}
	if labels != nil {
		params["labels"] = labels
	}
	var out struct {
		Terminals []terminal `json:"terminals"`
	}
	if err := c.call(ctx, "terminal.list", params, &out); err != nil {
		return nil, err
	}
	return out.Terminals, nil
}

// call sends one JSON-RPC request to herdr-core through a short-lived `hc connect`.
func (c *Core) call(ctx context.Context, method string, params, result any) error {
	request, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return err
	}
	cmd := c.env.Command(ctx, c.hc, "connect")
	cmd.Stdin = bytes.NewReader(append(request, '\n'))
	cmd.WaitDelay = time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil && len(bytes.TrimSpace(out)) == 0 {
		return fmt.Errorf("while running %s connect for %s: %w: %s", c.hc, method, err, strings.TrimSpace(stderr.String()))
	}
	var reply struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
			Data    struct {
				Kind string `json:"kind"`
			} `json:"data"`
		} `json:"error"`
	}
	line, _, _ := bytes.Cut(bytes.TrimSpace(out), []byte("\n"))
	if err := json.Unmarshal(line, &reply); err != nil {
		return fmt.Errorf("while reading %s connect reply to %s: %w", c.hc, method, err)
	}
	if reply.Error != nil {
		return &Refusal{Code: refusalCode(reply.Error.Data.Kind), Message: reply.Error.Message, Command: "hc:" + method}
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(reply.Result, result); err != nil {
		return fmt.Errorf("while reading %s connect result of %s: %w", c.hc, method, err)
	}
	return nil
}

// refusalCode is the port's name for a refusal where it has one, and hc's own kind otherwise.
func refusalCode(kind string) string {
	switch kind {
	case "not_found":
		return "agent_not_found"
	case "blocked":
		return "agent_blocked"
	}
	return kind
}
