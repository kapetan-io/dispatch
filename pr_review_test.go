package dispatch_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
)

// reviewedHead is the head commit of the PR the tests review, and reviewedPrefix the part a session passes.
const (
	reviewedHead   = "4f2a9c1d0b7e8a6f5c3d2e1f0a9b8c7d6e5f4a3b"
	reviewedPrefix = "4f2a9c1"
)

// Review runs `dispatch review` with args. A loop starts first when none runs.
func (d *Dispatch) Review(args ...string) Result {
	d.t.Helper()
	return d.Run(append([]string{"review"}, args...)...)
}

// ReviewWhileDown is Review with no dispatch running.
func (d *Dispatch) ReviewWhileDown(args ...string) Result {
	d.t.Helper()
	return d.run(d.options(dispatch.Options{}), append([]string{"review"}, args...)...)
}

// startReview has a session start reviewing ENG-1 at reviewedPrefix and requires it to succeed.
func (d *Dispatch) startReview() {
	d.t.Helper()
	res := d.Review("start", "ENG-1", reviewedPrefix)
	require.NoError(d.t, res.Err)
	require.Equal(d.t, "reviewing ENG-1 at "+reviewedPrefix+"\n", res.Stdout)
}

// reviewDispatch is a dispatch whose loop runs before the test sets up its tickets, so a review starts
// before the first cycle that sees them.
func reviewDispatch(t *testing.T) *Dispatch {
	d := newDispatch(t, dispatchOptions{})
	d.StartBeforeTheEvents()
	return d
}

// reviewedTicket is ENG-1 In Review with an idle, stopped worker and a ready PR at reviewedHead, with
// the loop running.
func reviewedTicket(t *testing.T) *Dispatch {
	d := reviewDispatch(t)
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{})
	d.GitHub.PullRequest("ENG-1", cleanPR(reviewedHead))
	return d
}

func TestReviewShowsAMagnifierForAnIdleOrGoneWorkerWhateverThePRsState(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		// agent is the herdr status of the worker, empty for a workspace without a Claude.
		agent string
		// stop is what the worker's Stop hook reported, empty when it never ran.
		stop        string
		unconfirmed bool
		pr          prState
	}{
		{name: "IdleStoppedReady", agent: "idle", stop: "idle", pr: cleanPR(reviewedHead)},
		{name: "IdleStoppedChecksRunning", agent: "idle", stop: "idle", pr: prState{head: reviewedHead, checks: "PENDING", mergeable: "MERGEABLE"}},
		{name: "IdleStoppedUnresolvedThread", agent: "idle", stop: "idle", pr: prState{head: reviewedHead, checks: "SUCCESS", mergeable: "MERGEABLE", unresolved: 1}},
		{name: "IdleNoStopReady", agent: "idle", pr: cleanPR(reviewedHead)},
		{name: "UnconfirmedReady", agent: "idle", stop: "idle", unconfirmed: true, pr: cleanPR(reviewedHead)},
		{name: "GoneReady", pr: cleanPR(reviewedHead)},
		{name: "GoneChecksRunning", pr: prState{head: reviewedHead, checks: "PENDING", mergeable: "MERGEABLE"}},
		{name: "IdleStoppedDraft", agent: "idle", stop: "idle", pr: prState{head: reviewedHead, checks: "SUCCESS", mergeable: "MERGEABLE", draft: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := reviewDispatch(t)
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", test.agent)
			if test.stop == "idle" {
				d.Workers.Stop("ENG-1", stopTasks{})
			}
			if test.unconfirmed {
				d.Workers.Unconfirmed("ENG-1")
			}
			d.GitHub.PullRequest("ENG-1", test.pr)

			d.startReview()
			d.Tick()

			assert.Equal(t, "w-ENG-1|🔍 ENG-1", d.Workers.lastRename(t))
		})
	}
}

func TestReviewTakesAHeadInEitherCaseAndATicketInEitherCase(t *testing.T) {
	t.Parallel()
	d := reviewedTicket(t)

	res := d.Review("start", "eng-1", "4F2A9C1D")
	require.NoError(t, res.Err)
	d.Tick()

	assert.Equal(t, "reviewing ENG-1 at 4f2a9c1d\n", res.Stdout)
	assert.Equal(t, []string{"w-ENG-1|🔍 ENG-1"}, d.Workers.Renames())
}

func TestReviewShowsNothingForATicketWithNoPR(t *testing.T) {
	t.Parallel()
	d := reviewDispatch(t)
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{})

	d.startReview()
	d.Tick()

	assert.Equal(t, []string{"w-ENG-1|💤 ENG-1"}, d.Workers.Renames())
}

