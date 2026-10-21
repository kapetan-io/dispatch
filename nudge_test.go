package dispatch_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/ledger"
)

const (
	resumeText1 = "I answered on ENG-1. Read the latest comments and continue."
)

func TestTickNudgesAnIdleWorkerWithoutCommenting(t *testing.T) {
	for _, status := range []string{"idle", "done"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", status)
			reviewFailsAfterEntry(h, prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

			require.NoError(t, reviewTick(t, h))

			assert.Equal(t, []string{prPrompt("a failed check")}, h.prompts())
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

func TestTickDoesNotCountANudgeHerdrRefused(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	reviewFailsAfterEntry(h, prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	h.blockAfterSnapshot("ENG-1")

	for range 4 {
		err := reviewTick(t, h)
		require.ErrorContains(t, err, "ENG-1 nudge failed: blocked")
	}

	assert.Empty(t, h.prompts())
	assert.Empty(t, h.linear.mutations())
	// Each tick retries the nudge, and every attempt is its own receipt.
	ledger := h.ledgerNow()
	require.Len(t, ledger, 4)
	for _, receipt := range ledger {
		assert.Equal(t, "nudge", receipt.Kind)
		assert.Equal(t, "failed", string(receipt.Outcome))
		assert.Equal(t, "blocked", receipt.Detail)
	}
}

func TestTickSendsTheResumeNudgeWhenAnswerMovesTicketBack(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "working")
	h.recordStop("ENG-1", noTasks)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	h.tick()
	// The nudge has to come after dispatch first saw the worker, as it does outside a test.
	h.advance(time.Second)
	h.setWorkerStatus("ENG-1", "idle")
	h.tick()
	h.advance(time.Minute)
	h.tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	h.tick()
	h.advance(time.Minute)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	// A worker gets no nudge of any kind until it stops after the last one.
	h.tick()
	assert.Equal(t, []string{prPrompt("a failed check")}, h.prompts())

	h.advance(time.Minute)
	h.recordStop("ENG-1", noTasks)
	h.tick()

	assert.Equal(t, []string{prPrompt("a failed check"), "w-ENG-1:p1|" + resumeText1}, h.prompts())
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

	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1"}, h.notifications())
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

	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1"}, h.notifications())
}

func TestTickRetriesANeedsHumanNotificationThatFailed(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "working")
	h.tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	h.tools.Alerts.Fail(errors.New("exit status 1"))

	_, err := h.run("tick")
	require.ErrorContains(t, err, "ENG-1 notify failed")
	assert.Empty(t, h.notifications())

	h.tools.Alerts.Fail(nil)
	h.tick()
	h.tick()

	assert.Len(t, h.notifications(), 1)
}

func TestTickNotifiesAgainWhileAWorkerStaysBlocked(t *testing.T) {
	for _, status := range []string{"In Progress", "In Review"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", status, "dispatch"))
			h.worker("ENG-1", "blocked")
			const want = "ENG-1 is waiting on your answer in its herdr workspace"

			var counts []int
			for i := range 7 {
				if i > 0 {
					h.advance(5 * time.Minute)
				}
				h.tick()
				counts = append(counts, len(h.notifications()))
			}

			// The first notice, then one every 15 minutes.
			assert.Equal(t, []int{1, 1, 1, 2, 2, 2, 3}, counts)
			assert.Equal(t, want, h.notifications()[0])
			assert.Empty(t, h.prompts())
			assert.Empty(t, h.linear.mutations())
		})
	}
}

func TestTickRenotifyIntervalComesFromConfig(t *testing.T) {
	h := newHarness(t)
	h.config("[rules]\nblocked_renotify_minutes = 5\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "blocked")

	for i := range 3 {
		if i > 0 {
			h.advance(5 * time.Minute)
		}
		h.tick()
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
	h.advance(time.Minute)
	_, err = h.run("tick")
	require.NoError(t, err)
	h.setWorkerStatus("ENG-1", "blocked")
	h.advance(time.Minute)
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
	for _, status := range []string{"idle", "working"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			h.worker("ENG-1", status)
			h.blockAfterSnapshot("ENG-1")

			_, err := h.run("tell", "ENG-1", "hi")

			require.ErrorContains(t, err, "ENG-1's worker is blocked on a question")
			assert.Empty(t, h.prompts())
		})
	}
}

func TestTellNamesAPoolItCouldNotRead(t *testing.T) {
	h := newHarness(t)
	h.addRepo("tiger", h.dir+"/moved")

	_, err := h.run("tell", "ENG-1", "hi")

	require.ErrorContains(t, err, "ENG-1 has no worker; could not read the pool of tiger")
}

