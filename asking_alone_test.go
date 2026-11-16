package dispatch_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/harnesstest"
)

const taskNotice = harnesstest.TaskNotice + "\n<task-id>b1</task-id>\n<status>completed</status>\n</task-notification>"

// askingInReview is an In Review worker that ran `dispatch ask` and stopped, on a PR in the given
// state, seen by one tick.
func askingInReview(t *testing.T, settings string, pr prState) *Dispatch {
	t.Helper()
	d := newDispatch(t, dispatchOptions{RepoSettings: settings})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.GitHub.PullRequest("ENG-1", pr)
	require.NoError(t, d.Workers.Ask("ENG-1").Err)
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Workers.SetStatus("ENG-1", "idle")
	d.Tick()
	return d
}

func failingPR(head string) prState {
	return prState{head: head, checks: "FAILURE", mergeable: "MERGEABLE"}
}

func TestAnAskingInReviewWorkerGetsNoPRNudgeThroughThreeResyncs(t *testing.T) {
	t.Parallel()
	d := askingInReview(t, "", failingPR("a1"))

	for range 3 {
		d.Advance(5 * time.Minute)
		d.Tick()
	}

	assert.Empty(t, d.Workers.Received("ENG-1"))
	assert.Empty(t, d.Linear.Moves())
	assert.Equal(t, "w-ENG-1|🔴 ENG-1", d.Workers.lastRename(t))
	assert.Equal(t, []string{askedNotice}, d.Followed.Told())
}

func TestAnAskingInReviewWorkersApprovedPRIsNotMergedAndShowsRed(t *testing.T) {
	t.Parallel()
	d := askingInReview(t, autoMergeSetting, approvedPR("a1"))

	for range 3 {
		d.Advance(5 * time.Minute)
		d.Tick()
	}

	assert.Empty(t, d.GitHub.Merged())
	assert.Equal(t, "w-ENG-1|🔴 ENG-1", d.Workers.lastRename(t))
	assert.Equal(t, []string{askedNotice}, d.Followed.Told())
}

func TestAnAskingInReviewWorkerIsNotEscalatedForNudgesItGotBeforeAsking(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[rules]\nnudge_limit = 1\n"})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", failingPR("a1"))
	d.Tick()
	require.Equal(t, []string{prPrompt("a failed check")}, d.Workers.Received("ENG-1"))

	// The nudged worker asks instead of pushing.
	d.Workers.SetStatus("ENG-1", "working")
	d.Advance(time.Second)
	d.Tick()
	require.NoError(t, d.Workers.Ask("ENG-1").Err)
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Workers.SetStatus("ENG-1", "idle")
	for range 3 {
		d.Advance(5 * time.Minute)
		d.Tick()
	}

	assert.Empty(t, d.Linear.Moves())
	assert.Len(t, d.Workers.Received("ENG-1"), 1)
	assert.Equal(t, "w-ENG-1|🔴 ENG-1", d.Workers.lastRename(t))
}

func TestTellTurnReachesAnAskingInReviewWorker(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.GitHub.PullRequest("ENG-1", failingPR("a1"))
	require.NoError(t, d.Workers.Ask("ENG-1").Err)
	d.Workers.Stop("ENG-1", stopTasks{Said: "retry for 24 hours?", Transcript: d.transcript("t1")})
	at := d.turnTime()
	d.Workers.SetStatus("ENG-1", "idle")
	d.Tick()
	require.Equal(t, "w-ENG-1|🔴 ENG-1", d.Workers.lastRename(t))

	res := d.Run("tell", "ENG-1", "--turn", at, "yes, retry")

	require.NoError(t, res.Err)
	assert.Equal(t, "told ENG-1\n", res.Stdout)
	assert.Equal(t, []string{"yes, retry"}, d.Workers.Received("ENG-1"))
}

func TestAMessageEndsTheAskAndTheNudgesStartAgainAfterTheNextStop(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		answer func(d *Dispatch)
		told   []string
	}{
		{name: "Typed", answer: func(d *Dispatch) { d.Workers.Say("ENG-1", "use the second option") }},
		// Claude's prompt hook reports a delivered message as it does a typed one.
		{name: "Told", answer: func(d *Dispatch) {
			require.NoError(d.t, d.Run("tell", "ENG-1", "use the second option").Err)
			d.Workers.Say("ENG-1", "use the second option")
		},
			told: []string{"use the second option"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := askingInReview(t, "", failingPR("a1"))
			require.Equal(t, "w-ENG-1|🔴 ENG-1", d.Workers.lastRename(t))

			// Herdr still reads the pane idle after the message lands.
			d.Advance(time.Minute)
			test.answer(d)
			d.Tick()
			d.Advance(5 * time.Minute)
			d.Tick()

			assert.NotEqual(t, "w-ENG-1|🔴 ENG-1", d.Workers.lastRename(t))
			assert.Equal(t, test.told, d.Workers.Received("ENG-1"))

			// Herdr reads the worker working on the message, then idle once it stops.
			d.Workers.SetStatus("ENG-1", "working")
			d.Tick()
			d.Workers.Stop("ENG-1", stopTasks{})
			d.Workers.SetStatus("ENG-1", "idle")
			d.Advance(time.Second)
			d.Tick()

			assert.Equal(t, append(test.told, prPrompt("a failed check")), d.Workers.Received("ENG-1"))
		})
	}
}

