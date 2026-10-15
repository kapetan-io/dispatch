package dispatch_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/launcher"
	"github.com/kapetan-io/dispatch/internal/ledger"
	"github.com/kapetan-io/dispatch/internal/lineartest"
	"github.com/kapetan-io/dispatch/internal/loop"
	"github.com/kapetan-io/dispatch/internal/treehouse"
)

// loopRunWithoutLoop runs a command in a state directory where no loop runs.
func loopRunWithoutLoop(h *harness, args ...string) (string, error) {
	h.t.Helper()
	var stdout bytes.Buffer
	opts := h.options(dispatch.Options{})
	opts.Stdout, opts.Stderr = &stdout, &stdout
	err := dispatch.Run(h.t.Context(), args, opts)
	return stdout.String(), err
}

// loopBlockedLauncher holds every launch until release closes.
type loopBlockedLauncher struct{ release chan struct{} }

func (b loopBlockedLauncher) Launch(ctx context.Context, r launcher.Request) launcher.Result {
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return launcher.Result{Status: launcher.StatusOK, Ticket: r.Ticket, Branch: r.Branch}
}

// loopWorkerLauncher starts a worker herdr can show, as the real launcher does, so a later
// read counts it against max_in_progress.
type loopWorkerLauncher struct{ h *harness }

func (l loopWorkerLauncher) Launch(_ context.Context, r launcher.Request) launcher.Result {
	l.h.worker(r.Ticket, "working")
	return launcher.Result{Status: launcher.StatusOK, Ticket: r.Ticket, Branch: r.Branch}
}

func TestTellAndTickExitThreeWhenNoLoopRuns(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
	}{
		{"Tell", []string{"tell", "ENG-1", "hello"}},
		{"Tick", []string{"tick"}},
		{"TickOneTicket", []string{"tick", "ENG-1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)

			_, err := loopRunWithoutLoop(h, test.args...)

			require.ErrorContains(t, err, "not running")
			assert.Equal(t, 3, dispatch.ExitCode(err))
		})
	}
}

func TestTickPrintsOneLinePerTicketInPlansWords(t *testing.T) {
	h := newHarness(t)
	h.config("[dispatch]\nmax_in_progress = 3\n")
	h.addRepo("tiger", filepath.Join(h.dir, "moved"))
	h.tickets(
		ticket("ENG-1", "In Progress", "dispatch"),
		ticket("ENG-2", "In Progress", "dispatch"),
		ticket("ENG-3", "In Progress", "tiger"),
		ticket("ENG-9", "In Progress", "dispatch"),
	)
	h.worker("ENG-9", "working")
	h.builtinLauncher = true

	out, _, err := h.runWith(context.Background(), dispatch.Options{Launcher: loopWorkerLauncher{h}}, "tick")
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, "ENG-1 launched", lines[0])
	assert.Regexp(t, `^ENG-2 queued: launch, 3 of 3 in progress, 1st in line$`, lines[1])
	assert.Regexp(t, `^ENG-3 held: treehouse pool tiger unread`, lines[2])
}

func TestTickOfOneTicketPrintsOnlyThatTicket(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "Needs Human", "dispatch"))

	out, err := h.run("tick", "eng-2")
	require.NoError(t, err)

	assert.Equal(t, "ENG-2 notified\n", out)
}

func TestTickExitsOneAndPrintsTheFailedAction(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.launchResult(`{"status":"pool_error","detail":"treehouse lease failed"}`)

	out, err := h.run("tick")

	require.ErrorContains(t, err, "ENG-1 launch failed: pool_error: treehouse lease failed")
	assert.Equal(t, 1, dispatch.ExitCode(err))
	assert.Equal(t, "ENG-1 launch failed: pool_error: treehouse lease failed\n", out)
}

