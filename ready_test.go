package dispatch_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdrtest"
	"github.com/kapetan-io/dispatch/internal/ledger"
)

const readyText1 = "ENG-1 is ready for review: " + prURL1

func TestTickNotifiesOnceWhenAnInReviewPRIsReady(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", cleanPR("a1"))

	h.tick()

	assert.Equal(t, []string{readyText1}, h.notifications())
	assert.Empty(t, h.prompts())
	assert.Empty(t, h.linear.mutations())

	// No later tick repeats it.
	h.tick()
	h.tick()
	assert.Len(t, h.notifications(), 1)

	// A push that leaves the PR ready again notifies again.
	h.pr("ENG-1", cleanPR("a2"))
	h.tick()
	assert.Len(t, h.notifications(), 2)
}

func TestTickDoesNotNotifyWhenAPRIsNotReady(t *testing.T) {
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
			h := newHarness(t)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", test.status)
			h.pr("ENG-1", test.pr)

			_, err := h.run("tick")
			require.NoError(t, err)

			for _, n := range h.notifications() {
				assert.NotContains(t, n, "ready for review")
			}
		})
	}
}

func TestTickNotifiesWhenAPRMidReviewFinishesItsChecks(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a1", checks: "PENDING", mergeable: "MERGEABLE"})

	_, err := h.run("tick")
	require.NoError(t, err)
	assert.Empty(t, h.notifications())

	h.pr("ENG-1", cleanPR("a1"))
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{readyText1}, h.notifications())
}

func TestTickRetriesAReadyNotificationThatFailed(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", cleanPR("a1"))
	h.tools.Alerts.Fail(errors.New("exit status 1"))

	_, err := h.run("tick")
	require.ErrorContains(t, err, "ENG-1")
	assert.Empty(t, h.notifications())

	h.tools.Alerts.Fail(nil)
	h.tick()

	assert.Equal(t, []string{readyText1}, h.notifications())
}

func TestTickKeepsAReadyNoticeFromFollowersWhenTheAlertFailsAndRunsItAgain(t *testing.T) {
	h := newHarness(t)
	h.startBeforeTheEvents()
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", cleanPR("a1"))
	socket, lines := sessionSocket(t)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
	_, err := h.run("watch", "ENG-1")
	require.NoError(t, err)
	h.tools.Alerts.Fail(errors.New("exit status 1"))

	// A notice that did not reach the alert has not reached a follower either.
	_, err = h.run("tick")
	require.ErrorContains(t, err, "ENG-1 notify failed")
	assert.Empty(t, h.notifications())
	assert.Empty(t, lines)
	notices := h.receipts("notify", "")
	require.Len(t, notices, 1)
	assert.Equal(t, ledger.Failed, notices[0].Outcome)

	// The next tick runs it again, and the followers hear it once the alert shows.
	h.tools.Alerts.Fail(nil)
	h.advance(time.Second)
	h.tick()

	assert.Contains(t, next(t, lines), "ENG-1 is ready for review")
	assert.Equal(t, []string{readyText1}, h.notifications())
	notices = h.receipts("notify", "")
	require.Len(t, notices, 2)
	assert.Equal(t, ledger.Done, notices[1].Outcome)

	h.advance(time.Second)
	h.tick()
	assert.Empty(t, lines)
	assert.Len(t, h.receipts("notify", ""), 2)
}

func TestTickLabelsEachWorkspaceWithItsStatusEmoji(t *testing.T) {
	for _, test := range []struct {
		name   string
		status string
		agent  string
		pr     *prState
		label  string
	}{
		{name: "InProgress", status: "In Progress", agent: "working", label: "🟢 ENG-1"},
		{name: "InReviewPRNotReady", status: "In Review", agent: "idle", pr: &prState{head: "a1", checks: "PENDING", mergeable: "MERGEABLE"}, label: "🟡 ENG-1"},
		{name: "InReviewWorkerBusy", status: "In Review", agent: "working", label: "🟡 ENG-1"},
		{name: "InReviewReady", status: "In Review", agent: "idle", pr: new(cleanPR("a1")), label: "👀 ENG-1"},
		{name: "NeedsHuman", status: "Needs Human", agent: "idle", label: "🔴 ENG-1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", test.status, "dispatch"))
			h.worker("ENG-1", test.agent)
			if test.pr != nil {
				h.pr("ENG-1", *test.pr)
			}

			_, err := h.run("tick")
			require.NoError(t, err)

			assert.Equal(t, []string{"w-ENG-1|" + test.label}, h.renames())

			// The label only changes when the status does.
			_, err = h.run("tick")
			require.NoError(t, err)
			assert.Len(t, h.renames(), 1)
		})
	}
}

