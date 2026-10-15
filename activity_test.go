package dispatch_test

import (
	"context"
	"fmt"
	"testing"
	"time"

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
// comments of before, and runs the tick that records them as seen. The clock
// then moves, so whatever arrives next is newer than the worker dispatch knows.
func watched(t *testing.T, before prState) *harness {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", before)
	_, err := h.run("tick")
	require.NoError(t, err)
	h.advance(time.Second)
	return h
}

// activityWorkerGone closes ENG-1's herdr workspace, which leaves its tree leased
// and no worker for dispatch to see.
func activityWorkerGone(t *testing.T, h *harness) {
	require.NoError(t, h.tools.Herdr.CloseWorkspace(context.Background(), "w-ENG-1"))
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

			notified := len(h.notifications())

			_, err := h.run("tick")
			require.NoError(t, err)

			assert.Equal(t, []string{activityPrompt(test.by)}, h.prompts())
			assert.Empty(t, h.linear.mutations())

			// The nudge is not repeated, and the ready notice for this head already went out.
			h.advance(time.Second)
			_, err = h.run("tick")
			require.NoError(t, err)
			assert.Len(t, h.prompts(), 1)
			assert.Len(t, h.notifications(), notified)
		})
	}
}

func TestTickNamesTheNewestPRActivity(t *testing.T) {
	before := cleanPR("a1")
	h := watched(t, before)
	before.reviews = []activity{{"COMMENTED", time2, "alice", "", ""}}
	before.comments = []activity{{"", time3, "bob", "", ""}}
	h.pr("ENG-1", before)

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{activityPrompt("a comment by bob")}, h.prompts())
}

func TestTickDoesNotNudgeForAnApprovalOrADraftReview(t *testing.T) {
	for _, state := range []string{"APPROVED", "DISMISSED", "PENDING"} {
		t.Run(state, func(t *testing.T) {
			before := cleanPR("a1")
			h := watched(t, before)
			before.reviews = []activity{{state, time2, "alice", "", ""}}
			h.pr("ENG-1", before)

			_, err := h.run("tick")
			require.NoError(t, err)

			assert.Empty(t, h.prompts())
		})
	}
}

func TestTickSkipsPRActivityThatPredatesDispatchsCache(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	// A worker dispatch first sees beside the review already knows about it.
	p := cleanPR("a1")
	p.reviews = []activity{{"COMMENTED", time1, "alice", "", ""}}
	h.pr("ENG-1", p)

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.prompts())
}

func TestTickNudgesForActivityPresentWhenTheTicketEnteredInReview(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	require.Len(t, h.launches(), 1)
	// Dispatch launched the worker, so it knows the worker from before the review arrived.
	h.worker("ENG-1", "working")
	_, err = h.run("tick")
	require.NoError(t, err)
	h.advance(time.Second)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	p := cleanPR("a1")
	p.reviews = []activity{{"COMMENTED", time1, "alice", "", ""}}
	h.pr("ENG-1", p)
	_, err = h.run("tick")
	require.NoError(t, err)
	assert.Empty(t, h.prompts())
	h.setWorkerStatus("ENG-1", "idle")

	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{activityPrompt("a review by alice")}, h.prompts())
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
	h.config("[rules]\nnudge_limit = 1\n")
	before := cleanPR("a1")
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", before)
	for range 2 {
		_, err := h.run("tick")
		require.NoError(t, err)
	}
	h.advance(time.Second)
	for i, at := range []string{time1, time2, time3} {
		before.comments = append(before.comments, activity{"", at, fmt.Sprintf("bob%d", i), "", ""})
		h.pr("ENG-1", before)

		h.advance(time.Second)
		_, err := h.run("tick")
		require.NoError(t, err)

		assert.Len(t, h.prompts(), i+1)
		assert.Equal(t, activityPrompt(fmt.Sprintf("a comment by bob%d", i)), h.prompts()[i])
	}
	assert.Empty(t, h.linear.mutations())
}

