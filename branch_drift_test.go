package dispatch_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tkt "github.com/kapetan-io/dispatch/internal/ticket"
)

// driftedBranch is what Linear reports once it truncates the branch dispatch launched.
const driftedBranch = "thrawn01/eng-"

func drifted(id, status string) fakeTicket {
	t := ticket(id, status, "dispatch")
	t.Branch = driftedBranch
	return t
}

func TestTickNudgesAnIdleWorkerWhoseTreeHoldsTheBranchLinearNoLongerNames(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(drifted("ENG-1", "In Review"))
	tree := d.Workers.CheckedOut("ENG-1", "idle")
	d.Trees.Git(tree, "branch", "-m", "thrawn01/eng-1")
	d.PullRequestFailsAfterEntry(prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	require.NoError(t, d.TickAfterASecond())

	assert.Equal(t, []string{prPrompt("a failed check")}, d.Workers.Received("ENG-1"))
}

func TestTickFindsThePRByTheBranchADetachedTreeWasLaunchedOn(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	d.launch("ENG-1", prBranch("ENG-1"))
	d.Trees.Git(tree, "checkout", "-q", "--detach")
	d.Linear.Tickets(drifted("ENG-1", "In Review"))
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	d.fakes().Herdr.SetStatus("w2:p2", "idle")

	d.Tick()

	assert.Contains(t, d.prReads(), prBranch("ENG-1"))
}

func TestTickFallsBackToLinearsBranchWhenTheTreesBranchHasNoOpenPR(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	tree := d.Workers.CheckedOut("ENG-1", "idle")
	d.Trees.Git(tree, "branch", "-m", "scratch")
	d.PullRequestFailsAfterEntry(prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	require.NoError(t, d.TickAfterASecond())

	assert.Equal(t, []string{"scratch", prBranch("ENG-1")}, d.prReads()[:2])
	assert.Equal(t, []string{prPrompt("a failed check")}, d.Workers.Received("ENG-1"))
}

func TestTickRelaunchesATicketOnTheBranchItWasLaunchedOnWhenLinearsNameDrifts(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	d.launch("ENG-1", prBranch("ENG-1"))
	hintGit(t, tree, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "ENG-1 work")
	work := revParse(t, tree, "HEAD")
	d.Trees.Git(tree, "checkout", "-q", "--detach")
	require.NoError(t, d.fakes().Herdr.CloseWorkspace(context.Background(), "w2"))
	require.NoError(t, d.fakes().Treehouse.Return(context.Background(), d.Repo, tree, "ENG-1"))
	d.Linear.Tickets(drifted("ENG-1", "In Progress"))

	d.Tick()

	assert.Equal(t, prBranch("ENG-1"), branchIn(t, tree))
	assert.Equal(t, work, revParse(t, tree, "HEAD"))
}

func TestExplainNamesEveryBranchTheReadOfAnInReviewTicketsPRAskedFor(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(drifted("ENG-1", "In Review"))
	tree := d.Workers.CheckedOut("ENG-1", "idle")
	d.Trees.Git(tree, "branch", "-m", "renamed-by-hand")

	d.Tick()

	res := d.Run("explain", "ENG-1")
	require.NoError(t, res.Err)
	assert.NotContains(t, res.Stdout, "wants nothing")
	assert.Contains(t, res.Stdout, "found no open PR on branches renamed-by-hand, "+driftedBranch)
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		assert.NotContains(t, line, "thrawn01/eng-1")
	}
}

// branchRule comments the branch ENG-1 reads as its own.
const branchRule = `
whenever({ name: "branch", each: "ticket", if: (t) => t.id === "ENG-1" && t.status === "in_progress",
  then: (t, ctx) => ctx.ticket.queueComment(t, "branch " + t.branch, { once: ["branch", t.branch] }) });`

// stackReport comments how ENG-2 reads its stack record, once for each title it has.
const stackReport = `
whenever({ name: "report", each: "ticket", if: (t) => t.id === "ENG-2" && t.status === "in_progress",
  then: (t, ctx) => {
    const body = known(() => t.stack)
      ? "stack " + (t.stack === undefined ? "none" : t.stack.parent + "@" + t.stack.forkPoint) + " branch " + t.branch
      : "stack unknown";
    return ctx.ticket.queueComment(t, body, { once: ["report", body, t.title] });
  } });`

func TestARuleReadsTheBranchADetachedTreeWasLaunchedOnAsTheTicketsBranch(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(branchRule)})
	tree := d.Trees.Pooled()
	d.launch("ENG-1", prBranch("ENG-1"))
	d.Trees.Git(tree, "checkout", "-q", "--detach")
	d.Linear.Tickets(drifted("ENG-1", "In Progress"))

	d.Tick()

	assert.Equal(t, "branch "+prBranch("ENG-1"), lastReadingOf(d, "ENG-1"))
}

func TestARuleReadsTheBranchCheckedOutInTheTreeAsTheTicketsBranch(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(branchRule)})
	d.Linear.Tickets(drifted("ENG-1", "In Progress"))
	tree := d.Workers.CheckedOut("ENG-1", "idle")
	d.Trees.Git(tree, "branch", "-m", "renamed-by-hand")

	d.Tick()

	assert.Equal(t, "branch renamed-by-hand", lastReadingOf(d, "ENG-1"))
}

