package dispatch_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
	"github.com/kapetan-io/dispatch/internal/lineartest"
)

const needsHuman = "ENG-1 -> Needs Human"

// OSAlerts shows alerts through the notifier dispatch picks for the operating system.
func (d *Dispatch) OSAlerts() { d.builtinAlerts = true }

// ExactRules keeps the [rules] table as the test wrote it, without the keys the shipped rules read.
func (d *Dispatch) ExactRules() { d.exactRules = true }

// CommentAttempts counts the comments dispatch asked Linear to post, accepted or not.
func (l *Linear) CommentAttempts() int { return l.d.linear.CallsTo(lineartest.OnComment) }

// Started lists the agents the built-in launcher started.
func (w *Workers) Started() []herdr.Worker { return w.d.tools.Herdr.Started() }

func TestTickLaunchesApprovedTicketWithNoWorker(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	out := d.Tick()

	assert.Equal(t, "ENG-1 launched\n", out)
	assert.Equal(t, []string{d.Repo + " ENG-1 thrawn01/eng-1 --model sonnet"}, d.Workers.Launched())
	assert.Empty(t, d.Linear.Changes())
}

func TestTickLeavesTicketWithLiveWorker(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")

	out := d.Tick()

	assert.Empty(t, out)
	assert.Empty(t, d.Workers.Launched())
}

func TestTickRelaunchesWhenWorkspaceHasNoClaude(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "")

	d.Tick()

	assert.Len(t, d.Workers.Launched(), 1)
}

func TestTickWaitsWhenWorkerCapIsFull(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 1\n"})
	d.Linear.Tickets(
		ticket("ENG-1", "In Progress", "dispatch"),
		ticket("ENG-2", "In Progress", "dispatch"),
	)
	d.Workers.Running("ENG-1", "working")

	out := d.Tick()

	assert.Equal(t, "ENG-2 queued: launch, 1 of 1 in progress, 1st in line\n", out)
	assert.Empty(t, d.Workers.Launched())
}

func TestTickLaunchesNothingForTicketsThatAreNotInProgress(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(
		ticket("ENG-1", "In Review", "dispatch"),
		ticket("ENG-2", "Needs Human", "dispatch"),
	)

	out := d.Tick()

	assert.Equal(t, "ENG-2 notified\n", out)
	assert.Equal(t, []string{"ENG-2 is in Needs Human: ticket ENG-2"}, d.Alerts.Shown())
	assert.Empty(t, d.Workers.Launched())
}

func TestTickEscalatesAfterRepeatedLaunchFailures(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.LaunchResult(`{"status":"pool_error","detail":"treehouse lease failed"}`)

	res := d.Run("tick")
	require.ErrorContains(t, res.Err, "ENG-1 launch failed: pool_error: treehouse lease failed")
	assert.Empty(t, d.Linear.Changes())

	// Two failures at the same millisecond are one receipt, so the clock moves between ticks.
	d.Advance(time.Second)
	res = d.Run("tick")

	require.ErrorContains(t, res.Err, "ENG-1 launch failed")
	assert.Contains(t, res.Stdout, "ENG-1 escalated: its worker failed to launch 2 times in a row")
	changes := d.Linear.Changes()
	require.Len(t, changes, 2)
	assert.Equal(t, needsHuman, changes[0])
	assert.Contains(t, changes[1], "treehouse lease failed")
}

func TestTickNeverEscalatesForAFullPool(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.LaunchResult(`{"status":"pool_exhausted","detail":"all 4/4 pool trees are taken"}`)
	// Another holder has the only tree, so the pool's return count can move when it lets go.
	other := d.Trees.LeasedTo("other")

	res := d.Run("tick")
	require.NoError(t, res.Err)
	assert.Equal(t, "ENG-1 launch refused: pool_exhausted\n", res.Stdout)
	for range 2 {
		d.Advance(time.Second)
		require.NoError(t, d.Run("tick").Err)
	}

	// While no tree comes back there is one attempt, not one per tick.
	assert.Len(t, d.Workers.Launched(), 1)
	assert.Empty(t, d.Linear.Changes())

	d.Trees.GiveBack(other, "other")
	d.Workers.LaunchResult(`{"status":"ok","ticket":"ENG-1","branch":"thrawn01/eng-1"}`)
	d.Advance(time.Second)
	res = d.Run("tick")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "ENG-1 launched")
	assert.Len(t, d.Workers.Launched(), 2)
	assert.Empty(t, d.Linear.Changes())
}

