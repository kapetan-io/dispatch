package dispatch_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			_, err := h.run("tick")
			require.NoError(t, err)
			h.tickets(withComment(ticket("ENG-1", test.status, "dispatch"), comment2))

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.NotContains(t, out, "new comment")
			for _, prompt := range h.prompts() {
				assert.NotContains(t, prompt, "new comment")
			}
		})
	}
}

func TestTickDryRunAfterANewTicketCommentShowsNoNudge(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "working")
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment1))
	_, err := h.run("tick")
	require.NoError(t, err)
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment2))

	out, err := h.run("tick", "--dry-run")
	require.NoError(t, err)

	assert.Empty(t, out)
}

func TestTickStallNudgeIgnoresANewTicketComment(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "working")
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment1))
	_, err := h.run("tick")
	require.NoError(t, err)
	h.setWorkerStatus("ENG-1", "idle")
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment2))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: stall\n", out)
}

func TestTickPRNudgeIsNotHeldBackByATicketComment(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "idle")
	h.tickets(withComment(ticket("ENG-1", "In Review", "dispatch"), comment1))
	_, err := h.run("tick")
	require.NoError(t, err)
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	h.tickets(withComment(ticket("ENG-1", "In Review", "dispatch"), comment2))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: PR needs work: a failed check\n", out)
	assert.Equal(t, []string{prPrompt("a failed check")}, h.prompts())
}

func TestTickLoadsACacheThatStillHoldsCommentSeen(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "working")
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment2))
	require.NoError(t, os.MkdirAll(h.state, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(h.state, "cache.json"), []byte(`{"ENG-1":{"last_status":"In Progress","comment_seen":"`+comment1+`"}}`), 0o600))

	out, stderr, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Empty(t, stderr)
	assert.Empty(t, h.prompts())
}

func TestTickActivityNudgeFiresOnTheTickATicketCommentLands(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "idle")
	h.tickets(withComment(ticket("ENG-1", "In Review", "dispatch"), comment1))
	require.NoError(t, os.MkdirAll(h.state, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(h.state, "cache.json"), []byte(`{"ENG-1":{"last_status":"In Review","pr_activity_seen":"`+time1+`"}}`), 0o600))
	p := cleanPR("a1")
	p.reviews = []activity{{"COMMENTED", time2, "alice", "", ""}}
	h.pr("ENG-1", p)
	h.tickets(withComment(ticket("ENG-1", "In Review", "dispatch"), comment2))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "nudge ENG-1: new PR activity")
	assert.Len(t, h.prompts(), 1)
}

func TestTickResumeNudgeIgnoresANewTicketComment(t *testing.T) {
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
