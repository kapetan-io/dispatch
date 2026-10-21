package dispatch_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/lineartest"
	"github.com/kapetan-io/dispatch/internal/sessionlog"
)

// turnsRule comments each time what a worker's turns read as changes, one comment for each reading.
const turnsRule = `
whenever({ name: "turns", each: "linear.ticket",
  if: (t) => t.status === "in_progress" || t.status === "in_review",
  then: (t, ctx) => {
    const w = ctx.herdr.worker(t);
    const body = known(() => w.turns)
      ? "turns " + w.turns.map((turn) => turn.running ? "running" : "idle").join(",") + " of " + w.turns.length
      : "turns unknown";
    return ctx.linear.queueComment(t, body, { once: ["turns", t.id, body] });
  } });`

func (h *harness) turnsRule() {
	h.t.Helper()
	h.config("[dispatch]\nrules = " + `"` + oneRule(h.t, turnsRule) + `"` + "\n")
}

// turnComments lists what the rule read, one comment for each distinct reading.
func (h *harness) turnComments() []string {
	var out []string
	for _, c := range h.linear.mutations() {
		if c.Kind == lineartest.Comment {
			out = append(out, c.Body)
		}
	}
	return out
}

func TestRulesReadEveryTurnTheSessionEndedOldestFirst(t *testing.T) {
	h := newHarness(t)
	h.turnsRule()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.recordStop("ENG-1", noTasks)
	h.advance(time.Minute)
	h.recordStop("ENG-1", runningTasks)
	h.advance(time.Minute)
	h.recordStop("ENG-1", "")

	h.tick()

	assert.Equal(t, []string{"turns idle,running,idle of 3"}, h.turnComments())
}

func TestRulesReadTurnsOfAWorkerInAnyStateOnAnInProgressOrInReviewTicket(t *testing.T) {
	for _, test := range []struct {
		name, status, worker string
	}{
		{"WorkingInProgress", "In Progress", "working"},
		{"BlockedInProgress", "In Progress", "blocked"},
		{"IdleInReview", "In Review", "done"},
		{"WorkingInReview", "In Review", "working"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.turnsRule()
			h.tickets(ticket("ENG-1", test.status, "dispatch"))
			h.worker("ENG-1", test.worker)
			h.recordStop("ENG-1", runningTasks)

			h.tick()

			assert.Equal(t, []string{"turns running of 1"}, h.turnComments())
		})
	}
}

func TestRulesReadNoTurnsOfASessionThatEndedNone(t *testing.T) {
	h := newHarness(t)
	h.turnsRule()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")

	h.tick()

	assert.Equal(t, []string{"turns  of 0"}, h.turnComments())
}

func TestRulesReadATurnWithoutBackgroundTaskReportingAsNotRunning(t *testing.T) {
	h := newHarness(t)
	h.turnsRule()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")
	h.recordStop("ENG-1", "")

	h.tick()

	assert.Equal(t, []string{"turns idle of 1"}, h.turnComments())
}

func TestRulesReadTurnsAsUnknownWhenHerdrReportsNoSession(t *testing.T) {
	h := newHarness(t)
	h.turnsRule()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")
	h.recordStop("ENG-1", noTasks)
	h.setWorkerSession("ENG-1", "")

	h.tick()

	assert.Equal(t, []string{"turns unknown"}, h.turnComments())
}

func TestRulesReadTurnsAsUnknownWhenTheSessionLogHasAHole(t *testing.T) {
	h := newHarness(t)
	h.turnsRule()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")
	h.recordStop("ENG-1", noTasks)
	appendToSessionLog(t, h.state, "not json\n")
	h.recordStop("ENG-1", noTasks)

	h.tick()

	assert.Equal(t, []string{"turns unknown"}, h.turnComments())
}

