package dispatch_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	time1 = "2026-10-09T10:00:00Z"
	time2 = "2026-10-09T11:00:00Z"
	time3 = "2026-10-09T12:00:00Z"
)

func activityPrompt(by string) string {
	return fmt.Sprintf("w-ENG-1:p1|PR %s for ENG-1 has new activity, %s. Read it and handle it as your build skill's step 7 says. If it asks for nothing, say nothing.", prURL1, by)
}

// watched starts an idle In Review worker whose PR has the reviews and
// comments of before, and runs the tick that records them as seen.
func watched(t *testing.T, before prState) *harness {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", before)
	_, err := h.run("tick")
	require.NoError(t, err)
	return h
}

func TestTickNudgesAnIdleWorkerOnceForNewPRActivity(t *testing.T) {
	for _, test := range []struct {
		name string
		add  func(p *prState)
		by   string
	}{
		{name: "BodyOnlyReview", add: func(p *prState) { p.reviews = append(p.reviews, activity{"COMMENTED", time2, "alice", "", ""}) }, by: "a review by alice"},
		{name: "ChangesRequestedReview", add: func(p *prState) { p.reviews = append(p.reviews, activity{"CHANGES_REQUESTED", time2, "bot", "", ""}) }, by: "a review by bot"},
		{name: "ConversationComment", add: func(p *prState) { p.comments = append(p.comments, activity{"", time2, "bob", "", ""}) }, by: "a comment by bob"},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := cleanPR("a1")
			h := watched(t, before)
			test.add(&before)
			h.pr("ENG-1", before)

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.Equal(t, "nudge ENG-1: new PR activity: "+test.by+"\n", out)
			assert.Equal(t, []string{activityPrompt(test.by)}, h.prompts())
			assert.Empty(t, h.linear.mutations())

			// The nudge is not repeated, and the ready notice for this head already went out.
			out, err = h.run("tick")
			require.NoError(t, err)
			assert.Empty(t, out)
			assert.Len(t, h.prompts(), 1)
		})
	}
}

func TestTickNamesTheNewestPRActivity(t *testing.T) {
	before := cleanPR("a1")
	h := watched(t, before)
	before.reviews = []activity{{"COMMENTED", time2, "alice", "", ""}}
	before.comments = []activity{{"", time3, "bob", "", ""}}
	h.pr("ENG-1", before)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: new PR activity: a comment by bob\n", out)
}

func TestTickDoesNotNudgeForAnApprovalOrADraftReview(t *testing.T) {
	for _, state := range []string{"APPROVED", "DISMISSED", "PENDING"} {
		t.Run(state, func(t *testing.T) {
			before := cleanPR("a1")
			h := watched(t, before)
			before.reviews = []activity{{state, time2, "alice", "", ""}}
			h.pr("ENG-1", before)

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.NotContains(t, out, "nudge")
			assert.Empty(t, h.prompts())
		})
	}
}

func TestTickSkipsPRActivityThatPredatesDispatchsCache(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	// A cache written before the field existed has the ticket In Review and no activity time.
	require.NoError(t, os.MkdirAll(h.state, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(h.state, "cache.json"), []byte(`{"ENG-1":{"last_status":"In Review"}}`), 0o600))
	p := cleanPR("a1")
	p.reviews = []activity{{"COMMENTED", time1, "alice", "", ""}}
	h.pr("ENG-1", p)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.NotContains(t, out, "nudge")
	assert.Empty(t, h.prompts())
}

func TestTickNudgesForActivityPresentWhenTheTicketEnteredInReview(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "working")
	p := cleanPR("a1")
	p.reviews = []activity{{"COMMENTED", time1, "alice", "", ""}}
	h.pr("ENG-1", p)
	_, err := h.run("tick")
	require.NoError(t, err)
	h.setWorkerStatus("ENG-1", "idle")

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: new PR activity: a review by alice\n", out)
}