func TestTickNamesNewActivityInALaterNudgeWhenAPRProblemComesFirst(t *testing.T) {
	before := cleanPR("a1")
	h := watched(t, before)
	before.checks = "FAILURE"
	before.reviews = []activity{{"COMMENTED", time2, "alice", "", ""}}
	h.pr("ENG-1", before)
	_, err := h.run("tick")
	require.NoError(t, err)
	require.Len(t, h.prompts(), 1)
	assert.Equal(t, prPrompt("a failed check"), h.prompts()[0])
	before.checks = "SUCCESS"
	h.pr("ENG-1", before)
	h.advance(time.Second)

	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{prPrompt("a failed check"), activityPrompt("a review by alice")}, h.prompts())
}

func TestTickHoldsAutoMergeForNewPRActivity(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", autoMergeSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	// No approval yet, so the first read only records the worker.
	h.pr("ENG-1", cleanPR("a1"))
	_, err := h.run("tick")
	require.NoError(t, err)
	h.advance(time.Second)
	p := approvedPR("a1")
	p.comments = []activity{{"", time2, "alice", "", ""}}
	h.pr("ENG-1", p)

	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{activityPrompt("a comment by alice")}, h.prompts())
	assert.Empty(t, h.merges())

	h.advance(time.Second)
	_, err = h.run("tick")
	require.NoError(t, err)
	assert.Len(t, h.merges(), 1)
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
	h.advance(time.Second)
	_, err = h.run("tick")
	require.NoError(t, err)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.advance(time.Second)
	_, err = h.run("tick")
	require.NoError(t, err)
	h.setWorkerStatus("ENG-1", "idle")
	h.advance(time.Second)

	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.prompts(), 1)
}

func TestTickNudgesForAnApprovalThatCarriesABody(t *testing.T) {
	before := cleanPR("a1")
	h := watched(t, before)
	before.reviews = []activity{{"APPROVED", time2, "alice", "Approved, but please rename X.", ""}}
	h.pr("ENG-1", before)

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{activityPrompt("a review by alice")}, h.prompts())
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

	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{activityPrompt("a review by alice")}, h.prompts())
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
	h.advance(time.Second)
	_, err = h.run("tick")
	require.NoError(t, err)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.advance(time.Second)

	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.prompts(), 1)
}

func TestTickRetriesAPRActivityNudgeHerdrRefused(t *testing.T) {
	before := cleanPR("a1")
	h := watched(t, before)
	before.reviews = []activity{{"COMMENTED", time2, "alice", "", ""}}
	h.pr("ENG-1", before)
	h.blockAfterSnapshot("ENG-1")
	_, err := h.run("tick")
	require.ErrorContains(t, err, "blocked")
	assert.Empty(t, h.prompts())
	h.unblock("ENG-1")

	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{activityPrompt("a review by alice")}, h.prompts())
}

func TestTickNudgesOnceForEachNewCommentOnAnOpenReviewThread(t *testing.T) {
	before := cleanPR("a1")
	h := watched(t, before)
	before.threads = []activity{{"", time2, "alice", "", ""}}
	h.pr("ENG-1", before)

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{activityPrompt("a review thread comment by alice")}, h.prompts())

	// The thread stays open, and later ticks stay quiet.
	for range 2 {
		h.advance(time.Second)
		_, err = h.run("tick")
		require.NoError(t, err)
	}
	assert.Len(t, h.prompts(), 1)

	// A reply on the same thread is a new comment.
	before.threads = []activity{{"", time3, "bob", "", ""}}
	h.pr("ENG-1", before)
	h.advance(time.Second)
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.prompts(), 2)
	assert.Equal(t, activityPrompt("a review thread comment by bob"), h.prompts()[1])
}

func TestTickIgnoresCommentsOnResolvedReviewThreads(t *testing.T) {
	before := cleanPR("a1")
	h := watched(t, before)
	before.resolved = 1
	h.pr("ENG-1", before)

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.prompts())
}

func TestTickRelaunchesAGoneWorkerForANewReviewThreadComment(t *testing.T) {
	before := cleanPR("a1")
	h := watched(t, before)
	activityWorkerGone(t, h)
	before.threads = []activity{{"", time2, "alice", "", ""}}
	h.pr("ENG-1", before)

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.launches(), 1)
}

