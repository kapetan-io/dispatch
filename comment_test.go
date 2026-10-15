package dispatch_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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

			h.tick()

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
			h.tick()
			h.advance(time.Second)
			h.setWorkerStatus("ENG-1", test.status)
			h.tickets(withComment(ticket("ENG-1", test.ticket, "dispatch"), comment2))

			h.tick()

			assert.Equal(t, []string{commentPrompt1}, h.prompts())
			assert.Empty(t, h.linear.mutations())

			// No new comment, so no second nudge.
			h.advance(time.Second)
			h.setWorkerStatus("ENG-1", "working")
			h.tick()
			assert.Len(t, h.prompts(), 1)
		})
	}
}

func TestTickCommentNudgeFiresForTheFirstCommentOnATicketWithNone(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "working")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.tick()
	h.advance(time.Second)
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment1))

	h.tick()

	assert.Equal(t, []string{commentPrompt1}, h.prompts())
}

func TestTickCommentNudgeWaitsForABlockedWorker(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "working")
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment1))
	h.tick()
	h.advance(time.Second)
	h.setWorkerStatus("ENG-1", "blocked")
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment2))

	h.tick()
	h.tick()
	assert.Empty(t, h.prompts())
	assert.NotEmpty(t, h.notifications())

	h.advance(time.Second)
	h.setWorkerStatus("ENG-1", "working")
	h.tick()

	assert.Equal(t, []string{commentPrompt1}, h.prompts())
}

func TestTickCommentNudgeRetriesAfterHerdrRefuses(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "working")
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment1))
	h.tick()
	h.advance(time.Second)
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment2))
	h.blockAfterSnapshot("ENG-1")

	_, err := h.run("tick")
	require.ErrorContains(t, err, "ENG-1 nudge failed: blocked")
	assert.Empty(t, h.prompts())

	h.unblock("ENG-1")
	h.tick()

	assert.Equal(t, []string{commentPrompt1}, h.prompts())
}

func TestTickCommentNudgeDoesNotCountTowardTheStallLimit(t *testing.T) {
	h := newHarness(t)
	h.config("[rules]\nnudge_limit = 1\n")
	h.worker("ENG-1", "idle")
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment1))
	h.tick()
	assert.Equal(t, []string{stallPrompt}, h.prompts())
	h.advance(time.Second)
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment2))

	h.tick()

	assert.Empty(t, h.linear.mutations())
	assert.Equal(t, []string{stallPrompt, commentPrompt1}, h.prompts())
}

func TestTickCommentNudgeIgnoresWorkerlessAndNeedsHumanTickets(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "working")
	h.worker("ENG-2", "working")
	h.tickets(
		withComment(ticket("ENG-1", "Needs Human", "dispatch"), comment1),
		withComment(withLabel(ticket("ENG-2", "In Progress", "dispatch"), "design-review"), comment1),
	)
	h.tick()
	h.advance(time.Second)
	h.tickets(
		withComment(ticket("ENG-1", "Needs Human", "dispatch"), comment2),
		withComment(withLabel(ticket("ENG-2", "In Progress", "dispatch"), "design-review"), comment2),
	)

	h.tick()

	assert.Empty(t, h.prompts())
}

func TestTickCommentNudgeYieldsToTheResumeNudge(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "idle")
	h.config("[rules]\nnudge_limit = 9\n")
	h.tickets(withComment(ticket("ENG-1", "Needs Human", "dispatch"), comment1))
	h.tick()
	h.advance(time.Second)
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment2))

	h.tick()

	assert.Equal(t, []string{"w-ENG-1:p1|" + resumeText1}, h.prompts())
}

func TestTickCommentNudgeSkipsOldCommentsWhenTheHistoryIsGone(t *testing.T) {
	h := newHarness(t)
	h.config("[rules]\nnudge_limit = 9\n")
	h.worker("ENG-1", "working")
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment1))
	h.tick()
	h.advance(time.Second)
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment2))
	require.NoError(t, h.stopLoop())
	require.NoError(t, os.RemoveAll(filepath.Join(h.state, "events")))

	h.tick()

	assert.Empty(t, h.prompts())

	// A worker dispatch has no record of starts out knowing the comments waiting, so only a newer comment nudges.
	h.advance(time.Second)
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), "2026-10-09T00:30:00.000Z"))
	h.tick()
	assert.Equal(t, []string{commentPrompt1}, h.prompts())
}

func TestTickCommentNudgeMakesNoExtraLinearCall(t *testing.T) {
	h := newHarness(t)
	h.config("[rules]\nnudge_limit = 9\n")
	h.worker("ENG-1", "working")
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment1))
	h.tick()
	before := h.linear.calls()
	h.tick()
	quiet := h.linear.calls() - before
	h.advance(time.Second)
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment2))
	before = h.linear.calls()

	h.tick()

	assert.Equal(t, []string{commentPrompt1}, h.prompts())
	assert.Equal(t, quiet, h.linear.calls()-before)
	assert.Empty(t, h.linear.childReadParents())
	assert.Empty(t, h.linear.mutations())
}

