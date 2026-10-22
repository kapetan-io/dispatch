package dispatch_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/treehouse"
)

// Agent starts a Claude agent in a workspace made with an empty status.
func (w *Workers) Agent(ticket, status string) {
	id := "w-" + ticket
	w.d.tools.Herdr.AddPane(herdr.Pane{ID: id + ":p1", WorkspaceID: id})
	w.d.tools.Herdr.AddAgent(herdr.Agent{Kind: "claude", Status: status, Session: "worker", PaneID: id + ":p1", WorkspaceID: id})
}

// Reads lists the branches dispatch read a PR for, with ".<cursor>" after a page past the first.
func (g *GitHub) Reads() []string { return g.d.prReads() }

// CheckReads lists the times dispatch asked GitHub for the checks behind a PR's rollup.
func (g *GitHub) CheckReads() []string { return g.d.tools.Calls("pullrequest", "checks") }

// RespondChecks sets the checks GitHub reports for PR 7 at head, and whether more pages follow.
func (g *GitHub) RespondChecks(head string, contexts []checkContext, more bool) {
	g.d.respondChecks(head, contexts, more)
}

// escalated requires that dispatch moved ENG-1 to Needs Human and said why in one comment.
func escalated(t *testing.T, d *Dispatch) {
	t.Helper()
	assert.Equal(t, []string{"ENG-1 -> Needs Human"}, d.Linear.Moves())
	assert.Len(t, d.Linear.Comments(), 1)
}

// workerComesAndGoes shows dispatch a relaunched worker that then exits, which starts a new
// stretch with no worker.
func workerComesAndGoes(t *testing.T, d *Dispatch) {
	d.Workers.Running("ENG-1", "working")
	require.NoError(t, d.TickAfterASecond())
	d.Workers.Gone("ENG-1")
}

// treeComesBack returns ENG-1's tree to the pool and takes it again.
func treeComesBack(t *testing.T, d *Dispatch) {
	d.Trees.EmptyPool()
	require.NoError(t, d.TickAfterASecond())
	d.Trees.Lease("ENG-1")
}

// EmptyPool has every tree of the dispatch repo go back to the pool.
func (t *Trees) EmptyPool() { t.d.tools.Treehouse.Clear(t.d.Repo) }

// Lease has the ticket take its tree from the pool.
func (t *Trees) Lease(ticket string) {
	t.d.tools.Treehouse.Seed(t.d.Repo, treehouse.Tree{Path: filepath.Join(t.d.dir, "trees", ticket), Status: "leased", LeaseHolder: ticket})
}

func TestTickNudgesAnIdleInReviewWorkerNamingTheProblem(t *testing.T) {
	t.Parallel()
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
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			d.GitHub.PullRequest("ENG-1", test.pr)

			d.Tick()

			assert.Equal(t, []string{prPrompt(test.problem)}, d.Workers.Prompts())
			noLinearChanges(t, d)
			assert.Empty(t, d.Workers.Launched())
		})
	}
}

func TestTickLeavesAnInReviewTicketAloneWhenThePRNeedsNothing(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		setup func(d *Dispatch)
	}{
		{name: "CleanPRNoWorker", setup: func(d *Dispatch) { d.Workers.Running("ENG-1", ""); d.GitHub.PullRequest("ENG-1", cleanPR("a1")) }},
		{name: "PendingChecksResolvedThreads", setup: func(d *Dispatch) {
			d.Workers.Running("ENG-1", "idle")
			d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "PENDING", mergeable: "UNKNOWN", resolved: 3})
		}},
		{name: "NoOpenPR", setup: func(d *Dispatch) { d.Workers.Running("ENG-1", "idle") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			test.setup(d)

			d.Tick()

			assert.Empty(t, d.Workers.Prompts())
			assert.Empty(t, d.Workers.Launched())
			noLinearChanges(t, d)
		})
	}
}

func TestTickDoesNotReadThePRWhileTheInReviewWorkerIsBusy(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"working", "unknown"} {
		t.Run(status, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", status)
			d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

			d.Tick()

			assert.Empty(t, d.Workers.Prompts())
			assert.Empty(t, d.GitHub.Reads())
		})
	}
}

func TestTickOnlyNotifiesForABlockedInReviewWorker(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "blocked")
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	d.Tick()

	assert.Len(t, d.Alerts.Shown(), 1)
	assert.Empty(t, d.Workers.Prompts())
	assert.Empty(t, d.GitHub.Reads())
}