func TestTickHoldsPRActivityUntilTheWorkerIsIdle(t *testing.T) {
	before := cleanPR("a1")
	h := watched(t, before)
	h.setWorkerStatus("ENG-1", "working")
	before.reviews = []activity{{"COMMENTED", time2, "alice", "", ""}}
	h.pr("ENG-1", before)
	_, err := h.run("tick")
	require.NoError(t, err)
	assert.Empty(t, h.prompts())
	h.setWorkerStatus("ENG-1", "idle")

	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{activityPrompt("a review by alice")}, h.prompts())
}

func TestTickActivityNudgeDoesNotCountTowardThePRLimit(t *testing.T) {
	h := newHarness(t)
	h.config("nudge_limit = 1\n")
	before := cleanPR("a1")
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", before)
	for range 2 {
		_, err := h.run("tick")
		require.NoError(t, err)
	}
	for i, at := range []string{time1, time2, time3} {
		before.comments = append(before.comments, activity{"", at, fmt.Sprintf("bob%d", i), "", ""})
		h.pr("ENG-1", before)

		out, err := h.run("tick")
		require.NoError(t, err)

		assert.Equal(t, fmt.Sprintf("nudge ENG-1: new PR activity: a comment by bob%d\n", i), out)
	}
	assert.Empty(t, h.linear.mutations())
}

func TestTickNamesNewActivityInALaterNudgeWhenAPRProblemComesFirst(t *testing.T) {
	before := cleanPR("a1")
	h := watched(t, before)
	before.checks = "FAILURE"
	before.reviews = []activity{{"COMMENTED", time2, "alice", "", ""}}
	h.pr("ENG-1", before)
	out, err := h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, "nudge ENG-1: PR needs work: a failed check\n", out)
	before.checks = "SUCCESS"
	h.pr("ENG-1", before)

	out, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: new PR activity: a review by alice\n", out)
	assert.Len(t, h.prompts(), 2)
}

func TestTickHoldsAutoMergeForNewPRActivity(t *testing.T) {
	h := newHarness(t)
	h.config(autoMergeConfig)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	p := approvedPR("a1")
	p.comments = []activity{{"", time2, "alice", "", ""}}
	h.pr("ENG-1", p)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: new PR activity: a comment by alice\n", out)
	assert.Empty(t, h.lines("merges.log"))

	out, err = h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, "merge ENG-1: PR approved\n", out)
}

func TestTickDryRunNamesThePRActivityNudgeWithoutSendingIt(t *testing.T) {
	before := cleanPR("a1")
	h := watched(t, before)
	before.reviews = []activity{{"COMMENTED", time2, "alice", "", ""}}
	h.pr("ENG-1", before)

	for range 2 {
		out, err := h.run("tick", "--dry-run")
		require.NoError(t, err)

		assert.Equal(t, "nudge ENG-1: new PR activity: a review by alice\n", out)
	}
	assert.Empty(t, h.prompts())

	out, err := h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, "nudge ENG-1: new PR activity: a review by alice\n", out)
	assert.Len(t, h.prompts(), 1)
}

func TestTickDoesNotRepeatActivityAfterTheTicketLeavesAndReturnsToInReview(t *testing.T) {
	before := cleanPR("a1")
	h := watched(t, before)
	before.comments = []activity{{"", time2, "bob", "", ""}}
	h.pr("ENG-1", before)
	_, err := h.run("tick")
	require.NoError(t, err)
	require.Len(t, h.prompts(), 1)
	h.setWorkerStatus("ENG-1", "working")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err = h.run("tick")
	require.NoError(t, err)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	_, err = h.run("tick")
	require.NoError(t, err)
	h.setWorkerStatus("ENG-1", "idle")

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.NotContains(t, out, "nudge")
	assert.Len(t, h.prompts(), 1)
}

func TestTickNudgesForAnApprovalThatCarriesABody(t *testing.T) {
	before := cleanPR("a1")
	h := watched(t, before)
	before.reviews = []activity{{"APPROVED", time2, "alice", "Approved, but please rename X.", ""}}
	h.pr("ENG-1", before)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: new PR activity: a review by alice\n", out)
}

