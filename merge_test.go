package dispatch_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// EditRepoSettings adds settings to the dispatch repo's table in config.toml, as an operator editing
// the file does. A running loop keeps the config it started with.
func (d *Dispatch) EditRepoSettings(settings string) { d.repoSettings("dispatch", settings) }

func TestTickNotifiesOnceWhenAnInReviewPRIsReady(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))

	d.Tick()

	assert.Equal(t, []string{readyText1}, d.Alerts.Shown())
	assert.Empty(t, d.Workers.Prompts())
	noLinearChanges(t, d)

	// No later tick repeats it.
	d.Tick()
	d.Tick()
	assert.Len(t, d.Alerts.Shown(), 1)

	// A push that leaves the PR ready again notifies again.
	d.GitHub.PullRequest("ENG-1", cleanPR("a2"))
	d.Tick()
	assert.Len(t, d.Alerts.Shown(), 2)
}

func TestTickDoesNotNotifyWhenAPRIsNotReady(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		status string
		pr     prState
	}{
		{name: "CheckStillRunning", status: "idle", pr: prState{head: "a1", checks: "PENDING", decision: "APPROVED", mergeable: "MERGEABLE"}},
		{name: "CheckExpected", status: "idle", pr: prState{head: "a1", checks: "EXPECTED", mergeable: "MERGEABLE"}},
		{name: "FailedCheck", status: "idle", pr: prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"}},
		{name: "UnresolvedThread", status: "idle", pr: prState{head: "a1", checks: "SUCCESS", mergeable: "MERGEABLE", unresolved: 1}},
		{name: "ChangesRequested", status: "idle", pr: prState{head: "a1", checks: "SUCCESS", decision: "CHANGES_REQUESTED", mergeable: "MERGEABLE"}},
		{name: "MergeConflict", status: "idle", pr: prState{head: "a1", checks: "SUCCESS", mergeable: "CONFLICTING"}},
		{name: "MergeStateNotComputed", status: "idle", pr: prState{head: "a1", checks: "SUCCESS", mergeable: "UNKNOWN"}},
		{name: "WorkerWorking", status: "working", pr: cleanPR("a1")},
		{name: "WorkerBlocked", status: "blocked", pr: cleanPR("a1")},
		{name: "NoWorker", status: "", pr: cleanPR("a1")},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", test.status)
			d.GitHub.PullRequest("ENG-1", test.pr)

			d.Tick()

			for _, n := range d.Alerts.Shown() {
				assert.NotContains(t, n, "ready for review")
			}
		})
	}
}

func TestTickNotifiesWhenAPRMidReviewFinishesItsChecks(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "PENDING", mergeable: "MERGEABLE"})

	d.Tick()
	assert.Empty(t, d.Alerts.Shown())

	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	d.Tick()

	assert.Equal(t, []string{readyText1}, d.Alerts.Shown())
}

func TestTickRetriesAReadyNotificationThatFailed(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	d.Alerts.Fail(errors.New("exit status 1"))

	require.ErrorContains(t, d.Run("tick").Err, "ENG-1")
	assert.Empty(t, d.Alerts.Shown())

	d.Alerts.Fail(nil)
	d.Tick()

	assert.Equal(t, []string{readyText1}, d.Alerts.Shown())
}

func TestTickNotifiesWhenAReadyPRHasNoChecks(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", mergeable: "MERGEABLE", noChecks: true})

	d.Tick()

	assert.Equal(t, []string{readyText1}, d.Alerts.Shown())
}

func TestTickNotifiesForPRsThatAreStillReady(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		pr   prState
	}{
		{name: "NoReviewDecision", pr: prState{head: "a1", checks: "SUCCESS", mergeable: "MERGEABLE"}},
		{name: "ReviewRequired", pr: prState{head: "a1", checks: "SUCCESS", decision: "REVIEW_REQUIRED", mergeable: "MERGEABLE"}},
		{name: "OnlyResolvedThreads", pr: prState{head: "a1", checks: "SUCCESS", decision: "APPROVED", mergeable: "MERGEABLE", resolved: 3}},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			d.GitHub.PullRequest("ENG-1", test.pr)

			d.Tick()

			assert.Equal(t, []string{readyText1}, d.Alerts.Shown())
		})
	}
}

