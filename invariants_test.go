package dispatch_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
	"github.com/kapetan-io/dispatch/internal/history"
	"github.com/kapetan-io/dispatch/internal/ledger"
	"github.com/kapetan-io/dispatch/internal/lineartest"
	"github.com/kapetan-io/dispatch/internal/treehouse"
)

// invariantRulesWith is the shipped rules plus one more rule file.
func invariantRulesWith(t *testing.T, h *harness, extra string) string {
	t.Helper()
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
			body = append(body, "\nimport \"./extra\";\n"...)
		}
		require.NoError(t, os.WriteFile(filepath.Join(dir, e.Name()), body, 0o644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "extra.ts"), []byte(extra), 0o644))
	return dir
}

func TestInvariantACrashAfterAnAttemptedLaunchGetsNoSecondWorker(t *testing.T) {
	// A first run shows what a launch receipt looks like, with its once key.
	first := newHarness(t)
	first.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	first.tick()
	var attempt ledger.Receipt
	for _, rc := range first.ledgerNow() {
		if rc.Kind == "launch" && rc.Subject == "ENG-1" {
			attempt = rc
		}
	}
	require.Equal(t, "launch", attempt.Kind)
	attempt.Outcome, attempt.Ended, attempt.Detail, attempt.Steps = "", 0, "", nil

	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.seedHistory(nil, attempt)

	// The crashed launch got as far as opening the worker, so herdr shows it.
	h.worker("ENG-1", "working")

	h.tick()
	h.advance(time.Hour)
	h.tick()
	h.advance(time.Hour)
	h.tick()

	assert.Empty(t, h.launches())
}

// A launch attempted before a crash with no worker visible is decided again from a fresh read.
// The launcher answers already_running when the worker did land, which is what keeps it single.
func TestInvariantACrashedLaunchIsDecidedAgainAndNeverCountsAsAFailure(t *testing.T) {
	first := newHarness(t)
	first.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	first.tick()
	var attempt ledger.Receipt
	for _, rc := range first.ledgerNow() {
		if rc.Kind == "launch" && rc.Subject == "ENG-1" {
			attempt = rc
		}
	}
	require.Equal(t, "launch", attempt.Kind)
	attempt.Outcome, attempt.Ended, attempt.Detail, attempt.Steps = "", 0, "", nil
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.seedHistory(nil, attempt)

	h.tick()

	assert.Len(t, h.launches(), 1)
	var crashed, launched int
	for _, rc := range h.ledgerNow() {
		if rc.Kind != "launch" {
			continue
		}
		if rc.Outcome == ledger.Failed && rc.Detail == "crashed" {
			crashed++
		}
		if rc.Outcome == ledger.Done {
			launched++
		}
	}
	assert.Equal(t, 1, crashed)
	assert.Equal(t, 1, launched)
	assert.Empty(t, h.linear.mutations())
}

func TestInvariantARestartWithALiveWorkerLaunchesNoSecondWorker(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")
	h.tick()

	h.restart()
	h.advance(time.Hour)
	h.tick()

	assert.Empty(t, h.launches())
}

func TestInvariantAnUnreadHerdrHoldsALaunchAndPlanSaysWhy(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")
	h.tools.Herdr.Fail(herdrtest.OpRead, errors.New("server not running"))
	h.restart()
	h.advance(time.Hour)
	h.tick()

	plan, err := h.run("plan")

	require.NoError(t, err)
	assert.Empty(t, h.launches())
	assert.Regexp(t, `(?m)^held +ENG-1 +- +launch-worker +\S*herdr`, plan)
}

func TestInvariantANudgeToldBeforeARestartIsNotToldAgain(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	h.tools.Herdr.StallPrompts()
	_, _, err := h.runWith(t.Context(), dispatch.Options{ActionTimeout: 100 * time.Millisecond}, "tick")
	require.NoError(t, err)
	require.Len(t, h.receipts("nudge", "ENG-1"), 1)

	// Dispatch cannot know the text never arrived, so a restart sends nothing more.
	h.restart()
	for range 2 {
		h.advance(time.Hour)
		h.tick()
	}

	assert.Len(t, h.receipts("nudge", "ENG-1"), 1)
}

