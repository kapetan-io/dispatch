package dispatch_test

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
)

// readKeptRules fetches in the first cycle and reads the kept answer in a later one.
const readKeptRules = `
whenever({ name: "fetch", each: "linear.ticket", if: (t, ctx) => !ctx.ledger.done(t, ["fetch", t.id]),
  then: (t, ctx) => { ctx.http.get("sentry", "/count"); return ctx.linear.queueComment(t, "fetched", { once: ["fetch", t.id] }); } });
whenever({ name: "use-kept", each: "linear.ticket", if: (t, ctx) => ctx.ledger.done(t, ["fetch", t.id]) && known(() => ctx.http.last("sentry", "/count")),
  then: (t, ctx) => ctx.linear.queueComment(t, "kept " + (ctx.http.last("sentry", "/count").body as any).open, { once: ["kept", t.id] }) });`

// httpHistory runs rules that call sentry for a few ticks and stops the loop, leaving history to replay.
func httpHistory(t *testing.T, h *harness, body string) {
	t.Helper()
	sentry := h.sentry()
	sentry.Respond("sentry", "GET", "/count", 200, `{"open":3}`)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.rulesDir = oneRule(t, body)
	_, err := h.run("tick")
	require.NoError(t, err)
	h.advance(time.Minute)
	h.tick()
	require.NoError(t, h.stopLoop())
}

func TestReplayMatchesARunWhoseRuleCalledGet(t *testing.T) {
	h := newHarness(t)
	httpHistory(t, h, countRule)
	live := len(h.http.Requests())

	cycles, matched := replayCounts(t, h)

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
	assert.Len(t, h.http.Requests(), live)
}

func TestReplayMatchesALastReadOfAnAnswerAnEarlierCycleKept(t *testing.T) {
	h := newHarness(t)
	httpHistory(t, h, readKeptRules)
	require.Contains(t, strings.Join(comments(h), "\n"), "kept 3")

	cycles, matched := replayCounts(t, h)

	assert.Greater(t, cycles, 1)
	assert.Equal(t, cycles, matched)
}

func TestReplayFromALaterCycleRebuildsWhatEarlierCyclesKept(t *testing.T) {
	h := newHarness(t)
	httpHistory(t, h, readKeptRules)

	out, err := h.run("replay", "--from", h.clock.Now().Add(-30*time.Second).Format(time.RFC3339))

	require.NoError(t, err)
	assert.NotContains(t, out, "difference at")
}

func TestReplayWithOtherRulesCountsAGetThatDiffersFromTheLiveRunAsADifference(t *testing.T) {
	for _, test := range []struct{ name, body string }{
		{"OtherPath", `whenever({ name: "count", each: "linear.ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.linear.queueComment(t, "open " + (ctx.http.get("sentry", "/other").body as any).open, { once: ["count", t.id] }) });`},
		{"ExtraGet", `whenever({ name: "count", each: "linear.ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => { ctx.http.get("sentry", "/count"); return ctx.linear.queueComment(t, "open " + (ctx.http.get("sentry", "/count").body as any).open, { once: ["count", t.id] }); } });`},
		{"SkippedGet", `whenever({ name: "count", each: "linear.ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.linear.queueComment(t, "open 3", { once: ["count", t.id] }) });`},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			httpHistory(t, h, countRule)
			live := len(h.http.Requests())

			out, err := h.run("replay", "--rules", oneRule(t, test.body))

			require.NoError(t, err)
			assert.Contains(t, out, "difference at")
			assert.Len(t, h.http.Requests(), live)
		})
	}
}

func TestReplayWithOtherRulesMatchesWhenTheGetsAreTheSame(t *testing.T) {
	h := newHarness(t)
	httpHistory(t, h, countRule)

	cycles, matched := replayCounts(t, h, "--rules", oneRule(t, countRule))

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}

func TestEachRecordedExchangeNamesItsRuleAndTicket(t *testing.T) {
	h := newHarness(t)
	httpHistory(t, h, countRule)

	segment, err := os.ReadFile(newestSegment(t, h))
	require.NoError(t, err)

	assert.Contains(t, string(segment), `"http":[{"service":"sentry","path":"/count","rule":"count","ticket":"ENG-1","status":200,"body":{"open":3}}]`)
}