func TestTickDoesNotNotifyForMoreNotReadyPRs(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		pr   prState
	}{
		{name: "CheckErrored", pr: prState{head: "a1", checks: "ERROR", decision: "APPROVED", mergeable: "MERGEABLE"}},
		{name: "ThreadsBeyondOnePage", pr: prState{head: "a1", checks: "SUCCESS", decision: "APPROVED", mergeable: "MERGEABLE", moreThreads: true}},
		{name: "OneUnresolvedAmongResolved", pr: prState{head: "a1", checks: "SUCCESS", decision: "APPROVED", mergeable: "MERGEABLE", resolved: 2, unresolved: 1}},
		{name: "NoMergeableState", pr: prState{head: "a1", checks: "SUCCESS", decision: "APPROVED"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			d.GitHub.PullRequest("ENG-1", test.pr)

			d.Tick()

			assert.Empty(t, d.Alerts.Shown())
		})
	}
}

func TestTickNotifiesOnlyForIdleWorkersOfInReviewTickets(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"In Progress", "Needs Human", "Backlog", "Todo"} {
		t.Run(status, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", status, "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			d.GitHub.PullRequest("ENG-1", cleanPR("a1"))

			d.Tick()

			for _, n := range d.Alerts.Shown() {
				assert.NotContains(t, n, "ready for review")
			}
		})
	}
}

func TestTickNotifiesWhenAWorkingWorkerGoesIdleOnAReadyPR(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	d.Tick()
	assert.Empty(t, d.Alerts.Shown())

	d.Workers.SetStatus("ENG-1", "idle")
	d.Tick()

	assert.Equal(t, []string{readyText1}, d.Alerts.Shown())
}

func TestTickDoesNotRepeatTheNotificationWhenTheSameHeadIsReadyAgain(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	d.Tick()

	// A check reruns on the same commit and passes again.
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "PENDING", decision: "APPROVED", mergeable: "MERGEABLE"})
	d.Tick()
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	d.Tick()

	assert.Len(t, d.Alerts.Shown(), 1)
}

func TestTickDoesNotRepeatTheNotificationWhenThePRIsUnreadableForATick(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	d.Tick()

	d.GitHub.FailRead(errors.New("exit status 1"))
	d.Tick()
	d.GitHub.FailRead(nil)
	d.Tick()

	assert.Len(t, d.Alerts.Shown(), 1)
}

func TestTickNotifiesForTheNewHeadWhenAFailedNotificationIsFollowedByAPush(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	d.Alerts.Fail(errors.New("exit status 1"))
	require.ErrorContains(t, d.Run("tick").Err, "ENG-1")

	d.Alerts.Fail(nil)
	d.Advance(time.Second)
	d.GitHub.PullRequest("ENG-1", cleanPR("a2"))
	d.Tick()
	d.Tick()

	assert.Equal(t, []string{readyText1}, d.Alerts.Shown())
}

func TestTickNotifiesOncePerReadyTicket(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"), ticket("ENG-2", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Running("ENG-2", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	d.GitHub.PullRequest("ENG-2", prState{head: "b1", checks: "PENDING", mergeable: "MERGEABLE"})

	d.Tick()

	assert.Equal(t, []string{readyText1}, d.Alerts.Shown())
}

func TestTickDoesNotNotifyForAReadyPRWhileTheTicketIsNotInReview(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	d.Tick()

	// Review sends the ticket back to In Progress and the worker pushes again.
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()

	assert.Len(t, d.Alerts.Shown(), 1)
}

