package dispatch_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
	"github.com/kapetan-io/dispatch/internal/lineartest"
	"github.com/kapetan-io/dispatch/internal/treehouse"
)

// RunWithoutLoop runs a command on a machine where dispatch run was never started.
func (d *Dispatch) RunWithoutLoop(args ...string) Result {
	d.h.t.Helper()
	return d.run(d.h.options(dispatch.Options{}), args...)
}

// RunInStateDir runs a command over a state directory of the test's choosing.
func (d *Dispatch) RunInStateDir(stateDir string, args ...string) Result {
	d.h.t.Helper()
	opts := d.h.options(dispatch.Options{})
	opts.StateDir = stateDir
	return d.run(opts, args...)
}

// RunWith runs a command over Options the command line has no flag for. The timeouts it sets run on
// the real clock, so tests give them short real durations.
func (d *Dispatch) RunWith(opts dispatch.Options, args ...string) Result {
	d.h.t.Helper()
	return d.result(d.h.runWith(context.Background(), opts, args...))
}

// LoopOut is what dispatch run has printed to stdout so far.
func (d *Dispatch) LoopOut() string { return d.h.loopOut() }

// LoopErr is what dispatch run has printed to stderr so far.
func (d *Dispatch) LoopErr() string { return d.h.loopErr() }

// LoopStops waits for the loop to stop without being asked to, and returns what it printed to
// stderr on the way out and what it stopped with.
func (d *Dispatch) LoopStops() (string, error) {
	d.h.t.Helper()
	running := d.h.running
	require.NotNil(d.h.t, running)
	var err error
	require.Eventually(d.h.t, func() bool {
		select {
		case err = <-running.done:
			d.h.running = nil
			return true
		default:
			return false
		}
	}, 20*time.Second, 5*time.Millisecond)
	return running.err.String(), err
}

// StartWithLaunchTimeout starts the loop with a launch deadline short enough that a stalled launch
// doesn't keep the test waiting for the default one.
func (d *Dispatch) StartWithLaunchTimeout(timeout time.Duration) {
	d.h.t.Helper()
	require.NoError(d.h.t, d.h.startLoop(dispatch.Options{LaunchTimeout: timeout}))
}

// MovedRepo adds a repo whose clone has moved, so treehouse can't read its pool.
func (d *Dispatch) MovedRepo(label string) { d.AddRepo(label, filepath.Join(d.h.dir, "moved")) }

// AddRepo adds a repo beside the dispatch repo.
func (d *Dispatch) AddRepo(label, dir string) { d.h.addRepo(label, dir) }

// ForgetRepos drops every repo added after the dispatch repo.
func (d *Dispatch) ForgetRepos() { d.h.resetRepos() }

// RealLauncher makes launches open a herdr workspace over a pooled tree, so a launched worker
// counts against max_in_progress the way it does in production.
func (d *Dispatch) RealLauncher() {
	d.h.t.Helper()
	parentsRealLauncher(d.h)
}

// TraceReceipts lists every receipt line trace prints for the ticket, one for each state an action
// went through, where Receipts keeps only the last.
func (d *Dispatch) TraceReceipts(ticket string) []Receipt {
	d.h.t.Helper()
	res := d.Run("trace", ticket, "--json")
	require.NoError(d.h.t, res.Err)
	var found []Receipt
	for _, text := range strings.Split(res.Stdout, "\n") {
		var line struct {
			Type, Kind, Subject, State, Once, Detail string
		}
		if text == "" || json.Unmarshal([]byte(text), &line) != nil || line.Type != "receipt" {
			continue
		}
		found = append(found, Receipt{Kind: line.Kind, Subject: line.Subject, State: line.State, Once: line.Once, Detail: line.Detail})
	}
	return found
}

// ReceiptsOf is Receipts narrowed to one kind of action.
func (d *Dispatch) ReceiptsOf(kind, ticket string) []Receipt {
	d.h.t.Helper()
	var found []Receipt
	for _, receipt := range d.Receipts(ticket) {
		if receipt.Kind == kind {
			found = append(found, receipt)
		}
	}
	return found
}

// LaunchAnswers makes every launch from now on end with this launcher result, as JSON.
func (w *Workers) LaunchAnswers(result string) { w.d.h.launchResult(result) }

