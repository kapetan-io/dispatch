package herdrtest

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/kapetan-io/dispatch/internal/herdr"
)

func (h *Herdr) OpenWorkspace(ctx context.Context, w herdr.NewWorkspace) (string, error) {
	h.journal.Record("herdr", "open-workspace", w.Path, w.Label)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.failures[OpOpenWorkspace]; err != nil {
		return "", err
	}
	for _, ws := range h.workspaces {
		if ws.CheckoutPath == w.Path {
			return ws.ID, nil
		}
	}
	// herdr nests a worktree's workspace under one for the repo it belongs to.
	if !slices.ContainsFunc(h.workspaces, func(ws herdr.Workspace) bool { return ws.CheckoutPath == w.Repo }) {
		h.openBare(filepath.Base(w.Repo), w.Repo, "1")
	}
	// herdr opens a worktree with a tab of its own that holds no agent.
	id := h.openBare(w.Label, w.Path, w.Label)
	h.opened = append(h.opened, w)
	return id, nil
}

// openBare adds a workspace showing path, with one tab and one pane that hold
// no agent.
func (h *Herdr) openBare(label, path, tab string) string {
	id := "w" + strconv.Itoa(h.nextWorkspace())
	h.workspaces = append(h.workspaces, herdr.Workspace{ID: id, Label: label, CheckoutPath: path})
	h.tabs = append(h.tabs, herdr.Tab{ID: id + ":t1", Label: tab, WorkspaceID: id})
	h.panes = append(h.panes, herdr.Pane{ID: id + ":p1", TabID: id + ":t1", WorkspaceID: id})
	return id
}

func (h *Herdr) StartWorker(ctx context.Context, w herdr.Worker) (herdr.Started, error) {
	h.journal.Record("herdr", "start-worker", w.Workspace, w.Name)
	if err := ctx.Err(); err != nil {
		return herdr.Started{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.workspaceAt(w.Workspace) < 0 {
		return herdr.Started{}, fmt.Errorf("herdr tab create failed in %s: %w", w.Workspace, notFound("workspace", w.Workspace, "cli:tab:create"))
	}
	if err := h.failures[OpStartTab]; err != nil {
		return herdr.Started{}, err
	}
	n := 1
	for _, t := range h.tabs {
		if t.WorkspaceID == w.Workspace {
			n = max(n, tabNumber(t.ID)+1)
		}
	}
	tab := fmt.Sprintf("%s:t%d", w.Workspace, n)
	pane := fmt.Sprintf("%s:p%d", w.Workspace, n)
	h.tabs = append(h.tabs, herdr.Tab{ID: tab, Label: w.Label, WorkspaceID: w.Workspace})
	h.panes = append(h.panes, herdr.Pane{ID: pane, TabID: tab, WorkspaceID: w.Workspace})
	h.dropAgentlessTabs(w.Workspace, tab)
	if err := h.failures[OpStartAgent]; err != nil {
		return herdr.Started{Pane: pane}, err
	}
	name := w.Name
	for i := 1; slices.Contains(h.agentNames(), name); i++ {
		name = fmt.Sprintf("%s-r%d", w.Name, i)
	}
	h.agents = append(h.agents, herdr.Agent{Kind: "claude", Status: "idle", PaneID: pane, TabID: tab, WorkspaceID: w.Workspace})
	h.names[pane] = name
	h.workers = append(h.workers, w)
	return herdr.Started{Pane: pane, Agent: name}, nil
}

func (h *Herdr) Kickoff(ctx context.Context, pane, text string) error {
	h.journal.Record("herdr", "kickoff", pane, text)
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.failures[OpKickoff]; err != nil {
		return err
	}
	if !slices.ContainsFunc(h.agents, func(a herdr.Agent) bool { return a.PaneID == pane }) {
		return herdr.ErrKickoffUnconfirmed
	}
	h.agent(pane, func(a *herdr.Agent) {
		if a.Status == "idle" {
			a.Status = "working"
		}
	})
	h.kicked = append(h.kicked, pane+"|"+text)
	return nil
}

// dropAgentlessTabs closes the tabs of a workspace that hold no agent, except
// keep, which is the new worker's tab and does not hold its agent yet.
func (h *Herdr) dropAgentlessTabs(workspace, keep string) {
	hasAgent := func(tab string) bool {
		return slices.ContainsFunc(h.agents, func(a herdr.Agent) bool { return a.TabID == tab })
	}
	var gone []string
	for _, t := range h.tabs {
		if t.WorkspaceID == workspace && t.ID != keep && !hasAgent(t.ID) {
			gone = append(gone, t.ID)
		}
	}
	h.tabs = slices.DeleteFunc(h.tabs, func(t herdr.Tab) bool { return slices.Contains(gone, t.ID) })
	h.panes = slices.DeleteFunc(h.panes, func(p herdr.Pane) bool { return slices.Contains(gone, p.TabID) })
}

func (h *Herdr) agentNames() []string {
	names := make([]string, 0, len(h.names))
	for pane, name := range h.names {
		if slices.ContainsFunc(h.agents, func(a herdr.Agent) bool { return a.PaneID == pane }) {
			names = append(names, name)
		}
	}
	return names
}

// nextWorkspace is one more than the highest workspace number herdr shows.
func (h *Herdr) nextWorkspace() int {
	highest := 0
	for _, w := range h.workspaces {
		if n, err := strconv.Atoi(strings.TrimPrefix(w.ID, "w")); err == nil {
			highest = max(highest, n)
		}
	}
	return highest + 1
}

func tabNumber(id string) int {
	_, n, _ := strings.Cut(id, ":t")
	v, _ := strconv.Atoi(n)
	return v
}
