package dispatch_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
	"github.com/kapetan-io/dispatch/internal/kickoffs"
	"github.com/kapetan-io/dispatch/internal/ledger"
)

func TestAWorkerWhoseLaunchReturnedIsNotClosedWhateverItsAge(t *testing.T) {
	h := newHarness(t)
	h.builtinLauncher = true
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.pool()
	_, err := h.run("tick")
	require.NoError(t, err)
	require.Len(t, h.tools.Herdr.Kicked(), 1)
	h.tools.Herdr.SetStatus("w2:p2", "idle")

	h.advance(10 * time.Minute)
	h.tick()

	assert.Empty(t, h.tools.Herdr.ClosedWorkspaces())
	assert.Len(t, h.launches(), 0)
	assert.Len(t, h.tools.Herdr.Started(), 1)
}

func TestAWorkerWhoseLaunchDiedBeforeItsKickoffIsClosedAndRelaunched(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	tree := filepath.Join(h.dir, "trees", "ENG-1")
	markLaunch(t, h.state, tree, h.clock.Now())

	// A launch still inside its grace period may be about to type the kickoff.
	h.tick()
	assert.Empty(t, h.tools.Herdr.ClosedWorkspaces())
	assert.Empty(t, h.launches())

	h.advance(6 * time.Minute)
	h.tick()

	assert.Equal(t, []string{"w-ENG-1"}, h.tools.Herdr.ClosedWorkspaces())
	assert.Contains(t, h.loopOut(), "close ENG-1 closed workspace w-ENG-1: its launch died before the kickoff ran")
	h.advance(time.Second)
	h.tick()
	assert.Len(t, h.launches(), 1)
}

func TestAWorkerWhoseKickoffRanIsNotClosedWhateverItsAge(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")

	h.tick()
	h.advance(3 * time.Hour)
	h.tick()

	assert.Empty(t, h.tools.Herdr.ClosedWorkspaces())
	assert.Empty(t, h.launches())
}

func TestADesignReviewWorkerWhoseLaunchDiedIsClosedToo(t *testing.T) {
	h := newHarness(t)
	h.tickets(withLabel(ticket("ENG-1", "In Progress", "dispatch"), "design"))
	h.worker("ENG-1", "idle")
	markLaunch(t, h.state, filepath.Join(h.dir, "trees", "ENG-1"), h.clock.Now())

	h.tick()
	h.advance(6 * time.Minute)
	h.tick()

	assert.Equal(t, []string{"w-ENG-1"}, h.tools.Herdr.ClosedWorkspaces())
}

func TestADesignReviewWorkerWhoseLaunchDiedIsClosedAndRelaunched(t *testing.T) {
	h := newHarness(t)
	h.tickets(withLabel(ticket("ENG-1", "In Progress", "dispatch"), "design-review"))
	h.worker("ENG-1", "idle")
	markLaunch(t, h.state, filepath.Join(h.dir, "trees", "ENG-1"), h.clock.Now())

	h.advance(6 * time.Minute)
	h.tick()

	assert.Equal(t, []string{"w-ENG-1"}, h.tools.Herdr.ClosedWorkspaces())
	assert.Contains(t, h.loopOut(), "close ENG-1 closed workspace w-ENG-1: its launch died before the kickoff ran")
	h.advance(time.Second)
	h.tick()
	require.Len(t, h.launches(), 1)
	assert.Contains(t, h.launches()[0], "/dispatch:design-review ENG-1")
}

func TestClosingAnUnconfirmedWorkerClearsItsMark(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	tree := filepath.Join(h.dir, "trees", "ENG-1")
	markLaunch(t, h.state, tree, h.clock.Now())

	h.advance(6 * time.Minute)
	h.tick()

	_, left := kickoffs.Since(h.state, tree)
	assert.False(t, left)
}

func TestAnIdleWorkerWithAnUnconfirmedKickoffIsNotNudged(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	markLaunch(t, h.state, filepath.Join(h.dir, "trees", "ENG-1"), h.clock.Now())

	h.tick()
	h.advance(90 * time.Second)
	h.tick()

	assert.Empty(t, h.prompts())
	assert.Empty(t, h.tools.Herdr.ClosedWorkspaces())
}

