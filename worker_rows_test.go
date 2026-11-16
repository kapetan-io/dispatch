package dispatch_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/treehouse"
)

// workerReport comments each In Progress ticket's worker state whenever it changes.
const workerReport = `
whenever({ name: "where", each: "ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => {
    const body = "worker " + ctx.worker.of(t).state;
    return ctx.ticket.queueComment(t, body, { once: ["where", t.id, body] });
  } });`

func strayWarning(ticket, tree string) string {
	return "dispatch: " + ticket + " holds tree " + tree + " in dispatch with no worker record; dispatch will not return it. A launch of " + ticket + " uses it, or return it with treehouse.\n"
}

func TestLaunchIsNoLongerACommand(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})

	res := d.Run("launch", "ENG-1", "br-eng-1")

	require.EqualError(t, res.Err, `unknown command "launch"`)
	assert.NotZero(t, res.Code)
}

func TestATreeReturnedOutsideDispatchEndsTheWorkerAndTheRelaunchUsesTheSameBranch(t *testing.T) {
	t.Parallel()
	// The shipped launch rule keys a launch by when the worker went, so it launches again.
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	d.StartBeforeTheEvents()
	first := d.launch("ENG-1", "br-eng-1")
	require.NoError(t, first.err)
	require.NoError(t, d.fakes().Multiplexer.CloseWorkspace(context.Background(), first.result["workspace"]))
	require.NoError(t, d.fakes().Treehouse.Return(context.Background(), d.Repo, tree, "ENG-1"))

	d.Tick()

	why := "tree " + tree + " is no longer leased to ENG-1"
	assert.Contains(t, d.Run("explain", "ENG-1").Stdout, "worker gone  "+why)
	assert.Contains(t, d.Run("explain", "ENG-1", "--json").Stdout, `"type":"worker_gone"`)
	assert.Equal(t, []string{tree}, d.leasedTo("ENG-1"))
	assert.Equal(t, tree, d.recordedTree("ENG-1"))
	assert.Equal(t, "br-eng-1", branchIn(t, tree))
	assert.Len(t, d.launchesEnded("ENG-1"), 2)
}

func TestALeaseTakenByHandIsWarnedAboutAtEachResyncAndALaunchRunsInIt(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(launchRule + workerReport)})
	tree := d.Trees.Pooled()
	d.spareTree()
	d.StartBeforeTheEvents()
	d.fakes().Treehouse.MoveLease(tree, "ENG-1")
	d.Linear.Tickets(ticket("ENG-1", "Todo", "dispatch"))

	d.Tick()
	d.Advance(time.Minute)
	d.Tick()

	assert.GreaterOrEqual(t, strings.Count(d.loopErr(), strayWarning("ENG-1", tree)), 2)
	rows, _ := d.recordedStops()
	assert.Zero(t, rows)

	require.NoError(t, d.launch("ENG-1", "br-eng-1").err)

	assert.Equal(t, []string{tree}, d.leasedTo("ENG-1"))
	assert.Equal(t, tree, d.recordedTree("ENG-1"))
	assert.Contains(t, d.Linear.Comments(), "ENG-1: worker none")
}

func TestALeaseTakenByHandIsNeverReturned(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	tree := d.Trees.Clean("ENG-1")
	d.StartBeforeTheEvents()
	d.fakes().Treehouse.Seed(d.Repo, treehouse.Tree{Path: tree, Status: "leased", LeaseHolder: "ENG-1"})
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	d.Run("tick")
	d.Advance(time.Minute)
	d.Run("tick")

	assert.Empty(t, d.Trees.Returned())
	assert.Equal(t, "ENG-1", d.Trees.Holder(tree))
	assert.Contains(t, d.loopErr(), strayWarning("ENG-1", tree))
	rows, _ := d.recordedStops()
	assert.Zero(t, rows)
}

func TestAPoolThatCannotBeReadChangesNoWorkerRow(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Tick()
	tree := d.recordedTree("ENG-1")
	require.NotEmpty(t, tree)
	down := errors.New("treehouse is down")
	d.fakes().Treehouse.FailTrees(down, down, down, down)

	d.Run("tick")
	d.Advance(time.Minute)
	d.Run("tick")

	assert.Equal(t, tree, d.recordedTree("ENG-1"))
	assert.NotContains(t, d.Run("explain", "ENG-1", "--json").Stdout, "worker_gone")
	assert.Empty(t, d.Workers.Launched())
	assert.Empty(t, d.Trees.Returned())
	assert.Empty(t, d.Workers.Closed())
}