// StallLaunches makes every launch from now on wait until its deadline.
func (w *Workers) StallLaunches() { w.d.h.tools.Launcher.Stall() }

// Calls counts the requests Linear answered.
func (l *Linear) Calls() int { return l.d.h.linear.calls() }

// Changes lists the status changes and comments Linear accepted, oldest first, as Moves and
// Comments print them.
func (l *Linear) Changes() []string {
	var out []string
	for _, c := range l.d.h.linear.mutations() {
		id := strings.TrimPrefix(c.TicketID, "uuid-")
		switch c.Kind {
		case lineartest.Move:
			out = append(out, id+" -> "+stateName(c.StateID))
		case lineartest.Comment:
			out = append(out, id+": "+c.Body)
		}
	}
	return out
}

// FailComments makes Linear fail every comment while on is true.
func (l *Linear) FailComments(on bool) { l.d.h.linear.failComments(on) }

// FailMoves makes Linear fail every status change.
func (l *Linear) FailMoves() { l.d.h.linear.failMoves() }

// RefuseMoves makes Linear answer every status change with success=false.
func (l *Linear) RefuseMoves() { l.d.h.linear.unsuccessfulMoves() }

// RefuseComments makes Linear answer every comment with success=false.
func (l *Linear) RefuseComments() { l.d.h.linear.unsuccessfulComments() }

// FailChildren makes Linear fail every read of a parent's children.
func (l *Linear) FailChildren() { l.d.h.linear.failChildren() }

// FailManaged makes Linear fail the read of started tickets while on is true.
func (l *Linear) FailManaged(on bool) { l.d.h.linear.FailOn(lineartest.OnManaged, on) }

// Overflow gives the user more started tickets than one page of Linear holds.
func (l *Linear) Overflow() { l.d.h.linear.overflowManaged() }

// LeasedTo leases a tree to a holder that has no workspace, and returns its path.
func (t *Trees) LeasedTo(holder string) string {
	t.d.h.t.Helper()
	path := t.PathOf(holder)
	t.d.h.tools.Treehouse.Seed(t.d.Repo, treehouse.Tree{Path: path, Status: "leased", LeaseHolder: holder})
	return path
}

// GiveBack returns a leased tree to the pool, as its holder does.
func (t *Trees) GiveBack(path, holder string) {
	t.d.h.t.Helper()
	require.NoError(t.d.h.t, t.d.h.tools.Treehouse.Return(t.d.h.t.Context(), t.d.Repo, path, holder))
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
			d := newDispatch(t, dispatchOptions{})

			res := d.RunWithoutLoop(test.args...)

			require.ErrorContains(t, res.Err, "not running")
			assert.Equal(t, 3, res.Code)
		})
	}
}

func TestTickPrintsOneLinePerTicketInPlansWords(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 3\n"})
	d.MovedRepo("tiger")
	d.RealLauncher()
	d.Linear.Tickets(
		ticket("ENG-1", "In Progress", "dispatch"),
		ticket("ENG-2", "In Progress", "dispatch"),
		ticket("ENG-3", "In Progress", "tiger"),
		ticket("ENG-9", "In Progress", "dispatch"),
	)
	d.Workers.Running("ENG-9", "working")

	out := d.Tick()

	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, "ENG-1 launched: workspace w2", lines[0])
	assert.Regexp(t, `^ENG-2 queued: launch, 3 of 3 in progress, 1st in line$`, lines[1])
	assert.Regexp(t, `^ENG-3 held: treehouse pool tiger unread`, lines[2])
}

func TestTickOfOneTicketPrintsOnlyThatTicket(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "Needs Human", "dispatch"))

	res := d.Run("tick", "eng-2")

	require.NoError(t, res.Err)
	assert.Equal(t, "ENG-2 notified\n", res.Stdout)
}

func TestTickExitsOneAndPrintsTheFailedAction(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.LaunchAnswers(`{"status":"pool_error","detail":"treehouse lease failed"}`)

	res := d.Run("tick")

	require.ErrorContains(t, res.Err, "ENG-1 launch failed: pool_error: treehouse lease failed")
	assert.Equal(t, 1, res.Code)
	assert.Equal(t, "ENG-1 launch failed: pool_error: treehouse lease failed\n", res.Stdout)
}

