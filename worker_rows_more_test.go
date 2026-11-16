package dispatch_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// launchPerTitle queues a launch for each title an In Progress ticket has had, whatever worker it
// has, so a test can ask for a second launch of a ticket whose worker is running.
const launchPerTitle = `
whenever({ name: "launch", each: "ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.worker.queueLaunch(t, { once: ["launch", t.id, t.title] }) });`

// A rule that asks for a launch beside a worker on record is stopped before the launcher runs, so
// the launcher's already_running is left for a session no read has shown yet.
func TestASecondLaunchOfATicketWhoseWorkerIsRunningIsSkippedAndLeasesNoSecondTree(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(launchPerTitle)})
	tree := d.Trees.Pooled()
	d.StartBeforeTheEvents()
	require.NoError(t, d.launch("ENG-1", "br-eng-1", "--title", "first").err)

	second := d.launch("ENG-1", "br-eng-1", "--title", "second")

	require.EqualError(t, second.err, "no launch ran")
	assert.Contains(t, d.loopErr(), "dispatch: not launching ENG-1: a worker is running\n")
	assert.Equal(t, []string{tree}, d.leasedTo("ENG-1"))
	assert.Len(t, d.fakes().Calls("treehouse", "lease"), 1)
	assert.Equal(t, tree, d.recordedTree("ENG-1"))
	rows, _ := d.recordedStops()
	assert.Equal(t, 1, rows)
	assert.Len(t, d.fakes().Multiplexer.Started(), 1)
	assert.Len(t, d.fakes().Multiplexer.Kicked(), 1)
}

func TestALaunchRecordsTheKickoffItTypedAndTheSessionThatReceivedIt(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.StartBeforeTheEvents()

	require.NoError(t, d.launch("ENG-1", "br-eng-1").err)

	require.Len(t, d.fakes().Multiplexer.Started(), 1)
	session := d.fakes().Multiplexer.Started()[0].Program.Session
	require.NotEmpty(t, session)
	kickoff, found := d.kickoffRecord("ENG-1")
	require.True(t, found)
	assert.Equal(t, "/dispatch:build ENG-1", kickoff.Text)
	assert.Equal(t, session, kickoff.Session)
	assert.Equal(t, d.Clock.Now().UnixMilli(), kickoff.At)
	assert.False(t, kickoff.Open)

	// The record stays as it is while the worker runs, across a restart of dispatch run.
	d.Restart()
	d.Tick()

	kept, found := d.kickoffRecord("ENG-1")
	require.True(t, found)
	assert.Equal(t, kickoff, kept)
	assert.Len(t, d.fakes().Multiplexer.Started(), 1)
}

// Call 3 of a launch is the write that records the leased tree, so a launch killed there leaves a
// lease its ticket holds and a worker row with no tree.
func TestATreeLeasedByAKilledLaunchIsRecordedInItsWorkerRowByTheNextPoolRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(launchRule)})
	tree := d.Trees.Pooled()
	d.StartBeforeTheEvents()
	d.fakes().Kill.ThrowAt(3)
	require.Error(t, d.launch("ENG-1", "br-eng-1").err)
	d.fakes().Kill.ThrowAt(0)
	require.Equal(t, []string{tree}, d.leasedTo("ENG-1"))
	require.Empty(t, d.recordedTree("ENG-1"))

	// No relaunch comes, because the ticket is finished.
	d.Linear.Tickets()
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	d.Tick()

	assert.Equal(t, tree, d.recordedTree("ENG-1"))
	assert.Equal(t, []string{tree}, d.leasedTo("ENG-1"))
	assert.NotContains(t, d.loopErr(), "no worker record")
	assert.Len(t, d.launchesEnded("ENG-1"), 1)
}

// Call 2 of a launch is the lease, so a launch killed there leaves a worker row with no tree and
// no lease.
func TestAWorkerRowWithNoTreeAndNoLeaseIsKeptUntilItsTicketIsFinished(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(launchRule)})
	d.Trees.Pooled()
	d.StartBeforeTheEvents()
	d.fakes().Kill.ThrowAt(2)
	require.Error(t, d.launch("ENG-1", "br-eng-1").err)
	d.fakes().Kill.ThrowAt(0)
	require.Empty(t, d.leasedTo("ENG-1"))

	// A ticket dispatch still tracks may be launched again, and that launch reuses the row.
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Tick()

	rows, _ := d.recordedStops()
	assert.Equal(t, 1, rows)

	d.Linear.Tickets()
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	d.Tick()

	rows, _ = d.recordedStops()
	assert.Zero(t, rows)
	assert.Empty(t, d.leasedTo("ENG-1"))
	assert.Len(t, d.launchesEnded("ENG-1"), 1)
}