func TestTickNudgesForAReviewSubmittedAfterItWasStarted(t *testing.T) {
	before := cleanPR("a1")
	before.comments = []activity{{"", time2, "bob", "", ""}}
	h := watched(t, before)
	_, err := h.run("tick")
	require.NoError(t, err)
	// The draft was started at time1, before the comment, and submitted at time3.
	before.reviews = []activity{{"COMMENTED", time1, "alice", "please rename X", time3}}
	h.pr("ENG-1", before)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: new PR activity: a review by alice\n", out)
}

func TestTickKeepsPRActivityAcrossATicketAbsentFromTheRead(t *testing.T) {
	before := cleanPR("a1")
	h := watched(t, before)
	before.comments = []activity{{"", time2, "bob", "", ""}}
	h.pr("ENG-1", before)
	_, err := h.run("tick")
	require.NoError(t, err)
	require.Len(t, h.prompts(), 1)
	h.lease("ENG-1", h.gitTree("ENG-1"))
	h.tickets()
	_, err = h.run("tick")
	require.NoError(t, err)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.NotContains(t, out, "nudge")
	assert.Len(t, h.prompts(), 1)
}

func TestTickRetriesAPRActivityNudgeHerdrRefused(t *testing.T) {
	before := cleanPR("a1")
	h := watched(t, before)
	before.reviews = []activity{{"COMMENTED", time2, "alice", "", ""}}
	h.pr("ENG-1", before)
	h.writeFile("prompt-fails", "")
	_, err := h.run("tick")
	require.NoError(t, err)
	h.removeFile("prompt-fails")

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: new PR activity: a review by alice\n", out)
	assert.Equal(t, []string{activityPrompt("a review by alice")}, h.prompts())
}

func TestTickNudgesOnceForEachNewCommentOnAnOpenReviewThread(t *testing.T) {
	before := cleanPR("a1")
	h := watched(t, before)
	before.threads = []activity{{"", time2, "alice", "", ""}}
	h.pr("ENG-1", before)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: new PR activity: a review thread comment by alice\n", out)
	assert.Equal(t, []string{activityPrompt("a review thread comment by alice")}, h.prompts())

	// The thread stays open, and later ticks and a dry run stay quiet.
	for _, args := range [][]string{{"tick"}, {"tick"}, {"tick", "--dry-run"}} {
		out, err = h.run(args...)
		require.NoError(t, err)
		assert.Empty(t, out)
	}
	assert.Len(t, h.prompts(), 1)

	// A reply on the same thread is a new comment.
	before.threads = []activity{{"", time3, "bob", "", ""}}
	h.pr("ENG-1", before)
	out, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: new PR activity: a review thread comment by bob\n", out)
	assert.Len(t, h.prompts(), 2)
}

func TestTickDryRunNamesTheThreadNudgeUntilItIsSent(t *testing.T) {
	before := cleanPR("a1")
	h := watched(t, before)
	before.threads = []activity{{"", time2, "alice", "", ""}}
	h.pr("ENG-1", before)

	for range 2 {
		out, err := h.run("tick", "--dry-run")
		require.NoError(t, err)
		assert.Equal(t, "nudge ENG-1: new PR activity: a review thread comment by alice\n", out)
	}
	assert.Empty(t, h.prompts())
}

func TestTickIgnoresCommentsOnResolvedReviewThreads(t *testing.T) {
	before := cleanPR("a1")
	h := watched(t, before)
	before.resolved = 1
	h.pr("ENG-1", before)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Empty(t, h.prompts())
}

func TestTickRelaunchesAGoneWorkerForANewReviewThreadComment(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "")
	require.NoError(t, os.MkdirAll(h.state, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(h.state, "cache.json"), []byte(`{"ENG-1":{"last_status":"In Review","pr_activity_seen":"`+time1+`"}}`), 0o600))
	p := cleanPR("a1")
	p.threads = []activity{{"", time2, "alice", "", ""}}
	h.pr("ENG-1", p)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "launch ENG-1: new PR activity: a review thread comment by alice: ok\n", out)
	assert.Len(t, h.launches(), 1)
}