func TestTickTimeoutSaysWhatIsStillPending(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.StartWithLaunchTimeout(10 * time.Second)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.StallLaunches()

	res := d.Run("tick", "--timeout", "1s")

	require.ErrorContains(t, res.Err, "timed out after 1s")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "ENG-1 launch is still running (launch-worker)")
	d.Stop()
}

func TestPlanExitsOneWithNoHistory(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})

	res := d.Run("plan")

	require.ErrorContains(t, res.Err, "there is no history to read")
	assert.Equal(t, 1, res.Code)
}

func TestPlanPrintsStateTicketActionRuleAndDetail(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 2\n"})
	d.RealLauncher()
	d.Linear.Tickets(
		ticket("ENG-1", "In Progress", "dispatch"),
		ticket("ENG-2", "In Progress", "dispatch"),
		ticket("ENG-3", "In Progress", "dispatch"),
	)
	d.Workers.Running("ENG-3", "working")
	d.Tick()

	res := d.Run("plan")

	require.NoError(t, res.Err)
	assert.Regexp(t, `(?m)^waiting +ENG-2 +launch +launch-worker +2 of 2 in progress, 1st in line$`, res.Stdout)
	assert.NotContains(t, res.Stdout, "ENG-1")
}

func TestPlanShowsALaunchThatHasAReceiptAsCovered(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()

	res := d.Run("plan")

	require.NoError(t, res.Err)
	assert.Regexp(t, `(?m)^covered +ENG-1 +launch +launch-worker +done \d\d:\d\d once \[launch `, res.Stdout)
}

func TestPlanHoldsATicketWhoseRepoPoolCouldNotBeRead(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.MovedRepo("tiger")
	d.Linear.Tickets(ticket("ENG-2", "In Progress", "tiger"))
	// A worker may already run in the repo whose pool nobody could read.
	d.h.tools.Herdr.AddWorkspace(herdr.Workspace{ID: "w-ENG-2", Label: "ENG-2", CheckoutPath: filepath.Join(d.h.dir, "trees", "ENG-2")})
	d.Tick()
	d.Advance(time.Second)
	d.Tick()

	res := d.Run("plan")

	require.NoError(t, res.Err)
	assert.Empty(t, d.Workers.Launched())
	assert.Regexp(t, `(?m)^held +ENG-2 +- +launch-worker +treehouse pool tiger unread: `, res.Stdout)
}

func TestLoopRestartAfterALaunchLaunchesNoSecondWorker(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	require.Len(t, d.Workers.Launched(), 1)

	// The fake launcher starts no worker herdr could show, as a launch whose workspace is not up yet.
	d.Advance(time.Second)
	d.Restart()
	out := d.Tick()

	assert.Len(t, d.Workers.Launched(), 1)
	assert.NotContains(t, out, "launched")
}

