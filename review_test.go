package dispatch_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/lineartest"
	"github.com/kapetan-io/dispatch/internal/treehouse"

	"github.com/kapetan-io/dispatch/internal/herdr"
)

// setWorkerAgent starts a Claude agent in a workspace made with an empty status.
func (h *harness) setWorkerAgent(ticket, status string) {
	id := "w-" + ticket
	h.tools.Herdr.AddPane(herdr.Pane{ID: id + ":p1", WorkspaceID: id})
	h.tools.Herdr.AddAgent(herdr.Agent{Kind: "claude", Status: status, Session: "worker", PaneID: id + ":p1", WorkspaceID: id})
}

// reviewWorkerComesAndGoes shows dispatch a relaunched worker that then exits, which
// starts a new stretch with no worker.
func reviewWorkerComesAndGoes(t *testing.T, h *harness) {
	h.worker("ENG-1", "working")
	require.NoError(t, reviewTick(t, h))
	require.NoError(t, h.tools.Herdr.CloseWorkspace(context.Background(), "w-ENG-1"))
}

// reviewTreeComesBack returns ENG-1's tree to the pool and takes it again.
func reviewTreeComesBack(t *testing.T, h *harness) {
	h.tools.Treehouse.Clear(h.repo)
	require.NoError(t, reviewTick(t, h))
	h.tools.Treehouse.Seed(h.repo, treehouse.Tree{Path: filepath.Join(h.dir, "trees", "ENG-1"), Status: "leased", LeaseHolder: "ENG-1"})
}

func TestTickNudgesAnIdleInReviewWorkerNamingTheProblem(t *testing.T) {
	for _, test := range []struct {
		name    string
		pr      prState
		problem string
	}{
		{name: "FailedCheck", pr: prState{head: "a1", checks: "FAILURE", decision: "APPROVED", mergeable: "MERGEABLE"}, problem: "a failed check"},
		{name: "ErroredCheck", pr: prState{head: "a1", checks: "ERROR", mergeable: "MERGEABLE"}, problem: "a failed check"},
		{name: "ChangesRequested", pr: prState{head: "a1", checks: "SUCCESS", decision: "CHANGES_REQUESTED", mergeable: "MERGEABLE"}, problem: "changes requested"},
		{name: "MergeConflict", pr: prState{head: "a1", checks: "SUCCESS", mergeable: "CONFLICTING"}, problem: "a merge conflict"},
		{name: "TwoProblems", pr: prState{head: "a1", checks: "FAILURE", mergeable: "CONFLICTING"}, problem: "a failed check and a merge conflict"},
		{name: "ThreeProblems", pr: prState{head: "a1", checks: "FAILURE", decision: "CHANGES_REQUESTED", mergeable: "CONFLICTING"}, problem: "a failed check, changes requested and a merge conflict"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", "idle")
			h.pr("ENG-1", test.pr)

			_, err := h.run("tick")
			require.NoError(t, err)

			assert.Equal(t, []string{prPrompt(test.problem)}, h.prompts())
			assert.Empty(t, h.linear.mutations())
			assert.Empty(t, h.launches())
		})
	}
}

func TestTickLeavesAnInReviewTicketAloneWhenThePRNeedsNothing(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(h *harness)
	}{
		{name: "CleanPRNoWorker", setup: func(h *harness) { h.worker("ENG-1", ""); h.pr("ENG-1", cleanPR("a1")) }},
		{name: "PendingChecksResolvedThreads", setup: func(h *harness) {
			h.worker("ENG-1", "idle")
			h.pr("ENG-1", prState{head: "a1", checks: "PENDING", mergeable: "UNKNOWN", resolved: 3})
		}},
		{name: "NoOpenPR", setup: func(h *harness) { h.worker("ENG-1", "idle") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			test.setup(h)

			_, err := h.run("tick")
			require.NoError(t, err)

			assert.Empty(t, h.prompts())
			assert.Empty(t, h.launches())
			assert.Empty(t, h.linear.mutations())
		})
	}
}

func TestTickDoesNotReadThePRWhileTheInReviewWorkerIsBusy(t *testing.T) {
	for _, status := range []string{"working", "unknown"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", status)
			h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

			_, err := h.run("tick")
			require.NoError(t, err)

			assert.Empty(t, h.prompts())
			assert.Empty(t, h.prReads())
		})
	}
}