func TestTickNeverNudgesDesignTicketsAboutPRs(t *testing.T) {
	t.Parallel()
	for _, label := range []string{"design", "design-review"} {
		t.Run(label, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(withLabel(ticket("ENG-1", "In Review", "dispatch"), label))
			d.Workers.Running("ENG-1", "idle")
			d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

			d.Tick()

			assert.Empty(t, d.Workers.Prompts())
			assert.Empty(t, d.GitHub.Reads())
		})
	}
}

func TestTickReadsOneInReviewPRPerTicketPerTick(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(
		ticket("ENG-1", "In Review", "dispatch"),
		ticket("ENG-2", "In Review", "dispatch"),
	)
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Running("ENG-2", "idle")

	d.Tick()
	assert.Equal(t, []string{"thrawn01/eng-1", "thrawn01/eng-2"}, d.GitHub.Reads())

	// Each tick after the loop's own first read takes one page per ticket.
	d.Tick()
	assert.Equal(t, []string{"thrawn01/eng-1", "thrawn01/eng-2", "thrawn01/eng-1", "thrawn01/eng-2"}, d.GitHub.Reads())
}

func TestTickEscalatesAnInReviewTicketAfterTwoPRNudgesWithNoPush(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.PullRequestFailsAfterEntry(prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	for range 2 {
		require.NoError(t, d.TickAfterASecond())
	}
	assert.Equal(t, []string{prPrompt("a failed check"), prPrompt("a failed check")}, d.Workers.Prompts())
	noLinearChanges(t, d)

	require.NoError(t, d.TickAfterASecond())

	assert.Len(t, d.Workers.Prompts(), 2)
	escalated(t, d)
	assert.Contains(t, d.Linear.Comments()[0], "stayed idle through 2 nudges about "+prURL1)

	// The ticket now reads Needs Human, so the dispatcher stays quiet.
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	for range 2 {
		require.NoError(t, d.TickAfterASecond())
	}
	escalated(t, d)
	assert.Len(t, d.Workers.Prompts(), 2)
}

func TestTickResetsThePRNudgeCountWhenTheHeadMoves(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.PullRequestFailsAfterEntry(prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	for range 2 {
		require.NoError(t, d.TickAfterASecond())
	}

	// The worker pushed a fix, and a review that arrives later is a new problem.
	d.GitHub.PullRequest("ENG-1", prState{head: "b2", checks: "SUCCESS", decision: "CHANGES_REQUESTED", mergeable: "MERGEABLE"})
	for range 2 {
		require.NoError(t, d.TickAfterASecond())
	}

	require.Len(t, d.Workers.Prompts(), 4)
	assert.Equal(t, []string{prPrompt("changes requested"), prPrompt("changes requested")}, d.Workers.Prompts()[2:])
	noLinearChanges(t, d)

	require.NoError(t, d.TickAfterASecond())
	escalated(t, d)
}

func TestTickKeepsThePRNudgeCountAcrossTicksWhereTheWorkerWasBusy(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.PullRequestFailsAfterEntry(prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	require.NoError(t, d.TickAfterASecond())

	// A busy tick reads no PR, and the unchanged head must not reset the count.
	d.Workers.SetStatus("ENG-1", "working")
	require.NoError(t, d.TickAfterASecond())
	d.Workers.SetStatus("ENG-1", "idle")
	require.NoError(t, d.TickAfterASecond())
	require.NoError(t, d.TickAfterASecond())

	escalated(t, d)
	assert.Len(t, d.Workers.Prompts(), 2)
}

func TestTickResetsThePRNudgeCountWhenAPushLandsWhileTheWorkerWasBusy(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.PullRequestFailsAfterEntry(prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	for range 2 {
		require.NoError(t, d.TickAfterASecond())
	}

	d.Workers.SetStatus("ENG-1", "working")
	require.NoError(t, d.TickAfterASecond())
	d.GitHub.PullRequest("ENG-1", prState{head: "b2", checks: "FAILURE", mergeable: "MERGEABLE"})
	d.Workers.SetStatus("ENG-1", "idle")
	require.NoError(t, d.TickAfterASecond())

	assert.Len(t, d.Workers.Prompts(), 3)
	noLinearChanges(t, d)
}

func TestTickRelaunchesAnInReviewWorkerThatIsGoneThenNudgesItOnceIdle(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "")
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "SUCCESS", decision: "CHANGES_REQUESTED", mergeable: "MERGEABLE"})

	d.Tick()

	assert.Equal(t, []string{d.Repo + " ENG-1 thrawn01/eng-1 --model sonnet"}, d.Workers.Launched())
	assert.Empty(t, d.Workers.Prompts())
	noLinearChanges(t, d)

	// The relaunched worker is up and finishes its kickoff.
	d.Workers.Agent("ENG-1", "idle")
	require.NoError(t, d.TickAfterASecond())

	assert.Equal(t, []string{prPrompt("changes requested")}, d.Workers.Prompts())
	assert.Len(t, d.Workers.Launched(), 1)
}

func TestTickRelaunchesAnInReviewWorkerWhenTheWorkerCapIsFull(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 1\n"})
	d.Linear.Tickets(
		ticket("ENG-1", "In Progress", "dispatch"),
		ticket("ENG-2", "In Review", "dispatch"),
	)
	d.Workers.Running("ENG-1", "working")
	d.Workers.Running("ENG-2", "")
	d.GitHub.PullRequest("ENG-2", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	d.Tick()

	assert.Equal(t, []string{d.Repo + " ENG-2 thrawn01/eng-2 --model sonnet"}, d.Workers.Launched())
}

func TestTickEscalatesAnInReviewTicketWhoseRelaunchKeepsFailing(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "")
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	d.Workers.LaunchResult(`{"status":"pool_error","detail":"treehouse lease failed"}`)

	// A failed launch fails the tick, and the next tick tries again.
	require.ErrorContains(t, d.Run("tick").Err, "treehouse lease failed")
	require.ErrorContains(t, d.TickAfterASecond(), "treehouse lease failed")

	escalated(t, d)
}

func TestTickSkipsATicketWhosePRCannotBeRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.FailRead(errors.New("exit status 1"))

	d.Tick()

	assert.Contains(t, d.Run("plan").Stdout, "held")
	assert.Contains(t, d.LoopStderr(), "while reading the PR for ENG-1")
	assert.Empty(t, d.Workers.Prompts())
	noLinearChanges(t, d)
}

