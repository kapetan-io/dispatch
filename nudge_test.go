package dispatch_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	stallText1  = "ENG-1 is still In Progress. Re-read the ticket and its latest comments, then keep going. If the PR is ready, move it to In Review. If you need a decision from me, move it to Needs Human and comment what you need."
	resumeText1 = "I answered on ENG-1. Read the latest comments and continue."
	stallPrompt = "w-ENG-1:p1|" + stallText1
)

func TestTickDryRunNamesTheNudgeWithoutSendingIt(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")

	out, err := h.run("tick", "--dry-run")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: stall\n", out)
	assert.Empty(t, h.prompts())
	assert.NoFileExists(t, h.state+"/cache.json")
}

func TestTickNudgesAnIdleWorkerWithoutCommenting(t *testing.T) {
	for _, status := range []string{"idle", "done"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
			h.worker("ENG-1", status)

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.Equal(t, "nudge ENG-1: stall\n", out)
			assert.Equal(t, []string{stallPrompt}, h.prompts())
			assert.Empty(t, h.linear.mutations())
			assert.Empty(t, h.launches())
		})
	}
}

func TestTickLeavesWorkingAndDesignWorkersAlone(t *testing.T) {
	for _, test := range []struct {
		name   string
		ticket fakeTicket
		status string
	}{
		{name: "Working", ticket: ticket("ENG-1", "In Progress", "dispatch"), status: "working"},
		{name: "Unknown", ticket: ticket("ENG-1", "In Progress", "dispatch"), status: "unknown"},
		{name: "DesignTicket", ticket: withLabel(ticket("ENG-1", "In Progress", "dispatch"), "design"), status: "idle"},
		{name: "ReviewTicket", ticket: withLabel(ticket("ENG-1", "In Progress", "dispatch"), "design-review"), status: "idle"},
		{name: "InReview", ticket: ticket("ENG-1", "In Review", "dispatch"), status: "idle"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(test.ticket)
			h.worker("ENG-1", test.status)

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.Empty(t, out)
			assert.Empty(t, h.prompts())
			assert.Empty(t, h.notifications())
			assert.Empty(t, h.linear.mutations())
		})
	}
}

func TestTickEscalatesAfterTwoNudgesWithOneComment(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")

	for range 2 {
		_, err := h.run("tick")
		require.NoError(t, err)
	}
	assert.Equal(t, []string{stallPrompt, stallPrompt}, h.prompts())
	assert.Empty(t, h.linear.mutations())

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "escalate ENG-1: its worker stayed idle through 2 nudges")
	assert.Len(t, h.prompts(), 2)
	mutations := h.linear.mutations()
	require.Len(t, mutations, 2)
	assert.Equal(t, "MoveTicket", mutations[0].operation)
	assert.Equal(t, needsHumanID, mutations[0].variables["state"])
	assert.Equal(t, "CommentTicket", mutations[1].operation)
	assert.Contains(t, mutations[1].variables["body"], "stayed idle through 2 nudges")

	// The ticket now reads Needs Human: no more comments, nudges or notifications.
	h.tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	for range 2 {
		_, err = h.run("tick")
		require.NoError(t, err)
	}
	assert.Len(t, h.linear.mutations(), 2)
	assert.Len(t, h.prompts(), 2)
	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1|dispatch"}, h.notifications())
}

func TestTickNudgeLimitComesFromConfig(t *testing.T) {
	h := newHarness(t)
	h.config("nudge_limit = 1\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")

	_, err := h.run("tick")
	require.NoError(t, err)
	assert.Empty(t, h.linear.mutations())

	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.prompts(), 1)
	assert.Len(t, h.linear.mutations(), 2)
}

func TestTickDoesNotCountANudgeHerdrRefused(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.writeFile("prompt-fails", "")

	var stderr string
	for range 4 {
		var err error
		_, stderr, err = h.runContext(t.Context(), "tick")
		require.NoError(t, err)
	}

	assert.Contains(t, stderr, "while nudging ENG-1")
	assert.Empty(t, h.linear.mutations())
}

func TestTickSendsTheResumeNudgeWhenAnswerMovesTicketBack(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "idle")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	for range 2 {
		_, err := h.run("tick")
		require.NoError(t, err)
	}
	h.tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	out, err := h.run("tick")
	require.NoError(t, err)

	// Two stall nudges had already been spent, so a resume instead of an
	// escalation shows the count restarted when the status changed.
	assert.Equal(t, "nudge ENG-1: resume\n", out)
	prompts := h.prompts()
	require.Len(t, prompts, 3)
	assert.Equal(t, "w-ENG-1:p1|"+resumeText1, prompts[2])
	assert.Empty(t, h.linear.mutations())
}

