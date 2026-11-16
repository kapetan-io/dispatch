package dispatch_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// relaunchable launches ENG-1 on its branch, then closes its workspace and returns its tree, so
// ENG-1 has a branch row and no worker. It returns the tree.
func relaunchable(t *testing.T, d *Dispatch) string {
	t.Helper()
	tree := d.Trees.Pooled()
	d.launch("ENG-1", prBranch("ENG-1"))
	d.Trees.Git(tree, "checkout", "-q", "--detach")
	require.NoError(t, d.fakes().Multiplexer.CloseWorkspace(context.Background(), "w2"))
	require.NoError(t, d.fakes().Treehouse.Return(context.Background(), d.Repo, tree, "ENG-1"))
	return tree
}

func TestTicketBranchPrintsTheBranchALaunchRecorded(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.launch("ENG-1", prBranch("ENG-1"))

	res := d.Run("ticket", "branch", "ENG-1")

	require.NoError(t, res.Err)
	assert.Equal(t, "ENG-1: branch "+prBranch("ENG-1")+" in dispatch\n", res.Stdout)
	assert.Empty(t, dispatchKeys(t, d.Repo))
}

func TestTicketBranchExitsThreeWhenDispatchIsNotRunning(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	for _, args := range [][]string{
		{"ticket", "branch", "ENG-1"},
		{"ticket", "branch", "ENG-1", "main"},
		{"ticket", "branch", "ENG-1", "--unstack"},
	} {
		res := d.Run(args...)
		assert.Equal(t, 3, res.Code, args)
	}
}

func TestTicketBranchRecordsTheBranchAWorkerRenamedItsWorkTo(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := relaunchable(t, d)
	d.Trees.Git(tree, "branch", "-m", prBranch("ENG-1"), fullBranch)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("ticket", "branch", "ENG-1", fullBranch)
	d.Tick()

	require.NoError(t, res.Err)
	assert.Equal(t, "ENG-1: branch "+fullBranch+" in dispatch\n", res.Stdout)
	assert.Equal(t, fullBranch, branchIn(t, tree))
	assert.Empty(t, d.Linear.Comments())
	assert.Empty(t, d.Linear.Moves())
}

func TestTicketBranchRefusesANameInNeitherTheCloneNorOrigin(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	relaunchable(t, d)
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))

	res := d.Run("ticket", "branch", "ENG-1", "thrawn01/eng-1-never-pushed")

	require.ErrorContains(t, res.Err, "branch thrawn01/eng-1-never-pushed is in neither "+d.Repo+" nor origin; nothing was recorded")
	assert.Equal(t, "ENG-1: branch "+prBranch("ENG-1")+" in dispatch\n", d.Run("ticket", "branch", "ENG-1").Stdout)
}

func TestTicketBranchRefusesANameAnotherTicketsRowHolds(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	relaunchable(t, d)
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"), ticket("ENG-2", "In Review", "dispatch"))
	d.Tick()

	res := d.Run("ticket", "branch", "ENG-2", prBranch("ENG-1"))

	require.ErrorContains(t, res.Err, "branch "+prBranch("ENG-1")+" in dispatch belongs to ENG-1, not ENG-2")
	assert.Equal(t, "ENG-2: no branch recorded in dispatch\n", d.Run("ticket", "branch", "ENG-2").Stdout)
}

func TestTicketBranchRefusesATicketWithNoRepo(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "Todo", ""))
	d.Tick()

	res := d.Run("ticket", "branch", "ENG-1", "main")

	require.ErrorContains(t, res.Err, "ENG-1 has no repo")
}

func TestTicketBranchNamesTheBranchOfALocalTicket(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.Trees.Pooled()
	d.Tick()
	id := d.fileLocal("Todo")
	hintGit(t, d.Repo, "branch", "loc-work", "main")

	res := d.Run("ticket", "branch", id, "loc-work")

	require.NoError(t, res.Err)
	assert.Equal(t, id+": branch loc-work in dispatch\n", res.Stdout)
}

