package dispatch_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	comment1       = "2026-10-09T00:09:00.000Z"
	comment2       = "2026-10-09T00:20:00.000Z"
	commentText1   = "ENG-1 has a new comment. Read the ticket's comments and act on them as your build skill says."
	commentPrompt1 = "w-ENG-1:p1|" + commentText1
)

func withComment(t fakeTicket, createdAt string) fakeTicket {
	t.LastComment = createdAt
	return t
}

func TestTickCommentNudgeSkipsCommentsSeenOnTheFirstTick(t *testing.T) {
	for _, status := range []string{"working", "idle"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			h.config("[rules]\nnudge_limit = 9\n")
			h.worker("ENG-1", status)
			h.tickets(withComment(ticket("ENG-1", "In Review", "dispatch"), comment1))

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.Empty(t, out)
			assert.Empty(t, h.prompts())
		})
	}
}

func TestTickCommentNudgeReachesBusyAndIdleWorkersOncePerComment(t *testing.T) {
	for _, test := range []struct {
		name   string
		status string
		ticket string
	}{
		{name: "WorkingInProgress", status: "working", ticket: "In Progress"},
		{name: "IdleInProgress", status: "idle", ticket: "In Progress"},
		{name: "WorkingInReview", status: "working", ticket: "In Review"},
		{name: "IdleInReview", status: "idle", ticket: "In Review"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.config("[rules]\nnudge_limit = 9\n")
			h.worker("ENG-1", "working")
			h.tickets(withComment(ticket("ENG-1", test.ticket, "dispatch"), comment1))
			_, err := h.run("tick")
			require.NoError(t, err)
			h.setWorkerStatus("ENG-1", test.status)
			h.tickets(withComment(ticket("ENG-1", test.ticket, "dispatch"), comment2))

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.Equal(t, "nudge ENG-1: new comment\n", out)
			assert.Equal(t, []string{commentPrompt1}, h.prompts())
			assert.Empty(t, h.linear.mutations())

			// No new comment, so no second nudge.
			h.setWorkerStatus("ENG-1", "working")
			_, err = h.run("tick")
			require.NoError(t, err)
			assert.Len(t, h.prompts(), 1)
		})
	}
}

func TestTickCommentNudgeFiresForTheFirstCommentOnATicketWithNone(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "working")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment1))

	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{commentPrompt1}, h.prompts())
}

func TestTickCommentNudgeWaitsForABlockedWorker(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "working")
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment1))
	_, err := h.run("tick")
	require.NoError(t, err)
	h.setWorkerStatus("ENG-1", "blocked")
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment2))

	for range 2 {
		_, err = h.run("tick")
		require.NoError(t, err)
	}
	assert.Empty(t, h.prompts())
	assert.NotEmpty(t, h.notifications())

	h.setWorkerStatus("ENG-1", "working")
	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: new comment\n", out)
	assert.Equal(t, []string{commentPrompt1}, h.prompts())
}

func TestTickCommentNudgeRetriesAfterHerdrRefuses(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "working")
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment1))
	_, err := h.run("tick")
	require.NoError(t, err)
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment2))
	h.blockAfterSnapshot("ENG-1")

	_, stderr, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)
	assert.Contains(t, stderr, "while nudging ENG-1")
	assert.Empty(t, h.prompts())

	h.unblock("ENG-1")
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{commentPrompt1}, h.prompts())
}

func TestTickCommentNudgeDoesNotCountTowardTheStallLimit(t *testing.T) {
	h := newHarness(t)
	h.config("[rules]\nnudge_limit = 1\n")
	h.worker("ENG-1", "idle")
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment1))
	_, err := h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, []string{stallPrompt}, h.prompts())
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment2))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: new comment\n", out)
	assert.Empty(t, h.linear.mutations())
	assert.Len(t, h.prompts(), 2)
}

func TestTickCommentNudgeIgnoresWorkerlessAndNeedsHumanTickets(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "working")
	h.worker("ENG-2", "working")
	h.tickets(
		withComment(ticket("ENG-1", "Needs Human", "dispatch"), comment1),
		withComment(withLabel(ticket("ENG-2", "In Progress", "dispatch"), "design-review"), comment1),
	)
	_, err := h.run("tick")
	require.NoError(t, err)
	h.tickets(
		withComment(ticket("ENG-1", "Needs Human", "dispatch"), comment2),
		withComment(withLabel(ticket("ENG-2", "In Progress", "dispatch"), "design-review"), comment2),
	)

	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.prompts())
}

func TestTickCommentNudgeYieldsToTheResumeNudge(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "idle")
	h.config("[rules]\nnudge_limit = 9\n")
	h.tickets(withComment(ticket("ENG-1", "Needs Human", "dispatch"), comment1))
	_, err := h.run("tick")
	require.NoError(t, err)
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment2))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: resume\n", out)
	assert.Equal(t, []string{"w-ENG-1:p1|" + resumeText1}, h.prompts())
}

