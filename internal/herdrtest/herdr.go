// Package herdrtest is an in-process stand-in for herdr that tracks
// workspaces, tabs, panes and agents and refuses what the real herdr refuses.
package herdrtest

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/journal"
)

// The operations Fail can break.
const (
	OpRenameWorkspace = "rename-workspace"
	OpRenameTab       = "rename-tab"
	OpCloseWorkspace  = "close-workspace"
	OpCloseTab        = "close-tab"
	// OpTabList makes Read return no tabs, as the adapter does when herdr can't
	// list them.
	OpTabList = "tab-list"
	// OpRead makes Read fail, as it does when the herdr server is down.
	OpRead = "read"
	// OpAgentAt makes AgentAt fail, as it does when the herdr server is down.
	OpAgentAt       = "agent-at"
	OpOpenWorkspace = "open-workspace"
	// OpStartTab fails StartWorker before any tab exists. OpStartAgent fails it
	// after the tab came up, with the program not running in it.
	OpStartTab   = "start-tab"
	OpStartAgent = "start-agent"
	// OpKickoff fails Kickoff with the error given, such as
	// herdr.ErrKickoffUnconfirmed.
	OpKickoff = "kickoff"
	// OpStream makes Changes end at once with the error given, as a stream does when the multiplexer is down.
	OpStream = "stream"
)

// Herdr is a herdr.Herdr backed by memory. Its refusals come only from its
// state, such as a blocked agent or an unknown workspace. Failures that don't
// come from state go through Fail, and never change what it accepts.
type Herdr struct {
	journal *journal.Journal

	mu          sync.Mutex
	workspaces  []herdr.Workspace
	tabs        []herdr.Tab
	panes       []herdr.Pane
	agents      []herdr.Agent
	failures    map[string]error
	renamed     []string
	tabRenamed  []string
	closed      []string
	opened      []herdr.NewWorkspace
	workers     []herdr.Worker
	kicked      []string
	startups    []herdr.Startup
	names       map[string]string // pane to the name of its agent
	beforeClose func(workspace string)
	slowRename  time.Duration     // how long the next RenameWorkspace waits before it lands
	kinds       []string          // the agent kinds Read keeps, or every kind when unset
	sinks       []*sink           // the streams Changes holds open
	live        int               // how many streams have named their first panes
	later       map[string]string // pane to the status Read applies, and streams, after it took its snapshot
}

// sink is one stream Changes holds open.
type sink struct {
	changed func(pane string)
	drop    chan error
}

// New returns an empty Herdr that records its calls in j.
func New(j *journal.Journal) *Herdr {
	return &Herdr{journal: j, failures: map[string]error{}, names: map[string]string{}, later: map[string]string{}}
}

// AcceptKinds makes Read leave out agents of every other kind, as the real adapter does
// when the root builds it with the kinds of the registered harnesses.
func (h *Herdr) AcceptKinds(kinds ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.kinds = kinds
}

// AddWorkspace puts a workspace in herdr exactly as given.
func (h *Herdr) AddWorkspace(w herdr.Workspace) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.workspaces = append(h.workspaces, w)
}

// AddTab puts a tab in herdr exactly as given.
func (h *Herdr) AddTab(t herdr.Tab) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tabs = append(h.tabs, t)
}

// AddPane puts a pane in herdr exactly as given.
func (h *Herdr) AddPane(p herdr.Pane) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.panes = append(h.panes, p)
}

// AddAgent puts an agent in herdr exactly as given.
func (h *Herdr) AddAgent(a herdr.Agent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.agents = append(h.agents, a)
}

// NameAgent gives the agent in pane the name herdr lists it under, which
// StartWorker avoids when it names a new agent.
func (h *Herdr) NameAgent(pane, name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.names[pane] = name
}

// SetStatus changes the status of the agent in pane, which Read reports from
// then on.
func (h *Herdr) SetStatus(pane, status string) {
	h.mu.Lock()
	h.agent(pane, func(a *herdr.Agent) { a.Status = status })
	h.mu.Unlock()
	h.notify(pane)
}

// SetStatusAfterSnapshot changes the status of the agent in pane right after the next Read took its
// snapshot, so that Read reports the old status while the stream names the pane and AgentAt answers with the new.
func (h *Herdr) SetStatusAfterSnapshot(pane, status string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.later[pane] = status
}

// ExitAgent removes the agent in pane and keeps the pane, as a Claude that exits leaves its block.
func (h *Herdr) ExitAgent(pane string) {
	h.mu.Lock()
	h.agents = slices.DeleteFunc(h.agents, func(a herdr.Agent) bool { return a.PaneID == pane })
	h.mu.Unlock()
	h.notify(pane)
}

// Changed makes the stream name pane, whether or not it holds a worker or exists.
func (h *Herdr) Changed(pane string) { h.notify(pane) }

// DropStream ends every open stream, as a multiplexer that disconnects does.
func (h *Herdr) DropStream(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, sk := range h.sinks {
		select {
		case sk.drop <- err:
		default:
		}
	}
}