func TestRulesSkipAHalfWrittenLastLineAndReadTurnsAsUnknownOnceItIsBuriedMidFile(t *testing.T) {
	h := newHarness(t)
	h.turnsRule()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")
	h.recordStop("ENG-1", noTasks)
	appendToSessionLog(t, h.state, `{"turn":{"at":`)

	h.tick()
	assert.Equal(t, []string{"turns idle of 1"}, h.turnComments())

	// A writer that died mid-line left a hole, which the next hook must not glue its line onto.
	h.recordStop("ENG-1", runningTasks)
	h.advance(5 * time.Minute)
	h.tick()
	assert.Equal(t, []string{"turns idle of 1", "turns unknown"}, h.turnComments())
}

func TestRulesReadTurnsOfAWorkerRelaunchedInANewSessionAsEmpty(t *testing.T) {
	h := newHarness(t)
	h.turnsRule()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")
	h.recordStopFrom("worker", "ENG-1", noTasks)

	h.setWorkerSession("ENG-1", "relaunched")
	h.tick()

	assert.Equal(t, []string{"turns  of 0"}, h.turnComments())
}

func TestRulesReadTurnsOfOnlyTheCurrentSession(t *testing.T) {
	h := newHarness(t)
	h.turnsRule()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")
	h.recordStopFrom("worker", "ENG-1", noTasks)
	h.recordStopFrom("stranger", "ENG-1", runningTasks)
	h.recordStopFrom("stranger", "ENG-1", runningTasks)

	h.tick()

	assert.Equal(t, []string{"turns idle of 1"}, h.turnComments())
}

func TestRulesReadTurnsAsUnknownForATicketThatIsNeitherInProgressNorInReview(t *testing.T) {
	h := newHarness(t)
	h.config("[dispatch]\nrules = " + `"` + oneRule(t, `
whenever({ name: "turns", each: "linear.ticket", if: (t) => t.status === "needs_human",
  then: (t, ctx) => ctx.linear.queueComment(t, known(() => ctx.herdr.worker(t).turns) ? "known" : "unknown", { once: ["turns", t.id] }) });`) + `"` + "\n")
	h.tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	h.worker("ENG-1", "idle")
	h.recordStop("ENG-1", noTasks)

	h.tick()

	assert.Equal(t, []string{"unknown"}, h.turnComments())
}

func TestTurnsEndedBeforeDispatchStartsAreReadAtItsFirstTick(t *testing.T) {
	h := newHarness(t)
	h.turnsRule()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")
	h.recordStop("ENG-1", noTasks)
	h.recordStop("ENG-1", runningTasks)

	h.tick()

	assert.Equal(t, []string{"turns idle,running of 2"}, h.turnComments())
}

func TestTurnsSurviveARestartAndAStateReset(t *testing.T) {
	h := newHarness(t)
	h.turnsRule()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")
	h.recordStop("ENG-1", noTasks)
	h.tick()
	require.NoError(t, h.stopLoop())
	_, err := h.run("state", "reset")
	require.NoError(t, err)

	h.restart()
	h.advance(5 * time.Minute)
	h.recordStop("ENG-1", runningTasks)
	h.tick()

	assert.Equal(t, "turns idle,running of 2", h.turnComments()[len(h.turnComments())-1])
}

func TestATurnEndedWhileTheLoopRunsReachesRulesAtTheNextTick(t *testing.T) {
	h := newHarness(t)
	h.turnsRule()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")
	h.tick()
	require.Equal(t, []string{"turns  of 0"}, h.turnComments())

	h.recordStop("ENG-1", runningTasks)
	h.tick()

	assert.Equal(t, []string{"turns  of 0", "turns running of 1"}, h.turnComments())
}

func TestReplayReproducesADecisionThatReadTurnsAndTraceShowsThem(t *testing.T) {
	h := newHarness(t)
	h.turnsRule()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")
	h.recordStop("ENG-1", noTasks)
	h.recordStop("ENG-1", runningTasks)
	h.tick()
	require.NoError(t, h.stopLoop())
	// Replay opens no log, so the tree's logs can go.
	require.NoError(t, os.RemoveAll(sessionlog.Dir(h.state)))

	cycles, matched := replayCounts(t, h)
	out, err := h.run("trace", "eng-1")

	require.NoError(t, err)
	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
	assert.Regexp(t, `"running":true`, out)
	assert.Regexp(t, `"running":false`, out)
}