func TestTickAlertsWhenItEscalatesADesignTicket(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(withLabel(ticket("ENG-1", "In Progress", ""), "design"))

	d.Tick()

	assert.Len(t, d.Linear.Changes(), 2)
	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1"}, d.Alerts.Shown())
}

func TestTickShowsHerdrNotificationsOffMacOS(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "darwin" {
		t.Skip("macOS shows alerts through osascript")
	}
	bin := t.TempDir()
	argv := filepath.Join(bin, "argv")
	require.NoError(t, os.WriteFile(filepath.Join(bin, "herdr"),
		[]byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > "+argv+"\n"), 0o755))
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"PATH": bin}})
	d.OSAlerts()
	d.Linear.Tickets(withLabel(ticket("ENG-1", "In Progress", ""), "design"))

	d.Tick()

	got, err := os.ReadFile(argv)
	require.NoError(t, err)
	assert.Equal(t, "notification\nshow\ndispatch\n--body\nENG-1 is in Needs Human: ticket ENG-1\n--sound\nrequest\n", string(got))
}

func TestTickAlertsOnceWhenItEscalatesADesignReviewTicket(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(withLabel(ticket("ENG-1", "In Progress", ""), "design-review"))

	d.Tick()

	assert.Len(t, d.Linear.Changes(), 2)
	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1"}, d.Alerts.Shown())
}

func TestTickEscalatesTicketItCannotPlaceInARepo(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		repo     string
		wantBody string
	}{
		{
			name:     "NoRepoLabel",
			repo:     "",
			wantBody: "has no repo label",
		},
		{
			name:     "RepoNotConfigured",
			repo:     "tiger",
			wantBody: "its repo \"tiger\" is not in the dispatcher's config",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", "In Progress", test.repo))

			d.Tick()

			assert.Empty(t, d.Workers.Launched())
			changes := d.Linear.Changes()
			require.Len(t, changes, 2)
			assert.Equal(t, needsHuman, changes[0])
			assert.Contains(t, changes[1], test.wantBody)
		})
	}
}

func TestTickLaunchesOtherTicketsWhenAnEscalationFails(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(
		ticket("ENG-1", "In Progress", ""),
		ticket("ENG-2", "In Progress", "dispatch"),
	)
	d.Linear.FailMoves()

	res := d.Run("tick")

	require.ErrorContains(t, res.Err, "ENG-1 escalate failed")
	assert.Contains(t, res.Stdout, "ENG-2 launched")
	assert.Len(t, d.Workers.Launched(), 1)
	// The failed move left ENG-1 In Progress, so no comment was posted that a
	// later tick would post again.
	assert.Empty(t, d.Linear.Changes())
	assert.Zero(t, d.Linear.CommentAttempts())
}

func TestTickNeverCommentsWhenLinearRefusesTheMove(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", ""))
	d.Linear.RefuseMoves()

	res := d.Run("tick")

	require.ErrorContains(t, res.Err, "ENG-1 escalate failed: linear MoveTicket: Linear reported success=false")
	assert.Contains(t, d.LoopOut(), "escalate ENG-1 failed: linear MoveTicket: Linear reported success=false")
	assert.Zero(t, d.Linear.CommentAttempts())
	assert.Empty(t, d.Linear.Changes())
}

func TestTickReadsNothingWhenManagedTicketsSpanMoreThanOnePage(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Linear.Overflow()

	res := d.Run("tick")

	// The tickets are unknown, which holds every rule that needs them, so the tick has nothing to fail on.
	require.NoError(t, res.Err)
	assert.Contains(t, d.LoopErr(), "more than 100 started \"ai\" tickets are assigned to you")
	assert.Empty(t, d.Workers.Launched())

	// The user hears of it once, however many resyncs find the same overflow.
	d.Advance(time.Hour)
	d.Advance(time.Hour)
	require.Len(t, d.Alerts.Shown(), 1)
	assert.Contains(t, d.Alerts.Shown()[0], "more than 100 started \"ai\" tickets are assigned to you")
}