// Streams is how many times a stream went live: it named the panes of the agents then in herdr and
// started to report changes.
func (h *Herdr) Streams() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.live
}

// notify names pane to every open stream, before the call that changed it returns.
func (h *Herdr) notify(pane string) {
	h.mu.Lock()
	sinks := slices.Clone(h.sinks)
	h.mu.Unlock()
	for _, sk := range sinks {
		sk.changed(pane)
	}
}

// Streaming is a Herdr that also reports its changes, as the Rex adapter does.
type Streaming struct{ *Herdr }

var _ herdr.Stream = (*Streaming)(nil)

// Streaming returns h as a multiplexer with a stream. A test that wants none uses h itself.
func (h *Herdr) Streaming() *Streaming { return &Streaming{h} }

// Changes names the pane of every later status change, agent exit and closed tab or workspace, in
// the order they happen, and first names each pane that holds an agent, as a live Rex stream does.
func (s *Streaming) Changes(ctx context.Context, changed func(pane string)) error {
	h := s.Herdr
	h.journal.Record("herdr", "changes")
	h.mu.Lock()
	if err := h.failures[OpStream]; err != nil {
		h.mu.Unlock()
		return err
	}
	sk := &sink{changed: changed, drop: make(chan error, 1)}
	h.sinks = append(h.sinks, sk)
	var panes []string
	for _, a := range h.agents {
		panes = append(panes, a.PaneID)
	}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.sinks = slices.DeleteFunc(h.sinks, func(x *sink) bool { return x == sk })
	}()
	for _, pane := range panes {
		changed(pane)
	}
	h.mu.Lock()
	h.live++
	h.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil
	case err := <-sk.drop:
		return err
	}
}

// SetSession changes the Claude session id of the agent in pane, as a relaunch
// does.
func (h *Herdr) SetSession(pane, session string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.agent(pane, func(a *herdr.Agent) { a.Session = session })
}

// SetWorkspaceLabel changes the label Read reports for a workspace.
func (h *Herdr) SetWorkspaceLabel(id, label string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if i := h.workspaceAt(id); i >= 0 {
		h.workspaces[i].Label = label
	}
}

// SetTabLabel changes the label Read reports for a tab.
func (h *Herdr) SetTabLabel(id, label string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if i := h.tabAt(id); i >= 0 {
		h.tabs[i].Label = label
	}
}

// Fail makes every call to op fail with err until Fail(op, nil).
func (h *Herdr) Fail(op string, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err == nil {
		delete(h.failures, op)
		return
	}
	h.failures[op] = err
}

// BeforeCloseWorkspace runs f with the workspace id at the start of every
// accepted CloseWorkspace, so a test can act while the worker is still running.
func (h *Herdr) BeforeCloseWorkspace(f func(workspace string)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.beforeClose = f
}

// Opened lists the workspaces OpenWorkspace was asked to open, one for each
// call that opened a new one.
func (h *Herdr) Opened() []herdr.NewWorkspace { return logged(h, &h.opened) }

// Started lists the workers StartWorker brought up, as it was asked to.
func (h *Herdr) Started() []herdr.Worker { return logged(h, &h.workers) }

// Kicked lists what Kickoff typed into agents, as "<pane>|<text>".
func (h *Herdr) Kicked() []string { return h.log(&h.kicked) }

// KickedWith lists the startup screens each Kickoff was given, in the order of Kicked.
func (h *Herdr) KickedWith() []herdr.Startup { return logged(h, &h.startups) }

// HasWorkspace says whether herdr still shows the workspace.
func (h *Herdr) HasWorkspace(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.workspaceAt(id) >= 0
}

// WorkspaceRenames lists the accepted workspace renames, as "<workspace>|<label>".
func (h *Herdr) WorkspaceRenames() []string { return h.log(&h.renamed) }

// TabRenames lists the accepted tab renames, as "<tab>|<label>".
func (h *Herdr) TabRenames() []string { return h.log(&h.tabRenamed) }

// ClosedWorkspaces lists the workspaces that were closed.
func (h *Herdr) ClosedWorkspaces() []string { return h.log(&h.closed) }

func (h *Herdr) Read(ctx context.Context) (herdr.Snapshot, error) {
	h.journal.Record("herdr", "read")
	if err := ctx.Err(); err != nil {
		return herdr.Snapshot{}, err
	}
	h.mu.Lock()
	if err := h.failures[OpRead]; err != nil {
		h.mu.Unlock()
		return herdr.Snapshot{}, err
	}
	s := herdr.Snapshot{
		Workspaces: slices.Clone(h.workspaces),
		Panes:      slices.Clone(h.panes),
		Agents:     slices.Clone(h.agents),
	}
	if h.failures[OpTabList] == nil {
		s.Tabs = slices.Clone(h.tabs)
	}
	if h.kinds != nil {
		s.Agents = slices.DeleteFunc(s.Agents, func(a herdr.Agent) bool { return !slices.Contains(h.kinds, a.Kind) })
	}
	later := h.later
	h.later = map[string]string{}
	for pane, status := range later {
		h.agent(pane, func(a *herdr.Agent) { a.Status = status })
	}
	h.mu.Unlock()
	for pane := range later {
		h.notify(pane)
	}
	return s, nil
}