func TestTickLabelsAPausedTicketsWorkspace(t *testing.T) {
	h := newHarness(t)
	h.gitTree("ENG-1")
	h.worker("ENG-1", "")
	h.linear.setElsewhere(ticket("ENG-1", "Todo", "dispatch"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"w-ENG-1|⏸️ ENG-1"}, h.renames())
}

func TestTickRelabelsWhenTheStatusChanges(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")
	_, err := h.run("tick")
	require.NoError(t, err)

	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1", "w-ENG-1|🟡 ENG-1"}, h.renames())
}

func TestTickLeavesTheTitleOutOfTheLabel(t *testing.T) {
	h := newHarness(t)
	long := ticket("ENG-1", "In Progress", "dispatch")
	long.Title = strings.Repeat("word ", 30)
	h.tickets(long)
	h.worker("ENG-1", "working")

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1"}, h.renames())
}

func TestTickRenamesAWorkspaceThatHasTheOldTitledLabel(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")
	h.labelWorkspace("w-ENG-1", "🟢 ENG-1 ticket ENG-1")

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1"}, h.renames())
}

func TestTickKeepsAWorkspaceThatHasTheShortLabel(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")
	h.labelWorkspace("w-ENG-1", "🟢 ENG-1")

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.renames())
}

func TestTickStillFindsAWorkerWhoseLabelChanged(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")

	// The first tick renames the workspace and nudges the worker in it.
	h.tick()
	h.advance(5 * time.Minute)
	h.recordStop("ENG-1", noTasks)
	h.tick()

	assert.Equal(t, []string{stallPrompt, stallPrompt}, h.prompts())
	assert.Empty(t, h.launches())
}

func TestTickNotifiesWhenAReadyPRHasNoChecks(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.respond("ENG-1", "", map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequests": map[string]any{"nodes": []any{map[string]any{
		"number":         7,
		"url":            prURL1,
		"headRefOid":     "a1",
		"mergeable":      "MERGEABLE",
		"reviewDecision": "",
		"commits":        map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{"statusCheckRollup": nil}}}},
		"reviewThreads":  map[string]any{"nodes": []any{}, "pageInfo": map[string]bool{"hasNextPage": false}},
	}}}}}})

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{readyText1}, h.notifications())
}

func TestTickNotifiesForPRsThatAreStillReady(t *testing.T) {
	for _, test := range []struct {
		name string
		pr   prState
	}{
		{name: "NoReviewDecision", pr: prState{head: "a1", checks: "SUCCESS", mergeable: "MERGEABLE"}},
		{name: "ReviewRequired", pr: prState{head: "a1", checks: "SUCCESS", decision: "REVIEW_REQUIRED", mergeable: "MERGEABLE"}},
		{name: "OnlyResolvedThreads", pr: prState{head: "a1", checks: "SUCCESS", decision: "APPROVED", mergeable: "MERGEABLE", resolved: 3}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", "idle")
			h.pr("ENG-1", test.pr)

			_, err := h.run("tick")
			require.NoError(t, err)

			assert.Equal(t, []string{readyText1}, h.notifications())
		})
	}
}

func TestTickDoesNotNotifyForMoreNotReadyPRs(t *testing.T) {
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
			h := newHarness(t)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", "idle")
			h.pr("ENG-1", test.pr)

			_, err := h.run("tick")
			require.NoError(t, err)

			assert.Empty(t, h.notifications())
		})
	}
}

func TestTickNotifiesOnlyForIdleWorkersOfInReviewTickets(t *testing.T) {
	for _, status := range []string{"In Progress", "Needs Human", "Backlog", "Todo"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", status, "dispatch"))
			h.worker("ENG-1", "idle")
			h.pr("ENG-1", cleanPR("a1"))

			_, err := h.run("tick")
			require.NoError(t, err)

			for _, n := range h.notifications() {
				assert.NotContains(t, n, "ready for review")
			}
		})
	}
}

func TestTickNotifiesWhenAWorkingWorkerGoesIdleOnAReadyPR(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "working")
	h.pr("ENG-1", cleanPR("a1"))
	_, err := h.run("tick")
	require.NoError(t, err)
	assert.Empty(t, h.notifications())

	h.setWorkerStatus("ENG-1", "idle")
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{readyText1}, h.notifications())
}

func TestTickDoesNotRepeatTheNotificationWhenTheSameHeadIsReadyAgain(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", cleanPR("a1"))
	_, err := h.run("tick")
	require.NoError(t, err)

	// A check reruns on the same commit and passes again.
	h.pr("ENG-1", prState{head: "a1", checks: "PENDING", decision: "APPROVED", mergeable: "MERGEABLE"})
	_, err = h.run("tick")
	require.NoError(t, err)
	h.pr("ENG-1", cleanPR("a1"))
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.notifications(), 1)
}