func TestTickHoldsBackOnlyTheRepoWhosePoolCannotBeRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.MovedRepo("tiger")
	d.Linear.Tickets(
		ticket("ENG-1", "In Progress", "dispatch"),
		ticket("ENG-2", "In Progress", "tiger"),
	)

	out := d.Tick()

	assert.Contains(t, out, "ENG-1 launched\n")
	assert.Contains(t, out, "ENG-2 held: treehouse pool tiger unread")
	assert.Contains(t, d.LoopErr(), "dispatch: while reading the tiger pool:")
	assert.Equal(t, []string{d.Repo + " ENG-1 thrawn01/eng-1 --model sonnet"}, d.Workers.Launched())
	assert.Empty(t, d.Linear.Changes())
	plan := d.Run("plan")
	require.NoError(t, plan.Err)
	assert.Regexp(t, `(?m)^held +ENG-2 +- +launch-worker +treehouse pool tiger unread`, plan.Stdout)
}

func TestTickCountsTicketsInAnUnreadRepoAgainstTheWorkerCap(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 1\n"})
	d.MovedRepo("tiger")
	d.Linear.Tickets(
		ticket("ENG-1", "In Progress", "dispatch"),
		ticket("ENG-2", "In Progress", "tiger"),
	)

	out := d.Tick()

	// ENG-2 may have a live worker nobody could see, so ENG-1 must not launch.
	assert.Contains(t, out, "ENG-1 queued: launch, 1 of 1 in progress")
	assert.Empty(t, d.Workers.Launched())
}

func TestTickPostsAnEscalationCommentThatFailedOnAnEarlierTick(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", ""))
	d.Linear.FailComments(true)

	res := d.Run("tick")
	require.ErrorContains(t, res.Err, "ENG-1 comment failed: while commenting on ENG-1")
	require.Len(t, d.Linear.Changes(), 1)

	// The ticket has since left the started states the dispatcher reads, so
	// the retry can't depend on finding it among managed tickets.
	d.Linear.Tickets()
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	d.Linear.FailComments(false)
	d.Advance(time.Second)

	d.Tick()

	changes := d.Linear.Changes()
	require.Len(t, changes, 2)
	assert.Equal(t, needsHuman, changes[0])
	assert.Contains(t, changes[1], "ENG-1: ")
	assert.Contains(t, changes[1], "has no repo label")

	d.Advance(time.Second)
	d.Tick()
	assert.Len(t, d.Linear.Changes(), 2)
}

func TestTickRetriesAnEscalationCommentThatLinearAnsweredWithSuccessFalse(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", ""))
	d.Linear.RefuseComments()

	res := d.Run("tick")

	require.ErrorContains(t, res.Err, "ENG-1 comment failed")
	require.ErrorContains(t, res.Err, "Linear reported success=false")
	assert.Equal(t, []string{needsHuman}, d.Linear.Changes())
}

func TestTickEscalatesATicketWhoseLabelsDidNotFitOnePage(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	// The design label may be among the labels Linear left off the page, so
	// this ticket must not be launched as a build.
	cut := ticket("ENG-1", "In Progress", "dispatch")
	cut.LabelsTruncated = true
	d.Linear.Tickets(cut)

	out := d.Tick()

	assert.Contains(t, out, "ENG-1 escalated: it has more labels than the dispatcher reads")
	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1"}, d.Alerts.Shown())
	assert.Empty(t, d.Workers.Launched())
	changes := d.Linear.Changes()
	require.Len(t, changes, 2)
	assert.Equal(t, needsHuman, changes[0])
}

func TestTickHoldsABlockedTicketAndSaysWhyOnce(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Review"}))

	d.Tick()

	assert.Empty(t, d.Workers.Launched())
	assert.Equal(t, []string{"ENG-2: No worker is running because this ticket is blocked by ENG-1. A worker starts once every blocker is Done."}, d.Linear.Comments())

	// The blocker moves status but is still open, so the ticket is not told again.
	d.Linear.Tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Progress"}))
	d.Tick()
	assert.Len(t, d.Linear.Comments(), 1)
	assert.Empty(t, d.Workers.Launched())
}