func TestRunRefusesToStartOnADatabaseItCannotOpen(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := filepath.Join(d.state, "dispatch.db")
	require.NoError(t, os.MkdirAll(d.state, 0o700))
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("not a database ", 1024)), 0o600))

	err := d.startLoop(dispatch.Options{})

	require.ErrorContains(t, err, path)
}

func TestALaunchWhoseWorkerRowCannotBeWrittenFailsAndIsWantedAgain(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(launchRule)})
	tree := d.Trees.Pooled()
	d.StartBeforeTheEvents()
	d.fakes().Kill.ThrowAt(1)

	failed := d.launch("ENG-1", "br-eng-1")

	require.Error(t, failed.err)
	assert.Equal(t, "worker_row_failed", failed.result["status"])
	assert.Empty(t, d.leasedTo("ENG-1"))

	d.fakes().Kill.ThrowAt(0)
	again := d.launch("ENG-1", "br-eng-1")

	require.NoError(t, again.err)
	assert.Equal(t, tree, again.result["worktree"])
}

func TestLaunchesThatCannotWriteTheWorkerRowCountTowardTheFailureLimit(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.StartBeforeTheEvents()
	d.fakes().Kill.ThrowAt(1)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	d.Run("tick")
	d.Advance(time.Minute)
	d.Run("tick")
	d.Advance(time.Minute)
	d.Run("tick")

	assert.Empty(t, d.Workers.Launched())
	assert.Equal(t, []string{"ENG-1 -> Needs Human"}, d.Linear.Moves())
}

func TestRulesCheckBesideARunningDispatchReadsTheWorkersItReads(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: rule(`
whenever({ name: "has-worker", each: "ticket", if: (t, ctx) => ctx.worker.of(t).state !== "none",
  then: (t, ctx) => ctx.ticket.queueComment(t, "has a worker", { once: ["has-worker", t.id] }) });`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.StartBeforeTheEvents()
	// ENG-2 has the lease, the workspace and the session of a worker, and no worker row.
	d.unrecordedWorker(d.Repo, "ENG-2", d.Trees.PathOf("ENG-2"), "w-ENG-2", "idle")
	d.Tick()
	require.Equal(t, []string{"ENG-1: has a worker"}, d.Linear.Comments())

	beside := d.Run("rules", "check")

	require.NoError(t, beside.Err)
	assert.Regexp(t, `has-worker +whenever +ticket +.*ENG-1`, beside.Stdout)
	assert.NotRegexp(t, `has-worker +whenever +ticket +.*ENG-2`, beside.Stdout)

	d.Stop()
	stopped := d.RunWhileDown("rules", "check")

	require.NoError(t, stopped.Err)
	assert.Regexp(t, `has-worker +whenever +ticket +.*ENG-1`, stopped.Stdout)
	assert.NotRegexp(t, `has-worker +whenever +ticket +.*ENG-2`, stopped.Stdout)
}

func TestTheFirstStartGivesAWorkerInFlightItsRowFromTheOldFiles(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	tree := d.Trees.PathOf("ENG-1")
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.unrecordedWorker(d.Repo, "ENG-1", tree, "w-ENG-1", "idle")
	now := d.Clock.Now()
	d.oldWorkerFiles(tree, "worker", now.Add(-time.Hour), now.Add(-30*time.Minute), now.Add(-time.Minute))

	d.StartBeforeTheEvents()
	d.Tick()

	assert.Equal(t, tree, d.recordedTree("ENG-1"))
	rows, stops := d.recordedStops()
	assert.Equal(t, 1, rows)
	assert.Equal(t, 1, stops)
	assert.Len(t, d.closings(false), 1)
	began, open := d.launchRecord(tree)
	assert.True(t, open)
	assert.Equal(t, now.Add(-30*time.Minute).UnixMilli(), began.UnixMilli())
	assert.Empty(t, d.Workers.Launched())
	assert.NotContains(t, d.loopErr(), "no worker record")
	assert.Empty(t, d.oldWorkerFilesLeft())

	// A second start finds the repo imported and changes nothing.
	d.Restart()
	d.Tick()

	rows, stops = d.recordedStops()
	assert.Equal(t, 1, rows)
	assert.Equal(t, 1, stops)
	assert.Empty(t, d.Workers.Launched())
	assert.NotContains(t, d.loopErr(), "no worker record")
}

func TestTheFirstStartRenamesTheLocalTicketFile(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.ticketOut("create", "local", "--repo", "dispatch", "--title", "kept")
	d.Stop()
	old := filepath.Join(d.state, "tickets.db")
	require.NoError(t, os.Rename(d.databasePath(), old))

	d.StartBeforeTheEvents()

	assert.Contains(t, d.ticketOut("list"), "kept")
	assert.FileExists(t, d.databasePath())
	assert.NoFileExists(t, old)
}