func TestTickDoesNotRepeatTheReadyNotificationAfterTheTicketLeavesAndReturnsToInReview(t *testing.T) {
	t.Parallel()
	for _, between := range []string{"Needs Human", "In Progress"} {
		t.Run(between, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
			d.Tick()

			d.Linear.Tickets(ticket("ENG-1", between, "dispatch"))
			d.Tick()
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Tick()

			var ready int
			for _, n := range d.Alerts.Shown() {
				if strings.Contains(n, "ready for review") {
					ready++
				}
			}
			assert.Equal(t, 1, ready)
		})
	}
}

func TestTickDoesNotRepeatTheReadyNotificationAfterTheTicketDropsOutOfTheManagedRead(t *testing.T) {
	t.Parallel()
	for _, between := range []string{"In Review", "Todo"} {
		t.Run(between, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
			d.Tick()

			// The ticket leaves the started, assigned read for one tick, such as
			// when it is unassigned, and comes back at the same head.
			d.Linear.Tickets()
			d.Linear.Finished(ticket("ENG-1", between, "dispatch"))
			d.Tick()
			d.Linear.Finished()
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Tick()

			var ready int
			for _, n := range d.Alerts.Shown() {
				if strings.Contains(n, "ready for review") {
					ready++
				}
			}
			assert.Equal(t, 1, ready)
		})
	}
}

func TestTickNotifiesWhenTheOnlyChangesRequestedIsOnAnOlderCommit(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", prState{head: "b2", checks: "SUCCESS", changesOn: []string{"a1"}, mergeable: "MERGEABLE"})

	d.Tick()

	assert.Len(t, d.Alerts.Shown(), 1)
	assert.Empty(t, d.Workers.Prompts())
}

func TestTickMergesAnApprovedReadyPRInAListedRepo(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", approvedPR("a1"))

	d.Tick()

	assert.Equal(t, []string{mergeCall1}, d.GitHub.Merged())
	assert.Equal(t, []string{mergedText1}, d.Alerts.Shown())
	assert.Empty(t, d.Workers.Prompts())

	// The merged PR is no longer open, so later ticks do nothing.
	d.Tick()
	assert.Len(t, d.GitHub.Merged(), 1)
	assert.Len(t, d.Alerts.Shown(), 1)
}

func TestTickDoesNotMergeInARepoWithoutAutoMerge(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		settings string
		other    string
	}{
		{name: "NoSetting"},
		{name: "SetFalse", settings: "auto_merge = false\n"},
		{name: "OnlyAnotherRepoSetsIt", other: "auto_merge = true\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{RepoSettings: test.settings, Config: tigerRepo(t, test.other)})
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			d.GitHub.PullRequest("ENG-1", approvedPR("a1"))

			d.Tick()

			assert.Equal(t, []string{readyText1}, d.Alerts.Shown())
			assert.Empty(t, d.GitHub.Merged())
		})
	}
}

func TestTickDoesNotMergeAPRWithoutAnApprovalOnItsHead(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		pr   prState
	}{
		{name: "NoReview", pr: prState{head: "a1", checks: "SUCCESS", mergeable: "MERGEABLE"}},
		{name: "ApprovedAnEarlierCommit", pr: prState{head: "a2", checks: "SUCCESS", decision: "APPROVED", mergeable: "MERGEABLE", approved: []string{"a1"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting})
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			d.GitHub.PullRequest("ENG-1", test.pr)

			d.Tick()

			assert.Equal(t, []string{readyText1}, d.Alerts.Shown())
			assert.Empty(t, d.GitHub.Merged())
		})
	}
}