func TestTickLaunchesAHeldTicketOnceEveryBlockerIsDone(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(blockedBy(ticket("ENG-3", "In Progress", "dispatch"), false, [2]string{"ENG-1", "Done"}, [2]string{"ENG-2", "Todo"}))
	d.Tick()
	require.Empty(t, d.Workers.Launched())

	d.Linear.Tickets(blockedBy(ticket("ENG-3", "In Progress", "dispatch"), false, [2]string{"ENG-1", "Done"}, [2]string{"ENG-2", "Done"}))
	d.Tick()

	assert.Len(t, d.Workers.Launched(), 1)
}

func TestTickLaunchesTicketsWithNoOpenBlockers(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(
		ticket("ENG-1", "In Progress", "dispatch"),
		blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-9", "Done"}),
	)

	d.Tick()

	assert.Len(t, d.Workers.Launched(), 2)
	assert.Empty(t, d.Linear.Comments())
}

func TestTickSendsAHeldTicketWithACanceledBlockerToNeedsHuman(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "Canceled"}))

	d.Tick()

	changes := d.Linear.Changes()
	require.Len(t, changes, 2)
	assert.Equal(t, "ENG-2 -> Needs Human", changes[0])
	assert.Contains(t, changes[1], "its blocker ENG-1 is Canceled")
	assert.Empty(t, d.Workers.Launched())
}

func TestTickLeavesARunningWorkerAloneWhenABlockerIsAdded(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "Todo"}))
	d.Workers.Running("ENG-2", "working")

	out := d.Tick()

	assert.Empty(t, out)
	assert.Empty(t, d.Workers.Closed())
	assert.Empty(t, d.Workers.Launched())
	assert.Empty(t, d.Linear.Changes())
}

func TestTickHoldsATicketWhoseBlockersWereCutShort(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), true, [2]string{"ENG-1", "Done"}))
	d.Tick()

	plan := d.Run("plan")

	require.NoError(t, plan.Err)
	assert.Contains(t, plan.Stdout, "held")
	assert.Contains(t, plan.Stdout, "more relations than dispatch reads")
	assert.Empty(t, d.Workers.Launched())
}

func TestTickDoesNotLetAHeldTicketTakeAPromotionSlot(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 1\n"})
	d.Linear.Tickets(
		parent("ENG-1", "In Progress"),
		blockedBy(ticket("ENG-5", "In Progress", "dispatch"), false, [2]string{"ENG-4", "Todo"}),
	)
	d.Linear.Children(child("ENG-2", "ENG-1", "Todo"))

	d.Tick()

	moves := d.Linear.Moves()
	require.Len(t, moves, 1)
	assert.Contains(t, moves[0], "ENG-2 -> ")
}

func TestTickLeavesARunningWorkerAloneWhenADoneBlockerReopens(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-2", "working")
	d.Linear.Tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "Done"}))
	d.Tick()

	d.Linear.Tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Progress"}))
	out := d.Tick()

	assert.Empty(t, out)
	assert.Empty(t, d.Workers.Closed())
	assert.Empty(t, d.Workers.Launched())
	assert.Empty(t, d.Linear.Changes())
}

func TestTickDoesNotRepeatTheHoldCommentAfterAnUnreadPool(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	held := blockedBy(ticket("ENG-2", "In Progress", "tiger"), false, [2]string{"ENG-1", "Todo"})
	tiger := filepath.Join(t.TempDir(), "tiger")
	require.NoError(t, os.MkdirAll(tiger, 0o755))
	d.AddRepo("tiger", tiger)
	d.Linear.Tickets(held)
	d.Tick()
	require.Len(t, d.Linear.Comments(), 1)

	// The clone moves, so the pool can't be read and Plan never reaches the blocker check.
	d.ForgetRepos()
	d.MovedRepo("tiger")
	d.Restart()
	d.Tick()
	require.Contains(t, d.LoopErr(), "while reading the tiger pool")

	d.ForgetRepos()
	d.AddRepo("tiger", tiger)
	d.Restart()
	d.Tick()
	assert.Len(t, d.Linear.Comments(), 1)
}

