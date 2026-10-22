package dispatch_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/lineartest"
	"github.com/kapetan-io/dispatch/internal/treehouse"
)

func TestTickLaunchesApprovedTicketWithNoWorker(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "ENG-1 launched\n", out)
	assert.Equal(t, []string{h.repo + " ENG-1 thrawn01/eng-1 --model sonnet"}, h.launches())
	assert.Empty(t, h.linear.mutations())
}

func TestTickLeavesTicketWithLiveWorker(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Empty(t, h.launches())
}

func TestTickRelaunchesWhenWorkspaceHasNoClaude(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "")

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.launches(), 1)
}

func TestTickWaitsWhenWorkerCapIsFull(t *testing.T) {
	h := newHarness(t)
	h.config("[dispatch]\nmax_in_progress = 1\n")
	h.tickets(
		ticket("ENG-1", "In Progress", "dispatch"),
		ticket("ENG-2", "In Progress", "dispatch"),
	)
	h.worker("ENG-1", "working")

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "ENG-2 queued: launch, 1 of 1 in progress, 1st in line\n", out)
	assert.Empty(t, h.launches())
}

func TestTickLaunchesNothingForTicketsThatAreNotInProgress(t *testing.T) {
	h := newHarness(t)
	h.tickets(
		ticket("ENG-1", "In Review", "dispatch"),
		ticket("ENG-2", "Needs Human", "dispatch"),
	)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "ENG-2 notified\n", out)
	assert.Equal(t, []string{"ENG-2 is in Needs Human: ticket ENG-2"}, h.notifications())
	assert.Empty(t, h.launches())
}

func TestTickEscalatesAfterRepeatedLaunchFailures(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.launchResult(`{"status":"pool_error","detail":"treehouse lease failed"}`)

	_, err := h.run("tick")
	require.ErrorContains(t, err, "ENG-1 launch failed: pool_error: treehouse lease failed")
	assert.Empty(t, h.linear.mutations())

	// Two failures at the same millisecond are one receipt, so the clock moves between ticks.
	h.advance(time.Second)
	out, err := h.run("tick")

	require.ErrorContains(t, err, "ENG-1 launch failed")
	assert.Contains(t, out, "ENG-1 escalated: its worker failed to launch 2 times in a row")
	mutations := h.linear.mutations()
	require.Len(t, mutations, 2)
	assert.Equal(t, lineartest.Move, mutations[0].Kind)
	assert.Equal(t, needsHumanID, mutations[0].StateID)
	assert.Equal(t, lineartest.Comment, mutations[1].Kind)
	assert.Contains(t, mutations[1].Body, "treehouse lease failed")
}

func TestTickNeverEscalatesForAFullPool(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.launchResult(`{"status":"pool_exhausted","detail":"all 4/4 pool trees are taken"}`)
	// Another holder has the only tree, so the pool's return count can move when it lets go.
	other := filepath.Join(h.dir, "trees", "other")
	h.tools.Treehouse.Seed(h.repo, treehouse.Tree{Path: other, Status: "leased", LeaseHolder: "other"})

	out, err := h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, "ENG-1 launch refused: pool_exhausted\n", out)
	for range 2 {
		h.advance(time.Second)
		_, err = h.run("tick")
		require.NoError(t, err)
	}

	// While no tree comes back there is one attempt, not one per tick.
	assert.Len(t, h.launches(), 1)
	assert.Empty(t, h.linear.mutations())

	require.NoError(t, h.tools.Treehouse.Return(t.Context(), h.repo, other, "other"))
	h.launchResult(`{"status":"ok","ticket":"ENG-1","branch":"thrawn01/eng-1"}`)
	h.advance(time.Second)
	out, err = h.run("tick")

	require.NoError(t, err)
	assert.Contains(t, out, "ENG-1 launched")
	assert.Len(t, h.launches(), 2)
	assert.Empty(t, h.linear.mutations())
}

func TestTickSkipsTicketsItNeverLaunches(t *testing.T) {
	for _, test := range []struct {
		name     string
		ticket   fakeTicket
		children []fakeTicket
		want     string
	}{
		{
			name:     "ParentTicket",
			ticket:   ticket("ENG-1", "In Progress", "dispatch"),
			children: []fakeTicket{child("ENG-2", "ENG-1", "Backlog")},
			want:     "",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(test.ticket)
			h.linear.setChildren(test.children...)

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.Equal(t, test.want, out)
			assert.Empty(t, h.launches())
			assert.Empty(t, h.linear.mutations())
		})
	}
}

func TestTickAlertsWhenItEscalatesADesignTicket(t *testing.T) {
	h := newHarness(t)
	h.tickets(withLabel(ticket("ENG-1", "In Progress", ""), "design"))

	h.tick()

	require.Len(t, h.linear.mutations(), 2)
	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1"}, h.notifications())
}

