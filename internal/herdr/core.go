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
	env         environment.Environment
	dispatchBin string
	kinds       []string
}

// NewCore returns a Herdr that runs hc.
func NewCore(hc string) *Core {
	return &Core{hc: hc, workLog: newWorkLog()}
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

// WithTrace appends every screen Kickoff reads to the file at path.
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
	Labels     map[string]string `json:"labels"`
	Status     *terminalStatus   `json:"status"`
	Foreground *struct {
		Argv []string `json:"argv"`
	} `json:"foreground"`
}

type terminalStatus struct {
	State string `json:"state"`
	Kind  string `json:"kind"`
}

// reasonServerStop is why herdr-core ends a terminal when the core itself stops.
const reasonServerStop = "server_stop"

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
	spaces, tabs := map[string]bool{}, map[string]bool{}
	for _, t := range terminals {
		if !t.startedByDispatch() {
			continue
		}
		space, tab := t.Labels[labelSpace], t.Labels[labelTab]
		if !spaces[space] {
			spaces[space] = true
			s.Workspaces = append(s.Workspaces, Workspace{ID: space, CheckoutPath: t.Labels[LabelTree]})
		}
		if !tabs[tab] {
			tabs[tab] = true
			s.Tabs = append(s.Tabs, Tab{ID: tab, WorkspaceID: space})
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

func (c *Core) Prompt(ctx context.Context, pane, text string) error {
	t, err := c.get(ctx, pane)
	if err != nil {
		return err
	}
	agent, ok := c.agent(t)
	if !ok {
		return &Refusal{Code: "agent_not_found", Message: "no worker session in terminal " + pane, Command: "hc:terminal.send"}
	}
	switch agent.Status {
	case "unknown":
		return ErrStatusUnknown
	case "blocked":
		return &Refusal{Code: "agent_blocked", Message: "terminal " + pane + " is blocked and requires interactive input", Command: "hc:terminal.send"}
	}
	_, err = c.send(ctx, pane, text, agent.Status)
	return err
}

// sendReply is what hc says of a send.
type sendReply struct {
	Started *bool `json:"started"`
}

// send types text and presses Enter. A worker that is idle is sent to only if it is still
// idle, so a status that moved on is refused with nothing typed.
func (c *Core) send(ctx context.Context, pane, text, status string) (sendReply, error) {
	params := map[string]any{"terminal": pane, "text": text}
	if status == "idle" {
		params["if"] = "idle"
	}
	var out sendReply
	return out, c.call(ctx, "terminal.send", params, &out)
}

// RenameWorkspace and RenameTab leave the names of workspaces and tabs to the Herdr app.
func (c *Core) RenameWorkspace(ctx context.Context, id, label string) error { return nil }

func (c *Core) RenameTab(ctx context.Context, id, label string) error { return nil }

func (c *Core) CloseWorkspace(ctx context.Context, id string) error {
	return c.closeWorkers(ctx, map[string]string{labelSpace: id})
}

func (c *Core) CloseTab(ctx context.Context, id string) error {
	return c.closeWorkers(ctx, map[string]string{labelTab: id})
}

// closeWorkers kills and removes every worker terminal with labels. A space or tab holding
// none has nothing to close, which is not an error: the ids are derived, so one can be
// asked for before its first terminal starts. Terminals the user opened there are left alone.
func (c *Core) closeWorkers(ctx context.Context, labels map[string]string) error {
	terminals, err := c.list(ctx, labels)
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range terminals {
		if !t.startedByDispatch() {
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
		}
	}
	return errors.Join(errs...)
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
	for _, t := range existing {
		if !t.live() {
			continue
		}
		// A live terminal that carries the tree is nearly always a worker whose Claude is
		// still loading, and killing it would let a racing launch kill the first one's.
		if _, running := c.runs(t); running || t.startedByDispatch() {
			return Started{}, ErrSessionRunning
		}
	}
	program := w.Program
	exe, err := c.find(program)
	if err != nil {
		return Started{}, err
	}
	// Only a core restart resumes: an exited or killed Claude crashed or was quit. The
	// newest finished terminal decides, and every one is removed before the new run.
	var previous string
	for _, t := range existing {
		if t.startedByDispatch() && !t.live() {
			previous = ""
			if t.Reason == reasonServerStop {
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
func (c *Core) find(p Program) (string, error) {
	dirs := append(slices.Clone(p.PathFirst), filepath.SplitList(c.env.Get("PATH"))...)
	for _, dir := range dirs {
		path := filepath.Join(dir, p.AgentKind)
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 && filepath.IsAbs(path) {
			return path, nil
		}
	}
	return "", fmt.Errorf("no %s executable in the worker's PATH", p.AgentKind)
}

func (c *Core) Kickoff(ctx context.Context, pane, text string, startup Startup) error {
	sightings := make([]int, len(startup.Answers))
	var t terminal
	ready := false
	for range kickoffChecks {
		if ctx.Err() != nil {
			return ErrKickoffUnconfirmed
		}
		var err error
		t, err = c.get(ctx, pane)
		if gone(err) {
			return ErrKickoffUnconfirmed
		}
		if err == nil && !t.live() {
			return ErrKickoffUnconfirmed
		}
		if err == nil && t.live() {
			if _, ok := c.runs(t); ok {
				switch t.status() {
				case "working":
					return nil
				case "blocked":
					return ErrKickoffUnconfirmed
				case "idle", "done":
					ready = true
				default:
					c.answer(ctx, pane, startup, sightings)
				}
			}
		}
		if ready {
			break
		}
		c.sleep(ctx, c.pace)
	}
	if !ready {
		return ErrKickoffUnconfirmed
	}
	for try := 1; try <= kickoffTries; try++ {
		c.record("typing", fmt.Sprintf("%s (try %d)", text, try))
		status := t.status()
		res, err := c.send(ctx, pane, text, status)
		if err != nil {
			var refusal *Refusal
			if !errors.As(err, &refusal) || refusal.Code != "status_mismatch" {
				return ErrKickoffUnconfirmed
			}
			// The refusal proves nothing was typed, so the worker leaving idle is no confirmation.
			// Another try is safe unless the worker now needs a person or says nothing.
			t, err = c.get(ctx, pane)
			if err != nil || !t.live() {
				return ErrKickoffUnconfirmed
			}
			if now := t.status(); now == "blocked" || now == "unknown" {
				return ErrKickoffUnconfirmed
			}
			continue
		}
		if res.Started != nil && *res.Started {
			return nil
		}
		// hc could not tell whether the text started a turn, so give the status time to move before judging.
		t, err = c.awaitLeft(ctx, pane, status)
		if err != nil || !t.live() {
			return ErrKickoffUnconfirmed
		}
		switch now := t.status(); {
		case now == "working" || now == "blocked" || now == "done":
			return nil
		case now == "idle":
			// Still at the prompt: press Enter if the text sits in the input box, else retype.
			if strings.Contains(c.screen(ctx, pane), probeOf(text)) {
				_ = c.call(ctx, "terminal.keys", map[string]any{"terminal": pane, "keys": []string{"enter"}}, nil)
				if t, err = c.awaitLeft(ctx, pane, "idle"); err != nil || !t.live() {
					return ErrKickoffUnconfirmed
				}
				if now := t.status(); now == "working" || now == "blocked" || now == "done" {
					return nil
				}
				return ErrKickoffUnconfirmed
			}
		default:
			return ErrKickoffUnconfirmed
		}
	}
	return ErrKickoffUnconfirmed
}

// awaitLeft reads the terminal until its status is no longer from, for up to kickoffWaits reads,
// and returns the last read.
func (c *Core) awaitLeft(ctx context.Context, pane, from string) (terminal, error) {
	var t terminal
	var err error
	for range kickoffWaits {
		if t, err = c.get(ctx, pane); err != nil || !t.live() || t.status() != from {
			return t, err
		}
		c.sleep(ctx, c.pace)
	}
	return t, err
}

// answer sends the next try for the startup dialog the screen shows, and nothing when it
// shows none.
func (c *Core) answer(ctx context.Context, pane string, startup Startup, sightings []int) {
	screen := c.screen(ctx, pane)
	c.record("dialogs", screen)
	try, ok := startup.Answer(screen, sightings)
	if !ok {
		return
	}
	for _, keys := range try {
		names := make([]string, len(keys))
		for i, key := range keys {
			names[i] = strings.ToLower(key)
		}
		_ = c.call(ctx, "terminal.keys", map[string]any{"terminal": pane, "keys": names}, nil)
	}
}

// screen is the last 30 lines of the terminal, joined on one line, or "" when it can't be read.
func (c *Core) screen(ctx context.Context, pane string) string {
	var read struct {
		Text string `json:"text"`
	}
	if err := c.call(ctx, "terminal.read", map[string]any{"terminal": pane, "lines": 30}, &read); err != nil {
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