func TestReviewShowsNothingForATicketNotInReview(t *testing.T) {
	t.Parallel()
	d := reviewDispatch(t)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{})
	d.GitHub.PullRequest("ENG-1", cleanPR(reviewedHead))

	d.startReview()
	d.Tick()

	assert.Equal(t, []string{"w-ENG-1|💤 ENG-1"}, d.Workers.Renames())
}

func TestReviewLeavesTheLabelOfABusyBlockedOrNudgedWorkerAsItIs(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		agent string
		stop  *stopTasks
		pr    prState
		want  string
	}{
		{name: "Working", agent: "working", pr: cleanPR(reviewedHead), want: "🟢"},
		{name: "Blocked", agent: "blocked", pr: cleanPR(reviewedHead), want: "🔴"},
		// Without a review a ready PR shows 👀 over the background task.
		{name: "IdleBackgroundTaskReady", agent: "idle", stop: &stopTasks{Running: 1}, pr: cleanPR(reviewedHead), want: "🟢"},
		{name: "IdleBackgroundTaskChecksRunning", agent: "idle", stop: &stopTasks{Running: 1}, pr: prState{head: reviewedHead, checks: "PENDING", mergeable: "MERGEABLE"}, want: "🟢"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := reviewDispatch(t)
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", test.agent)
			if test.stop != nil {
				d.Workers.Stop("ENG-1", *test.stop)
			}
			d.GitHub.PullRequest("ENG-1", test.pr)

			d.startReview()
			d.Tick()

			assert.Equal(t, "w-ENG-1|"+test.want+" ENG-1", d.Workers.lastRename(t))
			assert.NotContains(t, d.Workers.Renames(), "w-ENG-1|🔍 ENG-1")
		})
	}
}

func TestReviewShowsANudgedWorkerAsGreen(t *testing.T) {
	t.Parallel()
	d := reviewDispatch(t)
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{})
	d.GitHub.PullRequest("ENG-1", prState{head: reviewedHead, checks: "FAILURE", mergeable: "MERGEABLE"})

	d.startReview()
	d.Tick()

	// The idle worker shows 🔍 where it would show 💤, and the PR nudge it gets for the failed check turns it 🟢.
	assert.Len(t, d.Workers.Received("ENG-1"), 1)
	assert.Equal(t, []string{"w-ENG-1|🔍 ENG-1", "w-ENG-1|🟢 ENG-1"}, d.Workers.Renames())
}

func TestReviewLeavesTheRedOfAnAskingWorker(t *testing.T) {
	t.Parallel()
	d := reviewDispatch(t)
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.GitHub.PullRequest("ENG-1", cleanPR(reviewedHead))
	d.startReview()

	require.NoError(t, d.Workers.Ask("ENG-1").Err)
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Workers.SetStatus("ENG-1", "idle")
	d.Tick()
	d.Advance(5 * time.Minute)
	d.Tick()

	assert.Equal(t, "w-ENG-1|🔴 ENG-1", d.Workers.lastRename(t))
	assert.NotContains(t, d.Workers.Renames(), "w-ENG-1|🔍 ENG-1")
}

func TestReviewLeavesTheRedOfANeedsHumanTicket(t *testing.T) {
	t.Parallel()
	d := reviewDispatch(t)
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{})
	d.GitHub.PullRequest("ENG-1", cleanPR(reviewedHead))

	d.startReview()
	d.Tick()

	assert.Equal(t, []string{"w-ENG-1|🔴 ENG-1"}, d.Workers.Renames())
}

func TestReviewDoneReturnsTheLabelToThePRs(t *testing.T) {
	t.Parallel()
	d := reviewedTicket(t)
	d.startReview()
	d.Tick()

	res := d.Review("done", "ENG-1")
	require.NoError(t, res.Err)
	d.Tick()

	assert.Equal(t, "review of ENG-1 done\n", res.Stdout)
	assert.Equal(t, []string{"w-ENG-1|🔍 ENG-1", "w-ENG-1|👀 ENG-1"}, d.Workers.Renames())
}

func TestReviewDoneWithNoReviewFails(t *testing.T) {
	t.Parallel()
	d := reviewedTicket(t)
	d.startReview()
	require.NoError(t, d.Review("done", "ENG-1").Err)

	res := d.Review("done", "ENG-1")

	require.EqualError(t, res.Err, "ENG-1 has no review")
	assert.Equal(t, 1, res.Code)
	assert.Empty(t, res.Stdout)
}