func TestARuleThatThrowsStopsOnlyItsOwnActionsAndRaisesOneAlert(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: shippedRulesAnd(t, `
whenever({ name: "always-throws", each: "linear.ticket", if: () => { throw new Error("boom"); }, then: () => {} });
`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	d.Tick()
	d.Advance(time.Second)
	d.Tick()

	assert.Len(t, d.Workers.Launched(), 1)
	assert.Contains(t, d.LoopErr(), "always-throws threw in its if for ENG-1")
	var alerts []string
	for _, shown := range d.Alerts.Shown() {
		if strings.Contains(shown, "always-throws") {
			alerts = append(alerts, shown)
		}
	}
	assert.Len(t, alerts, 1)
}

// Unreachable from the CLI: a history directory that stops taking files is a disk fault the product can't inject.
func TestAHistoryWriteFailureStopsTheLoopAndAlerts(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so the write cannot be made to fail")
	}
	events := filepath.Join(t.TempDir(), "events")
	d := newDispatch(t, dispatchOptions{Config: fmt.Sprintf("[history]\ndir = %q\nrotate = \"1h\"\nkeep = \"24h\"\n", events)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	require.NoError(t, os.Chmod(events, 0o500))
	t.Cleanup(func() { _ = os.Chmod(events, 0o700) })

	// The open segment still takes lines, so the failure comes when the loop rotates into a new one.
	d.Clock.Advance(time.Hour)

	stderr, err := d.LoopStops()
	require.ErrorContains(t, err, "permission denied")
	require.Eventually(t, func() bool {
		for _, shown := range d.Alerts.Shown() {
			if strings.HasPrefix(shown, "dispatch stopped: ") {
				return true
			}
		}
		return false
	}, 5*time.Second, 5*time.Millisecond)
	assert.Contains(t, stderr, "dispatch: stopping:")
	assert.Equal(t, 3, d.RunWithoutLoop("tick").Code)
}

func TestAnIdleHourCostsThreeLinearQueriesPerResync(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	// A parent costs a children query, and a tree leased to a ticket nobody tracks costs a lease-holder query.
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Linear.Children(child("ENG-2", "ENG-1", "Backlog"))
	d.Linear.Finished(ticket("ENG-5", "Backlog", "dispatch"))
	d.Trees.LeasedTo("ENG-5")
	d.Tick()
	before := d.Linear.Calls()

	for range 12 {
		d.Advance(5 * time.Minute)
	}

	assert.Equal(t, 36, d.Linear.Calls()-before)
	assert.Empty(t, d.Linear.Changes())
}

func TestALaunchThatLandsAfterItsDeadlineGetsNoSecondWorker(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.StallLaunches()

	res := d.RunWith(dispatch.Options{LaunchTimeout: 100 * time.Millisecond}, "tick")
	require.ErrorContains(t, res.Err, "ENG-1 launch failed")
	require.Len(t, d.ReceiptsOf("launch", "ENG-1"), 1)

	// The launch landed after the deadline, so herdr shows the worker.
	d.Workers.Running("ENG-1", "working")
	d.Advance(time.Second)
	d.Tick()

	assert.Len(t, d.Workers.Launched(), 1)
	assert.Len(t, d.ReceiptsOf("launch", "ENG-1"), 1)
}

func TestANudgeThatTimesOutCountsAsToldAndIsNeverRepeated(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	require.NoError(t, d.RunWith(dispatch.Options{ActionTimeout: 100 * time.Millisecond}, "tick").Err)
	d.h.tools.Herdr.StallPrompts()
	d.Advance(time.Minute)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	for range 3 {
		d.Advance(time.Hour)
		d.Tick()
	}

	nudges := d.ReceiptsOf("nudge", "ENG-1")
	require.Len(t, nudges, 1)
	assert.Equal(t, "done", nudges[0].State)
	assert.Equal(t, "timeout", nudges[0].Detail)
}

func TestStoppingTheLoopAnswersATickThatIsWaiting(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.StartWithLaunchTimeout(3 * time.Second)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.StallLaunches()
	ticked := make(chan Result, 1)
	go func() { ticked <- d.Run("tick", "--timeout", "1m") }()
	require.Eventually(t, func() bool { return len(d.Workers.Launched()) == 1 }, 20*time.Second, 5*time.Millisecond)

	d.Stop()

	select {
	case res := <-ticked:
		require.ErrorContains(t, res.Err, "dispatch is stopping")
	case <-time.After(20 * time.Second):
		t.Fatal("the tick was never answered")
	}
}

func TestAStateDirectoryThatCannotHoldTheSocketLaunchesNothing(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	long := filepath.Join(t.TempDir(), strings.Repeat("x", 100))
	require.NoError(t, os.MkdirAll(long, 0o700))

	res := d.RunInStateDir(long, "run")

	require.ErrorContains(t, res.Err, "Use a shorter state directory")
	assert.Empty(t, d.Workers.Launched())
}

func TestStoppingTheLoopAnswersATellThatIsWaitingOnARead(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Tick()
	d.h.tools.Treehouse.DelayTrees(2 * time.Second)
	reads := len(d.h.tools.Calls("treehouse", "trees"))
	told := make(chan Result, 1)
	go func() { told <- d.Run("tell", "ENG-1", "hello") }()
	require.Eventually(t, func() bool { return len(d.h.tools.Calls("treehouse", "trees")) > reads }, 20*time.Second, 5*time.Millisecond)

	d.Stop()

	select {
	case res := <-told:
		require.ErrorContains(t, res.Err, "stopping")
	case <-time.After(15 * time.Second):
		t.Fatal("the tell was never answered")
	}
}

func TestRotatingHistoryKeepsAnEscalationCommentThatStillHasToBePosted(t *testing.T) {
	// An hour of keep with a rotation every minute, so only receipts a new segment carries forward outlive the first one.
	d := newDispatch(t, dispatchOptions{Config: "[history]\nrotate = \"1m\"\nkeep = \"1h\"\n"})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", ""))
	d.Linear.FailComments(true)
	res := d.Run("tick")
	require.ErrorContains(t, res.Err, "ENG-1 comment failed")

	// The ticket leaves the tracked set before the loop rotates into a new segment.
	d.Linear.Tickets()
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	d.Advance(2 * time.Hour)
	d.Advance(2 * time.Hour)
	require.NotEmpty(t, d.ReceiptsOf("escalate", "ENG-1"))
	d.Linear.FailComments(false)

	d.Tick()

	changes := d.Linear.Changes()
	require.Len(t, changes, 2)
	assert.Equal(t, "ENG-1 -> Needs Human", changes[0])
	assert.True(t, strings.HasPrefix(changes[1], "ENG-1: "))

	// Once the comment is posted nothing is owed, so the next rotations drop the ticket's receipts.
	d.Advance(2 * time.Hour)
	d.Advance(2 * time.Hour)
	assert.Empty(t, d.ReceiptsOf("escalate", "ENG-1"))
}

func TestAWorkerThatGoesIdleHasItsPRReadWithoutWaitingForTheResync(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	d.Tick()
	require.Empty(t, d.h.prReads())

	// A tell reads herdr without a resync, and finds the worker idle.
	d.Advance(time.Second)
	d.Workers.SetStatus("ENG-1", "idle")
	require.NoError(t, d.Run("tell", "ENG-1", "hello").Err)

	assert.NotEmpty(t, d.h.prReads())
}

func TestAnAlertThatHangsDoesNotStopTheLoopAnswering(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Linear.Overflow()
	d.h.tools.Alerts.Stall(true)
	opts := d.h.options(dispatch.Options{})
	opts.Stdout, opts.Stderr = &syncBuffer{}, &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- dispatch.Run(ctx, []string{"run"}, opts) }()
	t.Cleanup(func() { cancel(); <-done })

	// The overflow alert hangs for 10 seconds at most, and the loop answers well inside that.
	require.Eventually(t, func() bool {
		res := d.RunWithoutLoop("tick", "--timeout", "100ms")
		return res.Err != nil && strings.Contains(res.Err.Error(), "timed out after 100ms")
	}, 5*time.Second, 20*time.Millisecond)
}

func TestTheOverflowAlertIsShownAgainAfterItFailed(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Linear.Overflow()
	d.h.tools.Alerts.Fail(errors.New("notifications are denied"))
	d.StartBeforeTheEvents()
	require.Eventually(t, func() bool { return len(d.h.tools.Calls("alerts", "show")) == 1 }, 5*time.Second, 5*time.Millisecond)
	require.Empty(t, d.Alerts.Shown())

	d.h.tools.Alerts.Fail(nil)
	d.Advance(time.Hour)

	require.Len(t, d.Alerts.Shown(), 1)
	assert.Contains(t, d.Alerts.Shown()[0], "more than 100 started")
}

func TestRotatingHistoryKeepsTheReceiptsOfChildrenReadThroughTheirParent(t *testing.T) {
	for _, test := range []struct {
		name         string
		failChildren bool
	}{
		{"ChildrenRead", false},
		{"ChildrenUnread", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// The cap keeps ENG-2 in Todo, so only the children read knows it exists.
			d := newDispatch(t, dispatchOptions{
				Rules:  rule(childCommentRule),
				Config: "[dispatch]\nmax_in_progress = 1\n[history]\nrotate = \"1m\"\nkeep = \"1h\"\n",
			})
			d.Linear.Tickets(parent("ENG-1", "In Progress"), ticket("ENG-9", "In Progress", "dispatch"))
			d.Workers.Running("ENG-9", "working")
			d.Linear.Children(child("ENG-2", "ENG-1", "Todo"))
			d.Tick()
			require.Len(t, d.ReceiptsOf("comment", "ENG-2"), 1)

			if test.failChildren {
				d.Linear.FailChildren()
			}
			for range 4 {
				d.Advance(2 * time.Hour)
				d.Tick()
			}
			d.Restart()
			d.Advance(2 * time.Hour)
			d.Tick()

			assert.Len(t, d.ReceiptsOf("comment", "ENG-2"), 1)
			comments := 0
			for _, change := range d.Linear.Changes() {
				if strings.HasPrefix(change, "ENG-2: ") {
					comments++
				}
			}
			assert.Equal(t, 1, comments)
			d.Stop()
			require.NoError(t, d.Run("replay").Err)
		})
	}
}