func TestTickDoesNotMergeAnApprovedPRThatIsNotReady(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		pr   prState
	}{
		{name: "CheckStillRunning", pr: prState{head: "a1", checks: "PENDING", mergeable: "MERGEABLE", approved: []string{"a1"}}},
		{name: "FailedCheck", pr: prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE", approved: []string{"a1"}}},
		{name: "UnresolvedThread", pr: prState{head: "a1", checks: "SUCCESS", mergeable: "MERGEABLE", unresolved: 1, approved: []string{"a1"}}},
		{name: "MergeConflict", pr: prState{head: "a1", checks: "SUCCESS", mergeable: "CONFLICTING", approved: []string{"a1"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting})
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			d.GitHub.PullRequest("ENG-1", test.pr)

			d.Tick()

			assert.Empty(t, d.GitHub.Merged())
		})
	}
}

func TestTickMergesAfterAnApprovalArrivesOnAnAlreadyAnnouncedHead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "SUCCESS", mergeable: "MERGEABLE"})

	d.Tick()
	assert.Equal(t, []string{readyText1}, d.Alerts.Shown())

	d.GitHub.PullRequest("ENG-1", approvedPR("a1"))
	d.Tick()

	assert.Equal(t, []string{mergeCall1}, d.GitHub.Merged())
	assert.Equal(t, []string{readyText1, mergedText1}, d.Alerts.Shown())
}

func TestTickTellsFollowersAboutAMergeNoticeWhenTheAlertFails(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		refused bool
		want    string
	}{
		{name: "Merged", want: "ENG-1 was approved and merged"},
		{name: "MergeFailed", refused: true, want: "merging " + prURL1 + " failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			socket, lines := sessionSocket(t)
			d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting, Env: map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": socket}})
			d.StartBeforeTheEvents()
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			d.GitHub.PullRequest("ENG-1", approvedPR("a1"))
			require.NoError(t, d.Run("watch", "ENG-1").Err)
			d.Alerts.Fail(errors.New("exit status 1"))
			if test.refused {
				d.GitHub.FailMerge(errors.New("while running gh pr merge " + prURL1 + ": exit status 1: " + mergeRefused))
			}

			res := d.Run("tick")
			if test.refused {
				require.ErrorContains(t, res.Err, "merge refused")
			} else {
				require.NoError(t, res.Err)
			}

			// The alert failed, yet the followers heard and the notice is Done, not retried.
			assert.Contains(t, next(t, lines), test.want)
			assert.Empty(t, d.Alerts.Shown())
			notices := d.receiptsOfKind("ENG-1", "notify")
			require.Len(t, notices, 1)
			assert.Equal(t, "done", notices[0].State)
			assert.Contains(t, notices[0].Detail, "alert failed")

			d.Alerts.Fail(nil)
			d.GitHub.FailMerge(nil)
			d.Advance(5 * time.Minute)
			d.Advance(time.Second)
			d.Tick()
			assert.Empty(t, lines)
			assert.Empty(t, d.Alerts.Shown())
			assert.Len(t, d.receiptsOfKind("ENG-1", "notify"), 1)
		})
	}
}

func TestTickWaitsForANewApprovalAfterAPush(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", prState{head: "a2", checks: "SUCCESS", decision: "APPROVED", mergeable: "MERGEABLE", approved: []string{"a1"}})

	d.Tick()
	assert.Empty(t, d.GitHub.Merged())

	d.GitHub.PullRequest("ENG-1", approvedPR("a2"))
	d.Tick()

	assert.Equal(t, []string{strings.Replace(mergeCall1, "a1", "a2", 1)}, d.GitHub.Merged())
}

