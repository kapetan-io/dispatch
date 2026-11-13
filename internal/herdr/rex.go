package herdr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/lock"
	"github.com/kapetan-io/dispatch/internal/treekey"
)

// Rex is the Herdr that drives a local Rex server through the rex command. Each repo's workers
// run in one Rex session, labeled with the folder name of the repo's main checkout, and each
// worker is a window in it holding one terminal block. Rex never starts or stops from here.
//
// The port's words map onto Rex like this. A workspace is the tree's window in its repo session,
// with the id `<session label>|<tree>`. A tab is that window, `<session id>/<window id>`. A pane
// is the block, `<session id>/<block id>`.
type Rex struct {
	workLog
	stateDir    string
	socket      string
	env         environment.Environment
	dispatchBin string
	kinds       []string
	deleteEmpty bool

	mu  sync.Mutex
	bin string
}

// NewRex returns a Herdr for Rex that keeps its structure lock, rex.lock, in stateDir.
func NewRex(stateDir string) *Rex {
	return &Rex{stateDir: stateDir, workLog: newWorkLog()}
}

// WithSocket names the server's socket. Unset, rex picks its server as it always does, from REX_SERVER.
func (r *Rex) WithSocket(path string) *Rex {
	r.socket = path
	return r
}

// WithEnv runs rex, and finds it, with env instead of the process's environment.
func (r *Rex) WithEnv(env environment.Environment) *Rex {
	r.env = env
	return r
}

// WithDispatchBin names the dispatch binary stamped as a started worker's DISPATCH_BIN.
func (r *Rex) WithDispatchBin(bin string) *Rex {
	r.dispatchBin = bin
	return r
}

// WithKinds names the programs a block may run to hold a session. A block running any other
// program is never an agent, so unset, Rex finds none.
func (r *Rex) WithKinds(kinds ...string) *Rex {
	r.kinds = kinds
	return r
}

// WithDeleteEmptySessions makes a close delete the repo session it just emptied. Unset, the emptied session stays.
func (r *Rex) WithDeleteEmptySessions(on bool) *Rex {
	r.deleteEmpty = on
	return r
}

// WithPace sets the unit of waiting between polls while a worker starts. The default is 1s.
func (r *Rex) WithPace(d time.Duration) *Rex {
	if d > 0 {
		r.pace = d
	}
	return r
}

// WithTrace appends every screen Kickoff reads to the file at path.
func (r *Rex) WithTrace(path string) *Rex {
	r.trace = path
	return r
}

var _ Herdr = (*Rex)(nil)

func (r *Rex) Multiplexer() string { return "Rex" }

const (
	terminalFlavor = "com.superlogical.terminal.shell"
	// instanceLen is how much of the server's instance id a worker label keeps.
	instanceLen = 8
	// clearWait bounds the Ctrl-U clearing of a typed kickoff when the call's own context has ended.
	clearWait = 3 * time.Second
)

// rexBlock is a block as session.view lists it.
type rexBlock struct {
	ID    string
	Label string
}

type rexWindow struct {
	ID     string
	Label  string
	Blocks []rexBlock
}

type rexSession struct {
	ID      string
	Label   string
	Windows []rexWindow
}

// workers lists the window's blocks that are dispatch workers, with their parsed labels.
func (w rexWindow) workers() (blocks []rexBlock, labels []workerLabel) {
	for _, b := range w.Blocks {
		if l, ok := parseWorkerLabel(b.Label); ok {
			blocks, labels = append(blocks, b), append(labels, l)
		}
	}
	return blocks, labels
}