func TestTurnsOfAReturnedTreeAreNotInheritedByTheNextTicketLeasedIt(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "idle")
	h.recordStop("ENG-1", runningTasks)
	require.NotEmpty(t, sessionLogs(t, h.state))
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	remote := filepath.Join(h.dir, "remotes", "ENG-1.git")
	require.NoError(t, os.MkdirAll(filepath.Dir(remote), 0o755))
	hintGit(t, h.dir, "init", "-q", "--bare", remote)
	tree := filepath.Join(h.dir, "trees", "ENG-1")
	hintGit(t, tree, "remote", "add", "origin", remote)
	hintGit(t, tree, "push", "-q", "--set-upstream", "origin", "br-ENG-1")

	h.tick()

	assert.Empty(t, sessionLogs(t, h.state))
}

func TestRecordStopLeavesTheStopRecordWhatItWas(t *testing.T) {
	h := newHarness(t)
	h.recordStop("ENG-1", runningTasks)

	assert.Len(t, stopRecords(t, h.state), 1)
	assert.Contains(t, stopRecords(t, h.state)[0], `"running":1`)
}

func TestRecordStopStillRecordsTheStopWhenTheSessionLogCannotBeWritten(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, os.WriteFile(sessionlog.Dir(h.state), nil, 0o644))

	h.recordStop("ENG-1", runningTasks)

	assert.Len(t, stopRecords(t, h.state), 1)
}

func sessionLogs(t *testing.T, stateDir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(sessionlog.Dir(stateDir), func(path string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		out = append(out, path)
		return nil
	})
	if !os.IsNotExist(err) {
		require.NoError(t, err)
	}
	return out
}