func TestRotatingHistoryDropsTheReceiptsOfAChildNoParentListsAnymore(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(d *Dispatch)
	}{
		{"ParentIsDone", func(d *Dispatch) {
			d.Linear.Tickets(parent("ENG-1", "Done"), ticket("ENG-9", "In Progress", "dispatch"))
		}},
		{"ChildMovesToAnotherParent", func(d *Dispatch) {
			d.Linear.Children(child("ENG-2", "ENG-3", "Todo"))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{
				Rules:  rule(childCommentRule),
				Config: "[dispatch]\nmax_in_progress = 1\n[history]\nrotate = \"1m\"\nkeep = \"1h\"\n",
			})
			d.Linear.Tickets(parent("ENG-1", "In Progress"), ticket("ENG-9", "In Progress", "dispatch"))
			d.Workers.Running("ENG-9", "working")
			d.Linear.Children(child("ENG-2", "ENG-1", "Todo"))
			d.Tick()
			require.Len(t, d.ReceiptsOf("comment", "ENG-2"), 1)

			test.change(d)
			for range 3 {
				d.Advance(2 * time.Hour)
				d.Tick()
			}

			assert.Empty(t, d.ReceiptsOf("comment", "ENG-2"))
		})
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})

	res := d.Run("deploy")

	require.ErrorContains(t, res.Err, `unknown command "deploy"`)
}