func TestTickShowsHerdrNotificationsOffMacOS(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS shows alerts through osascript")
	}
	h := newHarness(t)
	h.builtinAlerts = true
	bin := t.TempDir()
	argv := filepath.Join(bin, "argv")
	require.NoError(t, os.WriteFile(filepath.Join(bin, "herdr"),
		[]byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > "+argv+"\n"), 0o755))
	t.Setenv("PATH", bin)
	h.tickets(withLabel(ticket("ENG-1", "In Progress", ""), "design"))

	h.tick()

	got, err := os.ReadFile(argv)
	require.NoError(t, err)
	assert.Equal(t, "notification\nshow\ndispatch\n--body\nENG-1 is in Needs Human: ticket ENG-1\n--sound\nrequest\n", string(got))
}

func TestTickAlertsOnceWhenItEscalatesADesignReviewTicket(t *testing.T) {
	h := newHarness(t)
	h.tickets(withLabel(ticket("ENG-1", "In Progress", ""), "design-review"))

	h.tick()

	require.Len(t, h.linear.mutations(), 2)
	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1"}, h.notifications())
}

func TestTickEscalatesTicketItCannotPlaceInARepo(t *testing.T) {
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
			h := newHarness(t)
			h.tickets(ticket("ENG-1", "In Progress", test.repo))

			_, err := h.run("tick")
			require.NoError(t, err)

			assert.Empty(t, h.launches())
			mutations := h.linear.mutations()
			require.Len(t, mutations, 2)
			assert.Equal(t, needsHumanID, mutations[0].StateID)
			assert.Contains(t, mutations[1].Body, test.wantBody)
		})
	}
}

func TestTickLaunchesOtherTicketsWhenAnEscalationFails(t *testing.T) {
	h := newHarness(t)
	h.tickets(
		ticket("ENG-1", "In Progress", ""),
		ticket("ENG-2", "In Progress", "dispatch"),
	)
	h.linear.failMoves()

	out, err := h.run("tick")

	require.ErrorContains(t, err, "ENG-1 escalate failed")
	assert.Contains(t, out, "ENG-2 launched")
	assert.Len(t, h.launches(), 1)
	// The failed move left ENG-1 In Progress, so no comment was posted that a
	// later tick would post again.
	assert.Empty(t, h.linear.mutations())
	assert.Zero(t, h.linear.CallsTo(lineartest.OnComment))
}

func TestTickNeverCommentsWhenLinearRefusesTheMove(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", ""))
	h.linear.unsuccessfulMoves()

	_, err := h.run("tick")

	require.ErrorContains(t, err, "ENG-1 escalate failed: linear MoveTicket: Linear reported success=false")
	assert.Contains(t, h.loopOut(), "escalate ENG-1 failed: linear MoveTicket: Linear reported success=false")
	assert.Zero(t, h.linear.CallsTo(lineartest.OnComment))
	assert.Empty(t, h.linear.mutations())
}

func TestTickReadsNothingWhenManagedTicketsSpanMoreThanOnePage(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.linear.overflowManaged()

	_, err := h.run("tick")

	// The tickets are unknown, which holds every rule that needs them, so the tick has nothing to fail on.
	require.NoError(t, err)
	assert.Contains(t, h.loopErr(), "more than 100 started \"ai\" tickets are assigned to you")
	assert.Empty(t, h.launches())

	// The user hears of it once, however many resyncs find the same overflow.
	h.advance(time.Hour)
	h.advance(time.Hour)
	require.Len(t, h.notifications(), 1)
	assert.Contains(t, h.notifications()[0], "more than 100 started \"ai\" tickets are assigned to you")
}

func TestTickHoldsBackOnlyTheRepoWhosePoolCannotBeRead(t *testing.T) {
	h := newHarness(t)
	// A moved clone: treehouse can't run in a directory that doesn't exist.
	h.addRepo("tiger", filepath.Join(h.dir, "moved"))
	h.tickets(
		ticket("ENG-1", "In Progress", "dispatch"),
		ticket("ENG-2", "In Progress", "tiger"),
	)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "ENG-1 launched\n")
	assert.Contains(t, out, "ENG-2 held: treehouse pool tiger unread")
	assert.Contains(t, h.loopErr(), "dispatch: while reading the tiger pool:")
	assert.Equal(t, []string{h.repo + " ENG-1 thrawn01/eng-1 --model sonnet"}, h.launches())
	assert.Empty(t, h.linear.mutations())
	plan, err := h.run("plan")
	require.NoError(t, err)
	assert.Regexp(t, `(?m)^held +ENG-2 +- +launch-worker +treehouse pool tiger unread`, plan)
}