// sessions reads every session with its windows and blocks.
func (r *Rex) sessions(ctx context.Context) ([]rexSession, error) {
	var list struct {
		Sessions []struct {
			ID    string `json:"session_id"`
			Label string `json:"label"`
		} `json:"sessions"`
	}
	if err := r.api(ctx, "", "session.list", nil, &list); err != nil {
		return nil, err
	}
	var out []rexSession
	for _, s := range list.Sessions {
		view, err := r.view(ctx, s.ID)
		if rexGone(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		view.Label = s.Label
		out = append(out, view)
	}
	return out, nil
}

// view reads one session's windows and the blocks in them.
func (r *Rex) view(ctx context.Context, session string) (rexSession, error) {
	var v struct {
		ID      string `json:"session_id"`
		Label   string `json:"label"`
		Windows []struct {
			ID     string `json:"window_id"`
			Label  string `json:"label"`
			Layers []struct {
				Blocks []struct {
					ID    string `json:"block_id"`
					Label string `json:"label"`
				} `json:"blocks"`
			} `json:"layers"`
		} `json:"windows"`
	}
	if err := r.api(ctx, session, "session.view", nil, &v); err != nil {
		return rexSession{}, err
	}
	out := rexSession{ID: v.ID, Label: v.Label}
	for _, w := range v.Windows {
		window := rexWindow{ID: w.ID, Label: w.Label}
		for _, layer := range w.Layers {
			for _, b := range layer.Blocks {
				window.Blocks = append(window.Blocks, rexBlock{ID: b.ID, Label: b.Label})
			}
		}
		out.Windows = append(out.Windows, window)
	}
	return out, nil
}

// repoSession is the session that holds the repo's workers: the one labeled label that already
// holds a dispatch block, else the first labeled label. Two repos whose main checkouts share a
// folder name share the session.
func repoSession(sessions []rexSession, label string) (rexSession, bool) {
	var first *rexSession
	for i, s := range sessions {
		if s.Label != label {
			continue
		}
		if first == nil {
			first = &sessions[i]
		}
		for _, w := range s.Windows {
			if blocks, _ := w.workers(); len(blocks) > 0 {
				return s, true
			}
		}
	}
	if first == nil {
		return rexSession{}, false
	}
	return *first, true
}

// rexProcess is what a block reports of its program.
type rexProcess struct {
	LastExit   *rexExit `json:"last_exit"`
	Foreground *struct {
		Argv0 string `json:"argv0"`
	} `json:"foreground"`
}

// rexExit is how a block's program ended.
type rexExit struct {
	Code  *int   `json:"exit_code"`
	Error string `json:"error"`
}

// text is the end in Rex's own words, "exit status 1", or "" when it reports none.
func (e rexExit) text() string {
	switch {
	case e.Error != "":
		return e.Error
	case e.Code != nil:
		return "exit status " + strconv.Itoa(*e.Code)
	}
	return ""
}

// kind is the agent kind the block's foreground program is, by the base name of its argv0.
func (r *Rex) kind(p rexProcess) (string, bool) {
	if p.LastExit != nil || p.Foreground == nil || p.Foreground.Argv0 == "" {
		return "", false
	}
	kind := filepath.Base(p.Foreground.Argv0)
	return kind, slices.Contains(r.kinds, kind)
}

func (r *Rex) process(ctx context.Context, session, block string) (rexProcess, error) {
	var p rexProcess
	return p, r.blockCall(ctx, session, block, "process", nil, &p)
}

// reported is a block's program status and when the program last reported it.
type reported struct {
	Status string
	At     uint64
}

// programStatus reads the status the block's program reported. The root record gives the
// status, but any blocked record, root or child, makes the worker blocked, since the protocol
// lets a child be blocked under a working root. Error, an unknown state or no record reads as
// unknown, because idle would let the kickoff type.
func (r *Rex) programStatus(ctx context.Context, session, block string) (reported, error) {
	var out struct {
		Records []struct {
			ID        string `json:"id"`
			State     string `json:"state"`
			UpdatedAt string `json:"updated_at"`
		} `json:"records"`
	}
	if err := r.blockCall(ctx, session, block, "program_status", nil, &out); err != nil {
		return reported{}, err
	}
	got := reported{Status: "unknown"}
	blocked := false
	for _, rec := range out.Records {
		blocked = blocked || rec.State == "blocked"
		if rec.ID != "" {
			continue
		}
		switch rec.State {
		case "idle", "done", "working":
			got.Status = rec.State
		}
		if at, err := time.Parse(time.RFC3339Nano, rec.UpdatedAt); err == nil {
			got.At = uint64(at.UnixNano())
		}
	}
	if blocked {
		got.Status = "blocked"
	}
	return got, nil
}

// rexPane is the pane id of a block, `<session id>/<block id>`.
func rexPane(session, block string) string { return session + "/" + block }

func splitRexPane(pane string) (session, block string, ok bool) {
	i := strings.LastIndex(pane, "/")
	if i <= 0 || i == len(pane)-1 {
		return "", "", false
	}
	return pane[:i], pane[i+1:], true
}

func (r *Rex) Read(ctx context.Context) (Snapshot, error) {
	sessions, err := r.sessions(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	var s Snapshot
	spaces := map[string]bool{}
	for _, session := range sessions {
		for _, window := range session.Windows {
			blocks, labels := window.workers()
			if len(blocks) == 0 {
				continue
			}
			tab := session.ID + "/" + window.ID
			for i, b := range blocks {
				space := rexWorkspaceID(session.Label, labels[i].Tree)
				if !spaces[space] {
					spaces[space] = true
					s.Workspaces = append(s.Workspaces, Workspace{ID: space, Label: workspaceLabelOf(window.Label), CheckoutPath: labels[i].Tree})
				}
				if !slices.ContainsFunc(s.Tabs, func(t Tab) bool { return t.ID == tab }) {
					_, tabLabel := SplitWindowLabel(window.Label)
					s.Tabs = append(s.Tabs, Tab{ID: tab, Label: tabLabel, WorkspaceID: space})
				}
				pane := rexPane(session.ID, b.ID)
				s.Panes = append(s.Panes, Pane{ID: pane, TabID: tab, WorkspaceID: space})
				proc, err := r.process(ctx, session.ID, b.ID)
				if rexGone(err) {
					continue
				}
				if err != nil {
					return Snapshot{}, err
				}
				kind, ok := r.kind(proc)
				if !ok {
					continue
				}
				got, err := r.programStatus(ctx, session.ID, b.ID)
				if rexGone(err) {
					continue
				}
				if err != nil {
					return Snapshot{}, err
				}
				s.Agents = append(s.Agents, Agent{Kind: kind, Status: got.Status, PaneID: pane, TabID: tab, WorkspaceID: space, Session: labels[i].Session})
			}
		}
	}
	return s, nil
}

func (r *Rex) AgentAt(ctx context.Context, pane string) (Agent, bool, error) {
	agent, _, found, err := r.observe(ctx, pane)
	return agent, found, err
}

// observe is the agent in pane, and when its program last reported. A pane that is not a worker's
// block, or whose program is not an agent of a known kind, holds no agent. It costs three calls.
func (r *Rex) observe(ctx context.Context, pane string) (agent Agent, at uint64, found bool, err error) {
	session, block, ok := splitRexPane(pane)
	if !ok {
		return Agent{}, 0, false, nil
	}
	view, err := r.view(ctx, session)
	if rexGone(err) {
		return Agent{}, 0, false, nil
	}
	if err != nil {
		return Agent{}, 0, false, err
	}
	for _, window := range view.Windows {
		for _, b := range window.Blocks {
			label, isWorker := parseWorkerLabel(b.Label)
			if b.ID != block || !isWorker {
				continue
			}
			proc, err := r.process(ctx, session, block)
			if rexGone(err) {
				return Agent{}, 0, false, nil
			}
			if err != nil {
				return Agent{}, 0, false, err
			}
			kind, running := r.kind(proc)
			if !running {
				return Agent{}, 0, false, nil
			}
			got, err := r.programStatus(ctx, session, block)
			if rexGone(err) {
				return Agent{}, 0, false, nil
			}
			if err != nil {
				return Agent{}, 0, false, err
			}
			return Agent{Kind: kind, Status: got.Status, PaneID: pane, TabID: session + "/" + window.ID, WorkspaceID: rexWorkspaceID(view.Label, label.Tree), Session: label.Session}, got.At, true, nil
		}
	}
	return Agent{}, 0, false, nil
}

// OpenWorkspace derives the workspace id of the checkout in its repo session and creates
// nothing. The window shows once a worker starts in it.
func (r *Rex) OpenWorkspace(ctx context.Context, w NewWorkspace) (string, error) {
	return rexWorkspaceID(filepath.Base(w.Repo), w.Path), nil
}

// withStructure runs fn holding the structure lock, the one lock every change to Rex sessions,
// windows or worker blocks takes. It waits on a held lock until ctx ends and never repeats a failed rex call.
func (r *Rex) withStructure(ctx context.Context, fn func() error) error {
	if err := os.MkdirAll(r.stateDir, 0o755); err != nil {
		return fmt.Errorf("while creating the state directory for rex.lock: %w", err)
	}
	held, err := lock.Wait(ctx, filepath.Join(r.stateDir, "rex.lock"))
	if err != nil {
		return err
	}
	defer func() { _ = held.Release() }()
	return fn()
}

func (r *Rex) RenameWorkspace(ctx context.Context, id, label string) error {
	repo, tree, ok := splitRexWorkspaceID(id)
	if !ok {
		return nil
	}
	sessions, err := r.sessions(ctx)
	if err != nil {
		return err
	}
	emoji, _ := SplitWindowLabel(label)
	for _, s := range sessions {
		if s.Label != repo {
			continue
		}
		for _, w := range s.Windows {
			if !holdsTree(w, tree) {
				continue
			}
			_, tab := SplitWindowLabel(w.Label)
			return r.setWindowLabel(ctx, s.ID, w.ID, JoinWindowLabel(emoji, tab))
		}
	}
	return nil
}

func (r *Rex) RenameTab(ctx context.Context, id, label string) error {
	session, window, ok := strings.Cut(id, "/")
	if !ok {
		return nil
	}
	view, err := r.view(ctx, session)
	if rexGone(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, w := range view.Windows {
		if w.ID == window {
			emoji, _ := SplitWindowLabel(w.Label)
			return r.setWindowLabel(ctx, session, window, JoinWindowLabel(emoji, label))
		}
	}
	return nil
}

func (r *Rex) setWindowLabel(ctx context.Context, session, window, label string) error {
	err := r.api(ctx, session, "session.set_window_label", map[string]any{"session_id": session, "window_id": window, "label": clipLabel(label)}, nil)
	if rexGone(err) {
		return nil
	}
	return err
}

// holdsTree reports whether the window holds a worker block of tree, compared as treekey.Of does.
func holdsTree(w rexWindow, tree string) bool {
	_, labels := w.workers()
	return slices.ContainsFunc(labels, func(l workerLabel) bool { return treekey.Of(l.Tree) == treekey.Of(tree) })
}

func (r *Rex) CloseWorkspace(ctx context.Context, id string) error {
	repo, tree, ok := splitRexWorkspaceID(id)
	if !ok {
		return nil
	}
	return r.withStructure(ctx, func() error {
		sessions, err := r.sessions(ctx)
		if err != nil {
			return err
		}
		var errs []error
		for _, s := range sessions {
			if s.Label != repo {
				continue
			}
			errs = append(errs, r.closeWorkers(ctx, s, func(_ rexWindow, l workerLabel) bool { return treekey.Of(l.Tree) == treekey.Of(tree) }))
		}
		return errors.Join(errs...)
	})
}

func (r *Rex) CloseTab(ctx context.Context, id string) error {
	session, window, ok := strings.Cut(id, "/")
	if !ok {
		return nil
	}
	return r.withStructure(ctx, func() error {
		view, err := r.view(ctx, session)
		if rexGone(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return r.closeWorkers(ctx, view, func(w rexWindow, _ workerLabel) bool { return w.ID == window })
	})
}

// closeWorkers closes the session's worker blocks that match. A window left empty closes with
// its last block, and a block already gone counts as closed. Blocks the user opened are left alone.
// With delete_empty_sessions on, a close that removed a block destroys the session when no block is left.
func (r *Rex) closeWorkers(ctx context.Context, s rexSession, match func(rexWindow, workerLabel) bool) error {
	var errs []error
	closed := 0
	for _, w := range s.Windows {
		blocks, labels := w.workers()
		for i, b := range blocks {
			if !match(w, labels[i]) {
				continue
			}
			removed, err := r.closeBlock(ctx, s.ID, b.ID)
			if err != nil {
				errs = append(errs, err)
			}
			if removed {
				closed++
			}
		}
	}
	if len(errs) > 0 || closed == 0 || !r.deleteEmpty {
		return errors.Join(errs...)
	}
	return r.destroyIfEmpty(ctx, s.ID)
}

// destroyIfEmpty destroys the session when it holds no block, detached ones included. A tab the
// user adds between the read and the destroy is lost, because session.destroy cannot be made conditional.
func (r *Rex) destroyIfEmpty(ctx context.Context, session string) error {
	var listed struct {
		Blocks []struct{} `json:"blocks"`
	}
	err := r.api(ctx, session, "session.list_blocks", map[string]any{"session_id": session}, &listed)
	if rexGone(err) {
		return nil
	}
	if err != nil || len(listed.Blocks) > 0 {
		return err
	}
	err = r.api(ctx, session, "session.destroy", map[string]any{"session_id": session}, nil)
	if rexGone(err) {
		return nil
	}
	return err
}

// closeBlock closes one block and reports whether this call removed it, which a block already gone does not.
func (r *Rex) closeBlock(ctx context.Context, session, block string) (bool, error) {
	err := r.api(ctx, session, "block.close", map[string]any{"session_id": session, "block_id": block}, nil)
	if rexGone(err) {
		return false, nil
	}
	return err == nil, err
}