func TestRunStartsActsAndStopsCleanlyOnCancel(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	// The loop acts on its first read, before any tick asks.
	d.StartBeforeTheEvents()
	require.Eventually(t, func() bool { return len(d.Workers.Launched()) == 1 }, 15*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool { return strings.Contains(d.LoopOut(), "launch ENG-1 ok") }, 15*time.Second, 10*time.Millisecond)
	out := d.LoopOut()

	d.Stop()

	assert.Contains(t, out, "start: 1 tracked ticket, 25 rules")
}

func TestRunRejectsANonPositiveResync(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nresync = \"0s\"\n"})

	res := d.Run("run")

	require.ErrorContains(t, res.Err, "[dispatch].resync must be positive")
}

func TestRunRejectsArguments(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})

	res := d.Run("run", "--interval", "20ms")

	require.ErrorContains(t, res.Err, "run takes no arguments")
}

func TestRunExitsWhenAnotherRunHoldsTheLock(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()

	res := d.Run("run")

	require.ErrorContains(t, res.Err, "another dispatch run holds the lock")
	assert.Len(t, d.Workers.Launched(), 1)
}

func TestRunFailsToStartWhenTheRulesDoNotLoad(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(`whenever({ name: "broken", each: "linear.ticket", if: (t) => t.status === , then: () => {} });`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("run")

	require.ErrorContains(t, res.Err, "problems found in the rules, so none loaded")
	assert.Empty(t, d.Workers.Launched())
}

func TestACrashAfterAnAttemptedLaunchGetsNoSecondWorker(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.EarlierRun(earlierReceipt{
		Kind: "launch", Subject: "ENG-1", Rule: "launch-worker",
		Once: []any{"launch", d.Clock.Now().UnixMilli(), 0}, Started: d.Clock.Now(),
	})
	// The crashed launch got as far as opening the worker, so herdr shows it.
	d.Workers.Running("ENG-1", "working")

	d.Tick()
	d.Advance(time.Hour)
	d.Tick()
	d.Advance(time.Hour)
	d.Tick()

	assert.Empty(t, d.Workers.Launched())
}

// A launch attempted before a crash with no worker visible is decided again from a fresh read.
// The launcher answers already_running when the worker did land, which is what keeps it single.
func TestACrashedLaunchIsDecidedAgainAndNeverCountsAsAFailure(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.EarlierRun(earlierReceipt{
		Kind: "launch", Subject: "ENG-1", Rule: "launch-worker",
		Once: []any{"launch", d.Clock.Now().UnixMilli(), 0}, Started: d.Clock.Now(),
	})

	d.Tick()

	assert.Len(t, d.Workers.Launched(), 1)
	var crashed, launched int
	for _, receipt := range d.TraceReceipts("ENG-1") {
		if receipt.Kind != "launch" {
			continue
		}
		if receipt.State == "failed" && receipt.Detail == "crashed" {
			crashed++
		}
		if receipt.State == "done" {
			launched++
		}
	}
	assert.Equal(t, 1, crashed)
	assert.Equal(t, 1, launched)
	assert.Empty(t, d.Linear.Changes())
}

func TestARestartWithALiveWorkerLaunchesNoSecondWorker(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.Tick()

	d.Restart()
	d.Advance(time.Hour)
	d.Tick()

	assert.Empty(t, d.Workers.Launched())
}

func TestAnUnreadHerdrHoldsALaunchAndPlanSaysWhy(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.h.tools.Herdr.Fail(herdrtest.OpRead, errors.New("server not running"))
	d.Restart()
	d.Advance(time.Hour)
	d.Tick()

	res := d.Run("plan")

	require.NoError(t, res.Err)
	assert.Empty(t, d.Workers.Launched())
	assert.Regexp(t, `(?m)^held +ENG-1 +- +launch-worker +\S*herdr`, res.Stdout)
}

func TestANudgeToldBeforeARestartIsNotToldAgain(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	d.h.tools.Herdr.StallPrompts()
	require.NoError(t, d.RunWith(dispatch.Options{ActionTimeout: 100 * time.Millisecond}, "tick").Err)
	require.Len(t, d.ReceiptsOf("nudge", "ENG-1"), 1)

	// Dispatch cannot know the text never arrived, so a restart sends nothing more.
	d.Restart()
	for range 2 {
		d.Advance(time.Hour)
		d.Tick()
	}

	assert.Len(t, d.ReceiptsOf("nudge", "ENG-1"), 1)
}

func TestAMissedResyncReadsLinearAgainBeforeActing(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	d.Tick()
	require.Len(t, d.Workers.Prompts(), 1)
	callsBefore := d.Linear.Calls()

	// While the machine slept someone moved the ticket to Needs Human, and the worker went idle again.
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Workers.SetStatus("ENG-1", "idle")
	d.Advance(6 * time.Hour)

	assert.Greater(t, d.Linear.Calls(), callsBefore)
	assert.Len(t, d.Workers.Prompts(), 1)
	assert.Empty(t, d.Workers.Launched())
}

func TestARuleThatRunsOutOfTimeStopsOnlyItsOwnActionsAndRaisesOneAlert(t *testing.T) {
	for _, test := range []struct {
		name    string
		rule    string
		timeout dispatch.RuleTimeout
	}{
		{
			name:    "If",
			rule:    `whenever({ name: "spins-in-if", each: "linear.ticket", if: () => { for (;;) {} }, then: () => {} });`,
			timeout: dispatch.RuleTimeout{If: 100 * time.Millisecond},
		},
		{
			name:    "Then",
			rule:    `whenever({ name: "spins-in-then", each: "linear.ticket", if: () => true, then: () => { for (;;) {} } });`,
			timeout: dispatch.RuleTimeout{Then: 100 * time.Millisecond},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Rules: shippedRulesAnd(t, test.rule)})
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

			require.NoError(t, d.RunWith(dispatch.Options{RuleTimeout: test.timeout}, "tick").Err)
			d.Advance(time.Second)
			d.Tick()

			assert.Len(t, d.Workers.Launched(), 1)
			var alerts []string
			for _, shown := range d.Alerts.Shown() {
				if strings.Contains(shown, "spins-in") {
					alerts = append(alerts, shown)
				}
			}
			assert.Len(t, alerts, 1)
		})
	}
}