func TestTickSendsOnlyOneResumeNudge(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "working")
	h.tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	h.tick()
	h.advance(time.Second)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.setWorkerStatus("ENG-1", "idle")

	h.tick()
	assert.Equal(t, []string{"w-ENG-1:p1|" + resumeText1}, h.prompts())
	h.tick()

	assert.Equal(t, []string{"w-ENG-1:p1|" + resumeText1}, h.prompts())
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

			assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1"}, h.notifications())
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
		h.tick()
		h.advance(time.Minute)
	}

	assert.Len(t, h.notifications(), 2)
}

func TestTickNotifiesAfterAnEscalationWhoseAlertFailed(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "working")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	h.tick()
	// Nudges count from the ticket entering In Review, so the worker idles a moment after it.
	h.advance(time.Second)
	h.setWorkerStatus("ENG-1", "idle")
	h.tick()
	h.advance(5 * time.Minute)
	h.tools.Alerts.Fail(errors.New("exit status 1"))

	// Two nudges went out, and the next pass finds the worker still ignoring them.
	h.advance(5 * time.Minute)
	_, err := h.run("tick")
	require.ErrorContains(t, err, "notify failed")
	assert.Empty(t, h.notifications())
	assert.Len(t, h.linear.mutations(), 2)

	h.tools.Alerts.Fail(nil)
	h.tick()
	h.tick()

	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1"}, h.notifications())
	assert.Len(t, h.linear.mutations(), 2)
}

func TestTickDoesNotNotifyForAnEscalationLinearRefused(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "working")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	h.tick()
	h.advance(time.Second)
	h.setWorkerStatus("ENG-1", "idle")
	h.tick()
	h.advance(5 * time.Minute)
	h.linear.failMoves()
	h.advance(5 * time.Minute)

	_, err := h.run("tick")

	require.ErrorContains(t, err, "ENG-1 escalate failed")
	assert.Empty(t, h.notifications())
	assert.Empty(t, h.linear.mutations())
}

func TestTickRetriesABlockedNotificationThatFailed(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "blocked")
	h.tools.Alerts.Fail(errors.New("exit status 1"))

	_, err := h.run("tick")
	require.ErrorContains(t, err, "ENG-1 notify failed")
	assert.Empty(t, h.notifications())

	h.tools.Alerts.Fail(nil)
	h.tick()

	assert.Len(t, h.notifications(), 1)
}

func TestTickTracksBlockedTicksPerTicket(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Review", "dispatch"))
	h.worker("ENG-1", "blocked")
	h.worker("ENG-2", "working")
	const (
		first  = "ENG-1 is waiting on your answer in its herdr workspace"
		second = "ENG-2 is waiting on your answer in its herdr workspace"
	)

	h.tick()
	h.setWorkerStatus("ENG-2", "blocked")
	for range 4 {
		h.advance(5 * time.Minute)
		h.tick()
	}

	// ENG-1 at minutes 0 and 15, ENG-2 at minutes 5 and 20.
	assert.Equal(t, []string{first, second, first, second}, h.notifications())
}

func TestTickNudgesAfterABlockedWorkerGoesIdle(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "blocked")
	reviewFailsAfterEntry(h, prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	for range 4 {
		h.tick()
	}
	h.setWorkerStatus("ENG-1", "idle")

	require.NoError(t, reviewTick(t, h))

	assert.Equal(t, []string{prPrompt("a failed check")}, h.prompts())
	assert.Empty(t, h.linear.mutations())
}