func TestTickEscalatesAnInReviewTicketWhoseRelaunchedWorkerNeverPushes(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "")
	d.PullRequestFailsAfterEntry(prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	require.NoError(t, d.TickAfterASecond())
	require.Len(t, d.Workers.Launched(), 1)

	// Each relaunched worker exits without pushing, so dispatch finds none again.
	workerComesAndGoes(t, d)
	require.NoError(t, d.TickAfterASecond())
	assert.Len(t, d.Workers.Launched(), 2)

	workerComesAndGoes(t, d)
	require.NoError(t, d.TickAfterASecond())

	assert.Len(t, d.Workers.Launched(), 2)
	escalated(t, d)
	assert.Contains(t, d.Linear.Comments()[0], "its worker was relaunched 2 times for "+prURL1)
}

func TestTickNeverCountsAFullPoolAsAnInReviewRelaunch(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "")
	d.PullRequestFailsAfterEntry(prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	d.Workers.LaunchResult(`{"status":"pool_exhausted","detail":"all 4/4 pool trees are taken"}`)

	// A full pool is tried again only once a tree comes back to it.
	for range 3 {
		require.NoError(t, d.TickAfterASecond())
		treeComesBack(t, d)
	}
	require.NoError(t, d.TickAfterASecond())

	assert.Len(t, d.Workers.Launched(), 4)
	noLinearChanges(t, d)
}