func TestAStackedTicketKeepsItsStackRecordWhenLinearsNameForItDrifts(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(stackRule + stackReport)})
	head := stackedPair(t, d)
	d.Tick()
	tree := filepath.Join(filepath.Dir(d.Repo), "tree")
	d.Trees.Git(tree, "checkout", "-q", "--detach")
	child := drifted("ENG-2", "In Progress")
	child.Title = "ticket ENG-2 renamed"
	child.Blockers = []tkt.Blocker{{Identifier: "ENG-1", Status: "In Review"}}
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"), child)

	d.Tick()

	want := "stack ENG-1@" + head + " branch " + prBranch("ENG-2")
	var readings []string
	for _, comment := range d.Linear.Comments() {
		if body, ok := strings.CutPrefix(comment, "ENG-2: "); ok {
			readings = append(readings, body)
		}
	}
	require.Len(t, readings, 3)
	assert.Equal(t, []string{want, want}, readings[1:])
}

func TestATicketStackedOnOneWhoseLinearNameDriftedLaunchesFromItsRealBranch(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(stackRule)})
	tree := d.Trees.Pooled()
	remote := filepath.Join(filepath.Dir(d.Repo), "remote.git")
	hintGit(t, filepath.Dir(d.Repo), "init", "-q", "--bare", remote)
	hintGit(t, d.Repo, "remote", "add", "origin", remote)
	hintGit(t, d.Repo, "push", "-q", "origin", "main")
	d.launch("ENG-1", prBranch("ENG-1"))
	hintGit(t, tree, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "ENG-1 work")
	hintGit(t, tree, "push", "-q", "origin", prBranch("ENG-1"))
	head := revParse(t, tree, "HEAD")
	d.Trees.Git(tree, "checkout", "-q", "--detach")
	require.NoError(t, d.fakes().Herdr.CloseWorkspace(context.Background(), "w2"))
	require.NoError(t, d.fakes().Treehouse.Return(context.Background(), d.Repo, tree, "ENG-1"))
	child := ticket("ENG-2", "In Progress", "dispatch")
	child.Blockers = []tkt.Blocker{{Identifier: "ENG-1", Status: "In Review"}}
	d.Linear.Tickets(drifted("ENG-1", "In Review"), child)
	d.GitHub.PullRequest("ENG-1", prState{head: head, mergeable: "MERGEABLE"})

	d.Tick()

	assert.Contains(t, d.fakes().Herdr.Kicked()[len(d.fakes().Herdr.Kicked())-1], "/dispatch:build ENG-2 --base "+prBranch("ENG-1"))
	assert.Equal(t, "ENG-1", gitConfig(t, d.Repo, "branch.thrawn01/eng-2.dispatchStackParent"))
	assert.Equal(t, head, gitConfig(t, d.Repo, "branch.thrawn01/eng-2.dispatchStackForkPoint"))
}

// inReviewBranchRule comments the branch ENG-1 reads as its own while it is In Review.
const inReviewBranchRule = `
whenever({ name: "branch", each: "ticket", if: (t) => t.id === "ENG-1" && t.status === "in_review",
  then: (t, ctx) => ctx.ticket.queueComment(t, "branch " + t.branch, { once: ["branch", t.branch] }) });`

func TestTickKeepsAWorkerOffThePROfAnotherTicketWhoseBranchItsTreeHasCheckedOut(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Review", "dispatch"))
	tree := d.Workers.CheckedOut("ENG-2", "idle")
	d.Trees.Git(tree, "branch", "-m", prBranch("ENG-1"))
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	d.Restart()

	require.NoError(t, d.TickAfterASecond())

	assert.NotContains(t, d.prReads(), prBranch("ENG-1"))
	assert.Empty(t, d.Workers.Received("ENG-2"))
}

func TestARuleReadsTheBranchThePRWasFoundOnAsTheTicketsBranch(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(inReviewBranchRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	tree := d.Workers.CheckedOut("ENG-1", "idle")
	d.Trees.Git(tree, "branch", "-m", "scratch")
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "SUCCESS", mergeable: "MERGEABLE"})

	d.Tick()

	assert.Equal(t, "branch "+prBranch("ENG-1"), lastReadingOf(d, "ENG-1"))
}

func TestARuleKeepsTheBranchItReadWhenALaterRefreshCannotReadThePool(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(inReviewBranchRule)})
	d.Linear.Tickets(drifted("ENG-1", "In Review"))
	tree := d.Workers.CheckedOut("ENG-1", "idle")
	d.Trees.Git(tree, "branch", "-m", "renamed-by-hand")
	d.Tick()
	require.Equal(t, "branch renamed-by-hand", lastReadingOf(d, "ENG-1"))

	d.fakes().Treehouse.FailTrees(errors.New("exit status 1"))
	d.Linear.Tickets(drifted("ENG-1", "In Review"))
	d.Tick()

	for _, comment := range d.Linear.Comments() {
		assert.NotContains(t, comment, "branch "+driftedBranch)
	}
}