func TestALaunchWhoseRecordedBranchIsGoneFailsWithBranchMissingAndEscalates(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := relaunchable(t, d)
	d.Trees.Git(tree, "branch", "-D", prBranch("ENG-1"))
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	d.Run("tick")
	d.Advance(time.Minute)
	d.Run("tick")
	d.Advance(time.Minute)
	d.Run("tick")

	ended := d.launchesEnded("ENG-1")
	// The first launch put ENG-1 on its branch, and the three after it reach the default limit.
	require.Len(t, ended, 4)
	for _, launch := range ended[1:] {
		assert.Equal(t, "failed", launch.State)
		assert.Contains(t, launch.Detail, "branch_missing: branch "+prBranch("ENG-1")+" of ENG-1 is in neither")
	}
	assert.Empty(t, d.leasedTo("ENG-1"))
	assert.Equal(t, []string{"ENG-1 -> Needs Human"}, d.Linear.Moves())
}

// workerStackReport comments how ENG-2 reads its stack record while it has a worker, so the
// readings start after its stacked launch.
const workerStackReport = `
whenever({ name: "report", each: "ticket", if: (t, ctx) => t.id === "ENG-2" && ctx.worker.of(t).state !== "none",
  then: (t, ctx) => {
    const body = known(() => t.stack)
      ? "stack " + (t.stack === undefined ? "none" : t.stack.parent + "@" + t.stack.forkPoint) + " branch " + t.branch
      : "stack unknown";
    return ctx.ticket.queueComment(t, body, { once: ["report", body] });
  } });`

func TestUnstackEndsTheStackRecordAndSaysSoAgainWhenThereIsNone(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(stackRule + workerStackReport)})
	head := stackedPair(t, d)
	d.Tick()
	require.Equal(t, "stack ENG-1@"+head+" branch thrawn01/eng-2", lastReadingOf(d, "ENG-2"))

	first := d.Run("ticket", "branch", "ENG-2", "--unstack")
	second := d.Run("ticket", "branch", "ENG-2", "--unstack")
	d.Tick()

	require.NoError(t, first.Err)
	require.NoError(t, second.Err)
	assert.Equal(t, "ENG-2: no stack record in dispatch\n", first.Stdout)
	assert.Equal(t, "ENG-2: no stack record in dispatch\n", second.Stdout)
	assert.Equal(t, "stack none branch thrawn01/eng-2", lastReadingOf(d, "ENG-2"))
}

func TestAFirstStartImportsTheTicketBranchKeyOfAnEarlierDispatch(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Trees.Pooled()
	d.Linear.Tickets(cut("ENG-1", "In Review"))
	hintGit(t, d.Repo, "branch", fullBranch, "main")
	hintGit(t, d.Repo, "config", "branch."+fullBranch+".dispatchTicket", "ENG-1")

	d.Tick()

	assert.Equal(t, "ENG-1: branch "+fullBranch+" in dispatch\n", d.Run("ticket", "branch", "ENG-1").Stdout)
	assert.Empty(t, dispatchKeys(t, d.Repo))
}

func TestAKeyAFirstStartCouldNotUnsetIsUnsetAtALaterResync(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Trees.Pooled()
	d.Linear.Tickets(cut("ENG-1", "In Review"))
	hintGit(t, d.Repo, "branch", fullBranch, "main")
	hintGit(t, d.Repo, "config", "branch."+fullBranch+".dispatchTicket", "ENG-1")
	// A held lock on the clone's config makes `git config --unset` refuse to write.
	lock := filepath.Join(d.Repo, ".git", "config.lock")
	require.NoError(t, os.WriteFile(lock, nil, 0o644))

	d.Tick()
	require.Contains(t, d.LoopErr(), "while unsetting the dispatch keys of "+fullBranch+" in dispatch")
	require.NotEmpty(t, dispatchKeys(t, d.Repo))
	require.NoError(t, os.Remove(lock))
	d.Advance(time.Minute)
	d.Tick()

	assert.Empty(t, dispatchKeys(t, d.Repo))
	assert.Equal(t, "ENG-1: branch "+fullBranch+" in dispatch\n", d.Run("ticket", "branch", "ENG-1").Stdout)
}