func TestTickNotifiesOnceWhenAPRNudgeEscalationReachesNeedsHuman(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.PullRequestFailsAfterEntry(prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	for range 3 {
		require.NoError(t, d.TickAfterASecond())
	}

	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	for range 3 {
		require.NoError(t, d.TickAfterASecond())
	}

	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1"}, d.Alerts.Shown())
}

func TestTickReportsAPRWithMoreReviewThreadsThanItReads(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE", moreThreads: true})

	d.Tick()

	assert.Contains(t, d.LoopStderr(), "more than 100 review threads")
	assert.Empty(t, d.Workers.Prompts())
}

func TestTickIgnoresInReviewParentTickets(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Linear.Children(child("ENG-2", "ENG-1", "Backlog"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	d.Tick()

	assert.Empty(t, d.GitHub.Reads())
}

func TestTickGivesARelaunchedWorkerTwoPRNudgesBeforeEscalating(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "")
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	d.Restart()
	require.Len(t, d.Workers.Launched(), 1)
	d.Workers.Agent("ENG-1", "idle")

	for range 2 {
		require.NoError(t, d.TickAfterASecond())
	}
	assert.Len(t, d.Workers.Prompts(), 2)
	noLinearChanges(t, d)

	require.NoError(t, d.TickAfterASecond())

	assert.Len(t, d.Workers.Prompts(), 2)
	escalated(t, d)
	assert.Contains(t, d.Linear.Comments()[0], "stayed idle through 2 nudges")
}

func TestTickHoldsBackOnlyTheTicketWhosePRReadFailed(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.FailRead(errors.New("context deadline exceeded"))

	d.Tick()

	assert.Contains(t, d.Run("plan").Stdout, "held")
	assert.Contains(t, d.LoopStderr(), "while reading the PR for ENG-1")
	assert.Len(t, d.Workers.Launched(), 1)
}

func TestTickReportsWhatGHSaidWhenItFails(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.FailRead(errors.New("HTTP 401: Bad credentials"))

	d.Tick()

	assert.Contains(t, d.LoopStderr(), "while reading the PR for ENG-1: HTTP 401: Bad credentials")
}

func TestTickIgnoresChangesRequestedOnACommitThatIsNoLongerTheHead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", prState{head: "b2", checks: "SUCCESS", decision: "CHANGES_REQUESTED", changesOn: []string{"a1"}, mergeable: "MERGEABLE"})

	for range 4 {
		require.NoError(t, d.TickAfterASecond())
	}

	assert.Empty(t, d.Workers.Prompts())
	noLinearChanges(t, d)
}

func TestTickDoesNotNudgeOverChangesRequestedWhileACheckOnTheHeadRuns(t *testing.T) {
	t.Parallel()
	for _, checks := range []string{"PENDING", "EXPECTED"} {
		t.Run(checks, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			d.GitHub.PullRequest("ENG-1", prState{head: "b2", checks: checks, decision: "CHANGES_REQUESTED", mergeable: "MERGEABLE"})

			for range 4 {
				require.NoError(t, d.TickAfterASecond())
			}

			assert.Empty(t, d.Workers.Prompts())
			noLinearChanges(t, d)
		})
	}
}

func TestTickNudgesOverChangesRequestedOnTheHeadOnceChecksFinish(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", prState{head: "b2", checks: "PENDING", decision: "CHANGES_REQUESTED", mergeable: "MERGEABLE"})
	d.Tick()
	assert.Empty(t, d.Workers.Prompts())

	d.GitHub.PullRequest("ENG-1", prState{head: "b2", checks: "SUCCESS", decision: "CHANGES_REQUESTED", mergeable: "MERGEABLE"})
	require.NoError(t, d.TickAfterASecond())

	assert.Equal(t, []string{prPrompt("changes requested")}, d.Workers.Prompts())
}

func TestTickStillNudgesForAFailedCheckWhileChangesRequestedIsStale(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", prState{head: "b2", checks: "FAILURE", decision: "CHANGES_REQUESTED", changesOn: []string{"a1"}, mergeable: "MERGEABLE"})

	d.Tick()

	assert.Equal(t, []string{prPrompt("a failed check")}, d.Workers.Prompts())
}

func TestTickNudgesWhenOneReviewerIsStaleAndAnotherRequestsChangesOnTheHead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", prState{head: "b2", checks: "SUCCESS", changesOn: []string{"a1", "b2"}, mergeable: "MERGEABLE"})

	d.Tick()

	assert.Equal(t, []string{prPrompt("changes requested")}, d.Workers.Prompts())
}

// tigerRepo is a second repo the config lists, with the given settings.
func tigerRepo(t *testing.T, settings string) string {
	return fmt.Sprintf("[repos.tiger]\npath = %q\n%s", filepath.Join(t.TempDir(), "tiger"), settings)
}

func requiredOnlyDispatch(t *testing.T, p prState) *Dispatch {
	d := newDispatch(t, dispatchOptions{RepoSettings: requiredOnly})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", p)
	return d
}

func TestTickIgnoresAnOptionalCheckThatFails(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		checks   string
		contexts []checkContext
	}{
		{name: "OptionalFailed", checks: "FAILURE", contexts: []checkContext{{"test", "SUCCESS", true}, {"CodeRabbit", "FAILURE", false}}},
		{name: "OptionalNeverFinishes", checks: "PENDING", contexts: []checkContext{{"test", "SUCCESS", true}, {"lint", "IN_PROGRESS", false}}},
		{name: "OptionalErrored", checks: "ERROR", contexts: []checkContext{{"test", "SUCCESS", true}, {"deploy", "FAILURE", false}}},
		{name: "NoRequiredChecks", checks: "FAILURE", contexts: []checkContext{{"lint", "FAILURE", false}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := requiredOnlyDispatch(t, prState{head: "a1", checks: test.checks, decision: "APPROVED", mergeable: "MERGEABLE", contexts: test.contexts})

			d.Tick()

			assert.Equal(t, []string{readyText1}, d.Alerts.Shown())
			assert.Empty(t, d.Workers.Prompts())
		})
	}
}