func TestTickHoldsTheReadyNoticeWhileAThreadStaysOpenAfterItsNudge(t *testing.T) {
	before := cleanPR("a1")
	h := watched(t, before)
	notified := len(h.notifications())
	before.threads = []activity{{"", time2, "alice", "", ""}}
	h.pr("ENG-1", before)
	out, err := h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, "nudge ENG-1: new PR activity: a review thread comment by alice\n", out)

	for range 2 {
		out, err = h.run("tick")
		require.NoError(t, err)
		assert.Empty(t, out)
	}
	assert.Len(t, h.notifications(), notified)

	// The reviewer resolves the thread and the worker pushes, so the new head is ready.
	before.threads = nil
	before.resolved = 1
	before.head = "a2"
	h.pr("ENG-1", before)
	out, err = h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, "notify ENG-1: PR ready for review\n", out)
}

func TestTickDoesNotAutoMergeWhileAThreadStaysOpenAfterItsNudge(t *testing.T) {
	h := newHarness(t)
	h.config(autoMergeConfig)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	p := approvedPR("a1")
	p.threads = []activity{{"", time2, "alice", "", ""}}
	h.pr("ENG-1", p)

	out, err := h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, "nudge ENG-1: new PR activity: a review thread comment by alice\n", out)

	for range 2 {
		out, err = h.run("tick")
		require.NoError(t, err)
		assert.Empty(t, out)
	}
	assert.Empty(t, h.lines("merges.log"))

	p.threads = nil
	p.resolved = 1
	h.pr("ENG-1", p)
	out, err = h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, "merge ENG-1: PR approved\n", out)
}

func TestTickNudgesAThreadCommentAfterAnotherPRProblemClears(t *testing.T) {
	before := cleanPR("a1")
	h := watched(t, before)
	before.checks = "FAILURE"
	before.threads = []activity{{"", time2, "alice", "", ""}}
	h.pr("ENG-1", before)

	out, err := h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, "nudge ENG-1: PR needs work: a failed check\n", out)

	before.checks = "SUCCESS"
	h.pr("ENG-1", before)
	out, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: new PR activity: a review thread comment by alice\n", out)
	assert.Len(t, h.prompts(), 2)
}

func TestTickRelaunchesAGoneWorkerOnceForAThreadComment(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "")
	require.NoError(t, os.MkdirAll(h.state, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(h.state, "cache.json"), []byte(`{"ENG-1":{"last_status":"In Review","pr_activity_seen":"`+time1+`"}}`), 0o600))
	p := cleanPR("a1")
	p.threads = []activity{{"", time2, "alice", "", ""}}
	h.pr("ENG-1", p)

	for range 2 {
		_, err := h.run("tick")
		require.NoError(t, err)
	}

	assert.Len(t, h.launches(), 1)
}

func TestTickDoesNotRelaunchAGoneWorkerForAThreadCommentAlreadySeen(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "")
	require.NoError(t, os.MkdirAll(h.state, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(h.state, "cache.json"), []byte(`{"ENG-1":{"last_status":"In Review","pr_activity_seen":"`+time2+`"}}`), 0o600))
	p := cleanPR("a1")
	p.threads = []activity{{"", time2, "alice", "", ""}}
	h.pr("ENG-1", p)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.launches())
	assert.NotContains(t, out, "launch")
}

func TestTickDoesNotRelaunchAGoneWorkerForAPlainCommentWhileAThreadIsOpen(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "")
	require.NoError(t, os.MkdirAll(h.state, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(h.state, "cache.json"), []byte(`{"ENG-1":{"last_status":"In Review","pr_activity_seen":"`+time2+`"}}`), 0o600))
	p := cleanPR("a1")
	p.threads = []activity{{"", time1, "alice", "", ""}}
	p.comments = []activity{{"", time3, "bob", "", ""}}
	h.pr("ENG-1", p)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Empty(t, h.launches())
}
