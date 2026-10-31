package herdrtest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdr"
)

// Layout names what a new workspace holds: its first tab and that tab's pane.
type Layout struct {
	Workspace, Tab, Pane string
}

// Subject is a herdr under test and the means to set up what it shows.
type Subject struct {
	Herdr herdr.Herdr
	// Workspace opens a workspace labeled label, with one tab and one pane.
	Workspace func(label string) Layout
	// Tab adds a tab labeled label, with one pane, to a workspace.
	Tab func(workspace, label string) Layout
	// Agent starts an agent in the pane, reporting status.
	Agent func(pane, status string)
	// Checkout makes a git repo with a linked worktree and returns both paths.
	Checkout func() (repo, tree string)
	// PluginRoot is the plugin a worker loads.
	PluginRoot string
	// Program is what the workers it starts run.
	Program herdr.Program
}

// Scenario is one situation with the outcome both the fake and the real herdr
// must give.
type Scenario struct {
	Name string
	Run  func(t *testing.T, s Subject)
}

// Scenarios is the contract for herdr.Herdr. The fake runs it in every test
// run, and the real adapter runs it under the drift build tag. The refusals
// checked are herdr's own codes and wording, which the fake copies.
var Scenarios = []Scenario{
	{Name: "ReadOfAnEmptySessionShowsNothing", Run: func(t *testing.T, s Subject) {
		snap, err := s.Herdr.Read(context.Background())

		require.NoError(t, err)
		assert.Empty(t, snap.Workspaces)
		assert.Empty(t, snap.Tabs)
		assert.Empty(t, snap.Panes)
		assert.Empty(t, snap.Agents)
	}},
	{Name: "ReadShowsWhatTheWorkspaceHolds", Run: func(t *testing.T, s Subject) {
		first := s.Workspace("alpha")
		second := s.Tab(first.Workspace, "beta")
		s.Agent(second.Pane, "idle")

		snap, err := s.Herdr.Read(context.Background())

		require.NoError(t, err)
		require.Len(t, snap.Workspaces, 1)
		assert.Equal(t, first.Workspace, snap.Workspaces[0].ID)
		assert.Equal(t, "alpha", snap.Workspaces[0].Label)
		require.Len(t, snap.Tabs, 2)
		assert.Contains(t, snap.Tabs, herdr.Tab{ID: second.Tab, Label: "beta", WorkspaceID: first.Workspace})
		assert.Len(t, snap.Panes, 2)
		assert.Contains(t, snap.Panes, herdr.Pane{ID: second.Pane, TabID: second.Tab, WorkspaceID: first.Workspace})
		require.Len(t, snap.Agents, 1)
		assert.Equal(t, herdr.Agent{Kind: "claude", Status: "idle", PaneID: second.Pane, TabID: second.Tab, WorkspaceID: first.Workspace}, snap.Agents[0])
	}},
	{Name: "ReadShowsAnAgentsChangedStatus", Run: func(t *testing.T, s Subject) {
		layout := s.Workspace("alpha")
		s.Agent(layout.Pane, "working")
		s.Agent(layout.Pane, "blocked")

		snap, err := s.Herdr.Read(context.Background())

		require.NoError(t, err)
		require.Len(t, snap.Agents, 1)
		assert.Equal(t, "blocked", snap.Agents[0].Status)
	}},
	{Name: "PromptTypesIntoAnIdleAgent", Run: func(t *testing.T, s Subject) {
		layout := s.Workspace("alpha")
		s.Agent(layout.Pane, "idle")

		require.NoError(t, s.Herdr.Prompt(context.Background(), layout.Pane, "keep going"))
	}},
	{Name: "PromptTypesIntoAWorkingAgent", Run: func(t *testing.T, s Subject) {
		layout := s.Workspace("alpha")
		s.Agent(layout.Pane, "working")

		require.NoError(t, s.Herdr.Prompt(context.Background(), layout.Pane, "keep going"))
	}},
	{Name: "PromptRefusesABlockedAgent", Run: func(t *testing.T, s Subject) {
		layout := s.Workspace("alpha")
		s.Agent(layout.Pane, "blocked")

		err := s.Herdr.Prompt(context.Background(), layout.Pane, "keep going")

		require.ErrorIs(t, err, herdr.ErrAgentBlocked)
		require.ErrorContains(t, err, "agent_blocked")
		assert.ErrorContains(t, err, "agent "+layout.Pane+" is blocked and requires interactive input")
	}},
	{Name: "PromptRefusesAPaneWithNoAgent", Run: func(t *testing.T, s Subject) {
		layout := s.Workspace("alpha")

		err := s.Herdr.Prompt(context.Background(), layout.Pane, "keep going")

		require.ErrorContains(t, err, "agent_not_found")
		assert.NotErrorIs(t, err, herdr.ErrAgentBlocked)
	}},
	{Name: "RenameWorkspaceChangesItsLabel", Run: func(t *testing.T, s Subject) {
		layout := s.Workspace("alpha")

		require.NoError(t, s.Herdr.RenameWorkspace(context.Background(), layout.Workspace, "🟢 ENG-1"))

		snap, err := s.Herdr.Read(context.Background())
		require.NoError(t, err)
		require.Len(t, snap.Workspaces, 1)
		assert.Equal(t, "🟢 ENG-1", snap.Workspaces[0].Label)
	}},
	{Name: "RenameWorkspaceRefusesAnUnknownWorkspace", Run: func(t *testing.T, s Subject) {
		err := s.Herdr.RenameWorkspace(context.Background(), "w999", "x")

		require.ErrorContains(t, err, "workspace_not_found")
		assert.ErrorContains(t, err, "workspace w999 not found")
	}},
	{Name: "RenameTabChangesItsLabel", Run: func(t *testing.T, s Subject) {
		layout := s.Workspace("alpha")

		require.NoError(t, s.Herdr.RenameTab(context.Background(), layout.Tab, "ENG-1 title"))

		snap, err := s.Herdr.Read(context.Background())
		require.NoError(t, err)
		require.Len(t, snap.Tabs, 1)
		assert.Equal(t, "ENG-1 title", snap.Tabs[0].Label)
	}},
	{Name: "RenameTabRefusesAnUnknownTab", Run: func(t *testing.T, s Subject) {
		layout := s.Workspace("alpha")

		err := s.Herdr.RenameTab(context.Background(), layout.Workspace+":t999", "x")

		require.ErrorContains(t, err, "tab_not_found")
	}},
	{Name: "CloseWorkspaceRemovesItsTabsPanesAndAgents", Run: func(t *testing.T, s Subject) {
		closing := s.Workspace("closing")
		second := s.Tab(closing.Workspace, "second")
		s.Agent(closing.Pane, "idle")
		s.Agent(second.Pane, "working")
		staying := s.Workspace("staying")

		require.NoError(t, s.Herdr.CloseWorkspace(context.Background(), closing.Workspace))

		snap, err := s.Herdr.Read(context.Background())
		require.NoError(t, err)
		require.Len(t, snap.Workspaces, 1)
		assert.Equal(t, staying.Workspace, snap.Workspaces[0].ID)
		require.Len(t, snap.Tabs, 1)
		assert.Equal(t, staying.Tab, snap.Tabs[0].ID)
		require.Len(t, snap.Panes, 1)
		assert.Equal(t, staying.Pane, snap.Panes[0].ID)
		assert.Empty(t, snap.Agents)
	}},
	{Name: "CloseWorkspaceRefusesAnUnknownWorkspace", Run: func(t *testing.T, s Subject) {
		err := s.Herdr.CloseWorkspace(context.Background(), "w999")

		require.ErrorContains(t, err, "workspace_not_found")
	}},
	{Name: "CloseWorkspaceRefusesAWorkspaceAlreadyClosed", Run: func(t *testing.T, s Subject) {
		layout := s.Workspace("alpha")
		require.NoError(t, s.Herdr.CloseWorkspace(context.Background(), layout.Workspace))

		err := s.Herdr.CloseWorkspace(context.Background(), layout.Workspace)

		require.ErrorContains(t, err, "workspace_not_found")
	}},
	{Name: "CloseTabRemovesItsPanesAndAgents", Run: func(t *testing.T, s Subject) {
		first := s.Workspace("alpha")
		second := s.Tab(first.Workspace, "second")
		s.Agent(first.Pane, "idle")
		s.Agent(second.Pane, "working")

		require.NoError(t, s.Herdr.CloseTab(context.Background(), second.Tab))

		snap, err := s.Herdr.Read(context.Background())
		require.NoError(t, err)
		require.Len(t, snap.Workspaces, 1)
		require.Len(t, snap.Tabs, 1)
		assert.Equal(t, first.Tab, snap.Tabs[0].ID)
		require.Len(t, snap.Panes, 1)
		assert.Equal(t, first.Pane, snap.Panes[0].ID)
		require.Len(t, snap.Agents, 1)
		assert.Equal(t, first.Pane, snap.Agents[0].PaneID)
	}},
	{Name: "CloseTabRefusesAnUnknownTab", Run: func(t *testing.T, s Subject) {
		layout := s.Workspace("alpha")

		err := s.Herdr.CloseTab(context.Background(), layout.Workspace+":t999")

		require.ErrorContains(t, err, "tab_not_found")
	}},
	{Name: "OpenWorkspaceShowsTheCheckout", Run: func(t *testing.T, s Subject) {
		repo, tree := s.Checkout()

		id, err := s.Herdr.OpenWorkspace(context.Background(), herdr.NewWorkspace{Repo: repo, Path: tree, Label: "ENG-1"})

		require.NoError(t, err)
		snap, err := s.Herdr.Read(context.Background())
		require.NoError(t, err)
		// herdr nests the checkout's workspace under one for its repo.
		assert.Contains(t, snap.Workspaces, herdr.Workspace{ID: id, Label: "ENG-1", CheckoutPath: tree})
		assert.Len(t, snap.Workspaces, 2)
	}},
	{Name: "OpenWorkspaceReturnsTheWorkspaceAlreadyShowingTheCheckout", Run: func(t *testing.T, s Subject) {
		repo, tree := s.Checkout()
		first, err := s.Herdr.OpenWorkspace(context.Background(), herdr.NewWorkspace{Repo: repo, Path: tree, Label: "ENG-1"})
		require.NoError(t, err)

		second, err := s.Herdr.OpenWorkspace(context.Background(), herdr.NewWorkspace{Repo: repo, Path: tree, Label: "ENG-1"})

		require.NoError(t, err)
		assert.Equal(t, first, second)
		snap, err := s.Herdr.Read(context.Background())
		require.NoError(t, err)
		assert.Len(t, snap.Workspaces, 2)
	}},
	{Name: "StartWorkerRunsTheProgramInANewTab", Run: func(t *testing.T, s Subject) {
		repo, tree := s.Checkout()
		id, err := s.Herdr.OpenWorkspace(context.Background(), herdr.NewWorkspace{Repo: repo, Path: tree, Label: "ENG-1"})
		require.NoError(t, err)

		started, err := s.Herdr.StartWorker(context.Background(), herdr.Worker{Workspace: id, Dir: tree, Label: "ENG-1 title", Name: "eng-1", PluginRoot: s.PluginRoot, Program: s.Program})

		require.NoError(t, err)
		assert.Equal(t, "eng-1", started.Agent)
		snap, err := s.Herdr.Read(context.Background())
		require.NoError(t, err)
		// The tab the workspace opened with held no agent and is gone.
		var tabs []herdr.Tab
		for _, tab := range snap.Tabs {
			if tab.WorkspaceID == id {
				tabs = append(tabs, tab)
			}
		}
		require.Len(t, tabs, 1)
		assert.Equal(t, "ENG-1 title", tabs[0].Label)
		assert.Contains(t, snap.Panes, herdr.Pane{ID: started.Pane, TabID: tabs[0].ID, WorkspaceID: id})
		require.Len(t, snap.Agents, 1)
		assert.Equal(t, s.Program.AgentKind, snap.Agents[0].Kind)
		assert.Equal(t, started.Pane, snap.Agents[0].PaneID)
		assert.Equal(t, "idle", snap.Agents[0].Status)
	}},
	{Name: "StartWorkerNamesASecondWorkerApartFromTheFirst", Run: func(t *testing.T, s Subject) {
		repo, tree := s.Checkout()
		id, err := s.Herdr.OpenWorkspace(context.Background(), herdr.NewWorkspace{Repo: repo, Path: tree, Label: "ENG-1"})
		require.NoError(t, err)
		worker := herdr.Worker{Workspace: id, Dir: tree, Label: "ENG-1", Name: "eng-1", PluginRoot: s.PluginRoot, Program: s.Program}
		first, err := s.Herdr.StartWorker(context.Background(), worker)
		require.NoError(t, err)

		second, err := s.Herdr.StartWorker(context.Background(), worker)

		require.NoError(t, err)
		assert.Equal(t, "eng-1", first.Agent)
		assert.Regexp(t, `^eng-1-r\d+$`, second.Agent)
		assert.NotEqual(t, first.Pane, second.Pane)
	}},
	{Name: "StartWorkerRefusesAnUnknownWorkspace", Run: func(t *testing.T, s Subject) {
		_, tree := s.Checkout()

		started, err := s.Herdr.StartWorker(context.Background(), herdr.Worker{Workspace: "w999", Dir: tree, Label: "ENG-1", Name: "eng-1", PluginRoot: s.PluginRoot, Program: s.Program})

		require.ErrorContains(t, err, "workspace_not_found")
		assert.Empty(t, started.Pane)
	}},
	{Name: "KickoffMakesTheWorkerRun", Run: func(t *testing.T, s Subject) {
		repo, tree := s.Checkout()
		id, err := s.Herdr.OpenWorkspace(context.Background(), herdr.NewWorkspace{Repo: repo, Path: tree, Label: "ENG-1"})
		require.NoError(t, err)
		started, err := s.Herdr.StartWorker(context.Background(), herdr.Worker{Workspace: id, Dir: tree, Label: "ENG-1", Name: "eng-1", PluginRoot: s.PluginRoot, Program: s.Program})
		require.NoError(t, err)

		require.NoError(t, s.Herdr.Kickoff(context.Background(), started.Pane, "/dispatch:build ENG-1", s.Program.Startup))

		snap, err := s.Herdr.Read(context.Background())
		require.NoError(t, err)
		require.Len(t, snap.Agents, 1)
		assert.Equal(t, "working", snap.Agents[0].Status)
	}},
	{Name: "KickoffGivesUpOnAPaneWithNoAgent", Run: func(t *testing.T, s Subject) {
		layout := s.Workspace("alpha")

		err := s.Herdr.Kickoff(context.Background(), layout.Pane, "/dispatch:build ENG-1", s.Program.Startup)

		// The adapter waits for an input box that never comes, so this takes
		// about 30s against real herdr.
		require.ErrorIs(t, err, herdr.ErrKickoffUnconfirmed)
	}},
}