func TestATreeReturnedOutsideDispatchTakesTheWorkersStopRecordsWithIt(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(workerReport)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	tree := d.Trees.PathOf("ENG-1")
	d.StartBeforeTheEvents()
	d.Workers.StopFrom("worker", "ENG-1", stopTasks{})
	rows, stops := d.recordedStops()
	require.Equal(t, 1, rows)
	require.Equal(t, 1, stops)
	require.NoError(t, d.fakes().Treehouse.Return(context.Background(), d.Repo, tree, "ENG-1"))

	d.Tick()

	rows, stops = d.recordedStops()
	assert.Zero(t, rows)
	assert.Zero(t, stops)
	files, err := filepath.Glob(filepath.Join(d.state, "events", "*"))
	require.NoError(t, err)
	require.Len(t, files, 1)
	raw, err := os.ReadFile(files[0])
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"type":"worker_gone","subject":"ENG-1","data":{"ticket":"ENG-1","repo":"dispatch","tree":"`+tree+`","why":"tree `+tree+` is no longer leased to ENG-1"}`)
	assert.Contains(t, d.Linear.Comments(), "ENG-1: worker none")

	// A Stop the worker's session reports after its row went has no worker to belong to.
	d.Workers.StopFrom("worker", "ENG-1", stopTasks{Running: 1})
	rows, stops = d.recordedStops()
	assert.Zero(t, rows)
	assert.Zero(t, stops)
}

func TestWhileThePoolCannotBeReadAWorkersRulesAreHeldAndItsRowStillTakesAStop(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(workerReport)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Tick()
	tree := d.recordedTree("ENG-1")
	require.NotEmpty(t, tree)
	before := d.Linear.Comments()
	down := errors.New("treehouse is down")
	d.fakes().Treehouse.FailTrees(down, down, down, down)

	out := d.Run("tick").Stdout
	d.Workers.StopFrom("worker", "ENG-1", stopTasks{})

	assert.Contains(t, out, "ENG-1 held: treehouse pool dispatch unread")
	assert.Equal(t, before, d.Linear.Comments())
	assert.Equal(t, tree, d.recordedTree("ENG-1"))
	rows, stops := d.recordedStops()
	assert.Equal(t, 1, rows)
	assert.Equal(t, 1, stops)
}

func TestAStartWithBothDatabaseFilesUsesDispatchDbAndNamesTheOldFile(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.ticketOut("create", "local", "--repo", "dispatch", "--title", "kept")
	d.Stop()
	old := filepath.Join(d.state, "tickets.db")
	require.NoError(t, os.WriteFile(old, []byte("left by an earlier dispatch"), 0o600))

	d.StartBeforeTheEvents()

	assert.Contains(t, d.ticketOut("list"), "kept")
	assert.Contains(t, d.loopErr(), "dispatch: "+d.databasePath()+" is in use and "+old+" beside it is not read; remove "+old+" once nothing in it is needed\n")
	raw, err := os.ReadFile(old)
	require.NoError(t, err)
	assert.Equal(t, "left by an earlier dispatch", string(raw))
}

// A command that runs while dispatch run is stopped never writes, so it reads the old file where
// it is and leaves the rename to the next start.
func TestRulesCheckBeforeTheFirstStartReadsTheOldLocalTicketFileAndLeavesItInPlace(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn, Rules: rule(`
whenever({ name: "seen", each: "ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.ticket.queueComment(t, "seen", { once: ["seen", t.id] }) });`)})
	d.StartBeforeTheEvents()
	id := d.fileLocal("In Progress")
	d.Stop()
	old := filepath.Join(d.state, "tickets.db")
	require.NoError(t, os.Rename(d.databasePath(), old))

	res := d.RunWhileDown("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `seen +whenever +ticket +.*`+id, res.Stdout)
	assert.FileExists(t, old)
	assert.NoFileExists(t, d.databasePath())
}

// The commit hook fails the import's write, the one fault that leaves a repo's workers not
// imported after a pool read that succeeded.
func TestALaunchWaitsUntilTheReposWorkersAreImportedAndCountsAsNoFailure(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.fakes().Kill.ThrowAt(1)
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	d.Run("tick")
	d.Advance(time.Minute)
	d.Run("tick")
	d.Advance(time.Minute)
	d.Run("tick")

	assert.Empty(t, d.Workers.Launched())
	assert.Empty(t, d.launchesEnded("ENG-1"))
	assert.Empty(t, d.Linear.Moves())
	assert.Contains(t, d.loopErr(), "dispatch: not launching ENG-1: the workers of dispatch are not imported yet\n")
	assert.Contains(t, d.loopErr(), "dispatch: while importing the workers of dispatch: ")

	d.fakes().Kill.ThrowAt(0)
	d.Tick()

	assert.Len(t, d.Workers.Launched(), 1)
}

// A renamed repo label leaves the worker's row under the old one. The row follows its tree to
// the new label with its records, so the worker is still the ticket's and its lease is no stray.
func TestAWorkerKeepsItsRowWhenItsRepoLabelIsRenamed(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(commandsRule)})
	tree := filepath.Join(d.dir, "library-tree")
	d.Workers.RunningInLibrary("ENG-1", tree, "w-library", "idle")
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "library"))
	d.StartBeforeTheEvents()
	d.Tick()
	require.Equal(t, tree, d.recordedTree("ENG-1"))
	d.repos[1].label = "shelf"
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "shelf"))
	d.Restart()

	d.Tick()

	assert.Equal(t, tree, d.recordedTree("ENG-1"))
	assert.Equal(t, []string{"shelf"}, d.recordedRepos("ENG-1"))
	assert.NotContains(t, d.loopErr(), "with no worker row")
	assert.NotContains(t, d.Run("explain", "ENG-1", "--json").Stdout, "worker_gone")
}

// A repo added to the config while dispatch is stopped has no imported workers yet, so a command
// run then finds every worker by its lease, as the next start's import will.
func TestRulesCheckWhileStoppedFindsWorkersByLeaseUntilEveryRepoIsImported(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: rule(`
whenever({ name: "has-worker", each: "ticket", if: (t, ctx) => ctx.worker.of(t).state !== "none",
  then: (t, ctx) => ctx.ticket.queueComment(t, "has a worker", { once: ["has-worker", t.id] }) });`)})
	d.Linear.Tickets(ticket("ENG-2", "In Progress", "library"))
	d.StartBeforeTheEvents()
	d.Tick()
	d.Stop()
	d.unrecordedWorker(d.libraryRepo(), "ENG-2", filepath.Join(d.dir, "library-tree"), "w-ENG-2", "idle")

	stopped := d.RunWhileDown("rules", "check")

	require.NoError(t, stopped.Err)
	assert.Regexp(t, `has-worker +whenever +ticket +.*ENG-2`, stopped.Stdout)
}