func TestTickReportsAFailedMergeOnceAndRetriesOnlyAfterAPush(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", approvedPR("a1"))
	d.GitHub.FailMerge(errors.New("while running gh pr merge " + prURL1 + ": exit status 1: " + mergeRefused))

	require.ErrorContains(t, d.Run("tick").Err, "the base branch policy prohibits the merge")

	require.Len(t, d.Alerts.Shown(), 1)
	assert.Contains(t, d.Alerts.Shown()[0], "ENG-1: merging "+prURL1+" failed")
	assert.Contains(t, d.Alerts.Shown()[0], "the base branch policy prohibits the merge")
	attempts := d.receiptsOfKind("ENG-1", "merge")
	require.Len(t, attempts, 1)
	assert.Equal(t, "refused", attempts[0].State)
	assert.Equal(t, "[merge a1]", attempts[0].Once)

	// A refused merge never runs again for its head, not at a tick and not at a resync,
	// and the user hears nothing more.
	d.GitHub.FailMerge(nil)
	d.Advance(time.Second)
	d.Tick()
	d.Advance(5 * time.Minute)
	d.Advance(time.Second)
	d.Tick()
	assert.Empty(t, d.GitHub.Merged())
	assert.Len(t, d.Alerts.Shown(), 1)
	assert.Len(t, d.receiptsOfKind("ENG-1", "merge"), 1)

	// A push that gets approved is a new head, so it merges.
	d.GitHub.PullRequest("ENG-1", approvedPR("a2"))
	d.Tick()
	assert.Len(t, d.GitHub.Merged(), 1)
	attempts = d.receiptsOfKind("ENG-1", "merge")
	require.Len(t, attempts, 2)
	assert.Equal(t, "done", attempts[1].State)
	assert.Equal(t, "[merge a2]", attempts[1].Once)
}

func TestTickMergesNothingWhenThePRHeadMovesAfterTheApprovalWasRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", approvedPR("a1"))
	// Someone pushes between dispatch reading the approval and merging.
	d.GitHub.PushAfterRead("ENG-1", "a2")

	require.ErrorContains(t, d.Run("tick").Err, "Head branch was modified")

	assert.Empty(t, d.GitHub.Merged())
	assert.Equal(t, "a2", d.GitHub.Head("ENG-1"))
	require.Len(t, d.Alerts.Shown(), 1)
	assert.Contains(t, d.Alerts.Shown()[0], "ENG-1: merging "+prURL1+" failed")

	// The refused merge is the only attempt for a1, and the new head has no approval, so it
	// is announced as ready for review and not merged.
	d.Advance(time.Second)
	d.Tick()
	assert.Empty(t, d.GitHub.Merged())
	assert.Contains(t, d.Alerts.Shown()[len(d.Alerts.Shown())-1], "ready for review")
	attempts := d.receiptsOfKind("ENG-1", "merge")
	require.Len(t, attempts, 1)
	assert.Equal(t, "refused", attempts[0].State)

	// An approval of the head that moved merges it: a push makes a new key.
	d.GitHub.PullRequest("ENG-1", approvedPR("a2"))
	d.Tick()
	assert.Equal(t, []string{strings.Replace(mergeCall1, "a1", "a2", 1)}, d.GitHub.Merged())
}

func TestTickReadsAutoMergeOnRestart(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", approvedPR("a1"))

	// Not listed yet, so the PR is announced.
	d.Tick()
	assert.Empty(t, d.GitHub.Merged())

	// The loop reads config.toml when it starts, so a running loop still does not merge.
	d.EditRepoSettings(autoMergeSetting)
	d.Tick()
	assert.Empty(t, d.GitHub.Merged())

	d.Restart()
	d.Tick()
	assert.Equal(t, []string{mergeCall1}, d.GitHub.Merged())
}

func TestTickDoesNotMergeWhenTheReviewOnTheHeadIsNotAnApproval(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"CHANGES_REQUESTED", "COMMENTED", "DISMISSED"} {
		t.Run(state, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting})
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "SUCCESS", decision: "APPROVED", mergeable: "MERGEABLE",
				opinions: []opinion{{state: state, commit: "a1", author: "reviewer"}}})

			d.Tick()

			assert.Empty(t, d.GitHub.Merged())
		})
	}
}

