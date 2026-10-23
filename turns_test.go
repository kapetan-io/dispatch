package dispatch_test

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// busyRule comments each time an idle worker's busy flag, which its last Stop sets, changes.
const busyRule = `
whenever({ name: "busy", each: "ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => {
    const w = ctx.worker.of(t);
    const body = known(() => w.busy) ? "busy " + w.busy : "busy unknown";
    return ctx.ticket.queueComment(t, body, { once: ["busy", t.id, body] });
  } });`

func TestRulesReadEveryTurnTheSessionEndedOldestFirst(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Advance(time.Minute)
	d.Workers.Stop("ENG-1", stopTasks{Running: 1})
	d.Advance(time.Minute)
	d.Workers.Stop("ENG-1", stopTasks{Running: 1})

	d.Tick()

	assert.Equal(t, []string{"ENG-1: turns idle,running,running of 3"}, d.Linear.Comments())
}

func TestRulesReadTurnsOfAWorkerInAnyStateOnAnInProgressOrInReviewTicket(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, status, worker string
	}{
		{"WorkingInProgress", "In Progress", "working"},
		{"BlockedInProgress", "In Progress", "blocked"},
		{"IdleInReview", "In Review", "done"},
		{"WorkingInReview", "In Review", "working"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule)})
			d.Linear.Tickets(ticket("ENG-1", test.status, "dispatch"))
			d.Workers.Running("ENG-1", test.worker)
			d.Workers.Stop("ENG-1", stopTasks{Running: 1})

			d.Tick()

			assert.Equal(t, []string{"ENG-1: turns running of 1"}, d.Linear.Comments())
		})
	}
}

func TestRulesReadNoTurnsOfASessionThatEndedNone(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")

	d.Tick()

	assert.Equal(t, []string{"ENG-1: turns  of 0"}, d.Linear.Comments())
}

func TestRulesReadATurnWithoutBackgroundTaskReportingAsNotRunning(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.Workers.Stop("ENG-1", stopTasks{Unreported: true})

	d.Tick()

	assert.Equal(t, []string{"ENG-1: turns idle of 1"}, d.Linear.Comments())
}

func TestRulesReadTurnsAsUnknownWhenHerdrReportsNoSession(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Workers.SetSession("ENG-1", "")

	d.Tick()

	assert.Equal(t, []string{"ENG-1: turns unknown"}, d.Linear.Comments())
}

// Unreachable from the CLI: only a writer that died mid-line leaves a hole in a session log.
func TestRulesReadTurnsAsUnknownWhenTheSessionLogHasAHole(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.Workers.Stop("ENG-1", stopTasks{})
	d.CorruptSessionLog("not json\n")
	d.Workers.Stop("ENG-1", stopTasks{})

	d.Tick()

	assert.Equal(t, []string{"ENG-1: turns unknown"}, d.Linear.Comments())
}

// Unreachable from the CLI: only a writer that died mid-line leaves a hole in a session log.
func TestRulesSkipAHalfWrittenLastLineAndReadTurnsAsUnknownOnceItIsBuriedMidFile(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.Workers.Stop("ENG-1", stopTasks{})
	d.CorruptSessionLog(`{"turn":{"at":`)

	d.Tick()
	assert.Equal(t, []string{"ENG-1: turns idle of 1"}, d.Linear.Comments())

	// A writer that died mid-line left a hole, which the next hook must not glue its line onto.
	d.Workers.Stop("ENG-1", stopTasks{Running: 1})
	d.Advance(5 * time.Minute)
	d.Tick()
	assert.Equal(t, []string{"ENG-1: turns idle of 1", "ENG-1: turns unknown"}, d.Linear.Comments())
}

func TestRulesReadTurnsOfAWorkerRelaunchedInANewSessionAsEmpty(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.Workers.StopFrom("worker", "ENG-1", stopTasks{})

	d.Workers.SetSession("ENG-1", "relaunched")
	d.Tick()

	assert.Equal(t, []string{"ENG-1: turns  of 0"}, d.Linear.Comments())
}