func TestTickCountsARequiredCheckThatFails(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		checks   string
		contexts []checkContext
	}{
		{name: "RequiredFailed", checks: "FAILURE", contexts: []checkContext{{"test", "FAILURE", true}, {"CodeRabbit", "FAILURE", false}}},
		{name: "RequiredCancelled", checks: "FAILURE", contexts: []checkContext{{"test", "CANCELLED", true}}},
		{name: "RequiredTimedOut", checks: "FAILURE", contexts: []checkContext{{"test", "TIMED_OUT", true}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := requiredOnlyDispatch(t, prState{head: "a1", checks: test.checks, mergeable: "MERGEABLE", contexts: test.contexts})

			d.Tick()

			require.Len(t, d.Workers.Prompts(), 1)
			assert.Contains(t, d.Workers.Prompts()[0], "has a failed check")
		})
	}
}

func TestTickWaitsOnARequiredCheckThatHasNotFinished(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		checks   string
		contexts []checkContext
	}{
		{name: "Running", checks: "PENDING", contexts: []checkContext{{"test", "IN_PROGRESS", true}}},
		{name: "RunningBesideAFailedOptional", checks: "FAILURE", contexts: []checkContext{{"test", "IN_PROGRESS", true}, {"CodeRabbit", "FAILURE", false}}},
		{name: "NotStarted", checks: "PENDING", contexts: []checkContext{{"test", "EXPECTED", true}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := requiredOnlyDispatch(t, prState{head: "a1", checks: test.checks, decision: "APPROVED", mergeable: "MERGEABLE", contexts: test.contexts})

			d.Tick()

			assert.Empty(t, d.Alerts.Shown())
			assert.Empty(t, d.Workers.Prompts())
		})
	}
}

func TestTickLeavesAnExpectedRollupNotReadyWithoutAskingForChecks(t *testing.T) {
	t.Parallel()
	d := requiredOnlyDispatch(t, prState{head: "a1", checks: "EXPECTED", decision: "APPROVED", mergeable: "MERGEABLE"})

	d.Tick()

	assert.Empty(t, d.Alerts.Shown())
	assert.Empty(t, d.Workers.Prompts())
	assert.Empty(t, d.GitHub.CheckReads())
}

func TestTickNudgesOverChangesRequestedWhileOnlyAnOptionalCheckRuns(t *testing.T) {
	t.Parallel()
	d := requiredOnlyDispatch(t, prState{head: "b2", checks: "PENDING", decision: "CHANGES_REQUESTED", mergeable: "MERGEABLE",
		contexts: []checkContext{{"test", "SUCCESS", true}, {"CodeRabbit", "IN_PROGRESS", false}}})

	d.Tick()

	require.Len(t, d.Workers.Prompts(), 1)
	assert.Contains(t, d.Workers.Prompts()[0], "has changes requested")
}

func TestTickMutesChangesRequestedWhileARequiredCheckRuns(t *testing.T) {
	t.Parallel()
	d := requiredOnlyDispatch(t, prState{head: "b2", checks: "PENDING", decision: "CHANGES_REQUESTED", mergeable: "MERGEABLE",
		contexts: []checkContext{{"test", "IN_PROGRESS", true}}})

	d.Tick()

	assert.Empty(t, d.Workers.Prompts())
}