func TestTraceShowsTheExchangesOfATicket(t *testing.T) {
	h := newHarness(t)
	httpHistory(t, h, countRule)

	out, err := h.run("trace", "ENG-1")

	require.NoError(t, err)
	assert.Contains(t, out, `http  count  GET sentry /count  200 {"open":3}`)
}

func TestReplayOfHistoryWithoutRuleAndTicketOnItsExchangesStillMatches(t *testing.T) {
	h := newHarness(t)
	httpHistory(t, h, countRule)
	editSegment(t, newestSegment(t, h), `,"rule":"count","ticket":"ENG-1"`, ``)

	cycles, matched := replayCounts(t, h)

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}

func TestStateResetQuietRecordsTheMessagesAnHTTPRuleWants(t *testing.T) {
	h := newHarness(t)
	httpHistory(t, h, countRule)
	require.Equal(t, []string{"open 3"}, comments(h))

	out, err := h.run("state", "reset", "--quiet")
	require.NoError(t, err)
	require.NoError(t, h.startLoop(dispatch.Options{}))
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "1 receipt for the messages the first cycle wants")
	assert.Equal(t, []string{"open 3"}, comments(h))
}

func TestStateResetQuietRefusesWhenAServiceAnswerIsUnknown(t *testing.T) {
	h := newHarness(t)
	httpHistory(t, h, countRule)
	h.http.Respond("sentry", "GET", "/count", 503, "")

	_, err := h.run("state", "reset", "--quiet")

	require.ErrorContains(t, err, "cannot record the messages the first cycle wants")
	assert.ErrorContains(t, err, "sentry /count")
}

// clearedRules comments once the kept answer is gone, which only a resync or a tick brings about.
const clearedRules = `
whenever({ name: "fetch", each: "linear.ticket", if: (t, ctx) => !ctx.ledger.done(t, ["fetch", t.id]),
  then: (t, ctx) => { ctx.http.get("sentry", "/count"); return ctx.linear.queueComment(t, "fetched", { once: ["fetch", t.id] }); } });
whenever({ name: "cleared", each: "linear.ticket", if: (t, ctx) => ctx.ledger.done(t, ["fetch", t.id]) && !known(() => ctx.http.last("sentry", "/count")),
  then: (t, ctx) => ctx.linear.queueComment(t, "cleared", { once: ["cleared", t.id] }) });`

func TestReplayFromACycleLaterThanTheOneThatKeptTheAnswerRebuildsIt(t *testing.T) {
	h := newHarness(t)
	start := h.clock.Now()
	httpHistory(t, h, readKeptRules)
	// The cycle that reads the kept answer moves to 20s after the cycle that fetched it, with no resync or tick between.
	editSegment(t, newestSegment(t, h),
		fmt.Sprintf(`"now":%d,"why":"event"`, start.UnixMilli()),
		fmt.Sprintf(`"now":%d,"why":"event"`, start.Add(20*time.Second).UnixMilli()))

	out, err := h.run("replay", "--from", start.Add(10*time.Second).Format(time.RFC3339))

	require.NoError(t, err)
	assert.NotContains(t, out, "difference at")
	assert.Regexp(t, `^3 cycles, 3 match\n$`, out)
}

func TestReplayClearsWhatWasKeptAtATickTheLiveRunClearedItAt(t *testing.T) {
	h := newHarness(t)
	httpHistory(t, h, clearedRules)
	require.Equal(t, []string{"fetched", "cleared"}, comments(h))

	cycles, matched := replayCounts(t, h)

	assert.Greater(t, cycles, 2)
	assert.Equal(t, cycles, matched)
}

func TestReplayClearsWhatWasKeptAtAResyncTheLiveRunClearedItAt(t *testing.T) {
	h := newHarness(t)
	sentry := h.sentry()
	sentry.Respond("sentry", "GET", "/count", 200, `{"open":3}`)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.rulesDir = oneRule(t, clearedRules)
	_, err := h.run("tick")
	require.NoError(t, err)
	h.advance(5 * time.Minute)
	require.NoError(t, h.stopLoop())
	require.Equal(t, []string{"fetched", "cleared"}, comments(h))

	cycles, matched := replayCounts(t, h)

	assert.Greater(t, cycles, 2)
	assert.Equal(t, cycles, matched)
}