func TestRulesReadTurnsOfOnlyTheCurrentSession(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.Workers.StopFrom("worker", "ENG-1", stopTasks{})
	d.Workers.StopFrom("stranger", "ENG-1", stopTasks{Running: 1})
	d.Workers.StopFrom("stranger", "ENG-1", stopTasks{Running: 1})

	d.Tick()

	assert.Equal(t, []string{"ENG-1: turns idle of 1"}, d.Linear.Comments())
}

func TestRulesReadTurnsAsUnknownForATicketThatIsNeitherInProgressNorInReview(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(`
whenever({ name: "turns", each: "ticket", if: (t) => t.status === "needs_human",
  then: (t, ctx) => ctx.ticket.queueComment(t, known(() => ctx.worker.of(t).turns) ? "known" : "unknown", { once: ["turns", t.id] }) });`)})
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{})

	d.Tick()

	assert.Equal(t, []string{"ENG-1: unknown"}, d.Linear.Comments())
}

func TestTurnsEndedBeforeDispatchStartsAreReadAtItsFirstTick(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Workers.Stop("ENG-1", stopTasks{Running: 1})

	d.Tick()

	assert.Equal(t, []string{"ENG-1: turns idle,running of 2"}, d.Linear.Comments())
}

func TestTurnsSurviveARestartAndAStateReset(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Tick()
	d.Stop()
	require.NoError(t, d.Run("state", "reset").Err)

	d.Restart()
	d.Advance(5 * time.Minute)
	d.Workers.Stop("ENG-1", stopTasks{Running: 1})
	d.Tick()

	comments := d.Linear.Comments()
	assert.Equal(t, "ENG-1: turns idle,running of 2", comments[len(comments)-1])
}

func TestATurnEndedWhileTheLoopRunsReachesRulesAtTheNextTick(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.Tick()
	require.Equal(t, []string{"ENG-1: turns  of 0"}, d.Linear.Comments())

	d.Workers.Stop("ENG-1", stopTasks{Running: 1})
	d.Tick()

	assert.Equal(t, []string{"ENG-1: turns  of 0", "ENG-1: turns running of 1"}, d.Linear.Comments())
}

func TestATurnEndedWhileTheLoopRunsReachesRulesAtTheNextResync(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.StartBeforeTheEvents()
	d.Workers.Stop("ENG-1", stopTasks{Running: 1})

	d.Advance(time.Hour)

	comments := d.Linear.Comments()
	assert.Equal(t, "ENG-1: turns running of 1", comments[len(comments)-1])
}

func TestRulesReadTurnsAfterTheLoopRestarts(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule)})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{Running: 1})
	d.Advance(time.Minute)
	d.Workers.Stop("ENG-1", stopTasks{})

	d.Restart()
	d.Tick()

	assert.Equal(t, []string{"ENG-1: turns running,idle of 2"}, d.Linear.Comments())
}

func TestRulesReadWhenEachTurnEnded(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(`
whenever({ name: "turns", each: "ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => {
    const body = "at " + ctx.worker.of(t).turns.map((turn) => turn.at).join(",");
    return ctx.ticket.queueComment(t, body, { once: ["turns", t.id, body] });
  } });`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	first := d.Clock.Now().UnixMilli()
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Advance(time.Minute)
	d.Workers.Stop("ENG-1", stopTasks{})

	d.Tick()

	assert.Equal(t, []string{fmt.Sprintf("ENG-1: at %d,%d", first, first+time.Minute.Milliseconds())}, d.Linear.Comments())
}

func TestReplayReproducesADecisionThatReadTurnsAndTraceShowsThem(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Workers.Stop("ENG-1", stopTasks{Running: 1})
	d.Tick()
	d.Stop()
	// Replay opens no log, so the tree's logs can go.
	d.LoseSessionLogs()

	replay := d.Run("replay")
	trace := d.Run("explain", "eng-1")

	require.NoError(t, replay.Err)
	require.NoError(t, trace.Err)
	counts := regexp.MustCompile(`^(\d+) cycles?, (\d+) match\n$`).FindStringSubmatch(replay.Stdout)
	require.Len(t, counts, 3)
	cycles, _ := strconv.Atoi(counts[1])
	assert.Positive(t, cycles)
	assert.Equal(t, counts[1], counts[2])
	assert.Regexp(t, `"running":true`, trace.Stdout)
	assert.Regexp(t, `"running":false`, trace.Stdout)
}

func TestTraceShowsTheTurnsADecisionRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.Workers.Stop("ENG-1", stopTasks{Running: 1})
	d.Tick()

	res := d.Run("explain", "eng-1")

	require.NoError(t, res.Err)
	assert.Regexp(t, `turns.*\{"at":\d+,"running":true\}`, res.Stdout)
}

