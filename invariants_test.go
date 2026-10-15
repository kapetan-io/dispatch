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
	"github.com/kapetan-io/dispatch/internal/ledger"
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
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.tools.Herdr.StallPrompts()
	_, _, err := h.runWith(t.Context(), dispatch.Options{ActionTimeout: 100 * time.Millisecond}, "tick")
	require.NoError(t, err)
	require.Len(t, loopReceipts(h, "nudge", "ENG-1"), 1)

	// Dispatch cannot know the text never arrived, so a restart sends nothing more.
	h.restart()
	for range 2 {
		h.advance(time.Hour)
		h.tick()
	}

	assert.Len(t, loopReceipts(h, "nudge", "ENG-1"), 1)
}

func TestInvariantACommentTheWorkerWasToldAboutIsNotToldAgainAfterARestart(t *testing.T) {
	h := newHarness(t)
	h.config("[rules]\nnudge_limit = 9\n")
	h.worker("ENG-1", "working")
	h.tickets(withComment(ticket("ENG-1", "In Review", "dispatch"), comment1))
	h.tick()
	h.advance(time.Second)
	h.tickets(withComment(ticket("ENG-1", "In Review", "dispatch"), comment2))
	h.tick()
	require.Equal(t, []string{commentPrompt1}, h.prompts())

	h.restart()
	h.advance(time.Hour)
	h.tick()
	require.NoError(t, h.stopLoop())
	h.advance(time.Hour)
	h.restart()
	h.tick()

	assert.Equal(t, []string{commentPrompt1}, h.prompts())
}

func TestInvariantOneNudgeIsSentWhenAStallAndACommentAreBothOwed(t *testing.T) {
	h := newHarness(t)
	h.config("[rules]\nnudge_limit = 9\n")
	h.worker("ENG-1", "working")
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment1))
	h.tick()
	h.advance(time.Second)
	h.recordStop("ENG-1", noTasks)
	h.setWorkerStatus("ENG-1", "idle")
	h.tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment2))

	h.tick()

	assert.Len(t, h.prompts(), 1)
}

func TestInvariantAWakeFromSleepActsOnFreshLinearFacts(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
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