func TestTickNudgesAgainWhenTheHistoryIsDeleted(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	reviewFailsAfterEntry(h, prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	require.NoError(t, reviewTick(t, h))
	require.NoError(t, h.stopLoop())
	require.NoError(t, os.RemoveAll(filepath.Join(h.state, "events")))
	h.advance(5 * time.Minute)

	h.tick()

	assert.Equal(t, []string{prPrompt("a failed check"), prPrompt("a failed check")}, h.prompts())
	assert.Empty(t, h.linear.mutations())
}

func TestTickNudgesEachIdleTicketIndependently(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"), ticket("ENG-2", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.worker("ENG-2", "working")
	h.pr("ENG-1", prState{head: "a1", checks: "PENDING", mergeable: "MERGEABLE"})
	h.pr("ENG-2", prState{head: "a1", checks: "PENDING", mergeable: "MERGEABLE"})
	h.restart()
	h.advance(time.Second)
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	h.pr("ENG-2", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	h.tick()
	h.tick()
	h.setWorkerStatus("ENG-2", "idle")

	require.NoError(t, reviewTick(t, h))

	assert.Equal(t, []string{prPrompt("a failed check"), strings.ReplaceAll(prPrompt("a failed check"), "ENG-1", "ENG-2")}, h.prompts())
	assert.Empty(t, h.linear.mutations())
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
	h.blockAfterSnapshot("ENG-1")
	_, err = h.run("tick")
	require.ErrorContains(t, err, "ENG-1 nudge failed: blocked")
	h.unblock("ENG-1")

	h.tick()

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

	h.tick()

	assert.Equal(t, []string{"w-ENG-1:p1|" + resumeText1}, h.prompts())
}

func TestTickSendsTheResumeNudgeAfterANeedsHumanNotificationFailed(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "idle")
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	reviewFailsAfterEntry(h, prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	require.NoError(t, reviewTick(t, h))
	h.tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	h.tools.Alerts.Fail(errors.New("exit status 1"))
	_, err := h.run("tick")
	require.ErrorContains(t, err, "ENG-1 notify failed")
	h.tools.Alerts.Fail(nil)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	// The PR nudge went first, so the worker has to stop before it hears the resume.
	h.advance(time.Minute)
	h.recordStop("ENG-1", noTasks)
	h.tick()

	assert.Equal(t, []string{prPrompt("a failed check"), "w-ENG-1:p1|" + resumeText1}, h.prompts())
}

func TestTickNotifiesWhenAReviewWorkerIsBlockedOnAQuestion(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "blocked")
	h.tickets(withLabel(ticket("ENG-1", "In Progress", "dispatch"), "design-review"))

	h.tick()

	assert.Equal(t, []string{"ENG-1 is waiting on your answer in its herdr workspace"}, h.notifications())
}

func TestTickNotifiesWhenAReviewTicketThatAlsoCarriesDesignIsBlocked(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "blocked")
	h.tickets(withLabel(designTicket("ENG-1", "In Progress"), "design-review"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"ENG-1 is waiting on your answer in its herdr workspace"}, h.notifications())
}

func TestTickNotifiesWhenAReviewTicketEntersNeedsHuman(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "working")
	h.tickets(withLabel(ticket("ENG-1", "In Progress", "dispatch"), "design-review"))
	_, err := h.run("tick")
	require.NoError(t, err)

	h.tickets(withLabel(ticket("ENG-1", "Needs Human", "dispatch"), "design-review"))
	for range 3 {
		_, err = h.run("tick")
		require.NoError(t, err)
	}

	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1"}, h.notifications())
}

func TestTickStartsFreshWhenAPausedTicketReturns(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "idle")
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	reviewFailsAfterEntry(h, prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	require.NoError(t, reviewTick(t, h))
	h.tick()
	// Moved to Todo, the ticket is paused and its worker is closed.
	h.tickets()
	h.linear.setElsewhere(ticket("ENG-1", "Todo", "dispatch"))
	h.advance(time.Minute)
	h.tick()
	h.linear.setElsewhere()
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.advance(time.Minute)

	h.tick()

	// The worker it had is gone, so a fresh one starts instead of a second nudge.
	assert.Equal(t, []string{"w-ENG-1"}, h.closes())
	assert.Len(t, h.prompts(), 1)
	assert.Len(t, h.launches(), 1)
	assert.Empty(t, h.linear.mutations())
}

func TestTickDropsAPendingResumeWhenTheTicketIsPaused(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "blocked")
	h.tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.advance(time.Minute)
	h.tick()
	h.tickets()
	h.linear.setElsewhere(ticket("ENG-1", "Todo", "dispatch"))
	h.advance(time.Minute)
	h.tick()
	h.linear.setElsewhere()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.advance(time.Minute)

	h.tick()

	// The paused ticket's worker was closed, so a fresh one starts and the resume nudge is gone.
	assert.Empty(t, h.prompts())
	assert.Len(t, h.launches(), 1)
}