func TestTickOnlyNotifiesForABlockedInReviewWorker(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "blocked")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.notifications(), 1)
	assert.Empty(t, h.prompts())
	assert.Empty(t, h.prReads())
}

func TestTickNeverNudgesDesignTicketsAboutPRs(t *testing.T) {
	for _, label := range []string{"design", "design-review"} {
		t.Run(label, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(withLabel(ticket("ENG-1", "In Review", "dispatch"), label))
			h.worker("ENG-1", "idle")
			h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

			_, err := h.run("tick")
			require.NoError(t, err)

			assert.Empty(t, h.prompts())
			assert.Empty(t, h.prReads())
		})
	}
}

func TestTickReadsOneInReviewPRPerTicketPerTick(t *testing.T) {
	h := newHarness(t)
	h.tickets(
		ticket("ENG-1", "In Review", "dispatch"),
		ticket("ENG-2", "In Review", "dispatch"),
	)
	h.worker("ENG-1", "idle")
	h.worker("ENG-2", "idle")

	_, err := h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, []string{"thrawn01/eng-1", "thrawn01/eng-2"}, h.prReads())

	// Each tick after the loop's own first read takes one page per ticket.
	_, err = h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, []string{"thrawn01/eng-1", "thrawn01/eng-2", "thrawn01/eng-1", "thrawn01/eng-2"}, h.prReads())
}