func TestTickHoldsTheReadyNoticeWhileAThreadStaysOpenAfterItsNudge(t *testing.T) {
	before := cleanPR("a1")
	h := watched(t, before)
	notified := len(h.notifications())
	before.threads = []activity{{"", time2, "alice", "", ""}}
	h.pr("ENG-1", before)
	_, err := h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, []string{activityPrompt("a review thread comment by alice")}, h.prompts())

	for range 2 {
		h.advance(time.Second)
		_, err = h.run("tick")
		require.NoError(t, err)
	}
	assert.Len(t, h.notifications(), notified)

	// The reviewer resolves the thread and the worker pushes, so the new head is ready.
	before.threads = nil
	before.resolved = 1
	before.head = "a2"
	h.pr("ENG-1", before)
	h.advance(time.Second)
	_, err = h.run("tick")
	require.NoError(t, err)
	assert.Len(t, h.notifications(), notified+1)
}

func TestTickDoesNotAutoMergeWhileAThreadStaysOpenAfterItsNudge(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", autoMergeSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", cleanPR("a1"))
	_, err := h.run("tick")
	require.NoError(t, err)
	h.advance(time.Second)
	p := approvedPR("a1")
	p.threads = []activity{{"", time2, "alice", "", ""}}
	h.pr("ENG-1", p)
	_, err = h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, []string{activityPrompt("a review thread comment by alice")}, h.prompts())

	for range 2 {
		h.advance(time.Second)
		_, err = h.run("tick")
		require.NoError(t, err)
	}
	assert.Empty(t, h.merges())

	p.threads = nil
	p.resolved = 1
	h.pr("ENG-1", p)
	h.advance(time.Second)
	_, err = h.run("tick")
	require.NoError(t, err)
	assert.Len(t, h.merges(), 1)
}

func TestTickNudgesAThreadCommentAfterAnotherPRProblemClears(t *testing.T) {
	before := cleanPR("a1")
	h := watched(t, before)
	before.checks = "FAILURE"
	before.threads = []activity{{"", time2, "alice", "", ""}}
	h.pr("ENG-1", before)

	_, err := h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, []string{prPrompt("a failed check")}, h.prompts())

	before.checks = "SUCCESS"
	h.pr("ENG-1", before)
	h.advance(time.Second)
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{prPrompt("a failed check"), activityPrompt("a review thread comment by alice")}, h.prompts())
}

func TestTickRelaunchesAGoneWorkerOnceForAThreadComment(t *testing.T) {
	before := cleanPR("a1")
	h := watched(t, before)
	activityWorkerGone(t, h)
	before.threads = []activity{{"", time2, "alice", "", ""}}
	h.pr("ENG-1", before)

	for range 2 {
		_, err := h.run("tick")
		require.NoError(t, err)
		h.advance(time.Second)
	}

	assert.Len(t, h.launches(), 1)
}

func TestTickDoesNotRelaunchAGoneWorkerForAThreadCommentAlreadySeen(t *testing.T) {
	before := cleanPR("a1")
	h := watched(t, before)
	before.threads = []activity{{"", time2, "alice", "", ""}}
	h.pr("ENG-1", before)
	_, err := h.run("tick")
	require.NoError(t, err)
	require.Len(t, h.prompts(), 1)
	activityWorkerGone(t, h)
	h.advance(time.Second)

	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.launches())
}

func TestTickDoesNotRelaunchAGoneWorkerForAPlainCommentWhileAThreadIsOpen(t *testing.T) {
	before := cleanPR("a1")
	before.threads = []activity{{"", time1, "alice", "", ""}}
	h := watched(t, before)
	before.comments = []activity{{"", time3, "bob", "", ""}}
	h.pr("ENG-1", before)
	_, err := h.run("tick")
	require.NoError(t, err)
	require.Len(t, h.prompts(), 1)
	activityWorkerGone(t, h)
	h.advance(time.Second)

	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.launches())
}

func TestTickRecordsAThreadCommentWhenAProblemRelaunchesAGoneWorker(t *testing.T) {
	before := cleanPR("a1")
	h := watched(t, before)
	activityWorkerGone(t, h)
	before.checks = "FAILURE"
	before.threads = []activity{{"", time2, "alice", "", ""}}
	h.pr("ENG-1", before)
	_, err := h.run("tick")
	require.NoError(t, err)
	require.Len(t, h.launches(), 1)

	// The failed check clears while the worker is still gone.
	before.checks = "SUCCESS"
	h.pr("ENG-1", before)
	h.advance(time.Second)
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.launches(), 1)
}