func TestTickDoesNotRepeatTheNotificationWhenThePRIsUnreadableForATick(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", cleanPR("a1"))
	_, err := h.run("tick")
	require.NoError(t, err)

	h.tools.PullRequests.FailRead(errors.New("exit status 1"))
	_, err = h.run("tick")
	require.NoError(t, err)
	h.tools.PullRequests.FailRead(nil)
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.notifications(), 1)
}

func TestTickNotifiesForTheNewHeadWhenAFailedNotificationIsFollowedByAPush(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", cleanPR("a1"))
	h.tools.Alerts.Fail(errors.New("exit status 1"))
	_, err := h.run("tick")
	require.ErrorContains(t, err, "ENG-1")

	h.tools.Alerts.Fail(nil)
	h.advance(time.Second)
	h.pr("ENG-1", cleanPR("a2"))
	h.tick()
	h.tick()

	assert.Equal(t, []string{readyText1}, h.notifications())
}

func TestTickNotifiesOncePerReadyTicket(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"), ticket("ENG-2", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.worker("ENG-2", "idle")
	h.pr("ENG-1", cleanPR("a1"))
	h.pr("ENG-2", prState{head: "b1", checks: "PENDING", mergeable: "MERGEABLE"})

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{readyText1}, h.notifications())
}

func TestTickDoesNotNotifyForAReadyPRWhileTheTicketIsNotInReview(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", cleanPR("a1"))
	_, err := h.run("tick")
	require.NoError(t, err)

	// Review sends the ticket back to In Progress and the worker pushes again.
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.notifications(), 1)
}

func TestTickLabelsABacklogTicketsKeptWorkspace(t *testing.T) {
	h := newHarness(t)
	h.gitTree("ENG-1")
	h.worker("ENG-1", "")
	h.linear.setElsewhere(ticket("ENG-1", "Backlog", "dispatch"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"w-ENG-1|⏸️ ENG-1"}, h.renames())
}

func TestTickDoesNotRenameATicketWithoutAWorkspace(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"), ticket("ENG-2", "Needs Human", "dispatch"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.renames())
}

func TestTickRetriesARenameHerdrRefused(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")
	h.tools.Herdr.Fail(herdrtest.OpRenameWorkspace, errors.New("exit status 1"))

	h.tick()
	assert.Contains(t, h.loopErr(), "while renaming herdr workspace w-ENG-1")
	assert.Empty(t, h.renames())

	h.tools.Herdr.Fail(herdrtest.OpRenameWorkspace, nil)
	h.tick()

	assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1"}, h.renames())
}

func TestTickLabelsEveryWorkerAfterOneTick(t *testing.T) {
	h := newHarness(t)
	h.tickets(
		ticket("ENG-1", "In Progress", "dispatch"),
		ticket("ENG-2", "In Review", "dispatch"),
		ticket("ENG-3", "Needs Human", "dispatch"),
	)
	h.worker("ENG-1", "working")
	h.worker("ENG-2", "idle")
	h.worker("ENG-3", "blocked")
	h.pr("ENG-2", cleanPR("a1"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{
		"w-ENG-1|🟢 ENG-1",
		"w-ENG-2|👀 ENG-2",
		"w-ENG-3|🔴 ENG-3",
	}, h.renames())
}

func TestTickRelabelsAReviewTicketWhenItsPRBecomesReady(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a1", checks: "PENDING", mergeable: "MERGEABLE"})
	_, err := h.run("tick")
	require.NoError(t, err)

	h.pr("ENG-1", cleanPR("a1"))
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"w-ENG-1|🟡 ENG-1", "w-ENG-1|👀 ENG-1"}, h.renames())
}

func TestTickLabelsAnEscalatedTicketNeedsHumanOnTheSameTick(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.tick()
	h.advance(time.Second)
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	h.tick()
	h.advance(5 * time.Minute)
	h.advance(time.Second)
	h.recordStop("ENG-1", noTasks)

	h.tick()
	require.Len(t, h.linear.mutations(), 2)

	assert.Equal(t, "w-ENG-1|🔴 ENG-1", h.renames()[len(h.renames())-1])
}

func TestTickFindsAWorkerWhoseLabelChangedWhenPromptingAReviewNudge(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	// A worker is nudged once per Stop.
	h.tick()
	h.advance(5 * time.Minute)
	h.recordStop("ENG-1", noTasks)
	h.tick()

	assert.Len(t, h.prompts(), 2)
	assert.Empty(t, h.launches())
}

func TestTickDoesNotLaunchASecondWorkerForARenamedWorkspace(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")

	for range 3 {
		_, err := h.run("tick")
		require.NoError(t, err)
	}

	assert.Empty(t, h.launches())
	assert.Empty(t, h.closes())
}

