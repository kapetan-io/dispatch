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