func TestTickCommentNudgeDoesNotCountTowardThePRLimit(t *testing.T) {
	h := newHarness(t)
	failing := prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"}
	h.worker("ENG-1", "working")
	h.pr("ENG-1", failing)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.tick()
	// A nudge has to come after dispatch saw the worker and the ticket, as it does outside a test.
	h.advance(time.Second)
	h.setWorkerStatus("ENG-1", "idle")
	h.tick()
	h.advance(time.Second)
	h.tickets(withComment(ticket("ENG-1", "In Review", "dispatch"), comment1))
	h.tick()
	h.advance(time.Second)
	h.tickets(withComment(ticket("ENG-1", "In Review", "dispatch"), comment2))
	h.tick()
	assert.Empty(t, h.linear.mutations())

	// Two comment nudges came between two PR nudges, so the second PR nudge still goes out.
	h.advance(time.Second)
	h.tick()
	assert.Equal(t, []string{prPrompt("a failed check"), commentPrompt1, commentPrompt1, prPrompt("a failed check")}, h.prompts())
	assert.Empty(t, h.linear.mutations())

	h.advance(time.Second)
	h.tick()
	assert.Len(t, h.linear.mutations(), 2)
}

func TestTickCommentNudgeNeverEscalatesTheTicket(t *testing.T) {
	h := newHarness(t)
	h.config("[rules]\nnudge_limit = 1\n")
	h.worker("ENG-1", "working")
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment1))
	h.tick()

	// Each comment finds the worker stopped, since a worker is not nudged again before it stops.
	for _, createdAt := range []string{comment2, "2026-10-09T00:30:00.000Z", "2026-10-09T00:40:00.000Z"} {
		h.advance(time.Second)
		h.setWorkerStatus("ENG-1", "idle")
		h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), createdAt))
		h.tick()
	}

	assert.Equal(t, []string{commentPrompt1, commentPrompt1, commentPrompt1}, h.prompts())
	assert.Empty(t, h.linear.mutations())
	assert.Empty(t, h.notifications())
}

func TestTickCommentNudgeIsOwedWhileTheRepoPoolCannotBeRead(t *testing.T) {
	h := newHarness(t)
	h.addRepo("tiger", filepath.Join(h.dir, "moved"))
	h.tickets(withComment(ticket("ENG-2", "In Progress", "tiger"), comment1))
	h.tick()
	h.advance(time.Second)
	h.tickets(withComment(ticket("ENG-2", "In Progress", "tiger"), comment2))

	h.tick()

	// The worker may be running unseen, so nothing is sent and nothing launches.
	plan, err := h.run("plan")
	require.NoError(t, err)
	assert.Regexp(t, `held\s+ENG-2\s+-\s+nudge-comment`, plan)
	assert.Empty(t, h.prompts())
	assert.Empty(t, h.launches())
}

func TestTickCommentNudgeAbsorbsACommentOnABlockedDesignReviewTicket(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "working")
	h.tickets(withComment(withLabel(ticket("ENG-1", "In Progress", "dispatch"), "design-review"), comment1))
	h.tick()
	h.advance(time.Second)
	h.setWorkerStatus("ENG-1", "blocked")
	h.tickets(withComment(withLabel(ticket("ENG-1", "In Progress", "dispatch"), "design-review"), comment2))
	h.tick()

	// The review worker is answered in its workspace, so the comment is never nudged once it is free.
	h.advance(time.Second)
	h.setWorkerStatus("ENG-1", "idle")
	h.tick()

	assert.Empty(t, h.prompts())
}

func TestTickCommentNudgeFiresForACommentPostedWhileTheTicketWasAway(t *testing.T) {
	for _, test := range []struct {
		name   string
		status string
		want   string
	}{
		{name: "Working", status: "working", want: commentPrompt1},
		// The resume nudge tells an idle worker to read every comment.
		{name: "Idle", status: "idle", want: "w-ENG-1:p1|" + resumeText1},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.config("[rules]\nnudge_limit = 9\n")
			h.worker("ENG-1", "working")
			h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment1))
			h.tick()
			// The ticket waits in Needs Human while comment2 is posted.
			h.advance(time.Second)
			h.tickets(withComment(ticket("ENG-1", "Needs Human", "dispatch"), comment2))
			h.tick()
			h.advance(time.Second)
			h.setWorkerStatus("ENG-1", test.status)
			h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment2))

			h.tick()

			assert.Equal(t, []string{test.want}, h.prompts())
		})
	}
}
