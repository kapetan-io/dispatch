//go:build tests_slow

// Slow because each case restarts dispatch run, and a case passes one second on a busy machine.

package dispatch_test

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// launchCalls is how many counted calls one launch makes: the commits to dispatch.db and the
// calls that lease, open, start and type.
func launchCalls(t *testing.T) int {
	t.Helper()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(launchRule)})
	d.Trees.Pooled()
	d.StartBeforeTheEvents()
	d.fakes().Kill.ThrowAt(0)
	require.NoError(t, d.launch("ENG-1", "br-eng-1").err)
	return d.fakes().Kill.Count()
}

func TestALaunchKilledBetweenAnyTwoStepsLeavesOneWorkerOnOneTreeAfterARestart(t *testing.T) {
	t.Parallel()
	calls := launchCalls(t)
	require.Equal(t, 9, calls)

	for at := 1; at <= calls; at++ {
		t.Run("KilledAtCall"+strconv.Itoa(at), func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(launchRule)})
			tree := d.Trees.Pooled()
			d.spareTree()
			d.StartBeforeTheEvents()
			d.fakes().Kill.ThrowAt(at)
			d.launch("ENG-1", "br-eng-1")

			d.fakes().Kill.ThrowAt(0)
			d.Restart()
			d.Tick()

			assert.Equal(t, []string{tree}, d.leasedTo("ENG-1"))
			assert.Equal(t, tree, d.recordedTree("ENG-1"))
			assert.Equal(t, "br-eng-1", branchIn(t, tree))
			rows, _ := d.recordedStops()
			assert.Equal(t, 1, rows)
			assert.Len(t, d.fakes().Multiplexer.Opened(), 1)
			assert.NotEmpty(t, d.fakes().Multiplexer.Started())
		})
	}
}

// returnCalls is how many counted calls the return of a finished ticket's worker makes.
func returnCalls(t *testing.T) int {
	t.Helper()
	d := newDispatch(t, dispatchOptions{})
	d.Trees.Clean("ENG-1")
	d.Workers.Running("ENG-1", "idle")
	d.StartBeforeTheEvents()
	d.fakes().Kill.ThrowAt(0)
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	d.Tick()
	require.Len(t, d.Trees.Returned(), 1)
	return d.fakes().Kill.Count()
}

func TestAReturnKilledBetweenAnyTwoStepsLeavesNoWorkerAndAFreeTreeAfterARestart(t *testing.T) {
	t.Parallel()
	calls := returnCalls(t)
	require.Equal(t, 3, calls)

	for at := 1; at <= calls; at++ {
		t.Run("KilledAtCall"+strconv.Itoa(at), func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{})
			tree := d.Trees.Clean("ENG-1")
			d.Workers.Running("ENG-1", "idle")
			d.StartBeforeTheEvents()
			d.Workers.StopFrom("worker", "ENG-1", stopTasks{})
			d.fakes().Kill.ThrowAt(at)
			d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
			d.Run("tick")

			d.fakes().Kill.ThrowAt(0)
			d.Restart()
			d.Tick()

			assert.Empty(t, d.leasedTo("ENG-1"))
			assert.Equal(t, "available", d.fakes().Treehouse.Status(tree))
			rows, stops := d.recordedStops()
			assert.Zero(t, rows)
			assert.Zero(t, stops)

			// A Stop the worker's session reports after the return has no worker to belong to.
			d.Workers.StopFrom("worker", "ENG-1", stopTasks{Running: 1})
			rows, stops = d.recordedStops()
			assert.Zero(t, rows)
			assert.Zero(t, stops)
		})
	}
}