func TestTickTimeoutSaysWhatIsStillPending(t *testing.T) {
	h := newHarness(t)
	h.builtinLauncher = true
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	blocked := loopBlockedLauncher{release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	opts := h.options(dispatch.Options{Launcher: blocked})
	opts.Stdout, opts.Stderr = &bytes.Buffer{}, &bytes.Buffer{}
	go func() { done <- dispatch.Run(ctx, []string{"run"}, opts) }()
	require.Eventually(t, func() bool {
		_, err := loop.Ask(ctx, h.state, loop.Request{Op: loop.OpPending})
		return err == nil
	}, 20*time.Second, 5*time.Millisecond)

	var stdout bytes.Buffer
	tickOpts := h.options(dispatch.Options{})
	tickOpts.Stdout, tickOpts.Stderr = &stdout, &bytes.Buffer{}
	err := dispatch.Run(ctx, []string{"tick", "--timeout", "300ms"}, tickOpts)

	require.ErrorContains(t, err, "timed out after 300ms")
	assert.Equal(t, 1, dispatch.ExitCode(err))
	assert.Contains(t, stdout.String(), "ENG-1 launch is still running (launch-worker)")
	close(blocked.release)
	cancel()
	require.NoError(t, <-done)
}

func TestPlanExitsOneWithNoHistory(t *testing.T) {
	h := newHarness(t)

	_, err := loopRunWithoutLoop(h, "plan")

	require.ErrorContains(t, err, "there is no history to read")
	assert.Equal(t, 1, dispatch.ExitCode(err))
}

func TestPlanPrintsStateTicketActionRuleAndDetail(t *testing.T) {
	h := newHarness(t)
	h.config("[dispatch]\nmax_in_progress = 2\n")
	h.tickets(
		ticket("ENG-1", "In Progress", "dispatch"),
		ticket("ENG-2", "In Progress", "dispatch"),
		ticket("ENG-3", "In Progress", "dispatch"),
	)
	h.worker("ENG-3", "working")
	h.builtinLauncher = true
	_, _, err := h.runWith(context.Background(), dispatch.Options{Launcher: loopWorkerLauncher{h}}, "tick")
	require.NoError(t, err)

	out, err := h.run("plan")

	require.NoError(t, err)
	assert.Regexp(t, `(?m)^waiting +ENG-2 +launch +launch-worker +2 of 2 in progress, 1st in line$`, out)
	assert.NotContains(t, out, "ENG-1")
}

func TestPlanShowsALaunchThatHasAReceiptAsCovered(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.tick()

	out, err := h.run("plan")

	require.NoError(t, err)
	assert.Regexp(t, `(?m)^covered +ENG-1 +launch +launch-worker +done \d\d:\d\d once \[launch `, out)
}

func TestPlanHoldsATicketWhoseRepoPoolCouldNotBeRead(t *testing.T) {
	h := newHarness(t)
	h.addRepo("tiger", filepath.Join(h.dir, "moved"))
	h.tickets(ticket("ENG-2", "In Progress", "tiger"))
	// A worker may already run in the repo whose pool nobody could read.
	h.tools.Herdr.AddWorkspace(herdr.Workspace{ID: "w-ENG-2", Label: "ENG-2", CheckoutPath: filepath.Join(h.dir, "trees", "ENG-2")})
	h.tick()
	h.advance(time.Second)
	h.tick()

	out, err := h.run("plan")

	require.NoError(t, err)
	assert.Empty(t, h.launches())
	assert.Regexp(t, `(?m)^held +ENG-2 +- +launch-worker +treehouse pool tiger unread: `, out)
}

func TestLoopRestartAfterALaunchLaunchesNoSecondWorker(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.tick()
	require.Len(t, h.launches(), 1)

	// The fake launcher starts no worker herdr could show, as a launch whose workspace is not up yet.
	h.advance(time.Second)
	h.restart()
	out := h.tick()

	assert.Len(t, h.launches(), 1)
	assert.NotContains(t, out, "launched")
}

func TestARuleThatThrowsStopsOnlyItsOwnActionsAndRaisesOneAlert(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	entries, err := os.ReadDir(h.rulesDir)
	require.NoError(t, err)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".ts") || strings.HasSuffix(e.Name(), ".test.ts") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(h.rulesDir, e.Name()))
		require.NoError(t, err)
		if e.Name() == "dispatch.ts" {
			body = append(body, "\nimport \"./throws\";\n"...)
		}
		require.NoError(t, os.WriteFile(filepath.Join(dir, e.Name()), body, 0o644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "throws.ts"), []byte(`import { whenever } from "dispatch";
whenever({ name: "always-throws", each: "linear.ticket", if: () => { throw new Error("boom"); }, then: () => {} });
`), 0o644))
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	_, _, err = h.runWith(context.Background(), dispatch.Options{RulesDir: dir}, "tick")
	require.NoError(t, err)
	h.advance(time.Second)
	h.tick()

	assert.Len(t, h.launches(), 1)
	assert.Contains(t, h.loopErr(), "always-throws threw in its if for ENG-1")
	var alerts []string
	for _, shown := range h.notifications() {
		if strings.Contains(shown, "always-throws") {
			alerts = append(alerts, shown)
		}
	}
	assert.Len(t, alerts, 1)
}