func TestReviewRejectsBadArguments(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "NoVerb", args: nil, wantErr: "review takes start"},
		{name: "UnknownVerb", args: []string{"begin", "ENG-1", reviewedPrefix}, wantErr: "review takes start"},
		{name: "StartWithoutHead", args: []string{"start", "ENG-1"}, wantErr: "review takes start"},
		{name: "DoneWithHead", args: []string{"done", "ENG-1", reviewedPrefix}, wantErr: "review takes start"},
		{name: "NotATicket", args: []string{"start", "ENG1", reviewedPrefix}, wantErr: `"ENG1" is not a ticket identifier`},
		{name: "DoneNotATicket", args: []string{"done", "1"}, wantErr: `"1" is not a ticket identifier`},
		{name: "HeadTooShort", args: []string{"start", "ENG-1", "4f2a9c"}, wantErr: `"4f2a9c" is not 7 to 40 hex digits`},
		{name: "HeadTooLong", args: []string{"start", "ENG-1", reviewedHead + "0"}, wantErr: "is not 7 to 40 hex digits"},
		{name: "HeadNotHex", args: []string{"start", "ENG-1", "4f2a9cz"}, wantErr: `"4f2a9cz" is not 7 to 40 hex digits`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := reviewedTicket(t)

			res := d.Review(test.args...)
			d.Tick()

			require.ErrorContains(t, res.Err, test.wantErr)
			assert.Equal(t, 1, res.Code)
			assert.Equal(t, []string{"w-ENG-1|👀 ENG-1"}, d.Workers.Renames())
		})
	}
}

func TestReviewEndsWhenThePRsHeadMoves(t *testing.T) {
	t.Parallel()
	d := reviewedTicket(t)
	d.startReview()
	d.Tick()

	d.GitHub.PullRequest("ENG-1", cleanPR("9e8d7c6b5a4f3e2d1c0b9a8f7e6d5c4b3a2f1e0d"))
	d.Tick()

	assert.Equal(t, []string{"w-ENG-1|🔍 ENG-1", "w-ENG-1|👀 ENG-1"}, d.Workers.Renames())
}

func TestReviewEndsAnHourAfterItsStart(t *testing.T) {
	t.Parallel()
	d := reviewedTicket(t)
	d.startReview()
	d.Tick()

	d.Advance(59 * time.Minute)
	d.Tick()
	assert.Equal(t, []string{"w-ENG-1|🔍 ENG-1"}, d.Workers.Renames())

	d.Advance(2 * time.Minute)
	d.Tick()
	assert.Equal(t, []string{"w-ENG-1|🔍 ENG-1", "w-ENG-1|👀 ENG-1"}, d.Workers.Renames())
}

func TestReviewOutlivesARestartAndKeepsItsStart(t *testing.T) {
	t.Parallel()
	d := reviewedTicket(t)
	d.startReview()
	d.Tick()
	d.Advance(30 * time.Minute)

	d.Restart()
	d.Tick()
	assert.Equal(t, []string{"w-ENG-1|🔍 ENG-1"}, d.Workers.Renames())

	d.Advance(31 * time.Minute)
	d.Tick()
	assert.Equal(t, []string{"w-ENG-1|🔍 ENG-1", "w-ENG-1|👀 ENG-1"}, d.Workers.Renames())
}

func TestReviewWhileDispatchIsDownRecordsNothing(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "Start", args: []string{"start", "ENG-1", reviewedPrefix}},
		{name: "Done", args: []string{"done", "ENG-1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			// No Stop record, since a Stop reported before any loop runs is answered on the loop's socket.
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			d.GitHub.PullRequest("ENG-1", cleanPR(reviewedHead))

			res := d.ReviewWhileDown(test.args...)
			d.Tick()

			require.EqualError(t, res.Err, "not running")
			assert.Equal(t, 3, res.Code)
			assert.Equal(t, []string{"w-ENG-1|👀 ENG-1"}, d.Workers.Renames())
		})
	}
}

func TestReviewKeepsTheMagnifierWhenThePRCannotBeRead(t *testing.T) {
	t.Parallel()
	d := reviewedTicket(t)
	d.startReview()
	d.Tick()

	d.GitHub.FailRead(errors.New("exit status 1"))
	d.Advance(time.Minute)
	d.Tick()

	assert.Equal(t, []string{"w-ENG-1|🔍 ENG-1"}, d.Workers.Renames())
}

