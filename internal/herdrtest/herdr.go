// Package herdrtest is an in-process stand-in for herdr that tracks
// workspaces, tabs, panes and agents and refuses what the real herdr refuses.
package herdrtest

import (
	"context"
	"fmt"
	"slices"
	"sync"

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
	lagging     map[string]string
	failures    map[string]error
	prompted    []string
	renamed     []string
	tabRenamed  []string
	closed      []string
	beforeClose func(workspace string)
}

// New returns an empty Herdr that records its calls in j.
func New(j *journal.Journal) *Herdr {
	return &Herdr{journal: j, lagging: map[string]string{}, failures: map[string]error{}}
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

// SetStatus changes the status of the agent in pane, which Read reports from
// then on. It ends any BlockAfterSnapshot.
func (h *Herdr) SetStatus(pane, status string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.lagging, pane)
	h.agent(pane, func(a *herdr.Agent) { a.Status = status })
}

// BlockAfterSnapshot blocks the agent in pane while Read keeps reporting the
// status it had, which is an agent that stopped to ask a question after
// dispatch looked. Prompt refuses it from then on. Unblock undoes it.
func (h *Herdr) BlockAfterSnapshot(pane string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.agent(pane, func(a *herdr.Agent) {
		if a.Status == "blocked" {
			return
		}
		h.lagging[pane] = a.Status
		a.Status = "blocked"
	})
}

// Unblock gives the agent in pane the status it had before BlockAfterSnapshot.
func (h *Herdr) Unblock(pane string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if shown, ok := h.lagging[pane]; ok {
		delete(h.lagging, pane)
		h.agent(pane, func(a *herdr.Agent) { a.Status = shown })
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

// HasWorkspace says whether herdr still shows the workspace.
func (h *Herdr) HasWorkspace(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.workspaceAt(id) >= 0
}

// Prompted lists what was typed into agents, as "<pane>|<text>".
func (h *Herdr) Prompted() []string { return h.log(&h.prompted) }

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
	defer h.mu.Unlock()
	s := herdr.Snapshot{
		Workspaces: slices.Clone(h.workspaces),
		Panes:      slices.Clone(h.panes),
		Agents:     slices.Clone(h.agents),
	}
	if h.failures[OpTabList] == nil {
		s.Tabs = slices.Clone(h.tabs)
	}
	for i, a := range s.Agents {
		if shown, ok := h.lagging[a.PaneID]; ok {
			s.Agents[i].Status = shown
		}
	}
	return s, nil
}

func (h *Herdr) Prompt(ctx context.Context, pane, text string) error {
	h.journal.Record("herdr", "prompt", pane, text)
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	const command = "cli:agent:prompt"
	i := slices.IndexFunc(h.agents, func(a herdr.Agent) bool { return a.PaneID == pane })
	switch {
	case i < 0:
		return &herdr.Refusal{Code: "agent_not_found", Message: fmt.Sprintf("agent target %s not found", pane), Command: command}
	case h.agents[i].Status == "blocked":
		return &herdr.Refusal{Code: "agent_blocked", Message: fmt.Sprintf("agent %s is blocked and requires interactive input", pane), Command: command}
	}
	h.prompted = append(h.prompted, pane+"|"+text)
	return nil
}

func (h *Herdr) RenameWorkspace(ctx context.Context, id, label string) error {
	h.journal.Record("herdr", "rename-workspace", id, label)
	if err := ctx.Err(); err != nil {
		return err
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
	defer h.mu.Unlock()
	if err := h.failures[OpCloseWorkspace]; err != nil {
		return err
	}
	i := h.workspaceAt(id)
	if i < 0 {
		return notFound("workspace", id, "cli:workspace:close")
	}
	h.workspaces = slices.Delete(h.workspaces, i, i+1)
	h.tabs = slices.DeleteFunc(h.tabs, func(t herdr.Tab) bool { return t.WorkspaceID == id })
	h.panes = slices.DeleteFunc(h.panes, func(p herdr.Pane) bool { return p.WorkspaceID == id })
	h.agents = slices.DeleteFunc(h.agents, func(a herdr.Agent) bool { return a.WorkspaceID == id })
	h.closed = append(h.closed, id)
	return nil
}

func (h *Herdr) CloseTab(ctx context.Context, id string) error {
	h.journal.Record("herdr", "close-tab", id)
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.failures[OpCloseTab]; err != nil {
		return err
	}
	i := h.tabAt(id)
	if i < 0 {
		return notFound("tab", id, "cli:tab:close")
	}
	h.tabs = slices.Delete(h.tabs, i, i+1)
	h.panes = slices.DeleteFunc(h.panes, func(p herdr.Pane) bool { return p.TabID == id })
	h.agents = slices.DeleteFunc(h.agents, func(a herdr.Agent) bool { return a.TabID == id })
	return nil
}

func notFound(kind, id, command string) error {
	return &herdr.Refusal{Code: kind + "_not_found", Message: fmt.Sprintf("%s %s not found", kind, id), Command: command}
}

func (h *Herdr) log(entries *[]string) []string {
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