// A launch that dies every time must end in Needs Human, not loop forever. The earlier
// close is seeded because a killed launch leaves a crashed receipt, which the limit ignores.
func TestALaunchThatKeepsDyingBeforeItsKickoffReachesNeedsHuman(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	tree := filepath.Join(h.dir, "trees", "ENG-1")
	earlier := h.clock.Now().Add(-30 * time.Minute).UnixMilli()
	h.seedHistory(nil, ledger.Receipt{
		Kind: "close", Subject: "ENG-1", Rule: "close-unconfirmed-worker", Outcome: ledger.Done,
		Once: ledger.Once{"close-unconfirmed", tree, earlier}, Started: earlier, Ended: earlier,
	})
	h.worker("ENG-1", "idle")
	markLaunch(t, h.state, tree, h.clock.Now())

	h.advance(6 * time.Minute)
	h.tick()

	assert.Equal(t, []string{"w-ENG-1"}, h.tools.Herdr.ClosedWorkspaces())
	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1"}, h.notifications())
	assert.Empty(t, h.launches())
}

func TestAStopFromTheWorkersOwnSessionConfirmsALaunchKilledAfterItsKickoffRan(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.recordStopFrom("worker", "ENG-1", "")
	markLaunch(t, h.state, filepath.Join(h.dir, "trees", "ENG-1"), h.clock.Now().Add(-time.Second))

	h.advance(10 * time.Minute)
	h.tick()

	assert.Empty(t, h.tools.Herdr.ClosedWorkspaces())
}

func TestAStopFromAnotherSessionInTheTreeDoesNotConfirmALaunch(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.recordStopFrom("someone-else", "ENG-1", "")
	markLaunch(t, h.state, filepath.Join(h.dir, "trees", "ENG-1"), h.clock.Now().Add(-time.Second))

	h.advance(10 * time.Minute)
	h.tick()

	assert.Equal(t, []string{"w-ENG-1"}, h.tools.Herdr.ClosedWorkspaces())
}

func TestAStopFromAnySessionConfirmsALaunchWhenHerdrNamesNoSession(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.setWorkerSession("ENG-1", "")
	h.recordStopFrom("worker", "ENG-1", "")
	markLaunch(t, h.state, filepath.Join(h.dir, "trees", "ENG-1"), h.clock.Now().Add(-time.Second))

	h.advance(10 * time.Minute)
	h.tick()

	assert.Empty(t, h.tools.Herdr.ClosedWorkspaces())
}

func TestAStopRecordThatCannotBeReadIsNotTakenForNoStop(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.recordStopFrom("worker", "ENG-1", "")
	records, err := filepath.Glob(filepath.Join(h.state, "stops", "*", "*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, records)
	for _, record := range records {
		require.NoError(t, os.WriteFile(record, []byte("not json"), 0o644))
	}
	markLaunch(t, h.state, filepath.Join(h.dir, "trees", "ENG-1"), h.clock.Now().Add(-time.Second))

	h.advance(10 * time.Minute)
	h.tick()

	assert.Empty(t, h.tools.Herdr.ClosedWorkspaces())
}

func TestAKickoffThatFailedWithTheWorkspaceStuckOpenIsClosedLater(t *testing.T) {
	h := newHarness(t)
	h.builtinLauncher = true
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.pool()
	h.tools.Herdr.Fail(herdrtest.OpKickoff, herdr.ErrKickoffUnconfirmed)
	h.tools.Herdr.Fail(herdrtest.OpCloseWorkspace, errors.New("herdr would not close it"))
	_, _ = h.run("tick")
	h.tools.Herdr.Fail(herdrtest.OpCloseWorkspace, nil)
	h.tools.Herdr.Fail(herdrtest.OpKickoff, nil)
	h.tools.Herdr.SetStatus("w2:p2", "idle")

	h.advance(10 * time.Minute)
	h.tick()

	assert.Equal(t, []string{"w2"}, h.tools.Herdr.ClosedWorkspaces())
}

func TestAnUnreadableStopBesideAReadableOneStillHoldsTheWorker(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.setWorkerSession("ENG-1", "")
	h.recordStopFrom("worker", "ENG-1", "")
	h.advance(time.Minute)
	h.recordStopFrom("other", "ENG-1", "")
	records, err := filepath.Glob(filepath.Join(h.state, "stops", "*", "*.json"))
	require.NoError(t, err)
	require.Len(t, records, 2)
	newest, newestAt := "", ""
	for _, record := range records {
		body, err := os.ReadFile(record)
		require.NoError(t, err)
		if string(body) > newestAt {
			newest, newestAt = record, string(body)
		}
	}
	require.NoError(t, os.WriteFile(newest, []byte("not json"), 0o644))
	markLaunch(t, h.state, filepath.Join(h.dir, "trees", "ENG-1"), h.clock.Now())

	h.advance(10 * time.Minute)
	h.tick()

	assert.Empty(t, h.tools.Herdr.ClosedWorkspaces())
}