func (h *Herdr) AgentAt(ctx context.Context, pane string) (herdr.Agent, bool, error) {
	h.journal.Record("herdr", "agent-at", pane)
	if err := ctx.Err(); err != nil {
		return herdr.Agent{}, false, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.failures[OpAgentAt]; err != nil {
		return herdr.Agent{}, false, err
	}
	i := slices.IndexFunc(h.agents, func(a herdr.Agent) bool { return a.PaneID == pane })
	if i < 0 {
		return herdr.Agent{}, false, nil
	}
	return h.agents[i], true, nil
}

// SlowNextRename delays the next RenameWorkspace so a test can finish a cycle while a
// rename is still in flight. The call is journaled when it starts and listed by
// WorkspaceRenames only once it lands.
func (h *Herdr) SlowNextRename(delay time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.slowRename = delay
}

func (h *Herdr) RenameWorkspace(ctx context.Context, id, label string) error {
	h.journal.Record("herdr", "rename-workspace", id, label)
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.Lock()
	delay := h.slowRename
	h.slowRename = 0
	h.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.failures[OpRenameWorkspace]; err != nil {
		return err
	}
	i := h.workspaceAt(id)
	if i < 0 {
		return notFound("workspace", id, "cli:workspace:rename")
	}
	h.workspaces[i].Label = label
	h.renamed = append(h.renamed, id+"|"+label)
	return nil
}

func (h *Herdr) RenameTab(ctx context.Context, id, label string) error {
	h.journal.Record("herdr", "rename-tab", id, label)
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.failures[OpRenameTab]; err != nil {
		return err
	}
	i := h.tabAt(id)
	if i < 0 {
		return notFound("tab", id, "cli:tab:rename")
	}
	h.tabs[i].Label = label
	h.tabRenamed = append(h.tabRenamed, id+"|"+label)
	return nil
}

func (h *Herdr) CloseWorkspace(ctx context.Context, id string) error {
	h.journal.Record("herdr", "close-workspace", id)
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.Lock()
	hook := h.beforeClose
	h.mu.Unlock()
	if hook != nil {
		hook(id)
	}

	h.mu.Lock()
	if err := h.failures[OpCloseWorkspace]; err != nil {
		h.mu.Unlock()
		return err
	}
	i := h.workspaceAt(id)
	if i < 0 {
		h.mu.Unlock()
		return notFound("workspace", id, "cli:workspace:close")
	}
	var gone []string
	for _, p := range h.panes {
		if p.WorkspaceID == id {
			gone = append(gone, p.ID)
		}
	}
	h.workspaces = slices.Delete(h.workspaces, i, i+1)
	h.tabs = slices.DeleteFunc(h.tabs, func(t herdr.Tab) bool { return t.WorkspaceID == id })
	h.panes = slices.DeleteFunc(h.panes, func(p herdr.Pane) bool { return p.WorkspaceID == id })
	h.agents = slices.DeleteFunc(h.agents, func(a herdr.Agent) bool { return a.WorkspaceID == id })
	h.closed = append(h.closed, id)
	h.mu.Unlock()
	for _, pane := range gone {
		h.notify(pane)
	}
	return nil
}

func (h *Herdr) CloseTab(ctx context.Context, id string) error {
	h.journal.Record("herdr", "close-tab", id)
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.Lock()
	if err := h.failures[OpCloseTab]; err != nil {
		h.mu.Unlock()
		return err
	}
	i := h.tabAt(id)
	if i < 0 {
		h.mu.Unlock()
		return notFound("tab", id, "cli:tab:close")
	}
	var gone []string
	for _, p := range h.panes {
		if p.TabID == id {
			gone = append(gone, p.ID)
		}
	}
	h.tabs = slices.Delete(h.tabs, i, i+1)
	h.panes = slices.DeleteFunc(h.panes, func(p herdr.Pane) bool { return p.TabID == id })
	h.agents = slices.DeleteFunc(h.agents, func(a herdr.Agent) bool { return a.TabID == id })
	h.mu.Unlock()
	for _, pane := range gone {
		h.notify(pane)
	}
	return nil
}

func notFound(kind, id, command string) error {
	return &herdr.Refusal{Code: kind + "_not_found", Message: fmt.Sprintf("%s %s not found", kind, id), Command: command}
}

func (h *Herdr) log(entries *[]string) []string { return logged(h, entries) }

func logged[T any](h *Herdr, entries *[]T) []T {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(*entries)
}

func (h *Herdr) workspaceAt(id string) int {
	return slices.IndexFunc(h.workspaces, func(w herdr.Workspace) bool { return w.ID == id })
}

func (h *Herdr) tabAt(id string) int {
	return slices.IndexFunc(h.tabs, func(t herdr.Tab) bool { return t.ID == id })
}

func (h *Herdr) agent(pane string, change func(*herdr.Agent)) {
	for i := range h.agents {
		if h.agents[i].PaneID == pane {
			change(&h.agents[i])
		}
	}
}

var _ herdr.Herdr = (*Herdr)(nil)