func TestTickErrorsWhenChecksNeedASecondPage(t *testing.T) {
	t.Parallel()
	d := requiredOnlyDispatch(t, prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	d.GitHub.RespondChecks("a1", []checkContext{{"test", "SUCCESS", true}}, true)

	d.Tick()

	assert.Contains(t, d.LoopStderr(), "while reading the PR for ENG-1: PR 7 has more than 100 checks")
	assert.Empty(t, d.Workers.Prompts())
}

func TestTickCountsEveryCheckInARepoWithoutTheSetting(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	d.Tick()

	require.Len(t, d.Workers.Prompts(), 1)
	assert.Contains(t, d.Workers.Prompts()[0], "has a failed check")
	assert.Empty(t, d.GitHub.CheckReads())
}

func TestTickDoesNotAskForChecksWhenTheRollupPassed(t *testing.T) {
	t.Parallel()
	d := requiredOnlyDispatch(t, cleanPR("a1"))

	d.Tick()

	assert.Equal(t, []string{readyText1}, d.Alerts.Shown())
	assert.Empty(t, d.GitHub.CheckReads())
}

func TestTickWaitsWhenAPushLandsBetweenReadingThePRAndItsChecks(t *testing.T) {
	t.Parallel()
	d := requiredOnlyDispatch(t, prState{head: "a1", checks: "FAILURE", decision: "APPROVED", mergeable: "MERGEABLE"})
	d.GitHub.RespondChecks("b2", []checkContext{{"test", "SUCCESS", true}}, false)

	d.Tick()

	assert.Empty(t, d.Alerts.Shown())
	assert.Empty(t, d.Workers.Prompts())
}

func TestTickIgnoresAFailedRollupThatListsNoChecks(t *testing.T) {
	t.Parallel()
	d := requiredOnlyDispatch(t, prState{head: "a1", checks: "FAILURE", decision: "APPROVED", mergeable: "MERGEABLE", contexts: []checkContext{}})

	d.Tick()

	assert.Equal(t, []string{readyText1}, d.Alerts.Shown())
	assert.Empty(t, d.Workers.Prompts())
}

func TestTickReadsStatusContextsByWhetherTheyAreRequired(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		required bool
		nudged   bool
	}{
		{name: "RequiredErrored", required: true, nudged: true},
		{name: "OptionalErrored", required: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := requiredOnlyDispatch(t, prState{head: "a1", checks: "ERROR", decision: "APPROVED", mergeable: "MERGEABLE"})
			d.GitHub.RespondChecks("a1", []checkContext{{"ci/legacy", "ERROR", test.required}}, false)

			d.Tick()

			if test.nudged {
				require.Len(t, d.Workers.Prompts(), 1)
				assert.Contains(t, d.Workers.Prompts()[0], "has a failed check")
				assert.Empty(t, d.Alerts.Shown())
				return
			}
			assert.Empty(t, d.Workers.Prompts())
			assert.Equal(t, []string{readyText1}, d.Alerts.Shown())
		})
	}
}

func TestTickCountsEveryCheckWhenOnlyAnotherRepoIgnoresOptionalOnes(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: tigerRepo(t, requiredOnly)})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	d.Tick()

	require.Len(t, d.Workers.Prompts(), 1)
	assert.Contains(t, d.Workers.Prompts()[0], "has a failed check")
	assert.Empty(t, d.GitHub.CheckReads())
}

const approvalGate = "approval_checks = [\"Mergify Merge Protections\"]\n"

func approvalDispatch(t *testing.T, settings string, p prState) *Dispatch {
	d := newDispatch(t, dispatchOptions{RepoSettings: settings})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", p)
	return d
}

func TestTickIgnoresAnApprovalCheckThatHasNotPassed(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		settings string
		checks   string
		contexts []checkContext
	}{
		{name: "Failed", settings: approvalGate, checks: "FAILURE", contexts: []checkContext{{"Mergify Merge Protections", "FAILURE", true}, {"premerge", "SUCCESS", true}}},
		{name: "NameInOtherCase", settings: "approval_checks = [\"mergify merge protections\"]\n", checks: "FAILURE", contexts: []checkContext{{"Mergify Merge Protections", "FAILURE", true}, {"premerge", "SUCCESS", true}}},
		{name: "NeverFinishes", settings: approvalGate, checks: "PENDING", contexts: []checkContext{{"Mergify Merge Protections", "IN_PROGRESS", true}, {"premerge", "SUCCESS", true}}},
		{name: "NotStarted", settings: approvalGate, checks: "EXPECTED", contexts: []checkContext{{"Mergify Merge Protections", "EXPECTED", true}, {"premerge", "SUCCESS", true}}},
		{name: "BesideRequiredOnly", settings: approvalGate + requiredOnly, checks: "FAILURE", contexts: []checkContext{{"Mergify Merge Protections", "FAILURE", true}, {"premerge", "SUCCESS", true}, {"CodeRabbit", "FAILURE", false}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := approvalDispatch(t, test.settings, prState{head: "a1", checks: test.checks, decision: "APPROVED", mergeable: "MERGEABLE", contexts: test.contexts})

			d.Tick()

			assert.Equal(t, []string{readyText1}, d.Alerts.Shown())
			assert.Empty(t, d.Workers.Prompts())
		})
	}
}