func TestTickSendsAHeldTicketWithADuplicateBlockerToNeedsHuman(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "Duplicate"}))

	d.Tick()

	require.Len(t, d.Linear.Comments(), 1)
	assert.Contains(t, d.Linear.Comments()[0], "its blocker ENG-1 is Duplicate")
	assert.Empty(t, d.Workers.Launched())
}

func TestTickDoesNotRepeatTheHoldCommentWhenLinearReordersBlockers(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(blockedBy(ticket("ENG-3", "In Progress", "dispatch"), false, [2]string{"ENG-1", "Todo"}, [2]string{"ENG-2", "Todo"}))
	d.Tick()

	d.Linear.Tickets(blockedBy(ticket("ENG-3", "In Progress", "dispatch"), false, [2]string{"ENG-2", "Todo"}, [2]string{"ENG-1", "Todo"}))
	d.Tick()

	assert.Equal(t, []string{"ENG-3: No worker is running because this ticket is blocked by ENG-1, ENG-2. A worker starts once every blocker is Done."}, d.Linear.Comments())
}

func TestTickLaunchesADesignTicketWithTheDesignKickoff(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(designTicket("ENG-1", "In Progress"), ticket("ENG-2", "In Progress", "dispatch"))

	d.Tick()

	// The loop starts both launches at once, so their order at the launcher is not fixed.
	assert.ElementsMatch(t, []string{
		d.Repo + " ENG-1 thrawn01/eng-1 --model opus --kickoff /dispatch:design ENG-1",
		d.Repo + " ENG-2 thrawn01/eng-2 --model sonnet",
	}, d.Workers.Launched())
}

func TestTickRunsTheDesignOnTheConfiguredModel(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[rules]\ndesign_model = \"fable\"\n"})
	d.Linear.Tickets(designTicket("ENG-1", "In Progress"))

	d.Tick()

	assert.Equal(t, []string{d.Repo + " ENG-1 thrawn01/eng-1 --model fable --kickoff /dispatch:design ENG-1"}, d.Workers.Launched())
}

func TestTickTypesTheDesignKickoffWithTheBuiltInLauncher(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.RealLauncher()
	d.Linear.Tickets(designTicket("ENG-1", "In Progress"))

	d.Tick()

	assert.Equal(t, []string{"w2:p2|/dispatch:design ENG-1"}, d.Workers.Kicked())
	require.Len(t, d.Workers.Started(), 1)
	assert.Equal(t, "opus", d.Workers.Started()[0].Model)
}

func TestTickNeverNudgesOrEscalatesADesignWorker(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(designTicket("ENG-1", "In Progress"))
	d.Workers.Running("ENG-1", "idle")

	for range 8 {
		assert.Empty(t, d.Tick())
		d.Advance(3 * time.Hour)
	}

	// A comment on the ticket, a blocked question and a stalled session
	// are all normal for a design waiting on the user.
	d.Linear.Tickets(withComment(designTicket("ENG-1", "In Progress"), comment1))
	for _, status := range []string{"idle", "blocked"} {
		d.Workers.SetStatus("ENG-1", status)
		assert.Empty(t, d.Tick())
		d.Advance(3 * time.Hour)
	}

	assert.Empty(t, d.Workers.Prompts())
	assert.Empty(t, d.Alerts.Shown())
	assert.Empty(t, d.Linear.Changes())
	assert.Empty(t, d.Workers.Launched())
	assert.Empty(t, d.Workers.Closed())
}

func TestTickKeepsTheDesignWorkerOnceItHasChildren(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(designTicket("ENG-1", "In Progress"))
	d.Linear.Children(child("ENG-2", "ENG-1", "Backlog"))
	d.Workers.Running("ENG-1", "idle")

	for range 3 {
		assert.Empty(t, d.Tick())
		d.Advance(3 * time.Hour)
	}

	assert.Empty(t, d.Workers.Prompts())
	assert.Empty(t, d.Alerts.Shown())
	assert.Empty(t, d.Linear.Changes())
	assert.Empty(t, d.Workers.Launched())
	assert.Empty(t, d.Workers.Closed())
}

