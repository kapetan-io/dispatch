package dispatch_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const idleNotice = "ENG-1's worker is idle with nothing running: ticket ENG-1"

// Prompt runs the UserPromptSubmit hook as the worker session typing text in dir.
func (d *Dispatch) Prompt(dir, text string) Result {
	d.t.Helper()
	return d.HookAndWait("wake", fmt.Sprintf(`{"hook_event_name":"UserPromptSubmit","session_id":"worker","cwd":%q,"prompt":%q}`, dir, text))
}

func TestWakeEndsThePauseOfAWorkerThatTookAMessageAfterItsStop(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, text string }{
		{name: "message", text: "keep going"},
		{name: "command", text: "/dispatch:pr-respond"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			d.Workers.Stop("ENG-1", stopTasks{})
			d.Tick()
			require.Equal(t, []string{"w-ENG-1|💤 ENG-1"}, d.Workers.Renames())
			d.Advance(time.Second)

			// Claude has not yet reported working when the message lands.
			require.NoError(t, d.Prompt(d.Workers.Tree("ENG-1"), test.text).Err)
			d.Advance(10 * time.Minute)
			d.Tick()

			assert.Equal(t, []string{"w-ENG-1|💤 ENG-1", "w-ENG-1|🟢 ENG-1"}, d.Workers.Renames())
			assert.Equal(t, []string{idleNotice}, d.Followed.Told())
		})
	}
}

func TestWakeKeepsThePauseOfAWorkerWhoseMessagePrecededItsStop(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Tick()
	d.Advance(time.Second)
	require.NoError(t, d.Prompt(d.Workers.Tree("ENG-1"), "keep going").Err)
	d.Advance(time.Second)
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Workers.SetStatus("ENG-1", "idle")

	d.Tick()

	assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1", "w-ENG-1|💤 ENG-1"}, d.Workers.Renames())
	assert.Equal(t, []string{idleNotice}, d.Followed.Told())
}

func TestWakeIgnoresAMessageOfAnotherSessionInTheTree(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Tick()
	d.Advance(time.Second)

	require.NoError(t, d.HookAndWait("wake", fmt.Sprintf(`{"session_id":"stranger","cwd":%q,"prompt":"hello"}`, d.Workers.Tree("ENG-1"))).Err)
	d.Tick()

	assert.Equal(t, []string{"w-ENG-1|💤 ENG-1"}, d.Workers.Renames())
}

func TestRulesReadWaitingOfAnIdleWorkerOnANeedsHumanTicket(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		message bool
		want    []string
	}{
		{name: "paused", want: []string{"ENG-1: waiting true"}},
		{name: "messaged", message: true, want: []string{"ENG-1: waiting true", "ENG-1: waiting false"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Rules: rule(`
whenever({ name: "waiting", each: "ticket", if: (t) => t.status === "needs_human",
  then: (t, ctx) => ctx.ticket.queueComment(t, "waiting " + ctx.worker.of(t).waiting, { once: ["waiting", t.id, String(ctx.worker.of(t).waiting)] }) });`)})
			d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			d.Workers.Stop("ENG-1", stopTasks{})
			d.Tick()
			d.Advance(time.Second)
			if test.message {
				require.NoError(t, d.Prompt(d.Workers.Tree("ENG-1"), "here is the answer").Err)
				d.Tick()
			}

			assert.Equal(t, test.want, d.Linear.Comments())
		})
	}
}

func TestWakeKeepsThePauseOfAWorkerWhoseHookCarriedNoText(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, text string }{
		{name: "empty", text: ""},
		{name: "blank", text: "  \n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			d.Workers.Stop("ENG-1", stopTasks{})
			d.Tick()
			d.Advance(time.Second)

			require.NoError(t, d.Prompt(d.Workers.Tree("ENG-1"), test.text).Err)
			d.Tick()

			assert.Equal(t, []string{"w-ENG-1|💤 ENG-1"}, d.Workers.Renames())
		})
	}
}

func TestRulesReadWaitingFalseWhileAStopListedARunningTaskUntilItExpires(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(`
whenever({ name: "waiting", each: "ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.ticket.queueComment(t, "waiting " + ctx.worker.of(t).waiting, { once: ["waiting", t.id, String(ctx.worker.of(t).waiting)] }) });`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{Running: 1})

	d.Tick()
	d.Advance(59 * time.Minute)
	d.Tick()
	d.Advance(time.Minute)
	d.Tick()

	assert.Equal(t, []string{"ENG-1: waiting false", "ENG-1: waiting true"}, d.Linear.Comments())
}