func TestTickCountsABuildCheckThatFailsBesideAnApprovalCheck(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		settings string
	}{
		{name: "AllChecksCount", settings: approvalGate},
		{name: "RequiredOnly", settings: approvalGate + requiredOnly},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := approvalDispatch(t, test.settings, prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE",
				contexts: []checkContext{{"Mergify Merge Protections", "FAILURE", true}, {"premerge", "FAILURE", true}}})

			d.Tick()

			require.Len(t, d.Workers.Prompts(), 1)
			assert.Contains(t, d.Workers.Prompts()[0], "has a failed check")
			assert.Empty(t, d.Alerts.Shown())
		})
	}
}

func TestTickWaitsOnABuildCheckThatRunsBesideAnApprovalCheck(t *testing.T) {
	t.Parallel()
	d := approvalDispatch(t, approvalGate, prState{head: "a1", checks: "FAILURE", decision: "APPROVED", mergeable: "MERGEABLE",
		contexts: []checkContext{{"Mergify Merge Protections", "FAILURE", true}, {"premerge", "IN_PROGRESS", true}}})

	d.Tick()

	assert.Empty(t, d.Alerts.Shown())
	assert.Empty(t, d.Workers.Prompts())
}

func TestTickCountsAnApprovalCheckInARepoThatDoesNotListIt(t *testing.T) {
	t.Parallel()
	d := approvalDispatch(t, "", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE",
		contexts: []checkContext{{"Mergify Merge Protections", "FAILURE", true}, {"premerge", "SUCCESS", true}}})

	d.Tick()

	require.Len(t, d.Workers.Prompts(), 1)
	assert.Contains(t, d.Workers.Prompts()[0], "has a failed check")
}

func TestTickDoesNotAutoMergeWhileAnApprovalCheckHasNotPassed(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		checks   string
		contexts []checkContext
	}{
		{name: "FailedAndOptional", checks: "FAILURE", contexts: []checkContext{{"Mergify Merge Protections", "FAILURE", false}, {"premerge", "SUCCESS", true}}},
		{name: "FailedAndRequired", checks: "FAILURE", contexts: []checkContext{{"Mergify Merge Protections", "FAILURE", true}, {"premerge", "SUCCESS", true}}},
		{name: "NotStarted", checks: "EXPECTED", contexts: []checkContext{{"Mergify Merge Protections", "EXPECTED", true}, {"premerge", "SUCCESS", true}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			pr := prState{head: "a1", checks: test.checks, mergeable: "MERGEABLE", approved: []string{"a1"}, contexts: test.contexts}
			d := approvalDispatch(t, approvalGate+autoMergeSetting, pr)

			d.Tick()

			assert.Empty(t, d.GitHub.Merged())
		})
	}
}

func TestTickAutoMergesOnceTheApprovalCheckPasses(t *testing.T) {
	t.Parallel()
	pr := prState{head: "a1", checks: "SUCCESS", mergeable: "MERGEABLE", approved: []string{"a1"},
		contexts: []checkContext{{"Mergify Merge Protections", "SUCCESS", true}, {"premerge", "SUCCESS", true}}}
	d := approvalDispatch(t, approvalGate+autoMergeSetting, pr)

	d.Tick()

	assert.Equal(t, []string{mergeCall1}, d.GitHub.Merged())
}