func TestTurnsOfAReturnedTreeAreNotInheritedByTheNextTicketLeasedIt(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: shippedRulesAnd(t, turnsRule)})
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{Running: 1})
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	tree := d.Trees.Pushed("ENG-1")
	d.Tick()
	require.Len(t, d.Trees.Returned(), 1)

	d.Linear.Tickets(ticket("ENG-2", "In Progress", "dispatch"))
	d.Workers.RunningIn("ENG-2", tree, "idle")
	d.Tick()

	assert.Equal(t, []string{"ENG-2: turns  of 0"}, d.Linear.Comments())
}

func TestReturningATreeLeavesTheTurnsOfOtherTrees(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: shippedRulesAnd(t, turnsRule)})
	d.Linear.Tickets(ticket("ENG-2", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Running("ENG-2", "idle")
	d.Workers.Stop("ENG-1", stopTasks{Running: 1})
	d.Workers.Stop("ENG-2", stopTasks{Running: 1})
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	d.Trees.Pushed("ENG-1")

	d.Tick()

	require.Len(t, d.Trees.Returned(), 1)
	assert.Equal(t, []string{"ENG-2: turns running of 1"}, d.Linear.Comments())
}

func TestTickForgetsTheStopRecordOfATreeItReturns(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: shippedRulesAnd(t, busyRule)})
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{Running: 1})
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	tree := d.Trees.Pushed("ENG-1")
	d.Tick()
	require.Len(t, d.Trees.Returned(), 1)

	d.Linear.Tickets(ticket("ENG-2", "In Progress", "dispatch"))
	d.Workers.RunningIn("ENG-2", tree, "idle")
	d.Tick()

	assert.Equal(t, []string{"ENG-2: busy false"}, d.Linear.Comments())
}

func TestAStopReportedAfterTheReturnDoesNotMakeTheNextSessionBusy(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: shippedRulesAnd(t, busyRule)})
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	tree := d.Trees.Pushed("ENG-1")
	d.Tick()
	require.Len(t, d.Trees.Returned(), 1)

	// The old session's last Stop arrives after the return.
	d.Workers.Stop("ENG-1", stopTasks{Running: 1})
	d.Linear.Tickets(ticket("ENG-2", "In Progress", "dispatch"))
	d.Workers.RunningIn("ENG-2", tree, "idle")
	// An unknown session reads the latest Stop of any session in the tree.
	d.Workers.SetSession("ENG-2", "")
	d.Tick()

	assert.Equal(t, []string{"ENG-2: busy false"}, d.Linear.Comments())
}

func TestAStopFromASessionStartedAfterTheReturnStillMakesItBusy(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: shippedRulesAnd(t, busyRule)})
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	tree := d.Trees.Pushed("ENG-1")
	d.Tick()
	require.Len(t, d.Trees.Returned(), 1)

	// The next session's Stop is later than the return.
	d.Advance(time.Minute)
	d.Workers.StopFrom("next", "ENG-1", stopTasks{Running: 1})
	d.Linear.Tickets(ticket("ENG-2", "In Progress", "dispatch"))
	d.Workers.RunningIn("ENG-2", tree, "idle")
	d.Workers.SetSession("ENG-2", "next")
	d.Tick()

	assert.Equal(t, []string{"ENG-2: busy true"}, d.Linear.Comments())
}