// inFlightBeforeTheUpgrade is ENG-2 In Review with a worker an earlier dispatch launched: a lease,
// a workspace and a session, on the branch thrawn01/eng-2-pushed whose ticket branch key and stack
// record the earlier dispatch kept in git config, with a PR on that branch. It returns the tree.
func inFlightBeforeTheUpgrade(t *testing.T, d *Dispatch) string {
	t.Helper()
	tree := d.worktreeOf("ENG-2")
	d.Trees.Git(tree, "branch", "-m", "thrawn01/eng-2-pushed")
	d.Linear.Tickets(ticket("ENG-2", "In Review", "dispatch"))
	d.unrecordedWorker(d.Repo, "ENG-2", tree, "w-ENG-2", "idle")
	hintGit(t, d.Repo, "config", "branch.thrawn01/eng-2-pushed.dispatchTicket", "ENG-2")
	hintGit(t, d.Repo, "config", "branch.thrawn01/eng-2-pushed.dispatchStackParent", "ENG-1")
	hintGit(t, d.Repo, "config", "branch.thrawn01/eng-2-pushed.dispatchStackForkPoint", "0123456789abcdef0123456789abcdef01234567")
	d.GitHub.PullRequestsOn("ENG-2", "thrawn01/eng-2-pushed")
	d.GitHub.PullRequest("ENG-2", prState{head: "a1", checks: "SUCCESS", mergeable: "MERGEABLE"})
	return tree
}

// inFlightReport comments how ENG-2 reads its stack record and branch, and whether a PR read found
// its PR.
const inFlightReport = `
whenever({ name: "report", each: "ticket", if: (t) => t.id === "ENG-2",
  then: (t, ctx) => {
    const stack = known(() => t.stack)
      ? "stack " + (t.stack === undefined ? "none" : t.stack.parent + "@" + t.stack.forkPoint)
      : "stack unknown";
    const body = stack + " branch " + t.branch + (ctx.pr.of(t) === undefined ? "" : " pr");
    return ctx.ticket.queueComment(t, body, { once: ["report", body] });
  } });`

func TestTheFirstStartKeepsATicketInFlightOnItsBranchTreePRAndStackRecordWithoutALaunch(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(inFlightReport)})
	tree := inFlightBeforeTheUpgrade(t, d)

	d.Tick()

	assert.Equal(t, "stack ENG-1@0123456789abcdef0123456789abcdef01234567 branch thrawn01/eng-2-pushed pr", lastReadingOf(d, "ENG-2"))
	assert.Equal(t, "ENG-2: branch thrawn01/eng-2-pushed in dispatch\n", d.Run("ticket", "branch", "ENG-2").Stdout)
	assert.Equal(t, tree, d.recordedTree("ENG-2"))
	assert.Equal(t, "thrawn01/eng-2-pushed", branchIn(t, tree))
	assert.Empty(t, d.Workers.Launched())
	assert.Empty(t, dispatchKeys(t, d.Repo))

	// A second start finds the repo imported and changes nothing.
	d.Restart()
	d.Tick()

	assert.Equal(t, "stack ENG-1@0123456789abcdef0123456789abcdef01234567 branch thrawn01/eng-2-pushed pr", lastReadingOf(d, "ENG-2"))
	assert.Equal(t, "ENG-2: branch thrawn01/eng-2-pushed in dispatch\n", d.Run("ticket", "branch", "ENG-2").Stdout)
	assert.Equal(t, tree, d.recordedTree("ENG-2"))
	assert.Empty(t, d.Workers.Launched())
}