func TestAMessageEndsTheAskAndTheApprovedPRMergesAfterTheNextStop(t *testing.T) {
	t.Parallel()
	d := askingInReview(t, autoMergeSetting, approvedPR("a1"))

	d.Advance(time.Minute)
	d.Workers.Say("ENG-1", "go ahead")
	d.Tick()
	d.Advance(5 * time.Minute)
	d.Tick()
	require.Empty(t, d.GitHub.Merged())

	d.Workers.Stop("ENG-1", stopTasks{})
	d.Advance(time.Second)
	d.Tick()

	assert.Equal(t, []string{mergeCall1}, d.GitHub.Merged())
}

func TestATaskNoticeLeavesTheAskOpen(t *testing.T) {
	t.Parallel()
	d := askingInReview(t, autoMergeSetting, approvedPR("a1"))
	require.Equal(t, "w-ENG-1|🔴 ENG-1", d.Workers.lastRename(t))

	d.Advance(time.Minute)
	d.Workers.Say("ENG-1", taskNotice)
	d.Workers.SetStatus("ENG-1", "working")
	d.Tick()
	require.Equal(t, "w-ENG-1|🟢 ENG-1", d.Workers.lastRename(t))
	d.Advance(time.Minute)
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Workers.SetStatus("ENG-1", "idle")
	d.Tick()
	d.Advance(5 * time.Minute)
	d.Tick()

	assert.Equal(t, "w-ENG-1|🔴 ENG-1", d.Workers.lastRename(t))
	assert.Equal(t, []string{askedNotice}, d.Followed.Told())
	assert.Empty(t, d.Workers.Received("ENG-1"))
	assert.Empty(t, d.GitHub.Merged())
}

func TestAnAnsweredAskWhoseTurnEndIsLostIsNudgedFifteenMinutesAfterTheMessage(t *testing.T) {
	t.Parallel()
	d := askingInReview(t, "", failingPR("a1"))

	d.Advance(time.Minute)
	d.Workers.Say("ENG-1", "use the second option")
	d.Tick()
	d.Advance(14 * time.Minute)
	d.Tick()
	require.Empty(t, d.Workers.Received("ENG-1"))

	d.Advance(time.Minute)
	d.Tick()

	assert.Equal(t, []string{prPrompt("a failed check")}, d.Workers.Received("ENG-1"))
}

func TestARuleNoShippedFileDefinesSendsNoNudgeToAnAskingWorker(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: shippedRulesAnd(t, `
whenever({
  name: "poke-idle",
  each: "ticket",
  if:   (t, ctx) => t.status === "in_progress" && ctx.worker.of(t).state === "idle",
  then: (t, ctx) => ctx.worker.queueNudge(t, "anything new?", { once: ["poke", t.id] }),
});
`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	require.NoError(t, d.Workers.Ask("ENG-1").Err)
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Workers.SetStatus("ENG-1", "idle")

	d.Tick()
	res := d.Run("rules", "check")

	assert.Empty(t, d.Workers.Received("ENG-1"))
	assert.Equal(t, "w-ENG-1|🔴 ENG-1", d.Workers.lastRename(t))
	assert.Contains(t, res.Stdout, "exempt for ENG-1 by an-asking-worker-is-left-alone")
}

func TestAnIdleInReviewWorkerThatTookAMessageIsLeftAloneUntilItsTurnEnds(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		settings string
		pr       prState
		done     func(d *Dispatch) []string
		want     []string
	}{
		{name: "Nudge", pr: failingPR("a1"), done: func(d *Dispatch) []string { return d.Workers.Received("ENG-1") },
			want: []string{prPrompt("a failed check")}},
		{name: "Merge", settings: autoMergeSetting, pr: approvedPR("a1"), done: func(d *Dispatch) []string { return d.GitHub.Merged() },
			want: []string{mergeCall1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{RepoSettings: test.settings})
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", "working")
			d.GitHub.PullRequest("ENG-1", test.pr)
			d.Tick()
			require.Empty(t, test.done(d))

			// The pane still reads idle after the message lands.
			d.Workers.Say("ENG-1", "look at the flaky test first")
			d.Workers.SetStatus("ENG-1", "idle")
			d.Tick()
			d.Advance(5 * time.Minute)
			d.Tick()
			assert.Empty(t, test.done(d))

			d.Workers.Stop("ENG-1", stopTasks{})
			d.Advance(time.Second)
			d.Tick()

			assert.Equal(t, test.want, test.done(d))
		})
	}
}
