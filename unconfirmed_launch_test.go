package dispatch_test

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
	"github.com/kapetan-io/dispatch/internal/kickoffs"
	"github.com/kapetan-io/dispatch/internal/ledger"
)

// kickoffWatcher notes whether the launch had marked its kickoff unconfirmed when
// herdr was asked to type it.
type kickoffWatcher struct {
	*herdrtest.Herdr
	state, tree string
	marked      bool
}

func (k *kickoffWatcher) Kickoff(ctx context.Context, pane, text string) error {
	_, k.marked = kickoffs.Since(k.state, k.tree)
	return k.Herdr.Kickoff(ctx, pane, text)
}

// launchOver launches ENG-1 through h, recording the mark in state.
func markLaunch(t *testing.T, state, tree string, at time.Time) {
	t.Helper()
	_, err := kickoffs.Mark(state, tree, at)
	require.NoError(t, err)
}

func launchOver(l *launchHarness, h herdr.Herdr, state string) error {
	l.t.Helper()
	opts := dispatch.Options{Dir: l.repo, Herdr: h, Treehouse: l.tools.Treehouse, LaunchPace: time.Millisecond, StateDir: state, Stdout: io.Discard, Stderr: io.Discard}
	return dispatch.Run(context.Background(), []string{"launch", "ENG-1", "thrawn01/eng-1"}, opts)
}

func TestLaunchHoldsTheMarkOnlyUntilItReturns(t *testing.T) {
	l := newLaunchHarness(t)
	state := t.TempDir()
	watch := &kickoffWatcher{Herdr: l.tools.Herdr, state: state, tree: l.tree}

	require.NoError(t, launchOver(l, watch, state))

	assert.True(t, watch.marked)
	_, left := kickoffs.Since(state, l.tree)
	assert.False(t, left)
}

func TestLaunchClearsTheMarkWhenItClosesTheWorkspaceItself(t *testing.T) {
	l := newLaunchHarness(t)
	state := t.TempDir()
	l.tools.Herdr.Fail(herdrtest.OpKickoff, herdr.ErrKickoffUnconfirmed)

	require.Error(t, launchOver(l, l.tools.Herdr, state))

	assert.Equal(t, []string{"w2"}, l.tools.Herdr.ClosedWorkspaces())
	_, left := kickoffs.Since(state, l.tree)
	assert.False(t, left)
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

func TestALaunchNeverReleasesTheMarkOfALaunchThatReplacedIt(t *testing.T) {
	state, tree := t.TempDir(), t.TempDir()
	first, err := kickoffs.Mark(state, tree, time.UnixMilli(1000))
	require.NoError(t, err)
	_, err = kickoffs.Mark(state, tree, time.UnixMilli(2000))
	require.NoError(t, err)

	kickoffs.Release(state, tree, first)

	at, held := kickoffs.Since(state, tree)
	assert.True(t, held)
	assert.Equal(t, time.UnixMilli(2000), at)
}