func TestAFirstStartKilledAtAnyCommitEndsAsAnUninterruptedOne(t *testing.T) {
	t.Parallel()
	clean := newDispatch(t, dispatchOptions{Rules: rule(inFlightReport)})
	inFlightBeforeTheUpgrade(t, clean)
	clean.fakes().Kill.ThrowAt(0)
	clean.Tick()
	calls := clean.fakes().Kill.Count()
	require.Positive(t, calls)

	for at := 1; at <= calls; at++ {
		t.Run("KilledAtCall"+strconv.Itoa(at), func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{Rules: rule(inFlightReport)})
			tree := inFlightBeforeTheUpgrade(t, d)
			d.fakes().Kill.ThrowAt(at)
			d.Run("tick")

			d.fakes().Kill.ThrowAt(0)
			d.Restart()
			d.Tick()

			assert.Equal(t, "stack ENG-1@0123456789abcdef0123456789abcdef01234567 branch thrawn01/eng-2-pushed pr", lastReadingOf(d, "ENG-2"))
			assert.Equal(t, "ENG-2: branch thrawn01/eng-2-pushed in dispatch\n", d.Run("ticket", "branch", "ENG-2").Stdout)
			assert.Equal(t, tree, d.recordedTree("ENG-2"))
			assert.Empty(t, d.Workers.Launched())
			assert.Empty(t, dispatchKeys(t, d.Repo))
		})
	}
}

func TestARepoWithHalfAStackRecordIsNamedAtEachResyncAndImportedOnceTheKeyIsSet(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(reportRule)})
	d.Trees.Pooled()
	d.Linear.Tickets(ticket("ENG-2", "In Progress", "dispatch"))
	hintGit(t, d.Repo, "branch", "thrawn01/eng-2", "main")
	hintGit(t, d.Repo, "config", "branch.thrawn01/eng-2.dispatchStackParent", "ENG-1")
	line := "dispatch: dispatch is not imported: branch thrawn01/eng-2 has half a stack record (only dispatchStackParent)."

	d.Tick()
	d.Advance(time.Minute)
	d.Tick()

	assert.Equal(t, "stack unknown", lastReadingOf(d, "ENG-2"))
	assert.GreaterOrEqual(t, strings.Count(d.LoopErr(), line), 2)
	assert.NotEmpty(t, dispatchKeys(t, d.Repo))

	hintGit(t, d.Repo, "config", "branch.thrawn01/eng-2.dispatchStackForkPoint", "0123456789abcdef0123456789abcdef01234567")
	d.Advance(time.Minute)
	d.Tick()

	assert.Equal(t, "stack ENG-1@0123456789abcdef0123456789abcdef01234567 branch thrawn01/eng-2", lastReadingOf(d, "ENG-2"))
	assert.Empty(t, dispatchKeys(t, d.Repo))
}

func TestAGoneBranchIsNamedAtEachResync(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := relaunchable(t, d)
	d.Trees.Git(tree, "branch", "-D", prBranch("ENG-1"))
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	line := "dispatch: ENG-1's branch " + prBranch("ENG-1") + " in dispatch is gone;"

	d.Tick()
	d.Advance(time.Minute)
	d.Tick()

	assert.GreaterOrEqual(t, strings.Count(d.LoopErr(), line), 2)
}

// branchIsRecorded comments ENG-1 when it reads the branch a rename recorded, not Linear's name.
const branchIsRecorded = `
whenever({ name: "recorded", each: "ticket", if: (t) => t.id === "ENG-1" && t.branch === "` + fullBranch + `",
  then: (t, ctx) => ctx.ticket.queueComment(t, "recorded", { once: ["recorded"] }) });`

func TestRulesCheckAndStateResetWhileStoppedReadTheBranchRowsAndChangeNone(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(launchRule + branchIsRecorded)})
	tree := relaunchable(t, d)
	d.Trees.Git(tree, "branch", "-m", prBranch("ENG-1"), fullBranch)
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	require.NoError(t, d.Run("ticket", "branch", "ENG-1", fullBranch).Err)
	d.Stop()

	check := d.RunWhileDown("rules", "check")
	reset := d.RunWhileDown("state", "reset")

	require.NoError(t, check.Err)
	assert.Regexp(t, `recorded +whenever +ticket +.*ENG-1`, check.Stdout)
	require.NoError(t, reset.Err)
	d.Tick()
	assert.Equal(t, "ENG-1: branch "+fullBranch+" in dispatch\n", d.Run("ticket", "branch", "ENG-1").Stdout)
}

func TestUnstackTouchesNoGitConfig(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(stackRule)})
	stackedPair(t, d)
	d.Tick()
	before := gitConfigList(t, d.Repo)

	require.NoError(t, d.Run("ticket", "branch", "ENG-2", "--unstack").Err)

	assert.Equal(t, before, gitConfigList(t, d.Repo))
}