func TestTickNotifiesOnceWhenATicketEntersNeedsHuman(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "working")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	assert.Empty(t, h.notifications())

	h.tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	for range 3 {
		_, err = h.run("tick")
		require.NoError(t, err)
	}

	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1|dispatch"}, h.notifications())
	assert.Empty(t, h.prompts())
	assert.Empty(t, h.linear.mutations())
}

func TestTickNotifiesWhenADispatcherEscalationMovesATicket(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", ""))

	_, err := h.run("tick")
	require.NoError(t, err)
	h.tickets(ticket("ENG-1", "Needs Human", ""))
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1|dispatch"}, h.notifications())
}

func TestTickRetriesANeedsHumanNotificationThatFailed(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "working")
	h.tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	h.writeFile("notify-fails", "")

	_, stderr, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)
	assert.Contains(t, stderr, "while notifying about ENG-1")
	assert.Empty(t, h.notifications())

	h.removeFile("notify-fails")
	_, err = h.run("tick")
	require.NoError(t, err)
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.notifications(), 1)
}

func TestTickNotifiesAgainWhileAWorkerStaysBlocked(t *testing.T) {
	for _, status := range []string{"In Progress", "In Review"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", status, "dispatch"))
			h.worker("ENG-1", "blocked")
			const want = "ENG-1 is waiting on your answer in its herdr workspace|dispatch"

			var counts []int
			for range 7 {
				out, err := h.run("tick")
				require.NoError(t, err)
				assert.NotContains(t, out, "nudge")
				counts = append(counts, len(h.notifications()))
			}

			// First blocked tick, then every 3rd.
			assert.Equal(t, []int{1, 1, 1, 2, 2, 2, 3}, counts)
			assert.Equal(t, want, h.notifications()[0])
			assert.Empty(t, h.prompts())
			assert.Empty(t, h.linear.mutations())
		})
	}
}

func TestTickRenotifyIntervalComesFromConfig(t *testing.T) {
	h := newHarness(t)
	h.config("blocked_renotify_ticks = 1\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "blocked")

	for range 3 {
		_, err := h.run("tick")
		require.NoError(t, err)
	}

	assert.Len(t, h.notifications(), 3)
}

func TestTickNotifiesImmediatelyWhenAWorkerBlocksAgain(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "blocked")
	_, err := h.run("tick")
	require.NoError(t, err)

	h.setWorkerStatus("ENG-1", "working")
	_, err = h.run("tick")
	require.NoError(t, err)
	h.setWorkerStatus("ENG-1", "blocked")
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.notifications(), 2)
}

func TestTellTypesTextIntoAWorker(t *testing.T) {
	for _, status := range []string{"idle", "working"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			h.worker("ENG-1", status)

			out, err := h.run("tell", "ENG-1", "use the v2 endpoint")
			require.NoError(t, err)

			assert.Equal(t, "told ENG-1\n", out)
			assert.Equal(t, []string{"w-ENG-1:p1|use the v2 endpoint"}, h.prompts())
		})
	}
}

func TestTellFails(t *testing.T) {
	for _, test := range []struct {
		name    string
		worker  string
		args    []string
		wantErr string
	}{
		{name: "WorkerBlocked", worker: "blocked", args: []string{"tell", "ENG-1", "hi"}, wantErr: "ENG-1's worker is blocked on a question"},
		{name: "NoWorker", worker: "", args: []string{"tell", "ENG-2", "hi"}, wantErr: "ENG-2 has no worker"},
		{name: "WorkspaceWithoutClaude", worker: "none", args: []string{"tell", "ENG-1", "hi"}, wantErr: "ENG-1 has no worker"},
		{name: "MissingText", worker: "idle", args: []string{"tell", "ENG-1"}, wantErr: "tell takes a ticket and the text to type"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			switch test.worker {
			case "none":
				h.worker("ENG-1", "")
			case "":
			default:
				h.worker("ENG-1", test.worker)
			}

			_, err := h.run(test.args...)

			require.ErrorContains(t, err, test.wantErr)
			assert.Empty(t, h.prompts())
		})
	}
}

func TestTellReportsAWorkerHerdrRefuses(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "idle")
	h.writeFile("prompt-fails", "")

	_, err := h.run("tell", "ENG-1", "hi")

	require.ErrorContains(t, err, "agent_blocked")
}