func TestTickEscalatesAnInReviewTicketAfterTwoPRNudgesWithNoPush(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	reviewFailsAfterEntry(h, prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	for range 2 {
		require.NoError(t, reviewTick(t, h))
	}
	assert.Equal(t, []string{prPrompt("a failed check"), prPrompt("a failed check")}, h.prompts())
	assert.Empty(t, h.linear.mutations())

	require.NoError(t, reviewTick(t, h))

	assert.Len(t, h.prompts(), 2)
	mutations := h.linear.mutations()
	require.Len(t, mutations, 2)
	assert.Equal(t, lineartest.Move, mutations[0].Kind)
	assert.Equal(t, needsHumanID, mutations[0].StateID)
	assert.Equal(t, lineartest.Comment, mutations[1].Kind)
	assert.Contains(t, mutations[1].Body, "stayed idle through 2 nudges about "+prURL1)

	// The ticket now reads Needs Human, so the dispatcher stays quiet.
	h.tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	for range 2 {
		require.NoError(t, reviewTick(t, h))
	}
	assert.Len(t, h.linear.mutations(), 2)
	assert.Len(t, h.prompts(), 2)
}

func TestTickResetsThePRNudgeCountWhenTheHeadMoves(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	reviewFailsAfterEntry(h, prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	for range 2 {
		require.NoError(t, reviewTick(t, h))
	}

	// The worker pushed a fix, and a review that arrives later is a new problem.
	h.pr("ENG-1", prState{head: "b2", checks: "SUCCESS", decision: "CHANGES_REQUESTED", mergeable: "MERGEABLE"})
	for range 2 {
		require.NoError(t, reviewTick(t, h))
	}

	require.Len(t, h.prompts(), 4)
	assert.Equal(t, []string{prPrompt("changes requested"), prPrompt("changes requested")}, h.prompts()[2:])
	assert.Empty(t, h.linear.mutations())

	require.NoError(t, reviewTick(t, h))
	assert.Len(t, h.linear.mutations(), 2)
}

func TestTickKeepsThePRNudgeCountAcrossTicksWhereTheWorkerWasBusy(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	reviewFailsAfterEntry(h, prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	require.NoError(t, reviewTick(t, h))

	// A busy tick reads no PR, and the unchanged head must not reset the count.
	h.setWorkerStatus("ENG-1", "working")
	require.NoError(t, reviewTick(t, h))
	h.setWorkerStatus("ENG-1", "idle")
	require.NoError(t, reviewTick(t, h))
	require.NoError(t, reviewTick(t, h))

	assert.Len(t, h.linear.mutations(), 2)
	assert.Len(t, h.prompts(), 2)
}

func TestTickResetsThePRNudgeCountWhenAPushLandsWhileTheWorkerWasBusy(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	reviewFailsAfterEntry(h, prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	for range 2 {
		require.NoError(t, reviewTick(t, h))
	}

	h.setWorkerStatus("ENG-1", "working")
	require.NoError(t, reviewTick(t, h))
	h.pr("ENG-1", prState{head: "b2", checks: "FAILURE", mergeable: "MERGEABLE"})
	h.setWorkerStatus("ENG-1", "idle")
	require.NoError(t, reviewTick(t, h))

	assert.Len(t, h.prompts(), 3)
	assert.Empty(t, h.linear.mutations())
}

func TestTickRelaunchesAnInReviewWorkerThatIsGoneThenNudgesItOnceIdle(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "")
	h.pr("ENG-1", prState{head: "a1", checks: "SUCCESS", decision: "CHANGES_REQUESTED", mergeable: "MERGEABLE"})

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{h.repo + " ENG-1 thrawn01/eng-1 --model sonnet"}, h.launches())
	assert.Empty(t, h.prompts())
	assert.Empty(t, h.linear.mutations())

	// The relaunched worker is up and finishes its kickoff.
	h.setWorkerAgent("ENG-1", "idle")
	require.NoError(t, reviewTick(t, h))

	assert.Equal(t, []string{prPrompt("changes requested")}, h.prompts())
	assert.Len(t, h.launches(), 1)
}

func TestTickRelaunchesAnInReviewWorkerWhenTheWorkerCapIsFull(t *testing.T) {
	h := newHarness(t)
	h.config("[dispatch]\nmax_in_progress = 1\n")
	h.tickets(
		ticket("ENG-1", "In Progress", "dispatch"),
		ticket("ENG-2", "In Review", "dispatch"),
	)
	h.worker("ENG-1", "working")
	h.worker("ENG-2", "")
	h.pr("ENG-2", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{h.repo + " ENG-2 thrawn01/eng-2 --model sonnet"}, h.launches())
}

func TestTickEscalatesAnInReviewTicketWhoseRelaunchKeepsFailing(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	h.launchResult(`{"status":"pool_error","detail":"treehouse lease failed"}`)

	// A failed launch fails the tick, and the next tick tries again.
	_, err := h.run("tick")
	require.ErrorContains(t, err, "treehouse lease failed")
	require.ErrorContains(t, reviewTick(t, h), "treehouse lease failed")

	assert.Len(t, h.linear.mutations(), 2)
}

func TestTickSkipsATicketWhosePRCannotBeRead(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.tools.PullRequests.FailRead(errors.New("exit status 1"))

	_, err := h.run("tick")
	require.NoError(t, err)

	plan, err := h.run("plan")
	require.NoError(t, err)
	assert.Contains(t, plan, "held")
	assert.Contains(t, h.loopErr(), "while reading the PR for ENG-1")
	assert.Empty(t, h.prompts())
	assert.Empty(t, h.linear.mutations())
}

func TestTickEscalatesAnInReviewTicketWhoseRelaunchedWorkerNeverPushes(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "")
	reviewFailsAfterEntry(h, prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	require.NoError(t, reviewTick(t, h))
	require.Len(t, h.launches(), 1)

	// Each relaunched worker exits without pushing, so dispatch finds none again.
	reviewWorkerComesAndGoes(t, h)
	require.NoError(t, reviewTick(t, h))
	assert.Len(t, h.launches(), 2)

	reviewWorkerComesAndGoes(t, h)
	require.NoError(t, reviewTick(t, h))

	assert.Len(t, h.launches(), 2)
	mutations := h.linear.mutations()
	require.Len(t, mutations, 2)
	assert.Contains(t, mutations[1].Body, "its worker was relaunched 2 times for "+prURL1)
}

func TestTickNeverCountsAFullPoolAsAnInReviewRelaunch(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "")
	reviewFailsAfterEntry(h, prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	h.launchResult(`{"status":"pool_exhausted","detail":"all 4/4 pool trees are taken"}`)

	// A full pool is tried again only once a tree comes back to it.
	for range 3 {
		require.NoError(t, reviewTick(t, h))
		reviewTreeComesBack(t, h)
	}
	require.NoError(t, reviewTick(t, h))

	assert.Len(t, h.launches(), 4)
	assert.Empty(t, h.linear.mutations())
}

func TestTickNotifiesOnceWhenAPRNudgeEscalationReachesNeedsHuman(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	reviewFailsAfterEntry(h, prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	for range 3 {
		require.NoError(t, reviewTick(t, h))
	}

	h.tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	for range 3 {
		require.NoError(t, reviewTick(t, h))
	}

	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1"}, h.notifications())
}

func TestTickReportsAPRWithMoreReviewThreadsThanItReads(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE", moreThreads: true})

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, h.loopErr(), "more than 100 review threads")
	assert.Empty(t, h.prompts())
}

func TestTickIgnoresInReviewParentTickets(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Backlog"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.prReads())
}

func TestTickGivesARelaunchedWorkerTwoPRNudgesBeforeEscalating(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	h.restart()
	require.Len(t, h.launches(), 1)
	h.setWorkerAgent("ENG-1", "idle")

	for range 2 {
		require.NoError(t, reviewTick(t, h))
	}
	assert.Len(t, h.prompts(), 2)
	assert.Empty(t, h.linear.mutations())

	require.NoError(t, reviewTick(t, h))

	assert.Len(t, h.prompts(), 2)
	require.Len(t, h.linear.mutations(), 2)
	assert.Contains(t, h.linear.mutations()[1].Body, "stayed idle through 2 nudges")
}

func TestTickHoldsBackOnlyTheTicketWhosePRReadFailed(t *testing.T) {
	h := newHarness(t)
	h.config("")
	h.tickets(ticket("ENG-1", "In Review", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.tools.PullRequests.FailRead(errors.New("context deadline exceeded"))

	_, err := h.run("tick")
	require.NoError(t, err)

	plan, err := h.run("plan")
	require.NoError(t, err)
	assert.Contains(t, plan, "held")
	assert.Contains(t, h.loopErr(), "while reading the PR for ENG-1")
	assert.Len(t, h.launches(), 1)
}

func TestTickReportsWhatGHSaidWhenItFails(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.tools.PullRequests.FailRead(errors.New("HTTP 401: Bad credentials"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, h.loopErr(), "while reading the PR for ENG-1: HTTP 401: Bad credentials")
}

func TestTickIgnoresChangesRequestedOnACommitThatIsNoLongerTheHead(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "b2", checks: "SUCCESS", decision: "CHANGES_REQUESTED", changesOn: []string{"a1"}, mergeable: "MERGEABLE"})

	for range 4 {
		require.NoError(t, reviewTick(t, h))
	}

	assert.Empty(t, h.prompts())
	assert.Empty(t, h.linear.mutations())
}

func TestTickDoesNotNudgeOverChangesRequestedWhileACheckOnTheHeadRuns(t *testing.T) {
	for _, checks := range []string{"PENDING", "EXPECTED"} {
		t.Run(checks, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", "idle")
			h.pr("ENG-1", prState{head: "b2", checks: checks, decision: "CHANGES_REQUESTED", mergeable: "MERGEABLE"})

			for range 4 {
				require.NoError(t, reviewTick(t, h))
			}

			assert.Empty(t, h.prompts())
			assert.Empty(t, h.linear.mutations())
		})
	}
}

func TestTickNudgesOverChangesRequestedOnTheHeadOnceChecksFinish(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "b2", checks: "PENDING", decision: "CHANGES_REQUESTED", mergeable: "MERGEABLE"})
	_, err := h.run("tick")
	require.NoError(t, err)
	assert.Empty(t, h.prompts())

	h.pr("ENG-1", prState{head: "b2", checks: "SUCCESS", decision: "CHANGES_REQUESTED", mergeable: "MERGEABLE"})
	require.NoError(t, reviewTick(t, h))

	assert.Equal(t, []string{prPrompt("changes requested")}, h.prompts())
}

func TestTickStillNudgesForAFailedCheckWhileChangesRequestedIsStale(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "b2", checks: "FAILURE", decision: "CHANGES_REQUESTED", changesOn: []string{"a1"}, mergeable: "MERGEABLE"})

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{prPrompt("a failed check")}, h.prompts())
}

func TestTickNudgesWhenOneReviewerIsStaleAndAnotherRequestsChangesOnTheHead(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "b2", checks: "SUCCESS", changesOn: []string{"a1", "b2"}, mergeable: "MERGEABLE"})

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{prPrompt("changes requested")}, h.prompts())
}
