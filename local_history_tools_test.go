package dispatch_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
)

const localShipped = shippedRulesConfig + localOn

func TestRulesCheckCountsLocalTicketsAndSaysWhyADesignTicketIsHeld(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		running bool
	}{
		{name: "RunStopped"},
		{name: "RunRunning", running: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{Config: localShipped})
			d.StartBeforeTheEvents()
			d.fileLocal("In Progress", "--label", "design")
			d.fileLocal("In Progress")
			if !test.running {
				d.Stop()
			}

			res := d.Run("rules", "check")

			require.NoError(t, res.Err)
			assert.Regexp(t, `ticket +2 tracked tickets \(.*local tickets`, res.Stdout)
			assert.Regexp(t, `launch-worker +whenever +ticket +wants launch now for LOC-2; exempt for LOC-1 by no-design-worker-on-local-ticket\n`, res.Stdout)
		})
	}
}

func TestRulesCheckAndStateResetWithRunStoppedLeaveTheDatabaseAsItWas(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "RulesCheck", args: []string{"rules", "check"}},
		{name: "StateReset", args: []string{"state", "reset"}},
		{name: "StateResetQuiet", args: []string{"state", "reset", "--quiet"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{Config: localShipped})
			d.StartBeforeTheEvents()
			d.fileLocal("In Progress")
			d.Stop()
			before, err := os.ReadFile(d.databasePath())
			require.NoError(t, err)

			res := d.Run(test.args...)

			require.NoError(t, res.Err)
			after, err := os.ReadFile(d.databasePath())
			require.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
}

func TestRulesCheckAndStateResetCreateNoDatabaseFile(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "RulesCheck", args: []string{"rules", "check"}},
		{name: "StateReset", args: []string{"state", "reset"}},
		{name: "StateResetQuiet", args: []string{"state", "reset", "--quiet"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{Config: localShipped})

			res := d.Run(test.args...)

			require.NoError(t, res.Err)
			assert.NoFileExists(t, d.databasePath())
		})
	}
}

func TestRulesCheckReadsAnUnseededDatabaseFileAsNoTicketsAndLeavesItEmpty(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localShipped})
	require.NoError(t, os.WriteFile(d.databasePath(), nil, 0o600))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "includes 0 local tickets (statuses Backlog, Todo, In Progress, In Review, Needs Human, Done, Canceled)")
	info, err := os.Stat(d.databasePath())
	require.NoError(t, err)
	assert.Zero(t, info.Size())
}

func TestStateResetQuietRecordsTheMessagesTheFirstCycleWantsForLocalTickets(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localShipped})
	d.StartBeforeTheEvents()
	d.fileLocal("In Review")
	d.fileLocal("In Progress", "--blocked-by", "LOC-1")
	d.Tick()
	d.Stop()
	d.Restart()
	// dispatch is the author of each comment it wrote on LOC-2.
	before := strings.Count(d.ticketOut("show", "LOC-2"), "dispatch")
	require.Positive(t, before)
	d.Stop()
	d.Advance(time.Minute)

	require.NoError(t, d.Run("state", "reset", "--quiet").Err)
	d.Restart()
	d.Tick()

	assert.Equal(t, before, strings.Count(d.ticketOut("show", "LOC-2"), "dispatch"))
}

func TestReplayMatchesARunThatReadLocalTickets(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.fileLocal("In Progress")
	d.Tick()
	d.fileLocal("Todo", "--parent", "LOC-1")
	d.Tick()
	d.Stop()

	cycles, matched := replayOf(t, d)

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}

func TestExplainAndWatchAcceptALocalTicketID(t *testing.T) {
	t.Parallel()
	socket, _ := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Config: localOn, Env: map[string]string{"CLAUDE_CODE_SESSION_ID": socket}})
	d.StartBeforeTheEvents()
	d.fileLocal("In Progress")
	d.Tick()

	watch := d.Run("watch", "LOC-1")
	explain := d.Run("explain", "LOC-1")

	require.NoError(t, watch.Err)
	assert.Contains(t, watch.Stdout, "watching LOC-1")
	require.NoError(t, explain.Err)
	assert.Contains(t, explain.Stdout, "LOC-1")
}

func TestStateResetKeepsTheHighestIssuedNumberSoARestoredOlderFileIsRefused(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.fileLocal("Todo")
	d.Stop()
	older, err := os.ReadFile(d.databasePath())
	require.NoError(t, err)
	d.Restart()
	d.fileLocal("Done")
	d.fileLocal("Done")
	d.Tick()
	d.Stop()

	require.NoError(t, d.Run("state", "reset").Err)
	require.NoError(t, os.WriteFile(d.databasePath(), older, 0o600))
	err = d.startLoop(dispatch.Options{})

	require.ErrorContains(t, err, "LOC-3")
	require.ErrorContains(t, err, d.databasePath())
}

func TestRulesCheckSaysTheRunningDispatchStartedWithLocalTicketsOff(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.StartBeforeTheEvents()
	d.config(localOn)

	res := d.Run("rules", "check")

	require.ErrorContains(t, res.Err, "started with local tickets off")
	require.ErrorContains(t, res.Err, "restart it")
	assert.NotContains(t, res.Err.Error(), "no [plugins.local] table")
}
