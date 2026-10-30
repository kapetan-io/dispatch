package dispatch_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/linear"
)

// stackRule launches ENG-2 stacked on the PR of its first blocker, and says how it reads its own
// stack record and its blocker's ticket and PR.
const stackRule = `
const parentPR = (t, ctx) => ctx.pr.of(ctx.ticket.blockers(t)[0].ticket);
whenever({ name: "stack", each: "ticket",
  if: (t, ctx) => t.id === "ENG-2" && t.status === "in_progress" && ctx.worker.of(t).state === "none"
        && parentPR(t, ctx) !== undefined,
  then: (t, ctx) => ctx.worker.queueLaunch(t, {
    kickoff: "/dispatch:build " + t.id + " --base " + ctx.ticket.blockers(t)[0].ticket.branch,
    stackOn: parentPR(t, ctx),
    once: ["stack"],
  }) });`

// reportRule comments how ENG-2 reads its own stack record.
const reportRule = `
whenever({ name: "report", each: "ticket", if: (t) => t.id === "ENG-2" && t.status === "in_progress",
  then: (t, ctx) => {
    const body = known(() => t.stack)
      ? "stack " + (t.stack === undefined ? "none" : t.stack.parent + "@" + t.stack.forkPoint) + " branch " + t.branch
      : "stack unknown";
    return ctx.ticket.queueComment(t, body, { once: ["report", body] });
  } });`

// stackedPair is ENG-1 In Review with a PR at a commit on its own pushed branch, and ENG-2 In Progress
// blocked by it, in a repo with an origin. It returns the commit the PR is at.
func stackedPair(t *testing.T, d *Dispatch, extra ...func(*linear.Ticket)) string {
	t.Helper()
	d.Trees.Pooled()
	remote := filepath.Join(filepath.Dir(d.Repo), "remote.git")
	hintGit(t, filepath.Dir(d.Repo), "init", "-q", "--bare", remote)
	hintGit(t, d.Repo, "remote", "add", "origin", remote)
	hintGit(t, d.Repo, "push", "-q", "origin", "main")
	hintGit(t, d.Repo, "switch", "-q", "-c", "thrawn01/eng-1")
	hintGit(t, d.Repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "ENG-1 work")
	hintGit(t, d.Repo, "push", "-q", "origin", "thrawn01/eng-1")
	head := revParse(t, d.Repo, "HEAD")
	hintGit(t, d.Repo, "switch", "-q", "main")

	child := ticket("ENG-2", "In Progress", "dispatch")
	child.Blockers = []linear.Blocker{{Identifier: "ENG-1", Status: "In Review"}}
	for _, edit := range extra {
		edit(&child.Ticket)
	}
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"), child)
	d.GitHub.PullRequest("ENG-1", prState{head: head, mergeable: "MERGEABLE"})
	return head
}

func gitConfig(t *testing.T, dir, key string) string {
	t.Helper()
	out, _ := exec.Command("git", "-C", dir, "config", "--get", key).Output()
	return strings.TrimSpace(string(out))
}

func TestARuleLaunchesATicketOnAnotherTicketsPRHead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(stackRule)})
	head := stackedPair(t, d)

	d.Tick()

	assert.Equal(t, "thrawn01/eng-2", branchIn(t, d.Repo+"/../tree"))
	assert.Equal(t, head, revParse(t, filepath.Join(filepath.Dir(d.Repo), "tree"), "HEAD"))
	assert.Equal(t, "ENG-1", gitConfig(t, d.Repo, "branch.thrawn01/eng-2.dispatchStackParent"))
	assert.Equal(t, head, gitConfig(t, d.Repo, "branch.thrawn01/eng-2.dispatchStackForkPoint"))
	assert.Equal(t, []string{"w2:p2|/dispatch:build ENG-2 --base thrawn01/eng-1"}, d.fakes().Herdr.Kicked())
	launches := d.receiptsOfKind("ENG-2", "launch")
	require.Len(t, launches, 1)
	assert.Equal(t, "ok: workspace w2, stacked on ENG-1 at "+head[:7]+" (thrawn01/eng-1)", launches[0].Detail)
}

func TestARuleReadsTheStackRecordOfABranch(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(stackRule + reportRule)})
	head := stackedPair(t, d)

	d.Tick()

	assert.Equal(t, "stack ENG-1@"+head+" branch thrawn01/eng-2", lastReadingOf(d, "ENG-2"))
}

