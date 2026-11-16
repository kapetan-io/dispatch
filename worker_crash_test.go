package dispatch_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// leasedTo lists the trees of the dispatch repo's pool the ticket holds.
func (d *Dispatch) leasedTo(ticket string) []string {
	d.t.Helper()
	trees, err := d.tools.Treehouse.Trees(context.Background(), d.Repo)
	require.NoError(d.t, err)
	var held []string
	for _, tree := range trees {
		if tree.LeaseHolder == ticket {
			held = append(held, tree.Path)
		}
	}
	return held
}

// spareTree adds a second free tree to the pool Pooled made, which a launch that forgot its
// first lease would take.
func (d *Dispatch) spareTree() {
	tree := filepath.Join(d.dir, "spare")
	hintGit(d.t, d.Repo, "worktree", "add", "-q", "--detach", tree)
	d.tools.Treehouse.AddTree(d.Repo, tree)
}

// The kill switch does not count git, so the state a kill leaves between recording the tree and
// switching it to the branch is built by hand: the row and the lease agree and the tree is detached.
func TestALaunchKilledBeforeTheTreeWasSwitchedPutsItOnTheBranchAtTheRelaunch(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(launchRule)})
	tree := d.Trees.Pooled()
	d.spareTree()
	d.StartBeforeTheEvents()
	d.fakes().Treehouse.MoveLease(tree, "ENG-1")
	d.recordWorker("ENG-1", d.Repo, tree)

	require.NoError(t, d.launch("ENG-1", "br-eng-1").err)

	assert.Equal(t, []string{tree}, d.leasedTo("ENG-1"))
	assert.Equal(t, "br-eng-1", branchIn(t, tree))
}

// A worker whose rebase stopped on a conflict leaves its tree detached. The kickoff record says a
// worker ran there, so the relaunch leaves the tree as the worker left it.
func TestARelaunchLeavesATreeAWorkerDetachedAsItIs(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(launchRule)})
	tree := d.Trees.Pooled()
	d.StartBeforeTheEvents()
	d.fakes().Treehouse.MoveLease(tree, "ENG-1")
	d.recordWorker("ENG-1", d.Repo, tree)
	d.markLaunch(tree, d.Clock.Now())

	require.NoError(t, d.launch("ENG-1", "br-eng-1").err)

	assert.Empty(t, branchIn(t, tree))
	assert.Equal(t, []string{tree}, d.leasedTo("ENG-1"))
}

// A worker imported from before worker rows has no kickoff record. Its bisect, like a stopped
// rebase, marks the detached tree as its own, and the relaunch starts the worker where it stopped.
func TestARelaunchLeavesATreeStoppedPartWayThroughAGitOperationAsItIs(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(launchRule)})
	tree := d.Trees.Pooled()
	d.StartBeforeTheEvents()
	d.fakes().Treehouse.MoveLease(tree, "ENG-1")
	d.recordWorker("ENG-1", d.Repo, tree)
	hintGit(t, tree, "bisect", "start")

	require.NoError(t, d.launch("ENG-1", "br-eng-1").err)

	assert.Empty(t, branchIn(t, tree))
	assert.Equal(t, []string{tree}, d.leasedTo("ENG-1"))
}

// A tree a killed launch leased and never switched has nothing of a worker's in it, so a
// relaunch whose switch fails gives the lease back as the launch that took it would have.
func TestARelaunchWhoseSwitchFailsReturnsTheLeaseAKilledLaunchTook(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(launchRule)})
	tree := d.Trees.Pooled()
	d.StartBeforeTheEvents()
	d.fakes().Treehouse.MoveLease(tree, "ENG-1")
	d.recordWorker("ENG-1", d.Repo, tree)
	hintGit(t, d.Repo, "checkout", "-q", "-b", "br-eng-1")

	out := d.launch("ENG-1", "br-eng-1")

	assert.Equal(t, "switch_failed", out.result["status"])
	assert.Contains(t, out.result["detail"], "(lease returned)")
	assert.Empty(t, d.leasedTo("ENG-1"))
	assert.Empty(t, d.recordedTree("ENG-1"))
}

// A detached HEAD holding a commit no branch has is a worker's own work, so a relaunch leaves
// the tree alone even when the row has no kickoff record.
func TestARelaunchLeavesATreeWithCommitsNoBranchHoldsAsItIs(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(launchRule)})
	tree := d.Trees.Pooled()
	d.StartBeforeTheEvents()
	d.fakes().Treehouse.MoveLease(tree, "ENG-1")
	d.recordWorker("ENG-1", d.Repo, tree)
	hintGit(t, tree, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "the worker's own")

	out := d.launch("ENG-1", "br-eng-1")

	require.NoError(t, out.err)
	assert.Empty(t, branchIn(t, tree))
}

// A launch that returns the lease it took also clears the tree from its worker row, or the row
// would hold the tree against the next ticket to lease it.
func TestALaunchThatReturnsItsLeaseLeavesTheTreeToTheNextTicket(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(launchRule)})
	tree := d.Trees.Pooled()
	d.StartBeforeTheEvents()
	hintGit(t, d.Repo, "checkout", "-q", "-b", "br-eng-1")
	require.Equal(t, "switch_failed", d.launch("ENG-1", "br-eng-1").result["status"])

	out := d.launch("ENG-2", "br-eng-2")

	require.NoError(t, out.err)
	assert.Empty(t, d.recordedTree("ENG-1"))
	assert.Equal(t, tree, d.recordedTree("ENG-2"))
	assert.NotContains(t, d.Run("explain", "ENG-1", "--json").Stdout, "worker_gone")
}