// An event that finds the clock past a resync that never ran is a wake from sleep: every
// source is read again before any cycle acts on the facts from before the sleep.
func TestAnEventAfterASleepReadsEverySourceBeforeActing(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Tick()
	before, prompted := d.Linear.Calls(), len(d.Workers.Prompts())
	d.Clock.Skew(6 * time.Hour)
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))

	// A tell is an event that is not a read of Linear.
	require.NoError(t, d.Run("tell", "ENG-1", "hello").Err)

	assert.Greater(t, d.Linear.Calls(), before)
	assert.Equal(t, []string{"w-ENG-1:p1|hello"}, d.Workers.Prompts()[prompted:])
}

// Pool returns decide whether a full pool is retried, so a restart must not forget one, or the next
// tree to come back would look like a return that was already answered.
func TestATreeReturnedAfterARestartStillLetsAFullPoolRetry(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.LaunchAnswers(`{"status":"pool_exhausted","detail":"all 4/4 pool trees are taken"}`)
	first, second := d.Trees.LeasedTo("first"), d.Trees.LeasedTo("second")
	d.Tick()
	require.Len(t, d.Workers.Launched(), 1)
	d.Trees.GiveBack(first, "first")
	d.Advance(time.Second)
	d.Tick()
	require.Len(t, d.Workers.Launched(), 2)

	d.Restart()
	d.Trees.GiveBack(second, "second")
	d.Advance(time.Second)
	d.Tick()

	assert.Len(t, d.Workers.Launched(), 3)
}