func TestTickKeepsTheReadyLabelWhenThePRCannotBeRead(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", cleanPR("a1"))
	_, err := h.run("tick")
	require.NoError(t, err)

	h.tools.PullRequests.FailRead(errors.New("exit status 1"))
	_, _, err = h.runContext(t.Context(), "tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"w-ENG-1|👀 ENG-1"}, h.renames())
}

func TestTickDoesNotRepeatTheReadyNotificationAfterTheTicketLeavesAndReturnsToInReview(t *testing.T) {
	for _, between := range []string{"Needs Human", "In Progress"} {
		t.Run(between, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", "idle")
			h.pr("ENG-1", cleanPR("a1"))
			_, err := h.run("tick")
			require.NoError(t, err)

			h.tickets(ticket("ENG-1", between, "dispatch"))
			_, err = h.run("tick")
			require.NoError(t, err)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			_, err = h.run("tick")
			require.NoError(t, err)

			var ready int
			for _, n := range h.notifications() {
				if strings.Contains(n, "ready for review") {
					ready++
				}
			}
			assert.Equal(t, 1, ready)
		})
	}
}

func TestTickDoesNotRepeatTheReadyNotificationAfterTheTicketDropsOutOfTheManagedRead(t *testing.T) {
	for _, between := range []string{"In Review", "Todo"} {
		t.Run(between, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", "idle")
			h.pr("ENG-1", cleanPR("a1"))
			_, err := h.run("tick")
			require.NoError(t, err)

			// The ticket leaves the started, assigned read for one tick, such as
			// when it is unassigned, and comes back at the same head.
			h.tickets()
			h.linear.setElsewhere(ticket("ENG-1", between, "dispatch"))
			_, err = h.run("tick")
			require.NoError(t, err)
			h.linear.setElsewhere()
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			_, err = h.run("tick")
			require.NoError(t, err)

			var ready int
			for _, n := range h.notifications() {
				if strings.Contains(n, "ready for review") {
					ready++
				}
			}
			assert.Equal(t, 1, ready)
		})
	}
}

func TestTickDropsTheReadyLabelWhenThePRIsClosed(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", cleanPR("a1"))
	_, err := h.run("tick")
	require.NoError(t, err)

	// The PR is closed without merging, so the branch has no open PR.
	h.closePR("ENG-1")
	for range 2 {
		_, err = h.run("tick")
		require.NoError(t, err)
	}

	assert.Equal(t, []string{"w-ENG-1|👀 ENG-1", "w-ENG-1|🟡 ENG-1"}, h.renames())
}

func TestTickNamesAWorkersTabByTicketAndTitle(t *testing.T) {
	for _, status := range []string{"In Progress", "In Review", "Needs Human"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", status, "dispatch"))
			h.worker("ENG-1", "working")

			_, err := h.run("tick")
			require.NoError(t, err)

			assert.Equal(t, []string{"w-ENG-1:t1|ENG-1 ticket ENG-1"}, h.tabRenames())

			// A tab that already matches is not renamed again.
			_, err = h.run("tick")
			require.NoError(t, err)
			assert.Len(t, h.tabRenames(), 1)
		})
	}
}

func TestTickKeepsTheFullTitleInATabName(t *testing.T) {
	h := newHarness(t)
	long := ticket("ENG-1", "In Progress", "dispatch")
	long.Title = strings.TrimSpace(strings.Repeat("word ", 21))
	h.tickets(long)
	h.worker("ENG-1", "working")

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"w-ENG-1:t1|ENG-1 " + long.Title}, h.tabRenames())
}

func TestTickRenamesATabWhenTheTitleChanges(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")
	_, err := h.run("tick")
	require.NoError(t, err)

	renamed := ticket("ENG-1", "In Progress", "dispatch")
	renamed.Title = "new title"
	h.tickets(renamed)
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"w-ENG-1:t1|ENG-1 ticket ENG-1", "w-ENG-1:t1|ENG-1 new title"}, h.tabRenames())
}

func TestTickRenamesATabLeftBehindByTheLauncher(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")
	h.labelTab("ENG-1", "ENG-1")

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"w-ENG-1:t1|ENG-1 ticket ENG-1"}, h.tabRenames())
}

func TestTickLeavesAWorkspaceLabelAloneWhenNamingTabs(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1"}, h.renames())
}

func TestTickStillWorksWhenTabListFails(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")
	h.tools.Herdr.Fail(herdrtest.OpTabList, errors.New("exit status 1"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1"}, h.renames())
	assert.Empty(t, h.tabRenames())
}

func TestTickNotifiesWhenTheOnlyChangesRequestedIsOnAnOlderCommit(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "b2", checks: "SUCCESS", changesOn: []string{"a1"}, mergeable: "MERGEABLE"})

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.notifications(), 1)
	assert.Empty(t, h.prompts())
}
