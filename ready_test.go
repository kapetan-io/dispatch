package dispatch_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const readyText1 = "ENG-1 is ready for review: " + prURL1

func TestTickNotifiesOnceWhenAnInReviewPRIsReady(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", cleanPR("a1"))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "notify ENG-1: PR ready for review\n", out)
	assert.Equal(t, []string{readyText1 + "|dispatch"}, h.notifications())
	assert.Empty(t, h.prompts())
	assert.Empty(t, h.linear.mutations())

	// No later tick repeats it.
	for range 2 {
		out, err = h.run("tick")
		require.NoError(t, err)
		assert.Empty(t, out)
	}
	assert.Len(t, h.notifications(), 1)

	// A push that leaves the PR ready again notifies again.
	h.pr("ENG-1", cleanPR("a2"))
	_, err = h.run("tick")
	require.NoError(t, err)
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

	assert.Equal(t, []string{readyText1 + "|dispatch"}, h.notifications())
}

func TestTickRetriesAReadyNotificationThatFailed(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", cleanPR("a1"))
	h.writeFile("notify-fails", "")

	_, stderr, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)
	assert.Contains(t, stderr, "while notifying about ENG-1")
	assert.Empty(t, h.notifications())

	h.removeFile("notify-fails")
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{readyText1 + "|dispatch"}, h.notifications())
}

func TestTickDryRunNamesTheReadyNotificationWithoutSendingIt(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", cleanPR("a1"))

	out, err := h.run("tick", "--dry-run")
	require.NoError(t, err)

	assert.Equal(t, "notify ENG-1: PR ready for review\n", out)
	assert.Empty(t, h.notifications())
	assert.Empty(t, h.renames())
}

func TestTickLabelsEachWorkspaceWithItsStatusEmoji(t *testing.T) {
	for _, test := range []struct {
		name   string
		status string
		agent  string
		pr     *prState
		label  string
	}{
		{name: "InProgress", status: "In Progress", agent: "working", label: "🟢 ENG-1 ticket ENG-1"},
		{name: "InReviewPRNotReady", status: "In Review", agent: "idle", pr: &prState{head: "a1", checks: "PENDING", mergeable: "MERGEABLE"}, label: "🟡 ENG-1 ticket ENG-1"},
		{name: "InReviewWorkerBusy", status: "In Review", agent: "working", label: "🟡 ENG-1 ticket ENG-1"},
		{name: "InReviewReady", status: "In Review", agent: "idle", pr: new(cleanPR("a1")), label: "👀 ENG-1 ticket ENG-1"},
		{name: "NeedsHuman", status: "Needs Human", agent: "idle", label: "🔴 ENG-1 ticket ENG-1"},
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

	assert.Equal(t, []string{"w-ENG-1|⏸️ ENG-1 ticket ENG-1"}, h.renames())
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

	assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1 ticket ENG-1", "w-ENG-1|🟡 ENG-1 ticket ENG-1"}, h.renames())
}

func TestTickTruncatesALongTitleInTheLabel(t *testing.T) {
	h := newHarness(t)
	long := ticket("ENG-1", "In Progress", "dispatch")
	long["title"] = strings.Repeat("word ", 30)
	h.tickets(long)
	h.worker("ENG-1", "working")

	_, err := h.run("tick")
	require.NoError(t, err)

	renames := h.renames()
	require.Len(t, renames, 1)
	label := strings.TrimPrefix(renames[0], "w-ENG-1|")
	assert.True(t, strings.HasPrefix(label, "🟢 ENG-1 word word"))
	assert.True(t, strings.HasSuffix(label, "…"))
	assert.LessOrEqual(t, len([]rune(label)), 40)
}

func TestTickStillFindsAWorkerWhoseLabelChanged(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")

	// The first tick renames the workspace and nudges the worker in it.
	_, err := h.run("tick")
	require.NoError(t, err)
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{stallPrompt, stallPrompt}, h.prompts())
	assert.Empty(t, h.launches())
}

func TestTickNotifiesWhenAReadyPRHasNoChecks(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.writeJSON("pr-eng-1.json", map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequests": map[string]any{"nodes": []any{map[string]any{
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

	assert.Equal(t, []string{readyText1 + "|dispatch"}, h.notifications())
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

			assert.Equal(t, []string{readyText1 + "|dispatch"}, h.notifications())
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

	assert.Equal(t, []string{readyText1 + "|dispatch"}, h.notifications())
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

	h.writeFile("gh-fails", "")
	_, err = h.run("tick")
	require.NoError(t, err)
	h.removeFile("gh-fails")
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.notifications(), 1)
}

