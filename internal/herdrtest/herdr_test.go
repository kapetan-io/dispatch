package herdrtest_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
	"github.com/kapetan-io/dispatch/internal/journal"
)

func TestHerdrKeepsTheHerdrContract(t *testing.T) {
	for _, scenario := range herdrtest.Scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			fake := herdrtest.New(&journal.Journal{})
			var workspaces int
			layout := func(workspace string, tab int, label string) herdrtest.Layout {
				l := herdrtest.Layout{Workspace: workspace, Tab: fmt.Sprintf("%s:t%d", workspace, tab), Pane: fmt.Sprintf("%s:p%d", workspace, tab)}
				fake.AddTab(herdr.Tab{ID: l.Tab, Label: label, WorkspaceID: workspace})
				fake.AddPane(herdr.Pane{ID: l.Pane, TabID: l.Tab, WorkspaceID: workspace})
				return l
			}
			tabs, started := map[string]int{}, map[string]bool{}
			scenario.Run(t, herdrtest.Subject{
				Herdr: fake,
				Workspace: func(label string) herdrtest.Layout {
					workspaces++
					id := fmt.Sprintf("w%d", workspaces)
					fake.AddWorkspace(herdr.Workspace{ID: id, Label: label})
					tabs[id] = 1
					return layout(id, 1, "1")
				},
				Tab: func(workspace, label string) herdrtest.Layout {
					tabs[workspace]++
					return layout(workspace, tabs[workspace], label)
				},
				Agent: func(pane, status string) {
					if started[pane] {
						fake.SetStatus(pane, status)
						return
					}
					started[pane] = true
					snap, err := fake.Read(context.Background())
					require.NoError(t, err)
					for _, p := range snap.Panes {
						if p.ID == pane {
							fake.AddAgent(herdr.Agent{Kind: "claude", Status: status, PaneID: p.ID, TabID: p.TabID, WorkspaceID: p.WorkspaceID})
						}
					}
				},
			})
		})
	}
}

func seeded() *herdrtest.Herdr {
	fake := herdrtest.New(&journal.Journal{})
	fake.AddWorkspace(herdr.Workspace{ID: "w1", Label: "ENG-1"})
	fake.AddTab(herdr.Tab{ID: "w1:t1", Label: "ENG-1", WorkspaceID: "w1"})
	fake.AddPane(herdr.Pane{ID: "w1:p1", TabID: "w1:t1", WorkspaceID: "w1"})
	fake.AddAgent(herdr.Agent{Kind: "claude", Status: "idle", PaneID: "w1:p1", TabID: "w1:t1", WorkspaceID: "w1"})
	return fake
}

func TestHerdrRefusesAPromptToAnAgentThatBlockedAfterTheSnapshot(t *testing.T) {
	ctx := context.Background()
	fake := seeded()
	fake.BlockAfterSnapshot("w1:p1")

	snap, err := fake.Read(ctx)
	require.NoError(t, err)
	assert.Equal(t, "idle", snap.Agents[0].Status)
	err = fake.Prompt(ctx, "w1:p1", "hi")

	require.ErrorIs(t, err, herdr.ErrAgentBlocked)
	assert.Empty(t, fake.Prompted())

	fake.Unblock("w1:p1")
	require.NoError(t, fake.Prompt(ctx, "w1:p1", "hi"))
	assert.Equal(t, []string{"w1:p1|hi"}, fake.Prompted())

	snap, err = fake.Read(ctx)
	require.NoError(t, err)
	assert.Equal(t, "idle", snap.Agents[0].Status)
}

func TestHerdrInjectedFailuresDoNotChangeWhatItAccepts(t *testing.T) {
	ctx := context.Background()
	fake := seeded()

	// A failed close leaves the workspace in place.
	fake.Fail(herdrtest.OpCloseWorkspace, errors.New("exit status 1"))
	require.ErrorContains(t, fake.CloseWorkspace(ctx, "w1"), "exit status 1")
	assert.True(t, fake.HasWorkspace("w1"))
	assert.Empty(t, fake.ClosedWorkspaces())

	// A failure never makes the fake accept an unknown workspace.
	fake.Fail(herdrtest.OpCloseWorkspace, nil)
	require.ErrorContains(t, fake.CloseWorkspace(ctx, "w9"), "workspace_not_found")

	// A tab list that fails leaves the rest of the snapshot whole.
	fake.Fail(herdrtest.OpTabList, errors.New("exit status 1"))
	snap, err := fake.Read(ctx)
	require.NoError(t, err)
	assert.Empty(t, snap.Tabs)
	assert.Len(t, snap.Workspaces, 1)
	assert.Len(t, snap.Agents, 1)
}

func TestHerdrRecordsItsCallsInOrder(t *testing.T) {
	ctx := context.Background()
	calls := &journal.Journal{}
	fake := herdrtest.New(calls)
	fake.AddWorkspace(herdr.Workspace{ID: "w1"})

	_, err := fake.Read(ctx)
	require.NoError(t, err)
	require.NoError(t, fake.RenameWorkspace(ctx, "w1", "x"))
	require.NoError(t, fake.CloseWorkspace(ctx, "w1"))

	assert.Equal(t, []journal.Call{
		{Tool: "herdr", Name: "read"},
		{Tool: "herdr", Name: "rename-workspace", Args: []string{"w1", "x"}},
		{Tool: "herdr", Name: "close-workspace", Args: []string{"w1"}},
	}, calls.Calls())
}