func TestReplayOfAGetThatFailedLiveHoldsTheRuleAgain(t *testing.T) {
	h := newHarness(t)
	sentry := h.sentry()
	sentry.Respond("sentry", "GET", "/count", 503, ``)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.rulesDir = oneRule(t, countRule)
	_, err := h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())
	require.Empty(t, comments(h))
	live := len(sentry.Requests())

	cycles, matched := replayCounts(t, h)

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
	assert.Len(t, sentry.Requests(), live)
}

func TestReplayWithOtherRulesCountsAnUnrecordedGetAsADifferenceEvenWhenTheActionsMatch(t *testing.T) {
	h := newHarness(t)
	httpHistory(t, h, countRule)
	live := len(h.http.Requests())
	other := oneRule(t, `whenever({ name: "count", each: "linear.ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => { known(() => ctx.http.get("sentry", "/other")); return ctx.linear.queueComment(t, "open 3", { once: ["count", t.id] }); } });`)

	out, err := h.run("replay", "--rules", other)

	require.NoError(t, err)
	assert.Contains(t, out, "difference at")
	assert.Contains(t, out, "GET sentry /other")
	assert.Len(t, h.http.Requests(), live)
}

func TestReplayMatchesAGetTwoTicketsEachMade(t *testing.T) {
	h := newHarness(t)
	sentry := h.sentry()
	sentry.Respond("sentry", "GET", "/count", 200, `{"open":3}`)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	h.rulesDir = oneRule(t, countRule)
	_, err := h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())
	require.Equal(t, []string{"open 3", "open 3"}, comments(h))

	cycles, matched := replayCounts(t, h)

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}

func TestTraceShowsOnlyTheExchangesOfTheTicketItIsGiven(t *testing.T) {
	h := newHarness(t)
	sentry := h.sentry()
	sentry.Respond("sentry", "GET", "/count/ENG-1", 200, `{"open":3}`)
	sentry.Respond("sentry", "GET", "/count/ENG-2", 200, `{"open":4}`)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	h.rulesDir = oneRule(t, `whenever({ name: "count", each: "linear.ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.linear.queueComment(t, "open " + (ctx.http.get("sentry", "/count/" + t.id).body as any).open, { once: ["count", t.id] }) });`)
	_, err := h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())

	out1, err := h.run("trace", "ENG-1")
	require.NoError(t, err)
	out2, err := h.run("trace", "ENG-2")
	require.NoError(t, err)

	assert.Contains(t, out1, "GET sentry /count/ENG-1")
	assert.NotContains(t, out1, "/count/ENG-2")
	assert.Contains(t, out2, "GET sentry /count/ENG-2")
	assert.NotContains(t, out2, "/count/ENG-1")
}

func TestStateResetQuietWorksOnceTheServiceAnswersAfterRefusing(t *testing.T) {
	h := newHarness(t)
	httpHistory(t, h, countRule)
	h.http.Respond("sentry", "GET", "/count", 503, "")

	_, err := h.run("state", "reset", "--quiet")
	require.Error(t, err)
	h.http.Respond("sentry", "GET", "/count", 200, `{"open":3}`)
	out, err := h.run("state", "reset", "--quiet")

	require.NoError(t, err)
	assert.Contains(t, out, "1 receipt for the messages the first cycle wants")
}

// markRules says whether the answer fetched earlier is still kept.
const markRules = `
whenever({ name: "fetch", each: "linear.ticket", if: (t, ctx) => !ctx.ledger.done(t, ["fetch", t.id]),
  then: (t, ctx) => { ctx.http.get("sentry", "/count"); return ctx.linear.queueComment(t, "fetched", { once: ["fetch", t.id] }); } });
whenever({ name: "mark", each: "linear.ticket", if: (t, ctx) => ctx.ledger.done(t, ["fetch", t.id]),
  then: (t, ctx) => { const state = known(() => ctx.http.last("sentry", "/count")) ? "kept" : "empty"; return ctx.linear.queueComment(t, state, { once: ["mark", t.id, state] }); } });`