func TestAnIdleWorkerIsBusyWhileItsLastStopReportedBackgroundTasks(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: shippedRulesAnd(t, busyRule)})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "done")

	d.Workers.Stop("ENG-1", stopTasks{Running: 1})

	for range 3 {
		d.Advance(5 * time.Minute)
		d.Tick()
	}
	assert.Equal(t, []string{"ENG-1: busy true"}, d.Linear.Comments())
	assert.Empty(t, d.Workers.Prompts())
}

func TestRecordStopReportsTheTurnToTheRunningLoop(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.StartBeforeTheEvents()

	res := d.Hook("record-stop", d.Workers.StopInput("ENG-1", stopTasks{Running: 1}))

	require.NoError(t, res.Err)
	assert.Empty(t, res.Stdout)
	assert.Empty(t, res.Stderr)
	d.Tick()
	assert.Equal(t, []string{"ENG-1: turns  of 0", "ENG-1: turns running of 1"}, d.Linear.Comments())
}

// Unreachable from the CLI: the command line can't hold a report back past its hook's timeout.
func TestALateStopReportDoesNotReplaceANewerTurn(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.StartBeforeTheEvents()
	d.Workers.Stop("ENG-1", stopTasks{Running: 1})

	d.Workers.LateStop("ENG-1", stopTasks{}, time.Minute)

	d.Tick()
	assert.Equal(t, []string{"ENG-1: turns  of 0", "ENG-1: turns running of 1"}, d.Linear.Comments())
}

func TestAStopReportFromBeforeAClockStepIsKeptAsATurn(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.StartBeforeTheEvents()
	d.Clock.Skew(time.Hour)
	d.Workers.Stop("ENG-1", stopTasks{Running: 1})
	d.Clock.Skew(-time.Hour)

	d.Workers.Stop("ENG-1", stopTasks{})

	d.Tick()
	assert.Equal(t, []string{"ENG-1: turns  of 0", "ENG-1: turns running of 1", "ENG-1: turns running,idle of 2"}, d.Linear.Comments())
}

func TestRecordStopDoesNothingOutsideAWorkerTree(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		cwd   func(t *testing.T, d *Dispatch) string
		input string
	}{
		{"MainCheckout", func(t *testing.T, d *Dispatch) string { return d.Repo }, ""},
		{"UnmanagedRepo", func(t *testing.T, d *Dispatch) string { return newHintRepo(t) }, ""},
		{"NotARepo", func(t *testing.T, d *Dispatch) string { return t.TempDir() }, ""},
		{"NotJSON", func(t *testing.T, d *Dispatch) string { return t.TempDir() }, "not json"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule)})
			// A Stop in a worker tree makes the dispatch repo a git checkout the cases can run in.
			d.Workers.Stop("ENG-9", stopTasks{})
			cwd := test.cwd(t, d)
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
			d.Workers.RunningIn("ENG-1", cwd, "working")
			input := test.input
			if input == "" {
				input = fmt.Sprintf(`{"session_id":"worker","cwd":%q,"background_tasks":[]}`, cwd)
			}

			res := d.Hook("record-stop", input)

			require.NoError(t, res.Err)
			assert.Empty(t, res.Stdout)
			assert.Empty(t, res.Stderr)
			d.Tick()
			assert.Equal(t, []string{"ENG-1: turns  of 0"}, d.Linear.Comments())
		})
	}
}

func TestRecordStopStaysSilentWithoutAConfig(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})

	res := d.HookWithoutConfig("record-stop", `{"cwd":"/tmp","background_tasks":[]}`)

	require.NoError(t, res.Err)
	assert.Empty(t, res.Stdout)
	assert.Empty(t, res.Stderr)
}

// Unreachable from the CLI: a running dispatch can't be made to stop answering.
func TestRecordStopDoesNotWaitOnAStuckDispatch(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	input := d.Workers.StopInput("ENG-1", stopTasks{})
	d.BrokenLoop(neverAnswers)

	hooked := later(func() Result { return d.Hook("record-stop", input) })
	require.Eventually(t, func() bool { return d.LoopCalls() == 1 }, 20*time.Second, time.Millisecond)
	d.Expire("record-stop")
	res := hooked()

	require.NoError(t, res.Err)
	assert.Equal(t, 1, d.LoopCalls())
	assert.Empty(t, res.Stdout)
	assert.Empty(t, res.Stderr)
}