func TestATicketWithNoStackRecordReadsItAsUndefined(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(reportRule)})
	d.Trees.Pooled()
	d.Linear.Tickets(ticket("ENG-2", "In Progress", "dispatch"))

	d.Tick()

	assert.Equal(t, "stack none branch thrawn01/eng-2", lastReadingOf(d, "ENG-2"))
}

func TestARelaunchNeverChangesTheStackRecord(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(stackRule)})
	stackedPair(t, d)
	// The branch exists with a record from an earlier launch, so this launch is a relaunch.
	hintGit(t, d.Repo, "branch", "thrawn01/eng-2", "main")
	hintGit(t, d.Repo, "config", "branch.thrawn01/eng-2.dispatchStackParent", "ENG-9")
	hintGit(t, d.Repo, "config", "branch.thrawn01/eng-2.dispatchStackForkPoint", "0123456789abcdef0123456789abcdef01234567")

	d.Tick()

	assert.Equal(t, "ENG-9", gitConfig(t, d.Repo, "branch.thrawn01/eng-2.dispatchStackParent"))
	assert.Equal(t, "0123456789abcdef0123456789abcdef01234567", gitConfig(t, d.Repo, "branch.thrawn01/eng-2.dispatchStackForkPoint"))
	launches := d.receiptsOfKind("ENG-2", "launch")
	require.Len(t, launches, 1)
	assert.Equal(t, "ok: workspace w2", launches[0].Detail)
}

func TestALaunchOnAnExistingBranchWritesNoStackRecord(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(stackRule)})
	stackedPair(t, d)
	hintGit(t, d.Repo, "branch", "thrawn01/eng-2", "main")

	d.Tick()

	assert.Empty(t, gitConfig(t, d.Repo, "branch.thrawn01/eng-2.dispatchStackParent"))
	assert.Empty(t, gitConfig(t, d.Repo, "branch.thrawn01/eng-2.dispatchStackForkPoint"))
}

func TestAStackedLaunchWhoseHeadIsMissingFailsAndLeavesNoRecord(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(stackRule)})
	stackedPair(t, d)
	d.GitHub.PullRequest("ENG-1", prState{head: "0123456789abcdef0123456789abcdef01234567", mergeable: "MERGEABLE"})

	res := d.Run("tick")

	require.ErrorContains(t, res.Err, "checkout_failed")
	launches := d.receiptsOfKind("ENG-2", "launch")
	require.Len(t, launches, 1)
	assert.Equal(t, "failed", launches[0].State)
	assert.Contains(t, launches[0].Detail, "checkout_failed")
	assert.Empty(t, gitConfig(t, d.Repo, "branch.thrawn01/eng-2.dispatchStackParent"))
	assert.Empty(t, gitConfig(t, d.Repo, "branch.thrawn01/eng-2.dispatchStackForkPoint"))
}

func TestAPartialStackRecordReadsAsUnknown(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(reportRule)})
	d.Trees.Pooled()
	child := ticket("ENG-2", "In Progress", "dispatch")
	d.Linear.Tickets(child)
	hintGit(t, d.Repo, "config", "branch.thrawn01/eng-2.dispatchStackParent", "ENG-1")

	d.Tick()

	assert.Equal(t, "stack unknown", lastReadingOf(d, "ENG-2"))
}

func TestAStackRecordThatCannotBeReadReadsAsUnknown(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(reportRule)})
	d.Trees.Pooled()
	d.Linear.Tickets(ticket("ENG-2", "In Progress", "dispatch"))
	// A repo directory git can't open fails the read, as a moved clone does.
	require.NoError(t, os.RemoveAll(d.Repo))

	d.Tick()

	assert.Equal(t, "stack unknown", lastReadingOf(d, "ENG-2"))
}

func TestAStackedLaunchWhoseRecordCannotBeWrittenFailsAndCreatesNoBranch(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(stackRule)})
	stackedPair(t, d)
	// A held lock on the clone's config makes `git config` refuse to write.
	require.NoError(t, os.WriteFile(filepath.Join(d.Repo, ".git", "config.lock"), nil, 0o644))

	res := d.Run("tick")

	require.ErrorContains(t, res.Err, "checkout_failed")
	assert.Len(t, d.Trees.Returned(), 1)
	assert.Equal(t, "failed", d.receiptsOfKind("ENG-2", "launch")[0].State)
	assert.Empty(t, gitConfig(t, d.Repo, "branch.thrawn01/eng-2.dispatchStackParent"))
	out, _ := exec.Command("git", "-C", d.Repo, "branch", "--list", "thrawn01/eng-2").Output()
	assert.Empty(t, strings.TrimSpace(string(out)))
}