func draftDispatch(t *testing.T) *Dispatch {
	d := newDispatch(t, dispatchOptions{RepoSettings: draftSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	return d
}

func draftPR(head string) prState {
	pr := cleanPR(head)
	pr.draft = true
	return pr
}

func TestTickNotifiesOncePerHeadWhenADraftIsReady(t *testing.T) {
	t.Parallel()
	d := draftDispatch(t)
	d.GitHub.PullRequest("ENG-1", draftPR("a1"))

	d.Tick()

	assert.Equal(t, []string{draftReadyText1}, d.Alerts.Shown())

	d.Tick()
	d.Tick()
	assert.Len(t, d.Alerts.Shown(), 1)

	d.GitHub.PullRequest("ENG-1", draftPR("a2"))
	d.Tick()
	assert.Equal(t, []string{draftReadyText1, draftReadyText1}, d.Alerts.Shown())
}

func TestTickNotifiesAgainWhenTheUserOpensAReadyDraft(t *testing.T) {
	t.Parallel()
	d := draftDispatch(t)
	d.GitHub.PullRequest("ENG-1", draftPR("a1"))
	d.Tick()

	// Opening the draft leaves the head where it was.
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	d.Tick()

	assert.Equal(t, []string{draftReadyText1, reviewsHandled1}, d.Alerts.Shown())

	d.Tick()
	assert.Len(t, d.Alerts.Shown(), 2)

	// Moving the PR back to draft at the same head says nothing more.
	d.GitHub.PullRequest("ENG-1", draftPR("a1"))
	d.Tick()
	assert.Len(t, d.Alerts.Shown(), 2)
}

func TestTickStaysSilentForADraftThatIsNotReady(t *testing.T) {
	t.Parallel()
	d := draftDispatch(t)
	pr := draftPR("a1")
	pr.unresolved = 1
	d.GitHub.PullRequest("ENG-1", pr)

	d.Tick()

	assert.Empty(t, d.Alerts.Shown())
}

func TestTickKeepsTheDraftNotificationAcrossALeasedTicketsAbsence(t *testing.T) {
	t.Parallel()
	d := draftDispatch(t)
	d.GitHub.PullRequest("ENG-1", draftPR("a1"))
	d.Tick()
	require.Len(t, d.Alerts.Shown(), 1)

	// Out of the Linear read while its worker tree is leased.
	d.Linear.Tickets()
	d.Tick()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Tick()

	assert.Len(t, d.Alerts.Shown(), 1)
}

func TestTickNeverMergesADraft(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		settings string
		text     string
	}{
		{name: "OpenRepo", settings: autoMergeSetting, text: readyText1},
		{name: "DraftRepo", settings: autoMergeSetting + draftSetting, text: draftReadyText1},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{RepoSettings: test.settings})
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			pr := approvedPR("a1")
			pr.draft = true
			d.GitHub.PullRequest("ENG-1", pr)

			d.Tick()
			d.Advance(5 * time.Minute)
			d.Tick()

			assert.Empty(t, d.GitHub.Merged())
			assert.Equal(t, []string{test.text}, d.Alerts.Shown())
			for _, receipt := range d.Receipts("ENG-1") {
				assert.NotEqual(t, "merge", receipt.Kind)
			}
		})
	}
}

func TestTickKeepsTodaysNotificationInAnOpenRepoForADraft(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", draftPR("a1"))
	d.Tick()

	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	d.Tick()

	assert.Equal(t, []string{readyText1}, d.Alerts.Shown())
}

func TestTickMergesAnApprovedOpenPRInADraftRepoWithAutoMerge(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting + draftSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", approvedPR("a1"))

	d.Tick()

	assert.Equal(t, []string{mergeCall1}, d.GitHub.Merged())
}

func TestTickNotifiesReviewsHandledOncePerHeadForAnOpenPRInADraftRepo(t *testing.T) {
	t.Parallel()
	d := draftDispatch(t)
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))

	d.Tick()

	assert.Equal(t, []string{reviewsHandled1}, d.Alerts.Shown())

	d.Tick()
	assert.Len(t, d.Alerts.Shown(), 1)

	d.GitHub.PullRequest("ENG-1", cleanPR("a2"))
	d.Tick()
	assert.Equal(t, []string{reviewsHandled1, reviewsHandled1}, d.Alerts.Shown())
}

func TestTickMergesADraftOnceTheUserOpensItAtTheSameHead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting + draftSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	pr := approvedPR("a1")
	pr.draft = true
	d.GitHub.PullRequest("ENG-1", pr)
	d.Tick()
	require.Empty(t, d.GitHub.Merged())

	d.GitHub.PullRequest("ENG-1", approvedPR("a1"))
	d.Tick()

	assert.Equal(t, []string{mergeCall1}, d.GitHub.Merged())
}

func TestTickRetriesADraftNotificationThatFailed(t *testing.T) {
	t.Parallel()
	d := draftDispatch(t)
	d.GitHub.PullRequest("ENG-1", draftPR("a1"))
	d.Alerts.Fail(errors.New("exit status 1"))

	require.ErrorContains(t, d.Run("tick").Err, "ENG-1")
	assert.Empty(t, d.Alerts.Shown())

	d.Alerts.Fail(nil)
	d.Tick()

	assert.Equal(t, []string{draftReadyText1}, d.Alerts.Shown())
}

func TestTickAppliesOpenPRAsOnlyToItsOwnRepo(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: tigerRepo(t, draftSetting)})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", draftPR("a1"))

	d.Tick()

	assert.Equal(t, []string{readyText1}, d.Alerts.Shown())
}
