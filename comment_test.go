package dispatch_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

const (
	comment1 = "2026-10-09T00:09:00.000Z"
	comment2 = "2026-10-09T00:20:00.000Z"
)

func withComment(t fakeTicket, createdAt string) fakeTicket {
	t.LastComment = createdAt
	return t
}

// A ticket comment is not an event dispatch nudges for. These tests pin that.
func TestTickNudgesNoWorkerAboutANewTicketComment(t *testing.T) {
	for _, test := range []struct {
		name   string
		worker string
		status string
	}{
		{name: "WorkingInProgress", worker: "working", status: "In Progress"},
		{name: "BlockedInProgress", worker: "blocked", status: "In Progress"},
		{name: "WorkingInReview", worker: "working", status: "In Review"},
		{name: "IdleInReview", worker: "idle", status: "In Review"},
		{name: "BlockedInReview", worker: "blocked", status: "In Review"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.worker("ENG-1", test.worker)
			h.tickets(withComment(ticket("ENG-1", test.status, "dispatch"), comment1))
			h.tick()
			h.advance(time.Second)
			h.tickets(withComment(ticket("ENG-1", test.status, "dispatch"), comment2))

			h.tick()

			for _, prompt := range h.prompts() {
				assert.NotContains(t, prompt, "new comment")
			}
		})
	}
}

func TestTickStallNudgeIgnoresANewTicketComment(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "working")
	h.recordStop("ENG-1", noTasks)
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment1))
	h.tick()
	h.advance(time.Second)
	h.setWorkerStatus("ENG-1", "idle")
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment2))

	h.tick()

	assert.Equal(t, []string{stallPrompt}, h.prompts())
}

func TestTickPRNudgeIsNotHeldBackByATicketComment(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "idle")
	h.tickets(withComment(ticket("ENG-1", "In Review", "dispatch"), comment1))
	h.tick()
	h.advance(time.Second)
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	h.tickets(withComment(ticket("ENG-1", "In Review", "dispatch"), comment2))

	h.tick()

	assert.Equal(t, []string{prPrompt("a failed check")}, h.prompts())
}

func TestTickActivityNudgeFiresOnTheTickATicketCommentLands(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "idle")
	h.tickets(withComment(ticket("ENG-1", "In Review", "dispatch"), comment1))
	h.pr("ENG-1", cleanPR("a1"))
	h.tick()
	h.advance(time.Second)
	p := cleanPR("a1")
	p.reviews = []activity{{"COMMENTED", time2, "alice", "", ""}}
	h.pr("ENG-1", p)
	h.tickets(withComment(ticket("ENG-1", "In Review", "dispatch"), comment2))

	h.tick()

	assert.Equal(t, []string{activityPrompt("a review by alice")}, h.prompts())
}

func TestTickResumeNudgeIgnoresANewTicketComment(t *testing.T) {
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