func TestTickCountsADesignTicketAgainstTheWorkerCap(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 1\n"})
	d.Linear.Tickets(designTicket("ENG-1", "In Progress"), ticket("ENG-2", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Tick()

	plan := d.Run("plan")

	require.NoError(t, plan.Err)
	assert.Regexp(t, planRow("waiting", "ENG-2"), plan.Stdout)
	assert.Empty(t, d.Workers.Launched())
}

func TestTickHoldsAPromotionForADesignTicketAboutToLaunch(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 1\n"})
	d.RealLauncher()
	d.Linear.Tickets(parent("ENG-1", "In Progress"), designTicket("ENG-3", "In Progress"))
	d.Linear.Children(child("ENG-2", "ENG-1", "Todo"))
	d.Tick()

	plan := d.Run("plan")

	require.NoError(t, plan.Err)
	assert.Regexp(t, planRow("waiting", "ENG-2"), plan.Stdout)
	assert.Empty(t, d.Linear.Changes())
	assert.Len(t, d.Workers.Started(), 1)
}

func TestTickLaunchesADesignReviewTicketOnTheDesignModel(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[rules]\ndesign_model = \"haiku\"\n"})
	d.Linear.Tickets(withLabel(ticket("ENG-1", "In Progress", "dispatch"), "design-review"))

	d.Tick()

	assert.Equal(t, []string{
		d.Repo + " ENG-1 thrawn01/eng-1 --model haiku --kickoff /dispatch:design-review ENG-1",
	}, d.Workers.Launched())
}

func TestTickLaunchesADesignReviewTicketThatAlsoCarriesDesignAsAReview(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(withLabel(designTicket("ENG-1", "In Progress"), "design-review"))

	d.Tick()

	assert.Equal(t, []string{
		d.Repo + " ENG-1 thrawn01/eng-1 --model opus --kickoff /dispatch:design-review ENG-1",
	}, d.Workers.Launched())
}

func TestTickCountsADesignReviewAgainstMaxInProgress(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 1\n"})
	d.RealLauncher()
	d.Linear.Tickets(
		withLabel(ticket("ENG-1", "In Progress", "dispatch"), "design-review"),
		ticket("ENG-2", "In Progress", "dispatch"),
	)
	d.Tick()

	plan := d.Run("plan")

	require.NoError(t, plan.Err)
	assert.Regexp(t, planRow("waiting", "ENG-2"), plan.Stdout)
	require.Len(t, d.Workers.Started(), 1)
	assert.Equal(t, "opus", d.Workers.Started()[0].Model)
}

func TestTickHoldsAPromotionWhileADesignReviewTakesTheLastSlot(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 1\n"})
	d.RealLauncher()
	d.Linear.Tickets(parent("ENG-1", "In Progress"), withLabel(ticket("ENG-3", "In Progress", "dispatch"), "design-review"))
	d.Linear.Children(child("ENG-2", "ENG-1", "Todo"))
	d.Tick()

	plan := d.Run("plan")

	require.NoError(t, plan.Err)
	assert.Regexp(t, planRow("waiting", "ENG-2"), plan.Stdout)
	assert.Empty(t, d.Linear.Changes())
}

func TestTickHoldsAPromotionForADesignParentInAnUnreadRepo(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 1\n"})
	d.MovedRepo("tiger")
	d.Linear.Tickets(parent("ENG-1", "In Progress"), withLabel(ticket("ENG-3", "In Progress", "tiger"), "design"))
	d.Linear.Children(child("ENG-2", "ENG-1", "Todo"), child("ENG-4", "ENG-3", "Backlog"))
	d.Tick()

	// The design parent's worker is unknown in the unread repo, so it holds the only slot.
	plan := d.Run("plan")

	require.NoError(t, plan.Err)
	assert.Regexp(t, planRow("waiting", "ENG-2"), plan.Stdout)
	assert.Empty(t, d.Linear.Changes())
}

func TestTickStopsADesignWhoseTicketIsPaused(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Trees.Clean("ENG-1")
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(designTicket("ENG-1", "Backlog"))

	d.Tick()

	assert.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())
}

func TestTickReturnsTheTreeOfACanceledDesignTicket(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	tree := d.Trees.Clean("ENG-1")
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(designTicket("ENG-1", "Canceled"))

	d.Tick()

	assert.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())
	assert.Equal(t, []string{d.Repo + "|" + tree + "|ENG-1"}, d.Trees.Returned())
}

