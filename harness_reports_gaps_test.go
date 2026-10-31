package dispatch_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdr"
)

func TestTickShowsTheStateOfARegisteredHarnessKindsSession(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		status string
		want   string
	}{
		{"working", "🟢"},
		{"blocked", "🔴"},
	} {
		t.Run(test.status, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{FakeHarness: true})
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
			d.Workers.Running("ENG-1", "")
			d.SessionOf("ENG-1", "fake", test.status)

			d.Tick()

			assert.Equal(t, []string{"w-ENG-1|" + test.want + " ENG-1"}, d.Workers.Renames())
		})
	}
}

func TestTickShowsAnIdleRegisteredHarnessKindsSessionAsStoppedOnlyForItsOwnSessionId(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		session string
		want    string
	}{
		{"OwnSession", "worker", "💤"},
		{"AnotherSession", "stranger", "🟢"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{FakeHarness: true})
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
			d.Workers.Running("ENG-1", "")
			pane := d.SessionOf("ENG-1", "fake", "idle")
			d.StartBeforeTheEvents()
			defer d.FromPane(pane)()

			input := fmt.Sprintf(`{"sid":%q,"where":%q,"tasks":0}`, test.session, d.worktreeOf("ENG-1"))
			require.NoError(t, d.Hook("record-stop", input).Err)

			d.Tick()
			assert.Equal(t, "w-ENG-1|"+test.want+" ENG-1", d.Workers.Renames()[len(d.Workers.Renames())-1])
		})
	}
}

func TestRecordStopWritesNothingFromARegisteredHarnessKindsSessionOutsideEveryWorkerTree(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule), FakeHarness: true})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "")
	pane := d.SessionOf("ENG-1", "fake", "idle")
	d.StartBeforeTheEvents()
	defer d.FromPane(pane)()

	require.NoError(t, d.Hook("record-stop", fakeStop(t.TempDir(), 1)).Err)
	require.NoError(t, d.Hook("record-stop", fakeStop(d.Repo, 1)).Err)

	d.Tick()
	assert.Equal(t, []string{"ENG-1: turns  of 0"}, d.Linear.Comments())
}

func TestWakeWritesNothingWhenNoRegisteredHarnessOwnsTheReportingPane(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		pane func(d *Dispatch) string
	}{
		{"PaneHerdrDoesNotList", func(d *Dispatch) string { return "w404:p9" }},
		{"PaneOfAnUnregisteredKind", func(d *Dispatch) string {
			d.tools.Herdr.AddAgent(herdr.Agent{Kind: "pi", Status: "idle", PaneID: "pi:p1"})
			return "pi:p1"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Rules: rule(commandsRule), FakeHarness: true})
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
			d.Workers.Running("ENG-1", "")
			d.SessionOf("ENG-1", "fake", "idle")
			tree := d.worktreeOf("ENG-1")
			d.StartBeforeTheEvents()
			defer d.FromPane(test.pane(d))()

			input := fmt.Sprintf(`{"sid":"worker","where":%q,"typed":"/dispatch:pr-respond"}`, tree)
			require.NoError(t, d.Hook("wake", input).Err)

			d.Tick()
			assert.Equal(t, "commands  of 0", lastReading(t, d))
		})
	}
}

func TestWakeWritesNothingFromARegisteredHarnessKindsSessionOutsideEveryWorkerTree(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(commandsRule), FakeHarness: true})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "")
	pane := d.SessionOf("ENG-1", "fake", "idle")
	d.StartBeforeTheEvents()
	defer d.FromPane(pane)()

	input := fmt.Sprintf(`{"sid":"worker","where":%q,"typed":"/dispatch:pr-respond"}`, t.TempDir())
	require.NoError(t, d.Hook("wake", input).Err)

	d.Tick()
	assert.Equal(t, "commands  of 0", lastReading(t, d))
}
