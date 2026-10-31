package dispatch_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
)

func fakeStop(tree string, tasks int) string {
	return fmt.Sprintf(`{"sid":"worker","where":%q,"tasks":%d}`, tree, tasks)
}

func TestRecordStopLandsTheTurnEndOfARegisteredHarnessKindsSession(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule), FakeHarness: true})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "")
	pane := d.SessionOf("ENG-1", "fake", "idle")
	d.StartBeforeTheEvents()

	// The hook input is the fake's form, which only the fake's parser reads.
	defer d.FromPane(pane)()
	require.NoError(t, d.Hook("record-stop", fakeStop(d.worktreeOf("ENG-1"), 1)).Err)

	d.Tick()
	assert.Equal(t, []string{"ENG-1: turns  of 0", "ENG-1: turns running of 1"}, d.Linear.Comments())
}

func TestRecordStopWritesNothingWhenNoRegisteredHarnessOwnsTheReportingPane(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		// pane returns HERDR_PANE_ID, or "" for a session outside herdr. fake is the pane of
		// the worker's fake session.
		pane  func(d *Dispatch, fake string) string
		input func(tree string) string
	}{
		{"NoPaneId", func(d *Dispatch, fake string) string { return "" }, func(tree string) string { return fakeStop(tree, 1) }},
		{"PaneHerdrDoesNotList", func(d *Dispatch, fake string) string { return "w404:p9" }, func(tree string) string { return fakeStop(tree, 1) }},
		{"PaneOfAnUnregisteredKind", func(d *Dispatch, fake string) string {
			d.tools.Herdr.AddAgent(herdr.Agent{Kind: "pi", Status: "idle", PaneID: "pi:p1"})
			return "pi:p1"
		}, func(tree string) string { return fakeStop(tree, 1) }},
		{"InputInAnotherHarnessesForm", func(d *Dispatch, fake string) string { return fake }, func(tree string) string {
			return fmt.Sprintf(`{"session_id":"worker","cwd":%q,"background_tasks":[]}`, tree)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule), FakeHarness: true})
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
			d.Workers.Running("ENG-1", "")
			fake := d.SessionOf("ENG-1", "fake", "idle")
			d.StartBeforeTheEvents()
			defer d.FromPane(test.pane(d, fake))()

			res := d.Hook("record-stop", test.input(d.worktreeOf("ENG-1")))

			require.NoError(t, res.Err)
			assert.Empty(t, res.Stdout)
			assert.Empty(t, res.Stderr)
			d.Tick()
			assert.Equal(t, []string{"ENG-1: turns  of 0"}, d.Linear.Comments())
		})
	}
}

func TestRecordStopFromAKindWithoutAnAdapterIsDroppedWhenOnlyClaudeCodeIsRegistered(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "")
	pane := d.SessionOf("ENG-1", "fake", "idle")
	d.StartBeforeTheEvents()
	defer d.FromPane(pane)()

	require.NoError(t, d.Hook("record-stop", fakeStop(d.worktreeOf("ENG-1"), 1)).Err)

	d.Tick()
	assert.Equal(t, []string{"ENG-1: turns unknown"}, d.Linear.Comments())
}

func TestWakeRecordsACommandTypedIntoARegisteredHarnessKindsSession(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(commandsRule), FakeHarness: true})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "")
	pane := d.SessionOf("ENG-1", "fake", "idle")
	tree := d.worktreeOf("ENG-1")
	defer d.FromPane(pane)()

	input := fmt.Sprintf(`{"sid":"worker","where":%q,"typed":"/dispatch:pr-respond"}`, tree)
	require.NoError(t, d.HookAndWaitForCommand("wake", input, tree, "worker", "/dispatch:pr-respond").Err)

	d.WaitForCommands(tree, "worker", 1)
	d.Tick()
	assert.Equal(t, "commands dispatch:pr-respond||false|person of 1", lastReading(t, d))
}

func TestWakeWritesNothingFromASessionOutsideHerdr(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(commandsRule), FakeHarness: true})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "")
	d.SessionOf("ENG-1", "fake", "idle")
	tree := d.worktreeOf("ENG-1")
	d.StartBeforeTheEvents()
	defer d.FromPane("")()

	input := fmt.Sprintf(`{"sid":"worker","where":%q,"typed":"/dispatch:pr-respond"}`, tree)
	require.NoError(t, d.Hook("wake", input).Err)

	d.Tick()
	assert.Equal(t, "commands  of 0", lastReading(t, d))
}

func TestLaunchReportsAWorkerWhoseSessionIsOfARegisteredHarnessKind(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, FakeHarness: true})
	tree := d.Trees.Pooled()
	d.fakes().Treehouse.MoveLease(tree, "ENG-1")
	d.fakes().Herdr.AddWorkspace(herdr.Workspace{ID: "w3", CheckoutPath: tree})
	d.fakes().Herdr.AddAgent(herdr.Agent{Kind: "fake", Status: "working", PaneID: "w3:p2", TabID: "w3:t1", WorkspaceID: "w3"})

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "already_running", out.result["status"])
	assert.Equal(t, "w3:p2", out.result["pane"])
	assert.Empty(t, d.fakes().Herdr.Started())
}

func TestLaunchIgnoresAnAgentOfAKindNoHarnessRuns(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, FakeHarness: true})
	tree := d.Trees.Pooled()
	d.fakes().Treehouse.MoveLease(tree, "ENG-1")
	d.fakes().Herdr.AddWorkspace(herdr.Workspace{ID: "w3", CheckoutPath: tree})
	d.fakes().Herdr.AddAgent(herdr.Agent{Kind: "pi", Status: "working", PaneID: "w3:p2", TabID: "w3:t1", WorkspaceID: "w3"})

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Len(t, d.fakes().Herdr.Started(), 1)
}

func TestRecordStopWritesNothingWhenHerdrCannotSayWhatRunsInThePane(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule), FakeHarness: true})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "")
	pane := d.SessionOf("ENG-1", "fake", "idle")
	d.StartBeforeTheEvents()
	d.fakes().Herdr.Fail(herdrtest.OpAgentAt, errors.New("herdr is down"))
	defer d.FromPane(pane)()

	res := d.Hook("record-stop", fakeStop(d.worktreeOf("ENG-1"), 1))

	require.NoError(t, res.Err)
	assert.Empty(t, res.Stdout)
	assert.Empty(t, res.Stderr)
	d.fakes().Herdr.Fail(herdrtest.OpAgentAt, nil)
	d.Tick()
	assert.Equal(t, []string{"ENG-1: turns  of 0"}, d.Linear.Comments())
}
