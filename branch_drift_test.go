package dispatch_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tkt "github.com/kapetan-io/dispatch/internal/ticket"
)

const (
	// fullBranch is the branch ENG-1's worker pushed.
	fullBranch = "thrawn01/eng-1-keep-the-launched-branch-when-linears-name-drifts"
	// cutBranch is what Linear later reports for it, the same name cut short.
	cutBranch = "thrawn01/eng-1-keep-the-launched-branch-when-linears"
)

// cut is a ticket whose Linear branch name is its id followed by a title that is not the one the
// worker pushed.
func cut(id, status string) fakeTicket {
	t := ticket(id, status, "dispatch")
	t.Branch = "thrawn01/" + strings.ToLower(id) + "-cut"
	return t
}

func TestTickNudgesAnIdleWorkerWhoseLinearBranchWasCutShort(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	ticket := ticket("ENG-1", "In Review", "dispatch")
	ticket.Branch = cutBranch
	d.Linear.Tickets(ticket)
	d.Workers.CheckedOut("ENG-1", "idle")
	d.GitHub.PullRequestsOn("ENG-1", fullBranch)
	d.PullRequestFailsAfterEntry(prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	require.NoError(t, d.TickAfterASecond())

	assert.Equal(t, []string{prPrompt("a failed check")}, d.Workers.Received("ENG-1"))
	assert.Contains(t, d.prReads(), fullBranch)
}

func TestTickFindsThePRWhenTheTicketWasRetitledAfterItsBranchWasPushed(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	ticket := ticket("ENG-1", "In Review", "dispatch")
	ticket.Branch = "thrawn01/eng-1-a-better-title"
	d.Linear.Tickets(ticket)
	d.Workers.CheckedOut("ENG-1", "idle")
	d.GitHub.PullRequestsOn("ENG-1", fullBranch)
	d.PullRequestFailsAfterEntry(prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	require.NoError(t, d.TickAfterASecond())

	assert.Equal(t, []string{prPrompt("a failed check")}, d.Workers.Received("ENG-1"))
}

func TestTickKeepsATicketOffThePROfAnotherTicketWhoseIdStartsWithItsOwn(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.CheckedOut("ENG-1", "idle")
	d.GitHub.PullRequestsOn("ENG-10", "thrawn01/eng-10-another-ticket")
	d.GitHub.PullRequest("ENG-10", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	d.Tick()

	assert.Empty(t, d.Workers.Received("ENG-1"))
	assert.NotContains(t, d.prReads(), "thrawn01/eng-10-another-ticket")
}

func TestTickReadsLinearsNameWhenTwoOpenBranchesStartWithTheTicketsId(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(cut("ENG-1", "In Review"))
	d.Workers.CheckedOut("ENG-1", "idle")
	for _, branch := range []string{"thrawn01/eng-1-first", "thrawn01/eng-1-second"} {
		d.GitHub.PullRequestsOn("ENG-1", branch)
		d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	}

	d.Tick()

	assert.Equal(t, []string{"thrawn01/eng-1-cut"}, d.prReads())
	assert.Empty(t, d.Workers.Received("ENG-1"))
}

func TestExplainNamesTheBranchTheReadOfAnInReviewTicketsPRAskedFor(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(cut("ENG-1", "In Review"))
	d.Workers.CheckedOut("ENG-1", "idle")

	d.Tick()

	res := d.Run("explain", "ENG-1")
	require.NoError(t, res.Err)
	assert.NotContains(t, res.Stdout, "wants nothing")
	assert.Contains(t, res.Stdout, "found no open PR on branch thrawn01/eng-1-cut")
}

// inReviewBranchRule comments the branch ENG-1 reads as its own while it is In Review.
const inReviewBranchRule = `
whenever({ name: "branch", each: "ticket", if: (t) => t.id === "ENG-1" && t.status === "in_review",
  then: (t, ctx) => ctx.ticket.queueComment(t, "branch " + t.branch, { once: ["branch", t.branch] }) });`

func TestARuleReadsTheBranchThePRWasFoundOnAsTheTicketsBranchAcrossLinearReads(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(inReviewBranchRule)})
	d.Linear.Tickets(cut("ENG-1", "In Review"))
	d.Workers.CheckedOut("ENG-1", "idle")
	d.GitHub.PullRequestsOn("ENG-1", fullBranch)
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "SUCCESS", mergeable: "MERGEABLE"})
	d.Tick()
	require.Equal(t, "branch "+fullBranch, lastReadingOf(d, "ENG-1"))

	d.Linear.Tickets(cut("ENG-1", "In Review"))
	d.Tick()

	for _, comment := range d.Linear.Comments() {
		assert.Equal(t, "ENG-1: branch "+fullBranch, comment)
	}
}

// stackReport comments how ENG-2 reads its stack record, once for each title it has.
const stackReport = `
whenever({ name: "report", each: "ticket", if: (t) => t.id === "ENG-2" && t.status === "in_progress",
  then: (t, ctx) => {
    const body = known(() => t.stack)
      ? "stack " + (t.stack === undefined ? "none" : t.stack.parent + "@" + t.stack.forkPoint)
      : "stack unknown";
    return ctx.ticket.queueComment(t, body, { once: ["report", body, t.title] });
  } });`

func TestTickRelaunchesATicketOnTheBranchItAlreadyHasWhenLinearsNameForItWasCutShort(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	d.launch("ENG-1", prBranch("ENG-1"))
	hintGit(t, tree, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "ENG-1 work")
	work := revParse(t, tree, "HEAD")
	d.Trees.Git(tree, "checkout", "-q", "--detach")
	require.NoError(t, d.fakes().Herdr.CloseWorkspace(context.Background(), "w2"))
	require.NoError(t, d.fakes().Treehouse.Return(context.Background(), d.Repo, tree, "ENG-1"))
	d.Linear.Tickets(cut("ENG-1", "In Progress"))

	d.Tick()

	assert.Equal(t, prBranch("ENG-1"), branchIn(t, tree))
	assert.Equal(t, work, revParse(t, tree, "HEAD"))
}

func TestAStackedTicketKeepsItsStackRecordWhenLinearsNameForItDrifts(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(stackRule + stackReport)})
	head := stackedPair(t, d)
	d.Tick()
	tree := filepath.Join(filepath.Dir(d.Repo), "tree")
	d.Trees.Git(tree, "checkout", "-q", "--detach")
	child := cut("ENG-2", "In Progress")
	child.Title = "ticket ENG-2 renamed"
	child.Blockers = []tkt.Blocker{{Identifier: "ENG-1", Status: "In Review"}}
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"), child)

	d.Tick()

	want := "stack ENG-1@" + head
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
	d.Linear.Tickets(cut("ENG-1", "In Review"), child)
	d.GitHub.PullRequest("ENG-1", prState{head: head, mergeable: "MERGEABLE"})

	d.Tick()

	assert.Contains(t, d.fakes().Herdr.Kicked()[len(d.fakes().Herdr.Kicked())-1], "/dispatch:build ENG-2 --base "+prBranch("ENG-1"))
	assert.Equal(t, "ENG-1", gitConfig(t, d.Repo, "branch.thrawn01/eng-2.dispatchStackParent"))
	assert.Equal(t, head, gitConfig(t, d.Repo, "branch.thrawn01/eng-2.dispatchStackForkPoint"))
}