func TestReplayStartsTheSegmentAfterARestartWithNothingKept(t *testing.T) {
	h := newHarness(t)
	h.sentry().Respond("sentry", "GET", "/count", 200, `{"open":3}`)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.rulesDir = oneRule(t, markRules)
	_, err := h.run("tick")
	require.NoError(t, err)
	h.restart()
	require.NoError(t, h.stopLoop())
	require.Equal(t, []string{"fetched", "kept", "empty"}, comments(h))

	cycles, matched := replayCounts(t, h)

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}

func TestReplayMatchesALastReadAfterACycleThatWroteNoLinesRefilledWhatAResyncCleared(t *testing.T) {
	h := newHarness(t)
	sentry := h.sentry()
	sentry.Respond("sentry", "GET", "/count", 200, `{"open":3}`)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	// poll gets on every cycle while the worker is idle, so the cycle after a tick refills what the tick
	// cleared, and the cycle that sees the worker typed into (working) only reads it.
	h.rulesDir = oneRule(t, `
whenever({ name: "poll", each: "linear.ticket", if: (t, ctx) => t.status === "in_progress" && ctx.herdr.worker(t).state === "idle",
  then: (t, ctx) => { ctx.http.get("sentry", "/count"); return ctx.linear.queueComment(t, "polled", { once: ["poll", t.id] }); } });
whenever({ name: "working-reader", each: "linear.ticket", if: (t, ctx) => ctx.herdr.worker(t).state === "working" && known(() => ctx.http.last("sentry", "/count")),
  then: (t, ctx) => ctx.linear.queueComment(t, "working with " + (ctx.http.last("sentry", "/count").body as any).open, { once: ["working", t.id] }) });`)
	_, err := h.run("tick")
	require.NoError(t, err)
	h.advance(time.Minute)
	h.tick()
	// Nothing changed since the last tick, so the cycle after this one writes no line at all.
	h.advance(time.Minute)
	h.tick()

	_, err = h.run("tell", "ENG-1", "hi")
	require.NoError(t, err)
	require.Eventually(t, func() bool { return slices.Contains(comments(h), "working with 3") }, 5*time.Second, 5*time.Millisecond)
	require.NoError(t, h.stopLoop())

	cycles, matched := replayCounts(t, h)

	assert.Equal(t, cycles, matched)
}

func TestTraceShowsAnAnswerThatChangedWhileTheRuleDecidedTheSame(t *testing.T) {
	h := newHarness(t)
	sentry := h.sentry()
	sentry.Respond("sentry", "GET", "/count", 200, `{"open":3}`)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.rulesDir = oneRule(t, countRule)
	_, err := h.run("tick")
	require.NoError(t, err)
	h.advance(time.Minute)
	h.tick()
	sentry.Respond("sentry", "GET", "/count", 200, `{"open":4}`)
	h.advance(time.Minute)
	h.tick()
	require.NoError(t, h.stopLoop())

	out, err := h.run("trace", "ENG-1")

	require.NoError(t, err)
	assert.Contains(t, out, `GET sentry /count  200 {"open":3}`)
	assert.Contains(t, out, `GET sentry /count  200 {"open":4}`)
}

func TestReplayWithOtherRulesMatchesTheSameGetsRegisteredInAnotherOrder(t *testing.T) {
	const first = `whenever({ name: "first", each: "linear.ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.linear.queueComment(t, "a " + (ctx.http.get("sentry", "/a").body as any).n, { once: ["a", t.id] }) });`
	const second = `whenever({ name: "second", each: "linear.ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.linear.queueComment(t, "b " + (ctx.http.get("sentry", "/b").body as any).n, { once: ["b", t.id] }) });`
	h := newHarness(t)
	sentry := h.sentry()
	sentry.Respond("sentry", "GET", "/a", 200, `{"n":1}`)
	sentry.Respond("sentry", "GET", "/b", 200, `{"n":2}`)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.rulesDir = oneRule(t, first+"\n"+second)
	_, err := h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())

	cycles, matched := replayCounts(t, h, "--rules", oneRule(t, second+"\n"+first))

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}