func TestAHistoryWriteFailureStopsTheLoopAndAlerts(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so the write cannot be made to fail")
	}
	h := newHarness(t)
	h.config("[history]\nrotate = \"1h\"\nkeep = \"24h\"\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.tick()
	events := filepath.Join(h.state, "events")
	require.NoError(t, os.Chmod(events, 0o500))
	t.Cleanup(func() { _ = os.Chmod(events, 0o700) })
	running := h.running

	// The open segment still takes lines, so the failure comes when the loop rotates into a new one.
	h.clock.Advance(time.Hour)

	var err error
	require.Eventually(t, func() bool {
		select {
		case err = <-running.done:
			h.running = nil
			return true
		default:
			return false
		}
	}, 20*time.Second, 5*time.Millisecond)
	require.ErrorContains(t, err, "permission denied")
	require.Eventually(t, func() bool {
		for _, shown := range h.notifications() {
			if strings.HasPrefix(shown, "dispatch stopped: ") {
				return true
			}
		}
		return false
	}, 5*time.Second, 5*time.Millisecond)
	assert.Contains(t, running.err.String(), "dispatch: stopping:")
	_, tickErr := loopRunWithoutLoop(h, "tick")
	assert.Equal(t, 3, dispatch.ExitCode(tickErr))
}

func TestTheIdleCountRisesAfterATickAndHoldsStillWithoutEvents(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.tick()
	before, err := loop.Ask(t.Context(), h.state, loop.Request{Op: loop.OpIdle})
	require.NoError(t, err)

	h.tick()

	after, err := loop.Ask(t.Context(), h.state, loop.Request{Op: loop.OpIdle})
	require.NoError(t, err)
	assert.Greater(t, after.Idle, before.Idle)
	assert.False(t, after.Busy)
	again, err := loop.Ask(t.Context(), h.state, loop.Request{Op: loop.OpIdle})
	require.NoError(t, err)
	assert.Equal(t, after.Idle, again.Idle)
}

func TestAnIdleHourCostsThreeLinearQueriesPerResync(t *testing.T) {
	h := newHarness(t)
	// A parent costs a children query, and a tree leased to a ticket nobody tracks costs a lease-holder query.
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Backlog"))
	h.linear.setElsewhere(ticket("ENG-5", "Backlog", "dispatch"))
	h.tools.Treehouse.Seed(h.repo, treehouse.Tree{Path: filepath.Join(h.dir, "trees", "ENG-5"), Status: "leased", LeaseHolder: "ENG-5"})
	h.tick()
	before := h.linear.calls()

	for range 12 {
		h.advance(5 * time.Minute)
	}

	assert.Equal(t, 36, h.linear.calls()-before)
	assert.Empty(t, h.linear.mutations())
}

func TestALaunchThatLandsAfterItsDeadlineGetsNoSecondWorker(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.tools.Launcher.Stall()

	_, _, err := h.runWith(t.Context(), dispatch.Options{LaunchTimeout: 100 * time.Millisecond}, "tick")
	require.ErrorContains(t, err, "ENG-1 launch failed")
	require.Len(t, h.receipts("launch", "ENG-1"), 1)

	// The launch landed after the deadline, so herdr shows the worker.
	h.worker("ENG-1", "working")
	h.advance(time.Second)
	_, err = h.run("tick")

	require.NoError(t, err)
	assert.Len(t, h.launches(), 1)
	assert.Len(t, h.receipts("launch", "ENG-1"), 1)
}

func TestANudgeThatTimesOutCountsAsToldAndIsNeverRepeated(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.recordStop("ENG-1", noTasks)
	h.tools.Herdr.StallPrompts()

	_, _, err := h.runWith(t.Context(), dispatch.Options{ActionTimeout: 100 * time.Millisecond}, "tick")
	require.NoError(t, err)
	for range 2 {
		h.advance(time.Hour)
		_, err = h.run("tick")
		require.NoError(t, err)
	}

	nudges := h.receipts("nudge", "ENG-1")
	require.Len(t, nudges, 1)
	assert.Equal(t, ledger.Done, nudges[0].Outcome)
	assert.Equal(t, "timeout", nudges[0].Detail)
}