func TestTickNotifiesForTheNewHeadWhenAFailedNotificationIsFollowedByAPush(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", cleanPR("a1"))
	h.writeFile("notify-fails", "")
	_, err := h.run("tick")
	require.NoError(t, err)

	h.removeFile("notify-fails")
	h.pr("ENG-1", cleanPR("a2"))
	for range 2 {
		_, err = h.run("tick")
		require.NoError(t, err)
	}

	assert.Equal(t, []string{readyText1 + "|dispatch"}, h.notifications())
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

	assert.Equal(t, []string{readyText1 + "|dispatch"}, h.notifications())
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

	assert.Equal(t, []string{"w-ENG-1|⏸️ ENG-1 ticket ENG-1"}, h.renames())
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
	real := h.dir + "/herdr-real"
	h.script("herdr-real", string(h.readFile("herdr")))
	h.script("herdr", `[ -f "`+h.dir+`/rename-fails" ] && [ "$1 $2" = "workspace rename" ] && exit 1
exec "`+real+`" "$@"`)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")
	h.writeFile("rename-fails", "")

	_, stderr, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)
	assert.Contains(t, stderr, "while renaming herdr workspace w-ENG-1")
	assert.Empty(t, h.renames())

	h.removeFile("rename-fails")
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1 ticket ENG-1"}, h.renames())
}

func TestTickDoesNotRenameOnADryRun(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")

	_, err := h.run("tick", "--dry-run")
	require.NoError(t, err)

	assert.Empty(t, h.renames())
}

func TestTickTruncatesTheLabelAtFortyCharacters(t *testing.T) {
	// "🟢 ENG-1 " is 8 characters, so 32 more fit and 33 do not.
	for _, test := range []struct {
		name  string
		title string
		want  string
	}{
		{name: "ExactlyFits", title: strings.Repeat("a", 32), want: "🟢 ENG-1 " + strings.Repeat("a", 32)},
		{name: "OneOver", title: strings.Repeat("a", 33), want: "🟢 ENG-1 " + strings.Repeat("a", 31) + "…"},
		{name: "MultiByteTitle", title: strings.Repeat("é", 40), want: "🟢 ENG-1 " + strings.Repeat("é", 31) + "…"},
		{name: "ShortTitle", title: "fix", want: "🟢 ENG-1 fix"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			tk := ticket("ENG-1", "In Progress", "dispatch")
			tk["title"] = test.title
			h.tickets(tk)
			h.worker("ENG-1", "working")

			_, err := h.run("tick")
			require.NoError(t, err)

			assert.Equal(t, []string{"w-ENG-1|" + test.want}, h.renames())
		})
	}
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
		"w-ENG-1|🟢 ENG-1 ticket ENG-1",
		"w-ENG-2|👀 ENG-2 ticket ENG-2",
		"w-ENG-3|🔴 ENG-3 ticket ENG-3",
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

	assert.Equal(t, []string{"w-ENG-1|🟡 ENG-1 ticket ENG-1", "w-ENG-1|👀 ENG-1 ticket ENG-1"}, h.renames())
}

func TestTickLabelsAnEscalatedTicketNeedsHumanOnTheSameTick(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	for range 2 {
		_, err := h.run("tick")
		require.NoError(t, err)
	}

	out, err := h.run("tick")
	require.NoError(t, err)
	require.Contains(t, out, "escalate ENG-1")

	assert.Equal(t, "w-ENG-1|🔴 ENG-1 ticket ENG-1", h.renames()[len(h.renames())-1])
}

func TestTickFindsAWorkerWhoseLabelChangedWhenPromptingAReviewNudge(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	for range 2 {
		_, err := h.run("tick")
		require.NoError(t, err)
	}

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

	h.writeFile("gh-fails", "")
	_, _, err = h.runContext(t.Context(), "tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"w-ENG-1|👀 ENG-1 ticket ENG-1"}, h.renames())
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
	h.removeFile("pr-eng-1.json")
	for range 2 {
		_, err = h.run("tick")
		require.NoError(t, err)
	}

	assert.Equal(t, []string{"w-ENG-1|👀 ENG-1 ticket ENG-1", "w-ENG-1|🟡 ENG-1 ticket ENG-1"}, h.renames())
}