// parentRule comments what ENG-2 reads of its first blocker's ticket and PR.
const parentRule = `
whenever({ name: "parent", each: "ticket", if: (t) => t.id === "ENG-2" && t.status === "in_progress",
  then: (t, ctx) => {
    const blocker = ctx.ticket.blockers(t)[0];
    const parent = blocker.ticket;
    let body = "blocker " + blocker.id + " ticket " + (parent === undefined ? "untracked" : parent.branch);
    if (parent !== undefined) {
      body += known(() => ctx.pr.of(parent))
        ? " pr " + (ctx.pr.of(parent) === undefined ? "none" : ctx.pr.of(parent).head)
        : " pr unknown";
    }
    return ctx.ticket.queueComment(t, body, { once: ["parent", body] });
  } });`

func TestARuleReadsABlockersTicketAndItsPR(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(parentRule)})
	head := stackedPair(t, d)

	d.Tick()

	assert.Equal(t, "blocker ENG-1 ticket thrawn01/eng-1 pr "+head, lastReadingOf(d, "ENG-2"))
}

func TestABlockerNobodyTracksHasNoTicket(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(parentRule)})
	d.Trees.Pooled()
	child := ticket("ENG-2", "In Progress", "dispatch")
	child.Blockers = []linear.Blocker{{Identifier: "ENG-7", Status: "In Review"}}
	d.Linear.Tickets(child)

	d.Tick()

	assert.Equal(t, "blocker ENG-7 ticket untracked", lastReadingOf(d, "ENG-2"))
}

func TestAPRIsNoneWhenDispatchReadItAndFoundNoneAndUnknownWhenItDidNotRead(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, worker, want string
	}{
		{"ReadWithNoPR", "none", "blocker ENG-1 ticket thrawn01/eng-1 pr none"},
		{"WorkerWorking", "working", "blocker ENG-1 ticket thrawn01/eng-1 pr unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Rules: rule(parentRule)})
			child := ticket("ENG-2", "In Progress", "dispatch")
			child.Blockers = []linear.Blocker{{Identifier: "ENG-1", Status: "In Review"}}
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"), child)
			if test.worker == "working" {
				d.Workers.CheckedOut("ENG-1", "working")
			}

			d.Tick()

			assert.Equal(t, test.want, lastReadingOf(d, "ENG-2"))
		})
	}
}

func TestReplayOfAStackedLaunchShowsNoDifferences(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(stackRule + reportRule)})
	stackedPair(t, d)
	d.Tick()

	res := d.Run("replay")

	require.NoError(t, res.Err)
	assert.Regexp(t, `\d+ cycles?, (\d+) match\n$`, res.Stdout)
	assert.NotContains(t, res.Stdout, "differ")
}

func TestExplainShowsTheStackedLaunchReceipt(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(stackRule)})
	head := stackedPair(t, d)
	d.Tick()
	d.Stop()

	res := d.Run("explain", "ENG-2")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "ok: workspace w2, stacked on ENG-1 at "+head[:7]+" (thrawn01/eng-1)")
}

func TestReplayOfAStackedLaunchCountsTheCycleAsMatching(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(stackRule + reportRule)})
	stackedPair(t, d)
	d.Tick()
	d.Stop()

	cycles, matched := replayOf(t, d)

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}

func TestAStackedLaunchWhoseSwitchFailsReturnsTheLease(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(stackRule)})
	stackedPair(t, d)
	d.GitHub.PullRequest("ENG-1", prState{head: "0123456789abcdef0123456789abcdef01234567", mergeable: "MERGEABLE"})

	d.Run("tick")

	assert.Len(t, d.Trees.Returned(), 1)
}

func TestAStackedLaunchWhoseSwitchFailsAfterCreatingTheBranchKeepsTheRecord(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(stackRule)})
	head := stackedPair(t, d)
	// A held lock on the tree's HEAD makes git create the branch and then fail to move onto it.
	require.NoError(t, os.WriteFile(filepath.Join(d.Repo, ".git", "worktrees", "tree", "HEAD.lock"), nil, 0o644))

	res := d.Run("tick")

	require.ErrorContains(t, res.Err, "checkout_failed")
	assert.Equal(t, head, revParse(t, d.Repo, "thrawn01/eng-2"))
	assert.Equal(t, "ENG-1", gitConfig(t, d.Repo, "branch.thrawn01/eng-2.dispatchStackParent"))
	assert.Equal(t, head, gitConfig(t, d.Repo, "branch.thrawn01/eng-2.dispatchStackForkPoint"))
}