func TestTickRenotifyIntervalIsTheConfiguredMinutes(t *testing.T) {
	h := newHarness(t)
	h.config("[dispatch]\nresync = \"30s\"\n[rules]\nblocked_renotify_minutes = 1\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "blocked")

	var counts []int
	for i := range 4 {
		if i > 0 {
			h.advance(30 * time.Second)
		}
		h.tick()
		counts = append(counts, len(h.notifications()))
	}

	// A minute is two 30-second resyncs: the first notice, then one every minute.
	assert.Equal(t, []int{1, 1, 2, 2}, counts)
}

func TestTickNudgedWorkerGetsNoPRNudgeBeforeTheNextResync(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	h.restart()
	require.Equal(t, []string{prPrompt("a failed check")}, h.prompts())

	// herdr still reports the nudged worker idle a second later. A tell reads herdr
	// without a resync, and the worker must still count as busy.
	h.advance(time.Second)
	_, err := h.run("tell", "ENG-1", "check the logs")
	require.NoError(t, err)
	assert.Equal(t, []string{prPrompt("a failed check"), "w-ENG-1:p1|check the logs"}, h.prompts())

	// The next resync believes herdr again, and a worker still idle then gets its PR nudge.
	h.advance(time.Second)
	h.tick()
	assert.Equal(t, []string{prPrompt("a failed check"), "w-ENG-1:p1|check the logs", prPrompt("a failed check")}, h.prompts())
}

func TestTickWorkerWithNoStopRecordIsNudgedAgainOnlyAtAResync(t *testing.T) {
	t.Run("TickOnTheSameClock", func(t *testing.T) {
		h := newHarness(t)
		h.tickets(ticket("ENG-1", "In Review", "dispatch"))
		h.worker("ENG-1", "idle")
		h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

		h.restart()
		assert.Equal(t, []string{prPrompt("a failed check")}, h.prompts())

		h.tick()
		h.tick()

		assert.Len(t, h.prompts(), 1)
		assert.Empty(t, h.notifications())
	})

	t.Run("ResyncPasses", func(t *testing.T) {
		h := newHarness(t)
		h.config("[rules]\nnudge_limit = 9\n")
		h.tickets(ticket("ENG-1", "In Review", "dispatch"))
		h.worker("ENG-1", "idle")
		h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

		h.restart()
		assert.Len(t, h.prompts(), 1)

		h.advance(5 * time.Minute)
		assert.Len(t, h.prompts(), 2)
		h.advance(5 * time.Minute)
		assert.Len(t, h.prompts(), 3)
		assert.Empty(t, h.notifications())
	})
}

func TestTickReadyNoticeAndMergeWaitWhileANudgedWorkerShowsAsWorking(t *testing.T) {
	for _, test := range []struct {
		name     string
		settings string
		done     func(h *harness) int
	}{
		{name: "ReadyNotice", done: func(h *harness) int { return len(h.notifications()) }},
		{name: "Merge", settings: "auto_merge = true\n", done: func(h *harness) int { return len(h.merges()) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.repoSettings("dispatch", test.settings)
			h.worker("ENG-1", "working")
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			pr := cleanPR("a1")
			pr.approved = []string{"a1"}
			h.pr("ENG-1", pr)
			h.tick()
			require.Zero(t, test.done(h))

			// The worker goes idle as a review lands, so the activity nudge goes first.
			h.advance(time.Second)
			h.setWorkerStatus("ENG-1", "idle")
			pr.reviews = []activity{{"COMMENTED", time2, "alice", "", ""}}
			h.pr("ENG-1", pr)
			h.tick()
			assert.Equal(t, []string{activityPrompt("a review by alice")}, h.prompts())
			assert.Zero(t, test.done(h))

			// Once a resync finds the nudged worker idle again, the notice and the merge follow.
			h.advance(time.Second)
			h.tick()
			assert.Equal(t, 1, test.done(h))
		})
	}
}

func TestTickNeverSendsANudgeAttemptedBeforeACrash(t *testing.T) {
	for _, test := range []struct {
		name    string
		seeded  bool
		prompts int
	}{
		{name: "NoEarlierAttempt", seeded: false, prompts: 1},
		{name: "AttemptWithNoOutcome", seeded: true, prompts: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", "idle")
			h.recordStop("ENG-1", noTasks)
			h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
			h.advance(time.Second)
			if test.seeded {
				// The crashed run wrote the receipt before it typed the text, and never learned how it ended.
				h.seedHistory(nil, ledger.Receipt{
					Kind: "nudge", Subject: "ENG-1", Once: ledger.Once{"pr", "a1", h.clock.Now().UnixMilli(), 0}, Rule: "nudge-pr-needs-work",
					Started: h.clock.Now().Add(-time.Minute).UnixMilli(),
				})
			}

			h.tick()
			h.advance(time.Minute)
			h.tick()

			assert.Len(t, h.prompts(), test.prompts)
			assert.Empty(t, h.linear.mutations())
		})
	}
}