func TestTickNamesTheRulesKeyAMissingConfigLeavesOut(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[rules]\nworker_model = \"sonnet\"\nlaunch_failure_limit = 2\n"})
	d.ExactRules()
	d.Linear.Tickets(withLabel(designTicket("ENG-1", "In Progress"), "design-review"))

	out := d.Tick()

	assert.Empty(t, d.Workers.Launched())
	assert.Contains(t, out, `ENG-1 launch-worker threw: Error: config.toml has no [rules] key "design_model". Add design_model = ... under [rules]`)
	assert.Contains(t, out, "restart dispatch run")
}

func TestAWorkerWhoseLaunchReturnedIsNotClosedWhateverItsAge(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.RealLauncher()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	require.Len(t, d.Workers.Kicked(), 1)
	d.tools.Herdr.SetStatus("w2:p2", "idle")

	d.Advance(10 * time.Minute)
	d.Tick()

	assert.Empty(t, d.Workers.Closed())
	assert.Empty(t, d.Workers.Launched())
	assert.Len(t, d.Workers.Started(), 1)
}

func TestAWorkerWhoseLaunchDiedBeforeItsKickoffIsClosedAndRelaunched(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.UnconfirmedLaunch("ENG-1", d.Clock.Now())

	// A launch still inside its grace period may be about to type the kickoff.
	d.Tick()
	assert.Empty(t, d.Workers.Closed())
	assert.Empty(t, d.Workers.Launched())

	d.Advance(6 * time.Minute)
	d.Tick()

	assert.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())
	assert.Contains(t, d.LoopOut(), "close ENG-1 closed workspace w-ENG-1: its launch died before the kickoff ran")
	d.Advance(time.Second)
	d.Tick()
	assert.Len(t, d.Workers.Launched(), 1)
}

func TestAWorkerWhoseKickoffRanIsNotClosedWhateverItsAge(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")

	d.Tick()
	d.Advance(3 * time.Hour)
	d.Tick()

	assert.Empty(t, d.Workers.Closed())
	assert.Empty(t, d.Workers.Launched())
}

func TestADesignWorkerWhoseLaunchDiedIsClosedToo(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(withLabel(ticket("ENG-1", "In Progress", "dispatch"), "design"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.UnconfirmedLaunch("ENG-1", d.Clock.Now())

	d.Tick()
	d.Advance(6 * time.Minute)
	d.Tick()

	assert.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())
}

func TestADesignReviewWorkerWhoseLaunchDiedIsClosedAndRelaunched(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(withLabel(ticket("ENG-1", "In Progress", "dispatch"), "design-review"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.UnconfirmedLaunch("ENG-1", d.Clock.Now())

	d.Advance(6 * time.Minute)
	d.Tick()

	assert.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())
	assert.Contains(t, d.LoopOut(), "close ENG-1 closed workspace w-ENG-1: its launch died before the kickoff ran")
	d.Advance(time.Second)
	d.Tick()
	require.Len(t, d.Workers.Launched(), 1)
	assert.Contains(t, d.Workers.Launched()[0], "/dispatch:design-review ENG-1")
}

func TestClosingAnUnconfirmedWorkerClearsItsMark(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.StopFrom("worker", "ENG-1", stopTasks{})
	d.Advance(time.Minute)
	d.Workers.UnconfirmedLaunch("ENG-1", d.Clock.Now())
	d.Advance(6 * time.Minute)
	d.Tick()
	require.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())

	// A Claude that opens in the same tree afterwards is not the launch that died, so its idle
	// workspace is labeled asleep, not as a worker that is gone.
	d.Workers.Running("ENG-1", "idle")
	d.Advance(time.Minute)
	d.Tick()

	renames := d.Workers.Renames()
	assert.Equal(t, "w-ENG-1|💤 ENG-1", renames[len(renames)-1])
}

func TestAnIdleWorkerWithAnUnconfirmedKickoffIsNotNudged(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.UnconfirmedLaunch("ENG-1", d.Clock.Now())

	d.Tick()
	d.Advance(90 * time.Second)
	d.Tick()

	assert.Empty(t, d.Workers.Prompts())
	assert.Empty(t, d.Workers.Closed())
}

// A launch that dies every time must end in Needs Human, not loop forever. The earlier
// close is seeded because a killed launch leaves a crashed receipt, which the limit ignores.
func TestALaunchThatKeepsDyingBeforeItsKickoffReachesNeedsHuman(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	earlier := d.Clock.Now().Add(-30 * time.Minute)
	d.CrashedEarlierRun(earlierReceipt{
		Kind: "close", Subject: "ENG-1", Rule: "close-unconfirmed-worker", Outcome: "done",
		Once: earlierOnce{"close-unconfirmed", d.Trees.PathOf("ENG-1"), earlier.UnixMilli()}, Started: earlier.UnixMilli(), Ended: earlier.UnixMilli(),
	})
	d.Workers.Running("ENG-1", "idle")
	d.Workers.UnconfirmedLaunch("ENG-1", d.Clock.Now())

	d.Advance(6 * time.Minute)
	d.Tick()

	assert.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())
	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1"}, d.Alerts.Shown())
	assert.Empty(t, d.Workers.Launched())
}