func TestTellNamesAPoolItCouldNotRead(t *testing.T) {
	h := newHarness(t)
	h.addRepo("tiger", h.dir+"/moved")

	_, err := h.run("tell", "ENG-1", "hi")

	require.ErrorContains(t, err, "ENG-1 has no worker; could not read the pool of tiger")
}

func TestTickDryRunNamesNotificationsWithoutShowingThem(t *testing.T) {
	for _, test := range []struct {
		name   string
		status string
		worker string
		want   string
	}{
		{name: "NeedsHuman", status: "Needs Human", worker: "working", want: "notify ENG-1: entered Needs Human\n"},
		{name: "Blocked", status: "In Progress", worker: "blocked", want: "notify ENG-1: worker is blocked on a question\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", test.status, "dispatch"))
			h.worker("ENG-1", test.worker)

			out, err := h.run("tick", "--dry-run")
			require.NoError(t, err)

			assert.Equal(t, test.want, out)
			assert.Empty(t, h.notifications())
			assert.Empty(t, h.prompts())
			assert.Empty(t, h.linear.mutations())
			assert.NoFileExists(t, h.state+"/cache.json")
		})
	}
}

func TestTickRestartsTheNudgeCountWhenStatusChangesWithoutNeedsHuman(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "idle")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)

	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	out, err := h.run("tick")
	require.NoError(t, err)
	assert.Empty(t, out)

	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	for range 2 {
		out, err = h.run("tick")
		require.NoError(t, err)
		assert.Equal(t, "nudge ENG-1: stall\n", out)
	}

	// One nudge before the detour and two after: the detour restarted the count.
	assert.Len(t, h.prompts(), 3)
	assert.Empty(t, h.linear.mutations())

	out, err = h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "escalate ENG-1")
	assert.Len(t, h.linear.mutations(), 2)
}

func TestTickCountsTheResumeNudgeTowardEscalation(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "idle")
	h.tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	for _, want := range []string{"nudge ENG-1: resume\n", "nudge ENG-1: stall\n"} {
		out, err := h.run("tick")
		require.NoError(t, err)
		assert.Equal(t, want, out)
	}
	assert.Empty(t, h.linear.mutations())

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "escalate ENG-1")
	assert.Equal(t, []string{"w-ENG-1:p1|" + resumeText1, stallPrompt}, h.prompts())
	assert.Len(t, h.linear.mutations(), 2)
}

func TestTickSendsOnlyOneResumeNudge(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "working")
	h.tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.setWorkerStatus("ENG-1", "idle")

	_, err = h.run("tick")
	require.NoError(t, err)
	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: stall\n", out)
}

func TestTickNotifiesForNeedsHumanWhateverTheWorkerIs(t *testing.T) {
	for _, test := range []struct {
		name   string
		worker string
		none   bool
	}{
		{name: "NoLease", none: true},
		{name: "NoClaudeAgent", worker: ""},
		{name: "Blocked", worker: "blocked"},
		{name: "Idle", worker: "idle"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", "Needs Human", "dispatch"))
			if !test.none {
				h.worker("ENG-1", test.worker)
			}

			for range 3 {
				_, err := h.run("tick")
				require.NoError(t, err)
			}

			assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1|dispatch"}, h.notifications())
			assert.Empty(t, h.prompts())
			assert.Empty(t, h.launches())
			assert.Empty(t, h.linear.mutations())
		})
	}
}

func TestTickNotifiesEachTimeATicketReentersNeedsHuman(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "working")

	for _, status := range []string{"Needs Human", "In Progress", "Needs Human", "Needs Human"} {
		h.tickets(ticket("ENG-1", status, "dispatch"))
		_, err := h.run("tick")
		require.NoError(t, err)
	}

	assert.Len(t, h.notifications(), 2)
}

func TestTickNotifiesAfterAnEscalationWhoseAlertFailed(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	for range 2 {
		_, err := h.run("tick")
		require.NoError(t, err)
	}
	h.writeFile("notify-fails", "")

	out, err := h.run("tick")
	require.NoError(t, err)
	assert.Contains(t, out, "escalate ENG-1")
	assert.Empty(t, h.notifications())
	assert.Len(t, h.linear.mutations(), 2)

	h.removeFile("notify-fails")
	h.tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	for range 2 {
		_, err = h.run("tick")
		require.NoError(t, err)
	}

	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1|dispatch"}, h.notifications())
	assert.Len(t, h.linear.mutations(), 2)
}