func TestTickCommentNudgeSkipsOldCommentsWhenTheCacheIsGone(t *testing.T) {
	h := newHarness(t)
	h.config("[rules]\nnudge_limit = 9\n")
	h.worker("ENG-1", "working")
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment1))
	_, err := h.run("tick")
	require.NoError(t, err)
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment2))
	require.NoError(t, os.Remove(filepath.Join(h.state, "cache.json")))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Empty(t, h.prompts())

	// The restarted cache holds comment2, so only a newer comment nudges.
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), "2026-10-09T00:30:00.000Z"))
	_, err = h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, []string{commentPrompt1}, h.prompts())
}

func TestTickCommentNudgeMakesNoExtraLinearCall(t *testing.T) {
	h := newHarness(t)
	h.config("[rules]\nnudge_limit = 9\n")
	h.worker("ENG-1", "working")
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment1))
	_, err := h.run("tick")
	require.NoError(t, err)
	lookups := h.linear.lookups()
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment2))

	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{commentPrompt1}, h.prompts())
	assert.Equal(t, lookups, h.linear.lookups())
	assert.Empty(t, h.linear.childReadParents())
	assert.Empty(t, h.linear.mutations())
}

func TestTickCommentNudgeDoesNotCountTowardThePRLimit(t *testing.T) {
	h := newHarness(t)
	failing := prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"}
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", failing)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	h.tickets(withComment(ticket("ENG-1", "In Review", "dispatch"), comment1))
	out, err := h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, "nudge ENG-1: new comment\n", out)
	h.tickets(withComment(ticket("ENG-1", "In Review", "dispatch"), comment2))
	out, err = h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, "nudge ENG-1: new comment\n", out)
	assert.Empty(t, h.linear.mutations())

	// One PR nudge so far; the second still goes out and only the third tick escalates.
	_, err = h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, []string{prPrompt("a failed check"), commentPrompt1, commentPrompt1, prPrompt("a failed check")}, h.prompts())
	assert.Empty(t, h.linear.mutations())

	out, err = h.run("tick")
	require.NoError(t, err)
	assert.Contains(t, out, "escalate ENG-1")
}

func TestTickCommentNudgeNeverEscalatesTheTicket(t *testing.T) {
	h := newHarness(t)
	h.config("[rules]\nnudge_limit = 1\n")
	h.worker("ENG-1", "working")
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment1))
	_, err := h.run("tick")
	require.NoError(t, err)

	for _, createdAt := range []string{comment2, "2026-10-09T00:30:00.000Z", "2026-10-09T00:40:00.000Z"} {
		h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), createdAt))
		out, err := h.run("tick")
		require.NoError(t, err)
		assert.Equal(t, "nudge ENG-1: new comment\n", out)
	}

	assert.Len(t, h.prompts(), 3)
	assert.Empty(t, h.linear.mutations())
	assert.Empty(t, h.notifications())
}

func TestTickDryRunNamesTheCommentNudgeWithoutSendingIt(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "working")
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment1))
	_, err := h.run("tick")
	require.NoError(t, err)
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment2))

	out, err := h.run("tick", "--dry-run")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: new comment\n", out)
	assert.Empty(t, h.prompts())

	// The dry run recorded nothing, so a real tick still nudges.
	_, err = h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, []string{commentPrompt1}, h.prompts())
}

func TestTickCommentNudgeIsOwedWhileTheRepoPoolCannotBeRead(t *testing.T) {
	h := newHarness(t)
	h.addRepo("tiger", filepath.Join(h.dir, "moved"))
	h.tickets(withComment(ticket("ENG-2", "In Progress", "tiger"), comment1))
	_, _, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)
	h.tickets(withComment(ticket("ENG-2", "In Progress", "tiger"), comment2))

	_, _, err = h.runContext(t.Context(), "tick")
	require.NoError(t, err)

	// The worker may be running unseen, so the new comment stays owed.
	raw, err := os.ReadFile(filepath.Join(h.state, "cache.json"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), comment1)
	assert.NotContains(t, string(raw), comment2)
}

func TestTickCommentNudgeAbsorbsACommentOnABlockedDesignReviewTicket(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "working")
	h.tickets(withComment(withLabel(ticket("ENG-1", "In Progress", "dispatch"), "design-review"), comment1))
	_, err := h.run("tick")
	require.NoError(t, err)
	h.setWorkerStatus("ENG-1", "blocked")
	h.tickets(withComment(withLabel(ticket("ENG-1", "In Progress", "dispatch"), "design-review"), comment2))

	_, err = h.run("tick")
	require.NoError(t, err)

	raw, err := os.ReadFile(filepath.Join(h.state, "cache.json"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), comment2)
}

func TestTickCommentNudgeFiresForACommentPostedWhileTheTicketWasAway(t *testing.T) {
	for _, status := range []string{"working", "idle"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			h.config("[rules]\nnudge_limit = 9\n")
			h.worker("ENG-1", "working")
			h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment1))
			_, err := h.run("tick")
			require.NoError(t, err)
			// The ticket drops out of the read while comment2 is posted.
			h.tickets()
			_, err = h.run("tick")
			require.NoError(t, err)
			h.setWorkerStatus("ENG-1", status)
			h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment2))

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.Equal(t, "nudge ENG-1: new comment\n", out)
			assert.Equal(t, []string{commentPrompt1}, h.prompts())
		})
	}
}