// A tool that fails for one read says nothing about a worker, so what dispatch knew of it stays.
func TestAFailedHerdrReadDoesNotMakeAFullPoolRetry(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.LaunchAnswers(`{"status":"pool_exhausted","detail":"all 4/4 pool trees are taken"}`)
	d.Tick()
	require.Len(t, d.Workers.Launched(), 1)

	d.h.tools.Herdr.Fail(herdrtest.OpRead, errors.New("herdr is down"))
	d.Advance(time.Second)
	d.Tick()
	d.h.tools.Herdr.Fail(herdrtest.OpRead, nil)
	d.Advance(time.Second)
	d.Tick()

	assert.Len(t, d.Workers.Launched(), 1)
}

// A segment that was created but never given its snapshot holds nothing, so the one before it is the state.
func TestAHalfWrittenSegmentDoesNotStopTheStart(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.EarlierRun(earlierReceipt{
		Kind: "launch", Subject: "ENG-1", Rule: "launch-worker", Once: []any{"launch", d.Clock.Now().UnixMilli(), 0},
		State: "done", Started: d.Clock.Now(), Ended: d.Clock.Now(),
	})
	d.HalfWrittenSegment()

	require.NoError(t, d.Run("tick").Err)

	// The launch the older segment recorded still covers the ticket.
	assert.Empty(t, d.Workers.Launched())
}

// Waits are measured from when dispatch first saw a status, so a start while Linear is down
// must not forget them, or every notice would be sent again.
func TestARestartDuringALinearOutageKeepsEveryWait(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Tick()
	require.Len(t, d.Alerts.Shown(), 1)
	d.Advance(time.Hour)
	d.Linear.FailManaged(true)
	d.Restart()

	d.Linear.FailManaged(false)
	d.Advance(5 * time.Minute)
	d.Tick()

	assert.Len(t, d.Alerts.Shown(), 1)
}

// A first run that crashed before its segment had a header leaves a file with nothing in it.
func TestAnEmptyFirstSegmentIsNoHistory(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.EmptyFirstSegment()

	require.NoError(t, d.Run("tick").Err)
}

// A clock that stepped back must not make a newer run's history look older than an earlier run's.
func TestAClockThatSteppedBackStillLoadsTheNewestRun(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.HistoryDatedAhead(48 * time.Hour)
	d.Tick()
	require.Len(t, d.Workers.Launched(), 1)

	d.Restart()
	d.Tick()

	assert.Len(t, d.Workers.Launched(), 1)
	assert.NotEmpty(t, d.ReceiptsOf("launch", "ENG-1"))
}

func TestAnOrderedStepThatFailedIsTriedAgainAtTheNextResync(t *testing.T) {
	// The first step makes the rule's own condition false, so only the loop can run the second again.
	d := newDispatch(t, dispatchOptions{Rules: shippedRulesAnd(t, `
whenever({
  name: "ordered-pair",
  each: "linear.ticket",
  if:   (t, ctx) => t.status === "in_progress" && ctx.ledger.receipts(t, { kind: "nudge", topic: "ordered-a" }).length === 0,
  then: (t, ctx) => [
    ctx.herdr.queueNudge(t, "ordered first", { once: ["ordered-a"] }),
    ctx.linear.queueComment(t, "ordered second", { once: ["ordered-b"] }),
  ],
});
`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.Linear.FailComments(true)
	res := d.Run("tick")
	require.ErrorContains(t, res.Err, "ENG-1 comment failed")
	require.Empty(t, d.Linear.Changes())

	d.Linear.FailComments(false)
	d.Advance(time.Second)
	d.Tick()

	assert.Equal(t, []string{"ENG-1: ordered second"}, d.Linear.Changes())
}