func TestTickDoesNotNotifyForAnEscalationLinearRefused(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	for range 2 {
		_, err := h.run("tick")
		require.NoError(t, err)
	}
	h.linear.failMoves()

	_, stderr, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)

	assert.Contains(t, stderr, "while escalating ENG-1")
	assert.Empty(t, h.notifications())
	assert.Empty(t, h.linear.mutations())
}

func TestTickRetriesABlockedNotificationThatFailed(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "blocked")
	h.writeFile("notify-fails", "")

	_, stderr, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)
	assert.Contains(t, stderr, "while notifying about ENG-1")
	assert.Empty(t, h.notifications())

	h.removeFile("notify-fails")
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.notifications(), 1)
}

func TestTickTracksBlockedTicksPerTicket(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Review", "dispatch"))
	h.worker("ENG-1", "blocked")
	h.worker("ENG-2", "working")
	const (
		first  = "ENG-1 is waiting on your answer in its herdr workspace|dispatch"
		second = "ENG-2 is waiting on your answer in its herdr workspace|dispatch"
	)

	_, err := h.run("tick")
	require.NoError(t, err)
	h.setWorkerStatus("ENG-2", "blocked")
	for range 4 {
		_, err = h.run("tick")
		require.NoError(t, err)
	}

	// ENG-1 at ticks 1 and 4, ENG-2 at ticks 2 and 5.
	assert.Equal(t, []string{first, second, first, second}, h.notifications())
}

func TestTickNudgesAfterABlockedWorkerGoesIdle(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "blocked")
	for range 4 {
		_, err := h.run("tick")
		require.NoError(t, err)
	}
	h.setWorkerStatus("ENG-1", "idle")

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: stall\n", out)
	assert.Empty(t, h.linear.mutations())
}

func TestTickRestartsNudgesWhenTheCacheIsDeleted(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	for range 2 {
		_, err := h.run("tick")
		require.NoError(t, err)
	}
	h.removeFile("state/cache.json")

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: stall\n", out)
	assert.Len(t, h.prompts(), 3)
	assert.Empty(t, h.linear.mutations())
}

func TestTickNudgesEachIdleTicketIndependently(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.worker("ENG-2", "working")
	_, err := h.run("tick")
	require.NoError(t, err)
	_, err = h.run("tick")
	require.NoError(t, err)
	h.setWorkerStatus("ENG-2", "idle")

	_, err = h.run("tick")
	require.NoError(t, err)

	// ENG-1 is out of nudges; ENG-2 has had none.
	mutations := h.linear.mutations()
	require.Len(t, mutations, 2)
	assert.Equal(t, "uuid-ENG-1", mutations[0].variables["id"])
	assert.Contains(t, h.prompts(), "w-ENG-2:p1|ENG-2 is still In Progress. Re-read the ticket and its latest comments, then keep going. If the PR is ready, move it to In Review. If you need a decision from me, move it to Needs Human and comment what you need.")
}

func TestTellTypesTheTextVerbatim(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "idle")

	_, err := h.run("tell", "ENG-1", `fix "it" && echo $HOME`)
	require.NoError(t, err)

	assert.Equal(t, []string{`w-ENG-1:p1|fix "it" && echo $HOME`}, h.prompts())
}

func TestTellRejectsExtraArguments(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "idle")

	_, err := h.run("tell", "ENG-1", "use", "v2")

	require.ErrorContains(t, err, "tell takes a ticket and the text to type")
	assert.Empty(t, h.prompts())
}

func TestTickRetriesAResumeNudgeHerdrRefused(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "idle")
	h.tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.writeFile("prompt-fails", "")
	_, err = h.run("tick")
	require.NoError(t, err)
	h.removeFile("prompt-fails")

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: resume\n", out)
	assert.Equal(t, []string{"w-ENG-1:p1|" + resumeText1}, h.prompts())
}

func TestTickSendsTheResumeNudgeToAWorkerThatWasBlockedWhenTheTicketMovedBack(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "working")
	h.tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.setWorkerStatus("ENG-1", "blocked")
	_, err = h.run("tick")
	require.NoError(t, err)
	assert.Empty(t, h.prompts())
	h.setWorkerStatus("ENG-1", "idle")

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: resume\n", out)
	assert.Equal(t, []string{"w-ENG-1:p1|" + resumeText1}, h.prompts())
}