func TestStoppingTheLoopAnswersATickThatIsWaiting(t *testing.T) {
	h := newHarness(t)
	h.builtinLauncher = true
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	blocked := loopBlockedLauncher{release: make(chan struct{})}
	loopCtx, stop := context.WithCancel(context.Background())
	defer stop()
	done := make(chan error, 1)
	opts := h.options(dispatch.Options{Launcher: blocked})
	opts.Stdout, opts.Stderr = &bytes.Buffer{}, &bytes.Buffer{}
	go func() { done <- dispatch.Run(loopCtx, []string{"run"}, opts) }()
	require.Eventually(t, func() bool {
		_, err := loop.Ask(t.Context(), h.state, loop.Request{Op: loop.OpPending})
		return err == nil
	}, 20*time.Second, 5*time.Millisecond)
	ticked := make(chan error, 1)
	tickOpts := h.options(dispatch.Options{})
	tickOpts.Stdout, tickOpts.Stderr = &bytes.Buffer{}, &bytes.Buffer{}
	go func() { ticked <- dispatch.Run(t.Context(), []string{"tick", "--timeout", "1m"}, tickOpts) }()
	require.Eventually(t, func() bool {
		resp, err := loop.Ask(t.Context(), h.state, loop.Request{Op: loop.OpPending})
		return err == nil && len(resp.Lines) > 0
	}, 20*time.Second, 5*time.Millisecond)

	stop()
	close(blocked.release)

	select {
	case err := <-ticked:
		require.ErrorContains(t, err, "dispatch is stopping")
	case <-time.After(20 * time.Second):
		t.Fatal("the tick was never answered")
	}
	require.NoError(t, <-done)
}

func TestAStateDirectoryThatCannotHoldTheSocketLaunchesNothing(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	long := filepath.Join(h.state, strings.Repeat("x", 100))
	require.NoError(t, os.MkdirAll(long, 0o700))
	opts := h.options(dispatch.Options{})
	opts.StateDir = long
	opts.Stdout, opts.Stderr = &bytes.Buffer{}, &bytes.Buffer{}

	err := dispatch.Run(t.Context(), []string{"run"}, opts)

	require.ErrorContains(t, err, "Use a shorter state directory")
	assert.Empty(t, h.launches())
}

func TestStoppingTheLoopAnswersATellThatIsWaitingOnARead(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.tick()
	h.tools.Treehouse.DelayTrees(2 * time.Second)
	told := make(chan error, 1)
	tellOpts := h.options(dispatch.Options{})
	tellOpts.Stdout, tellOpts.Stderr = &bytes.Buffer{}, &bytes.Buffer{}
	go func() { told <- dispatch.Run(t.Context(), []string{"tell", "ENG-1", "hello"}, tellOpts) }()
	require.Eventually(t, func() bool {
		resp, err := loop.Ask(t.Context(), h.state, loop.Request{Op: loop.OpPending})
		return err == nil && len(resp.Lines) > 0
	}, 20*time.Second, 5*time.Millisecond)

	require.NoError(t, h.stopLoop())

	select {
	case err := <-told:
		require.ErrorContains(t, err, "stopping")
	case <-time.After(15 * time.Second):
		t.Fatal("the tell was never answered")
	}
}

func TestRotatingHistoryKeepsAnEscalationCommentThatStillHasToBePosted(t *testing.T) {
	h := newHarness(t)
	h.config("[history]\nrotate = \"1m\"\nkeep = \"24h\"\n")
	h.tickets(ticket("ENG-1", "In Progress", ""))
	h.linear.failComments(true)
	_, err := h.run("tick")
	require.ErrorContains(t, err, "ENG-1 comment failed")

	// The ticket leaves the tracked set before the loop rotates into a new segment.
	h.tickets()
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	h.advance(6 * time.Minute)
	h.advance(6 * time.Minute)
	h.linear.failComments(false)

	_, err = h.run("tick")

	require.NoError(t, err)
	mutations := h.linear.mutations()
	require.Len(t, mutations, 2)
	assert.Equal(t, lineartest.Comment, mutations[1].Kind)
	assert.Equal(t, "uuid-ENG-1", mutations[1].TicketID)
}