func TestReviewDropsTheKeptMagnifierAnHourAfterItsStartWhenThePRCannotBeRead(t *testing.T) {
	t.Parallel()
	d := reviewedTicket(t)
	d.startReview()
	d.Tick()

	d.GitHub.FailRead(errors.New("exit status 1"))
	d.Advance(61 * time.Minute)
	d.Tick()

	assert.Equal(t, []string{"w-ENG-1|🔍 ENG-1", "w-ENG-1|💤 ENG-1"}, d.Workers.Renames())
}

func TestReviewDropsTheKeptMagnifierOfAWorkerThatStartsWorkingWhenThePRCannotBeRead(t *testing.T) {
	t.Parallel()
	d := reviewedTicket(t)
	d.startReview()
	d.Tick()

	d.GitHub.FailRead(errors.New("exit status 1"))
	d.Workers.SetStatus("ENG-1", "working")
	d.Tick()

	assert.Equal(t, []string{"w-ENG-1|🔍 ENG-1", "w-ENG-1|🟢 ENG-1"}, d.Workers.Renames())
}

func TestReviewShowsAMagnifierAtTheNextCycle(t *testing.T) {
	t.Parallel()
	d := reviewedTicket(t)

	d.startReview()
	// The resync, every 5 minutes by default, runs the next cycle.
	d.Advance(5 * time.Minute)

	assert.Equal(t, []string{"w-ENG-1|🔍 ENG-1"}, d.Workers.Renames())
}

func TestReviewStartReplacesAnEarlierStart(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		first string
		later string
		want  []string
	}{
		{name: "OtherHeadThenThisHead", first: "9e8d7c6", later: reviewedPrefix, want: []string{"w-ENG-1|🔍 ENG-1"}},
		{name: "ThisHeadThenOtherHead", first: reviewedPrefix, later: "9e8d7c6", want: []string{"w-ENG-1|👀 ENG-1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := reviewedTicket(t)

			require.NoError(t, d.Review("start", "ENG-1", test.first).Err)
			require.NoError(t, d.Review("start", "ENG-1", test.later).Err)
			d.Tick()

			assert.Equal(t, test.want, d.Workers.Renames())
		})
	}
}

func TestReviewStartAgainCountsTheHourFromTheLaterStart(t *testing.T) {
	t.Parallel()
	d := reviewedTicket(t)
	d.startReview()
	d.Tick()

	d.Advance(40 * time.Minute)
	d.startReview()
	d.Advance(30 * time.Minute)
	d.Tick()
	assert.Equal(t, []string{"w-ENG-1|🔍 ENG-1"}, d.Workers.Renames())

	d.Advance(31 * time.Minute)
	d.Tick()
	assert.Equal(t, []string{"w-ENG-1|🔍 ENG-1", "w-ENG-1|👀 ENG-1"}, d.Workers.Renames())
}

func TestReviewDoneEndsAReviewAnotherSessionStarted(t *testing.T) {
	t.Parallel()
	d := reviewedTicket(t)
	d.env = d.env.With("CLAUDE_CODE_SESSION_ID", "reviewer-session")
	d.startReview()
	d.Tick()

	d.env = d.env.With("CLAUDE_CODE_SESSION_ID", "other-session")
	res := d.Review("done", "eng-1")
	require.NoError(t, res.Err)
	d.Tick()

	assert.Equal(t, "review of ENG-1 done\n", res.Stdout)
	assert.Equal(t, []string{"w-ENG-1|🔍 ENG-1", "w-ENG-1|👀 ENG-1"}, d.Workers.Renames())
}

func TestReviewDoneOfATicketNeverReviewedFails(t *testing.T) {
	t.Parallel()
	d := reviewedTicket(t)

	res := d.Review("done", "ENG-1")
	d.Tick()

	require.EqualError(t, res.Err, "ENG-1 has no review")
	assert.Equal(t, 1, res.Code)
	assert.Empty(t, res.Stdout)
	assert.Equal(t, []string{"w-ENG-1|👀 ENG-1"}, d.Workers.Renames())
}

func TestReviewDoneWhileDispatchIsDownLeavesTheReview(t *testing.T) {
	t.Parallel()
	d := reviewedTicket(t)
	d.startReview()
	d.Tick()
	d.Stop()

	res := d.ReviewWhileDown("done", "ENG-1")
	d.Restart()
	d.Tick()

	require.EqualError(t, res.Err, "not running")
	assert.Equal(t, 3, res.Code)
	assert.Equal(t, []string{"w-ENG-1|🔍 ENG-1"}, d.Workers.Renames())
}