func TestAStopFromTheWorkersOwnSessionConfirmsALaunchKilledAfterItsKickoffRan(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.StopFrom("worker", "ENG-1", stopTasks{Unreported: true})
	d.Workers.UnconfirmedLaunch("ENG-1", d.Clock.Now().Add(-time.Second))

	d.Advance(10 * time.Minute)
	d.Tick()

	assert.Empty(t, d.Workers.Closed())
}

func TestAStopFromAnotherSessionInTheTreeDoesNotConfirmALaunch(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.StopFrom("someone-else", "ENG-1", stopTasks{Unreported: true})
	d.Workers.UnconfirmedLaunch("ENG-1", d.Clock.Now().Add(-time.Second))

	d.Advance(10 * time.Minute)
	d.Tick()

	assert.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())
}

func TestAStopFromAnySessionConfirmsALaunchWhenHerdrNamesNoSession(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.SetSession("ENG-1", "")
	d.Workers.StopFrom("worker", "ENG-1", stopTasks{Unreported: true})
	d.Workers.UnconfirmedLaunch("ENG-1", d.Clock.Now().Add(-time.Second))

	d.Advance(10 * time.Minute)
	d.Tick()

	assert.Empty(t, d.Workers.Closed())
}

func TestAStopRecordThatCannotBeReadIsNotTakenForNoStop(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.StopFrom("worker", "ENG-1", stopTasks{Unreported: true})
	d.Workers.CorruptStopRecords(false)
	d.Workers.UnconfirmedLaunch("ENG-1", d.Clock.Now().Add(-time.Second))

	d.Advance(10 * time.Minute)
	d.Tick()

	assert.Empty(t, d.Workers.Closed())
}

func TestAKickoffThatFailedWithTheWorkspaceStuckOpenIsClosedLater(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.RealLauncher()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.tools.Herdr.Fail(herdrtest.OpKickoff, herdr.ErrKickoffUnconfirmed)
	d.tools.Herdr.Fail(herdrtest.OpCloseWorkspace, errors.New("herdr would not close it"))
	_ = d.Run("tick")
	d.tools.Herdr.Fail(herdrtest.OpCloseWorkspace, nil)
	d.tools.Herdr.Fail(herdrtest.OpKickoff, nil)
	d.tools.Herdr.SetStatus("w2:p2", "idle")

	d.Advance(10 * time.Minute)
	d.Tick()

	assert.Equal(t, []string{"w2"}, d.Workers.Closed())
}

func TestAnUnreadableStopBesideAReadableOneStillHoldsTheWorker(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.SetSession("ENG-1", "")
	d.Workers.StopFrom("worker", "ENG-1", stopTasks{Unreported: true})
	d.Advance(time.Minute)
	d.Workers.StopFrom("other", "ENG-1", stopTasks{Unreported: true})
	d.Workers.CorruptStopRecords(true)
	d.Workers.UnconfirmedLaunch("ENG-1", d.Clock.Now())

	d.Advance(10 * time.Minute)
	d.Tick()

	assert.Empty(t, d.Workers.Closed())
}