// appendToSessionLog writes text to the end of the only session log there is.
func appendToSessionLog(t *testing.T, stateDir, text string) {
	t.Helper()
	logs := sessionLogs(t, stateDir)
	require.Len(t, logs, 1)
	f, err := os.OpenFile(logs[0], os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(text)
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

func TestWorkerFixtureTakesTurnsAndASessionThatCannotBeRead(t *testing.T) {
	h := newHarness(t)
	dir := rulesDir(t, map[string]string{
		"dispatch.ts": `import { whenever, known } from "dispatch";
whenever({ name: "second-turn-ran", each: "linear.ticket", if: (t, ctx) => {
    const w = ctx.herdr.worker(t);
    return t.status === "in_progress" && known(() => w.turns) && w.turns.length === 2 && w.turns[1].running && w.turns[1].at > w.turns[0].at;
  }, then: (t, ctx) => ctx.herdr.queueNudge(t, "hi", { once: ["second", t.id] }) });
whenever({ name: "turns-unread", each: "linear.ticket", if: (t, ctx) => t.status === "in_progress" && !known(() => ctx.herdr.worker(t).turns),
  then: (t, ctx) => ctx.herdr.queueNudge(t, "hi", { once: ["unread", t.id] }) });`,
		"turns.test.ts": `import { scenario, ticket, worker, rulesFrom } from "dispatch/testing";
const rules = rulesFrom("./dispatch");
const mine = { status: "in_progress", optedIn: true, repo: "dispatch" };

scenario(rules, "a rule reads the turns the fixture gives", s => {
  s.given(ticket("ENG-1", mine), worker("ENG-1", "idle", { turns: [{ at: 1000, running: false }, { at: 2000, running: true }] }));
  s.expect([["second-turn-ran", "herdr.nudge", "ENG-1"]]);
});

scenario(rules, "a worker given no turns has ended none", s => {
  s.given(ticket("ENG-1", mine), worker("ENG-1", "idle"));
  s.expect([]);
});

scenario(rules, "sessionUnread makes the turns unknown", s => {
  s.given(ticket("ENG-1", mine), worker("ENG-1", "idle", { sessionUnread: "no session" }));
  s.expect([["turns-unread", "herdr.nudge", "ENG-1"]]);
});

scenario(rules, "a worker with no session has no turns to read", s => {
  s.given(ticket("ENG-1", mine), worker("ENG-1", "none"));
  s.expect([["turns-unread", "herdr.nudge", "ENG-1"]]);
});
`,
	})

	out, err := h.rules(dir, "test")

	require.NoError(t, err, out)
	assert.Contains(t, out, "0 failed")
}

func TestRulesReadWhenEachTurnEnded(t *testing.T) {
	h := newHarness(t)
	h.config("[dispatch]\nrules = " + `"` + oneRule(t, `
whenever({ name: "turns", each: "linear.ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => {
    const body = "at " + ctx.herdr.worker(t).turns.map((turn) => turn.at).join(",");
    return ctx.linear.queueComment(t, body, { once: ["turns", t.id, body] });
  } });`) + `"` + "\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	first := h.clock.Now().UnixMilli()
	h.recordStop("ENG-1", noTasks)
	h.advance(time.Minute)
	h.recordStop("ENG-1", noTasks)

	h.tick()

	assert.Equal(t, []string{fmt.Sprintf("at %d,%d", first, first+time.Minute.Milliseconds())}, h.turnComments())
}

func TestRecordStopWritesNoSessionLogOutsideAWorkerTree(t *testing.T) {
	h := newHarness(t)
	h.recordStop("ENG-1", noTasks)
	logsBefore := sessionLogs(t, h.state)
	require.Len(t, logsBefore, 1)
	other := newHintRepo(t)

	for _, test := range []struct {
		name  string
		input string
	}{
		{"MainCheckout", fmt.Sprintf(`{"session_id":"s","cwd":%q,%s}`, h.repo, noTasks)},
		{"UnmanagedRepo", fmt.Sprintf(`{"session_id":"s","cwd":%q,%s}`, other, noTasks)},
		{"NotARepo", fmt.Sprintf(`{"session_id":"s","cwd":%q,%s}`, t.TempDir(), noTasks)},
		{"NotJSON", "not json"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := h.runWith(t.Context(), dispatch.Options{Stdin: strings.NewReader(test.input)}, "record-stop")

			require.NoError(t, err)
			assert.Equal(t, logsBefore, sessionLogs(t, h.state))
		})
	}
}

func TestReturningATreeLeavesTheSessionLogsOfOtherTrees(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-2", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.worker("ENG-2", "idle")
	h.recordStop("ENG-1", runningTasks)
	h.recordStop("ENG-2", runningTasks)
	require.Len(t, sessionLogs(t, h.state), 2)
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	remote := filepath.Join(h.dir, "remotes", "ENG-1.git")
	require.NoError(t, os.MkdirAll(filepath.Dir(remote), 0o755))
	hintGit(t, h.dir, "init", "-q", "--bare", remote)
	tree := filepath.Join(h.dir, "trees", "ENG-1")
	hintGit(t, tree, "remote", "add", "origin", remote)
	hintGit(t, tree, "push", "-q", "--set-upstream", "origin", "br-ENG-1")

	h.tick()

	assert.Len(t, sessionLogs(t, h.state), 1)
}

func TestATurnEndedWhileTheLoopRunsReachesRulesAtTheNextResync(t *testing.T) {
	h := newHarness(t)
	h.turnsRule()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")
	require.NoError(t, h.startLoop(dispatch.Options{}))
	t.Cleanup(func() { _ = h.stopLoop() })
	h.recordStop("ENG-1", runningTasks)

	h.advance(time.Hour)

	assert.Equal(t, "turns running of 1", h.turnComments()[len(h.turnComments())-1])
}

func TestTraceShowsTheTurnsADecisionRead(t *testing.T) {
	h := newHarness(t)
	h.turnsRule()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")
	h.recordStop("ENG-1", runningTasks)
	h.tick()

	out, err := h.run("trace", "eng-1")

	require.NoError(t, err)
	assert.Regexp(t, `turns.*\{"at":\d+,"running":true\}`, out)
}
