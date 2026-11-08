package dispatch_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTellDeliversATextWholeWhateverItsShape(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		text string
	}{
		{"LongSingleLine", strings.Repeat("keep going with the recommendation ", 33)[:1125]},
		{"MultiLine", "first line\nsecond line\n\nfourth line after a blank"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Workers.Running("ENG-1", "idle")

			res := d.Run("tell", "ENG-1", test.text)

			require.NoError(t, res.Err)
			assert.Equal(t, "told ENG-1\n", res.Stdout)
			assert.Equal(t, []string{test.text}, d.Workers.Received("ENG-1"))
			assert.Equal(t, []string{"done"}, d.nudgeStates("ENG-1"))
		})
	}
}

func TestTellRefusesAWorkerWhoseSessionIsNotKnownYet(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "idle")
	d.Workers.SetSession("ENG-1", "")

	res := d.Run("tell", "ENG-1", "hi")

	require.ErrorContains(t, res.Err, "nothing sent: session not known yet")
	assert.Empty(t, d.Workers.Received("ENG-1"))
}

func TestTickSendsARuleNudgeOnceTheWorkerHasASession(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.SetSession("ENG-1", "")
	d.PullRequestFailsAfterEntry(prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	require.ErrorContains(t, d.TickAfterASecond(), "ENG-1 nudge failed: nothing sent: session not known yet")
	assert.Empty(t, d.Workers.Received("ENG-1"))

	d.Workers.SetSession("ENG-1", "worker")
	require.NoError(t, d.TickAfterASecond())

	assert.Equal(t, []string{prPrompt("a failed check")}, d.Workers.Received("ENG-1"))
}

func TestTickSendsARuleNudgeOnceAfterAWriteError(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.PullRequestFailsAfterEntry(prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	d.Workers.FailNextDelivery(errors.New("write unix /tmp/cc-socks/1.sock: broken pipe"))

	require.ErrorContains(t, d.TickAfterASecond(), "ENG-1 nudge failed: nothing sent: write unix /tmp/cc-socks/1.sock: broken pipe")
	assert.Empty(t, d.Workers.Received("ENG-1"))

	require.NoError(t, d.TickAfterASecond())

	assert.Equal(t, []string{prPrompt("a failed check")}, d.Workers.Received("ENG-1"))
}

func TestATellThatFailedIsNotRetried(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "idle")
	d.Workers.FailNextDelivery(errors.New("write unix: broken pipe"))

	require.ErrorContains(t, d.Run("tell", "ENG-1", "hi").Err, "nothing sent: write unix: broken pipe")
	d.Tick()
	d.Tick()

	assert.Empty(t, d.Workers.Received("ENG-1"))
}

func TestANudgeFollowsAWorkerToItsNewSession(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "idle")
	require.NoError(t, d.Run("tell", "ENG-1", "before").Err)

	d.Workers.SetSession("ENG-1", "restarted")
	d.Workers.SessionExits("ENG-1")
	d.Workers.SessionOpens("ENG-1")
	require.NoError(t, d.Run("tell", "ENG-1", "after").Err)

	assert.Equal(t, []string{"before", "after"}, d.Workers.Received("ENG-1"))
}

func TestANudgeToAResumedSessionNeedsOnlyThatSessionToBeOpen(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "idle")
	d.Workers.SessionExits("ENG-1")
	require.ErrorContains(t, d.Run("tell", "ENG-1", "lost").Err, "nothing sent: the session is not open")

	d.Workers.SetSession("ENG-1", "resumed")
	require.NoError(t, d.Run("tell", "ENG-1", "kept").Err)

	assert.Equal(t, []string{"kept"}, d.Workers.Received("ENG-1"))
}

func TestANudgeIsSentAsDispatchAndTheSameTextTypedByAPersonIsAPerson(t *testing.T) {
	t.Parallel()
	const text = "/dispatch:pr-respond PR 42. Load the dispatch:pr-respond skill with the Skill tool, then handle it."
	d := newDispatch(t, dispatchOptions{Rules: rule(commandsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.CheckedOut("ENG-1", "idle")
	require.NoError(t, d.Run("tell", "ENG-1", text).Err)

	d.Workers.Type("ENG-1", text)
	d.Workers.Type("ENG-1", text)
	d.Tick()

	assert.Equal(t, "commands dispatch:pr-respond|PR 42. Load the dispatch:pr-respond skill with the Skill tool, then handle it.|false|dispatch;"+
		"dispatch:pr-respond|PR 42. Load the dispatch:pr-respond skill with the Skill tool, then handle it.|false|person of 2", lastReading(t, d))
}

func TestANudgeToALaunchedWorkerGoesToItsSessionAndOnlyTheKickoffIsTyped(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	require.Equal(t, "ok", d.launch("ENG-1", "thrawn01/eng-1").result["status"])
	require.Len(t, d.fakes().Herdr.Kicked(), 1)

	res := d.Run("tell", "ENG-1", "keep going")

	require.NoError(t, res.Err)
	assert.Equal(t, []string{"keep going"}, d.Workers.Received("ENG-1"))
	assert.Len(t, d.fakes().Herdr.Kicked(), 1)
}

// nudgeStates lists the outcome of each nudge receipt for the ticket, oldest first.
func (d *Dispatch) nudgeStates(ticket string) []string {
	var states []string
	for _, receipt := range d.Attempts(ticket) {
		if receipt.Kind == "nudge" && receipt.State != "attempted" {
			states = append(states, receipt.State)
		}
	}
	return states
}
