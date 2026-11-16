//go:build tests_slow

// Slow because each case restarts dispatch run, and a case passes one second on a busy machine.

package dispatch_test

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One worker is one session in one workspace, so a relaunch after a kill must not start a second
// session beside the one the killed launch started.
func TestALaunchKilledBetweenAnyTwoStepsNeverStartsASecondSession(t *testing.T) {
	t.Parallel()
	calls := launchCalls(t)

	for at := 1; at <= calls; at++ {
		t.Run("KilledAtCall"+strconv.Itoa(at), func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(launchRule)})
			d.Trees.Pooled()
			d.StartBeforeTheEvents()
			d.fakes().Kill.ThrowAt(at)
			d.launch("ENG-1", "br-eng-1")

			d.fakes().Kill.ThrowAt(0)
			d.Restart()
			d.Tick()
			d.Tick()

			require.Len(t, d.fakes().Multiplexer.Started(), 1)
			// A kill between starting the harness and typing leaves a session with no kickoff.
			assert.LessOrEqual(t, len(d.fakes().Multiplexer.Kicked()), 1)
			_, found := d.kickoffRecord("ENG-1")
			assert.True(t, found)
		})
	}
}