func gitConfigList(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "config", "--local", "--list").Output()
	require.NoError(t, err)
	return string(out)
}

func TestABranchTakenLaunchAndNameLeaveEveryRowAsItWas(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	relaunchable(t, d)
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))

	launched := d.launch("ENG-2", prBranch("ENG-1"))
	named := d.Run("ticket", "branch", "ENG-2", prBranch("ENG-1"))

	assert.Equal(t, "branch_taken", launched.result["status"])
	require.Error(t, named.Err)
	assert.Empty(t, d.leasedTo("ENG-2"))
	assert.Empty(t, d.recordedRepos("ENG-2"))
	assert.Equal(t, "ENG-2: no branch recorded in dispatch\n", d.Run("ticket", "branch", "ENG-2").Stdout)
	assert.Equal(t, "ENG-1: branch "+prBranch("ENG-1")+" in dispatch\n", d.Run("ticket", "branch", "ENG-1").Stdout)
	assert.Empty(t, d.recordedRepos("ENG-1"))
}

// waitingImport gives ENG-2 a stacked branch in git config the way an earlier dispatch kept it, and
// a branch with half a stack record, so the repo's import waits.
func waitingImport(t *testing.T, d *Dispatch) {
	t.Helper()
	d.Trees.Pooled()
	hintGit(t, d.Repo, "branch", "thrawn01/eng-2", "main")
	hintGit(t, d.Repo, "config", "branch.thrawn01/eng-2.dispatchTicket", "ENG-2")
	hintGit(t, d.Repo, "config", "branch.thrawn01/eng-2.dispatchStackParent", "ENG-1")
	hintGit(t, d.Repo, "config", "branch.thrawn01/eng-2.dispatchStackForkPoint", "0123456789abcdef0123456789abcdef01234567")
	hintGit(t, d.Repo, "branch", "other", "main")
	hintGit(t, d.Repo, "config", "branch.other.dispatchStackParent", "ENG-9")
}

func TestTicketBranchChangesNothingBeforeTheRepoIsImported(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(reportRule)})
	waitingImport(t, d)
	d.Linear.Tickets(ticket("ENG-2", "In Progress", "dispatch"))
	d.Tick()
	hintGit(t, d.Repo, "branch", "-m", "thrawn01/eng-2", "thrawn01/eng-2-new")

	unstack := d.Run("ticket", "branch", "ENG-2", "--unstack")
	rename := d.Run("ticket", "branch", "ENG-2", "thrawn01/eng-2-new")

	const refused = "the branches of dispatch are not imported yet, so nothing was changed"
	require.ErrorContains(t, unstack.Err, refused)
	require.ErrorContains(t, rename.Err, refused)
	hintGit(t, d.Repo, "config", "branch.other.dispatchStackForkPoint", "0123456789abcdef0123456789abcdef01234567")
	d.Advance(time.Minute)
	d.Tick()
	assert.Equal(t, "stack ENG-1@0123456789abcdef0123456789abcdef01234567 branch thrawn01/eng-2-new", lastReadingOf(d, "ENG-2"))
	assert.Empty(t, dispatchKeys(t, d.Repo))
}

func TestATicketReadsItsPROnTheBranchItsGitConfigKeyNamesWhileTheImportWaits(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(inReviewBranchRule)})
	waitingImport(t, d)
	hintGit(t, d.Repo, "branch", fullBranch, "main")
	hintGit(t, d.Repo, "config", "branch."+fullBranch+".dispatchTicket", "ENG-1")
	d.Linear.Tickets(cut("ENG-1", "In Review"))
	d.GitHub.PullRequestsOn("ENG-1", fullBranch)
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "SUCCESS", mergeable: "MERGEABLE"})

	d.Tick()

	require.Contains(t, d.LoopErr(), "dispatch: dispatch is not imported: branch other has half a stack record")
	assert.Equal(t, "branch "+fullBranch, lastReadingOf(d, "ENG-1"))
	assert.Contains(t, d.prReads(), fullBranch)
}