func TestInvariantAWakeFromSleepActsOnFreshLinearFacts(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	h.tick()
	require.Len(t, h.prompts(), 1)
	callsBefore := h.linear.calls()

	// While the machine slept someone moved the ticket to Needs Human, and the worker went idle again.
	h.tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	h.setWorkerStatus("ENG-1", "idle")
	h.advance(6 * time.Hour)

	assert.Greater(t, h.linear.calls(), callsBefore)
	assert.Len(t, h.prompts(), 1)
	assert.Empty(t, h.launches())
}

func TestInvariantARuleThatRunsOutOfTimeStopsOnlyItsOwnActionsAndRaisesOneAlert(t *testing.T) {
	for _, test := range []struct {
		name    string
		rule    string
		timeout dispatch.RuleTimeout
	}{
		{
			name: "If",
			rule: `import { whenever } from "dispatch";
whenever({ name: "spins-in-if", each: "linear.ticket", if: () => { for (;;) {} }, then: () => {} });
`,
			timeout: dispatch.RuleTimeout{If: 100 * time.Millisecond},
		},
		{
			name: "Then",
			rule: `import { whenever } from "dispatch";
whenever({ name: "spins-in-then", each: "linear.ticket", if: () => true, then: () => { for (;;) {} } });
`,
			timeout: dispatch.RuleTimeout{Then: 100 * time.Millisecond},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
			dir := invariantRulesWith(t, h, test.rule)

			_, _, err := h.runWith(t.Context(), dispatch.Options{RulesDir: dir, RuleTimeout: test.timeout}, "tick")
			require.NoError(t, err)
			h.advance(time.Second)
			h.tick()

			assert.Len(t, h.launches(), 1)
			var alerts []string
			for _, shown := range h.notifications() {
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
func TestInvariantAnEventAfterASleepReadsEverySourceBeforeActing(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.tick()
	before, prompted := h.linear.calls(), len(h.prompts())
	h.clock.Skew(6 * time.Hour)
	h.tickets(ticket("ENG-1", "Needs Human", "dispatch"))

	// A tell is an event that is not a read of Linear.
	_, err := h.run("tell", "ENG-1", "hello")
	require.NoError(t, err)

	assert.Greater(t, h.linear.calls(), before)
	assert.Equal(t, []string{"w-ENG-1:p1|hello"}, h.prompts()[prompted:])
}

// Pool returns decide whether a full pool is retried, so a restart must not forget one.
func TestInvariantATreeReturnedToThePoolIsInHistory(t *testing.T) {
	h := newHarness(t)
	tree := filepath.Join(h.dir, "trees", "ENG-9")
	h.tools.Treehouse.Seed(h.repo, treehouse.Tree{Path: tree, Status: "leased", LeaseHolder: "ENG-9"})
	h.tick()

	require.NoError(t, h.tools.Treehouse.Return(t.Context(), h.repo, tree, "ENG-9"))
	h.advance(time.Second)
	h.tick()

	seg, ok, err := history.Latest(filepath.Join(h.state, "events"))
	require.NoError(t, err)
	require.True(t, ok)
	state, err := seg.Load()
	require.NoError(t, err)
	assert.Equal(t, 1, state.Memory.Pools["dispatch"].Returns)
}

// A tool that fails for one read says nothing about a worker, so what dispatch knew of it stays.
func TestInvariantAFailedHerdrReadDoesNotMakeAFullPoolRetry(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.launchResult(`{"status":"pool_exhausted","detail":"all 4/4 pool trees are taken"}`)
	h.tick()
	require.Len(t, h.launches(), 1)

	h.tools.Herdr.Fail(herdrtest.OpRead, errors.New("herdr is down"))
	h.advance(time.Second)
	h.tick()
	h.tools.Herdr.Fail(herdrtest.OpRead, nil)
	h.advance(time.Second)
	h.tick()

	assert.Len(t, h.launches(), 1)
}

// A segment that was created but never given its snapshot holds nothing, so the one before it is the state.
func TestInvariantAHalfWrittenSegmentDoesNotStopTheStart(t *testing.T) {
	h := newHarness(t)
	h.seedHistory(nil, ledger.Receipt{Kind: "nudge", Subject: "ENG-1", Once: ledger.Once{"resume", float64(1)}, Rule: "nudge-resume", Outcome: ledger.Done, Started: 1})
	events := filepath.Join(h.state, "events")
	torn := filepath.Join(events, "events-"+h.clock.Now().Add(-30*time.Minute).UTC().Format("20060102T150405Z")+".jsonl")
	require.NoError(t, os.WriteFile(torn, []byte(`{"line":"header","why":"start","at":1}`+"\n"), 0o600))

	_, err := h.run("tick")

	require.NoError(t, err)
	var seeded int
	for _, rc := range h.ledgerNow() {
		if rc.Rule == "nudge-resume" && rc.Outcome == ledger.Done {
			seeded++
		}
	}
	assert.Equal(t, 1, seeded)
}

// Waits are measured from when dispatch first saw a status, so a start while Linear is down
// must not forget them, or every notice would be sent again.
func TestInvariantARestartDuringALinearOutageKeepsEveryWait(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	h.tick()
	require.Len(t, h.notifications(), 1)
	h.advance(time.Hour)
	h.linear.FailOn(lineartest.OnManaged, true)
	h.restart()

	h.linear.FailOn(lineartest.OnManaged, false)
	h.advance(5 * time.Minute)
	h.tick()

	assert.Len(t, h.notifications(), 1)
}

// A first run that crashed before its segment had a header leaves a file with nothing in it.
func TestInvariantAnEmptyFirstSegmentIsNoHistory(t *testing.T) {
	h := newHarness(t)
	events := filepath.Join(h.state, "events")
	require.NoError(t, os.MkdirAll(events, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(events, "events-20260101T000000Z.jsonl"), nil, 0o600))

	_, err := h.run("tick")

	require.NoError(t, err)
}

// A clock that stepped back must not make a newer run's history look older than an earlier run's.
func TestInvariantAClockThatSteppedBackStillLoadsTheNewestRun(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	future := h.clock.Now().Add(48 * time.Hour)
	w, err := history.Start(filepath.Join(h.state, "events"), future,
		history.Header{Why: "start", At: future.UnixMilli()}, history.Snapshot{At: future.UnixMilli()})
	require.NoError(t, err)
	require.NoError(t, w.Close(true))
	h.tick()
	require.Len(t, h.launches(), 1)

	h.restart()
	h.tick()

	assert.Len(t, h.launches(), 1)
	assert.NotEmpty(t, h.receipts("launch", "ENG-1"))
}

func TestInvariantAnOrderedStepThatFailedIsTriedAgainAtTheNextResync(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")
	// The first step makes the rule's own condition false, so only the loop can run the second again.
	dir := invariantRulesWith(t, h, `import { whenever } from "dispatch";
whenever({
  name: "ordered-pair",
  each: "linear.ticket",
  if:   (t, ctx) => t.status === "in_progress" && ctx.ledger.receipts(t, { kind: "nudge", topic: "ordered-a" }).length === 0,
  then: (t, ctx) => [
    ctx.herdr.queueNudge(t, "ordered first", { once: ["ordered-a"] }),
    ctx.linear.queueComment(t, "ordered second", { once: ["ordered-b"] }),
  ],
});
`)
	h.linear.failComments(true)
	_, _, err := h.runWith(t.Context(), dispatch.Options{RulesDir: dir}, "tick")
	require.ErrorContains(t, err, "ENG-1 comment failed")
	require.Empty(t, h.linear.mutations())

	h.linear.failComments(false)
	h.advance(time.Second)
	h.tick()

	mutations := h.linear.mutations()
	require.Len(t, mutations, 1)
	assert.Equal(t, "ordered second", mutations[0].Body)
}