// Unreachable from the CLI: a running dispatch can't be made to fail these ways.
func TestRecordStopStaysSilentHoweverTheConnectionFails(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		fault loopFault
	}{
		{"StaleSocketFile", staleSocket},
		{"ClosesWithoutAnswering", closesWithoutAnswering},
		{"AnswersGarbage", answersGarbage},
		{"AnswersWithAnError", answersWithAnError},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			input := d.Workers.StopInput("ENG-1", stopTasks{})
			d.BrokenLoop(test.fault)
			entries := d.StateEntries()

			res := d.Hook("record-stop", input)

			require.NoError(t, res.Err)
			assert.Empty(t, res.Stdout)
			assert.Empty(t, res.Stderr)
			assert.Equal(t, entries, d.StateEntries())
		})
	}
}

func TestRecordStopExitsCleanlyOnABadConfig(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "this is = = not toml"})
	entries := d.StateEntries()

	res := d.HookWhileDown("record-stop", d.Workers.StopInput("ENG-1", stopTasks{}))

	require.NoError(t, res.Err)
	assert.Empty(t, res.Stdout)
	assert.Equal(t, entries, d.StateEntries())
}

func TestWorkerFixtureTakesTurnsAndASessionThatCannotBeRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	dir := rulesDir(t, map[string]string{
		"dispatch.ts": `import { whenever, known } from "dispatch";
whenever({ name: "second-turn-ran", each: "ticket", if: (t, ctx) => {
    const w = ctx.worker.of(t);
    return t.status === "in_progress" && known(() => w.turns) && w.turns.length === 2 && w.turns[1].running && w.turns[1].at > w.turns[0].at;
  }, then: (t, ctx) => ctx.worker.queueNudge(t, "hi", { once: ["second", t.id] }) });
whenever({ name: "turns-unread", each: "ticket", if: (t, ctx) => t.status === "in_progress" && !known(() => ctx.worker.of(t).turns),
  then: (t, ctx) => ctx.worker.queueNudge(t, "hi", { once: ["unread", t.id] }) });`,
		"turns.test.ts": `import { scenario, ticket, worker, rulesFrom } from "dispatch/testing";
const rules = rulesFrom("./dispatch");
const mine = { status: "in_progress", optedIn: true, repo: "dispatch" };

scenario(rules, "a rule reads the turns the fixture gives", s => {
  s.given(ticket("ENG-1", mine), worker("ENG-1", "idle", { turns: [{ at: 1000, running: false }, { at: 2000, running: true }] }));
  s.expect([["second-turn-ran", "nudge", "ENG-1"]]);
});

scenario(rules, "a worker given no turns has ended none", s => {
  s.given(ticket("ENG-1", mine), worker("ENG-1", "idle"));
  s.expect([]);
});

scenario(rules, "sessionUnread makes the turns unknown", s => {
  s.given(ticket("ENG-1", mine), worker("ENG-1", "idle", { sessionUnread: "no session" }));
  s.expect([["turns-unread", "nudge", "ENG-1"]]);
});

scenario(rules, "a worker with no session has no turns to read", s => {
  s.given(ticket("ENG-1", mine), worker("ENG-1", "none"));
  s.expect([["turns-unread", "nudge", "ENG-1"]]);
});
`,
	})

	res := d.Run("rules", "test", dir)

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "0 failed")
}