func TestTickCountsTicketsInAnUnreadRepoAgainstTheWorkerCap(t *testing.T) {
	h := newHarness(t)
	h.config("[dispatch]\nmax_in_progress = 1\n")
	h.addRepo("tiger", filepath.Join(h.dir, "moved"))
	h.tickets(
		ticket("ENG-1", "In Progress", "dispatch"),
		ticket("ENG-2", "In Progress", "tiger"),
	)

	out, err := h.run("tick")
	require.NoError(t, err)

	// ENG-2 may have a live worker nobody could see, so ENG-1 must not launch.
	assert.Contains(t, out, "ENG-1 queued: launch, 1 of 1 in progress")
	assert.Empty(t, h.launches())
}

func TestTickPostsAnEscalationCommentThatFailedOnAnEarlierTick(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", ""))
	h.linear.failComments(true)

	_, err := h.run("tick")
	require.ErrorContains(t, err, "ENG-1 comment failed: while commenting on ENG-1")
	require.Len(t, h.linear.mutations(), 1)

	// The ticket has since left the started states the dispatcher reads, so
	// the retry can't depend on finding it among managed tickets.
	h.tickets()
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	h.linear.failComments(false)
	h.advance(time.Second)

	_, err = h.run("tick")
	require.NoError(t, err)

	mutations := h.linear.mutations()
	require.Len(t, mutations, 2)
	assert.Equal(t, lineartest.Move, mutations[0].Kind)
	assert.Equal(t, lineartest.Comment, mutations[1].Kind)
	assert.Contains(t, mutations[1].Body, "has no repo label")
	assert.Equal(t, "uuid-ENG-1", mutations[1].TicketID)

	h.advance(time.Second)
	_, err = h.run("tick")
	require.NoError(t, err)
	assert.Len(t, h.linear.mutations(), 2)
}

func TestTickRetriesAnEscalationCommentThatLinearAnsweredWithSuccessFalse(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", ""))
	h.linear.unsuccessfulComments()

	_, err := h.run("tick")

	require.ErrorContains(t, err, "ENG-1 comment failed")
	require.ErrorContains(t, err, "Linear reported success=false")
	require.Len(t, h.linear.mutations(), 1)
	assert.Equal(t, lineartest.Move, h.linear.mutations()[0].Kind)
}

func TestTickEscalatesATicketWhoseLabelsDidNotFitOnePage(t *testing.T) {
	h := newHarness(t)
	// The design label may be among the labels Linear left off the page, so
	// this ticket must not be launched as a build.
	cut := ticket("ENG-1", "In Progress", "dispatch")
	cut.LabelsTruncated = true
	h.tickets(cut)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "ENG-1 escalated: it has more labels than the dispatcher reads")
	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1"}, h.notifications())
	assert.Empty(t, h.launches())
	mutations := h.linear.mutations()
	require.Len(t, mutations, 2)
	assert.Equal(t, needsHumanID, mutations[0].StateID)
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	h := newHarness(t)

	_, err := h.run("deploy")

	require.ErrorContains(t, err, `unknown command "deploy"`)
}

func TestRunStartsActsAndStopsCleanlyOnCancel(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	var out string

	go func() {
		var err error
		out, _, err = h.runContext(ctx, "run")
		done <- err
	}()
	// The loop acts on its first read, before any tick asks.
	require.Eventually(t, func() bool { return len(h.launches()) == 1 }, 15*time.Second, 10*time.Millisecond)
	cancel()

	require.NoError(t, <-done)
	assert.Contains(t, out, "start: 1 tracked ticket, 25 rules")
	assert.Contains(t, out, "launch ENG-1 ok")
}

func TestRunRejectsANonPositiveResync(t *testing.T) {
	h := newHarness(t)
	h.config("[dispatch]\nresync = \"0s\"\n")

	_, err := h.run("run")

	require.ErrorContains(t, err, "[dispatch].resync must be positive")
}

func TestRunRejectsArguments(t *testing.T) {
	h := newHarness(t)

	_, err := h.run("run", "--interval", "20ms")

	require.ErrorContains(t, err, "run takes no arguments")
}

func TestRunExitsWhenAnotherRunHoldsTheLock(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.tick()

	_, err := h.run("run")

	require.ErrorContains(t, err, "another dispatch run holds the lock")
	assert.Len(t, h.launches(), 1)
}

func TestRunFailsToStartOnAnUnknownConfigKey(t *testing.T) {
	h := newHarness(t)
	h.config("max_in_progres = 9\n")

	_, err := h.run("run")

	require.ErrorContains(t, err, "unknown setting max_in_progres")
}

func TestRunFailsToStartWhenTheRulesDoNotLoad(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `whenever({ name: "broken", each: "linear.ticket", if: (t) => t.status === , then: () => {} });`)

	_, _, err := h.runWith(context.Background(), dispatch.Options{RulesDir: dir}, "run")

	require.ErrorContains(t, err, "problems found in the rules, so none loaded")
	assert.Empty(t, h.launches())
}