func TestTickDoesNotRetryAFailedMergeOnAHeadAnnouncedAsReady(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	d.Tick()
	d.GitHub.PullRequest("ENG-1", approvedPR("a1"))
	d.GitHub.FailMerge(errors.New("while running gh pr merge " + prURL1 + ": exit status 1: " + mergeRefused))
	require.ErrorContains(t, d.Run("tick").Err, "merge refused")
	d.GitHub.FailMerge(nil)

	d.Advance(time.Second)
	d.Tick()

	assert.Empty(t, d.GitHub.Merged())
	assert.Len(t, d.Alerts.Shown(), 2)
}

func TestTickDoesNotRetryAFailedMergeAfterTheTicketLeavesTheReadAndReturns(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", approvedPR("a1"))
	d.GitHub.FailMerge(errors.New("while running gh pr merge " + prURL1 + ": exit status 1: " + mergeRefused))
	require.ErrorContains(t, d.Run("tick").Err, "merge refused")
	require.Len(t, d.Alerts.Shown(), 1)

	// One tick where the ticket is missing from Linear's read, with its tree still leased.
	d.Linear.Tickets()
	d.Advance(time.Second)
	d.Tick()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.GitHub.FailMerge(nil)
	d.Advance(time.Second)
	d.Tick()

	assert.Len(t, d.Alerts.Shown(), 1)
	assert.Empty(t, d.GitHub.Merged())
}

// Without branch protection GitHub leaves reviewDecision empty even when a
// reviewer's latest review requests changes.
func TestTickDoesNotMergeOverAnotherReviewersChangesRequested(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "SUCCESS", mergeable: "MERGEABLE", approved: []string{"a1"}, changesOn: []string{"a1"}})

	d.Tick()

	assert.Empty(t, d.GitHub.Merged())
}

func TestTickDoesNotMergeAForkPRThatSharesTheWorkersBranchName(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	fork := approvedPR("evil")
	fork.fork = true
	d.GitHub.PullRequest("ENG-1", fork)

	d.Tick()
	d.Advance(5 * time.Minute)
	d.Tick()

	assert.Empty(t, d.GitHub.Merged())
	assert.Empty(t, d.Alerts.Shown())
	assert.Empty(t, d.receiptsOfKind("ENG-1", "merge"))
}

func TestTickUsesTheWorkersPRWhenAForkPRSharesItsBranchName(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	pr := approvedPR("a1")
	pr.forksAhead = 1
	d.GitHub.PullRequest("ENG-1", pr)

	d.Tick()

	assert.Equal(t, []string{mergeCall1}, d.GitHub.Merged())
}

func TestTickFindsTheWorkersPRBehindAFullPageOfForkPRs(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	pr := approvedPR("a1")
	pr.forkPages = 1
	d.GitHub.PullRequest("ENG-1", pr)

	d.Tick()

	assert.Equal(t, []string{mergeCall1}, d.GitHub.Merged())
	// Each read of the branch follows the cursor to the second page; the loop reads again
	// at the tick and after the merge.
	require.GreaterOrEqual(t, len(d.GitHub.Reads()), 2)
	assert.Equal(t, []string{"thrawn01/eng-1", "thrawn01/eng-1.c1"}, d.GitHub.Reads()[:2])
}

func TestTickReportsABranchWhoseForkPRsFillEveryPageItReads(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	// Every page points at the next one, so the worker's PR is never reached.
	pr := approvedPR("a1")
	pr.forkPages, pr.endlessForks = 1, true
	d.GitHub.PullRequest("ENG-1", pr)

	// The ticket is held and the tick still succeeds.
	d.Tick()

	assert.Contains(t, d.LoopStderr(), "while reading the PR for ENG-1")
	assert.Contains(t, d.LoopStderr(), "none from this repo")
	assert.Regexp(t, `held\s+ENG-1\s+-\s+auto-merge\s+the PR for ENG-1 could not be read`, d.Run("plan").Stdout)
	assert.Empty(t, d.GitHub.Merged())
	// Each read of the branch stops after five pages.
	assert.NotEmpty(t, d.GitHub.Reads())
	assert.Zero(t, len(d.GitHub.Reads())%5)
}