func TestTickNudgesAgainAtTheNextResyncWhenTheWorkerLeftNoStopRecord(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		stop func(d *Dispatch)
	}{
		{"NoRecord", func(d *Dispatch) {}},
		{"NoBackgroundTasksField", func(d *Dispatch) { d.Workers.Stop("ENG-1", stopTasks{Unreported: true}) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Config: "[rules]\nnudge_limit = 10\n"})
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			test.stop(d)
			// A PR that fails after the ticket was seen In Review is what the nudge count measures from.
			d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "PENDING", mergeable: "MERGEABLE"})
			d.Restart()
			d.Advance(time.Second)
			d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

			d.Tick()
			assert.Len(t, d.Workers.Prompts(), 1)

			// A nudged worker shows as working until the next resync, so a resync that
			// finds it idle again starts a new stretch of idling, and a new nudge.
			d.Advance(5 * time.Minute)
			assert.Len(t, d.Workers.Prompts(), 2)

			// A tick on the same clock starts no new stretch.
			d.Tick()
			assert.Len(t, d.Workers.Prompts(), 2)

			d.Workers.SetStatus("ENG-1", "working")
			d.Advance(5 * time.Minute)
			assert.Len(t, d.Workers.Prompts(), 2)
			d.Workers.SetStatus("ENG-1", "idle")
			d.Advance(5 * time.Minute)
			assert.Len(t, d.Workers.Prompts(), 3)
		})
	}
}

func TestTickNudgesAnIdleWorkerAfterAnotherSessionStopsBusyInItsTree(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[rules]\nnudge_limit = 10\n"})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	d.Workers.StopFrom("worker", "ENG-1", stopTasks{})
	d.Tick()
	assert.Len(t, d.Workers.Prompts(), 1)

	d.Advance(time.Minute)
	d.Workers.StopFrom("stranger", "ENG-1", stopTasks{Running: 1})
	d.Advance(5 * time.Minute)
	d.Workers.StopFrom("worker", "ENG-1", stopTasks{})
	d.Tick()

	assert.Len(t, d.Workers.Prompts(), 2)
}

func TestTickSendsTheResumeNudgeWhateverTheStopRecordSays(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Tick()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Stop("ENG-1", stopTasks{Running: 1})

	d.Tick()
	d.Tick()

	assert.Equal(t, []string{"w-ENG-1:p1|" + resumeText1}, d.Workers.Prompts())
	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1"}, d.Alerts.Shown())
}

func TestStopHookRunsRecordStop(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("hooks/hooks.json")
	require.NoError(t, err)

	assert.Contains(t, string(body), `"Stop"`)
	assert.Contains(t, string(body), "exec dispatch record-stop")
}

func TestALateStopReportDoesNotReplaceTheStopRecordOfANewerOne(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: shippedRulesAnd(t, busyRule)})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "done")
	d.Workers.Stop("ENG-1", stopTasks{Running: 1})

	d.Workers.LateStop("ENG-1", stopTasks{}, time.Minute)

	d.Tick()
	assert.Equal(t, []string{"ENG-1: busy true"}, d.Linear.Comments())
}

func TestAStopReportAfterAClockStepReplacesTheStopRecordStampedAhead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: shippedRulesAnd(t, busyRule)})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "done")
	d.Clock.Skew(time.Hour)
	d.Workers.Stop("ENG-1", stopTasks{Running: 1})
	d.Clock.Skew(-time.Hour)

	d.Workers.Stop("ENG-1", stopTasks{})

	d.Tick()
	comments := d.Linear.Comments()
	assert.Equal(t, "ENG-1: busy false", comments[len(comments)-1])
}

// Unreachable from the CLI: no command makes the session log directory unwritable.
func TestTheRunningLoopKeepsTheStopRecordWhenTheSessionLogCannotBeWritten(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: shippedRulesAnd(t, busyRule)})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "done")
	d.BlockSessionLogs()

	res := d.Hook("record-stop", d.Workers.StopInput("ENG-1", stopTasks{Running: 1}))

	require.NoError(t, res.Err)
	d.Tick()
	assert.Equal(t, []string{"ENG-1: busy true"}, d.Linear.Comments())
}

func TestAStopReportDroppedWhileNoDispatchRunsIsNeverRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	input := d.Workers.StopInput("ENG-1", stopTasks{Running: 1})
	before := d.StateEntries()

	res := d.HookWhileDown("record-stop", input)

	require.NoError(t, res.Err)
	assert.Empty(t, res.Stdout)
	assert.Empty(t, res.Stderr)
	assert.Equal(t, before, d.StateEntries())
	d.Tick()
	assert.Equal(t, []string{"ENG-1: turns  of 0"}, d.Linear.Comments())
}
