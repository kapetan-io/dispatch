package dispatch_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/history"
	"github.com/kapetan-io/dispatch/internal/ledger"
	"github.com/kapetan-io/dispatch/internal/lineartest"
)

var replayed = regexp.MustCompile(`^(\d+) cycles?, (\d+) match\n$`)

// replayCounts runs replay and returns the cycles it ran again and how many matched.
func replayCounts(t *testing.T, h *harness, args ...string) (cycles, matched int) {
	t.Helper()
	out, err := h.run(append([]string{"replay"}, args...)...)
	require.NoError(t, err)
	found := replayed.FindStringSubmatch(out)
	require.NotNil(t, found, out)
	cycles, _ = strconv.Atoi(found[1])
	matched, _ = strconv.Atoi(found[2])
	return cycles, matched
}

// newestSegment is the path of the history segment the loop wrote last.
func newestSegment(t *testing.T, h *harness) string {
	t.Helper()
	paths, err := history.Segments(filepath.Join(h.state, "events"))
	require.NoError(t, err)
	require.NotEmpty(t, paths)
	return paths[len(paths)-1]
}

func TestReplayMatchesTheLiveRun(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())

	cycles, matched := replayCounts(t, h)

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}

// Each case leaves the same lines in the world's memory as its first tick and changes what the
// rules decide, so only a replay over the recorded read can match.
func TestReplayMatchesWhatMemoryAloneCannotTellApart(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(h *harness)
	}{
		{"BlockersCutShort", func(h *harness) {
			h.tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), true, [2]string{"ENG-1", "Done"}))
		}},
		{"AnotherOpenBlocker", func(h *harness) {
			h.tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Progress"}))
			_, err := h.run("tick")
			require.NoError(t, err)
			h.tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-5", "In Progress"}))
		}},
		{"AnUnreadPool", func(h *harness) {
			h.addRepo("tiger", filepath.Join(h.dir, "moved"))
			h.tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "tiger"))
		}},
		{"ChildrenUnread", func(h *harness) {
			h.tickets(parent("ENG-1", "In Progress"))
			h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"))
			h.linear.failChildren()
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			test.setup(h)
			_, err := h.run("tick")
			require.NoError(t, err)
			require.NoError(t, h.stopLoop())

			cycles, matched := replayCounts(t, h)

			assert.Positive(t, cycles)
			assert.Equal(t, cycles, matched)
		})
	}
}

func TestReplayPrintsTheFirstDifferenceAndExitsOne(t *testing.T) {
	h := newHarness(t)
	h.tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Review"}))
	_, err := h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())
	editSegment(t, newestSegment(t, h), `"rule":"note-blocker-hold"`, `"rule":"another-rule"`)

	out, err := h.run("replay")

	require.Error(t, err)
	assert.Equal(t, 1, dispatch.ExitCode(err))
	assert.Contains(t, out, "difference at")
	assert.Contains(t, out, "recorded: wants comment ENG-2 once")
	assert.Contains(t, out, "(another-rule)")
	assert.Contains(t, out, "(note-blocker-hold)")
}

func TestReplayNamesTheMemoryItFoundDifferent(t *testing.T) {
	h := newHarness(t)
	h.tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Review"}))
	_, err := h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())
	// The blocker was Done when the cycle ran, as far as replay can tell.
	editSegment(t, newestSegment(t, h), `"Status":"In Review"`, `"Status":"Done"`)

	out, err := h.run("replay")

	require.Error(t, err)
	assert.Equal(t, 1, dispatch.ExitCode(err))
	assert.Contains(t, out, "memory: recorded")
}

func editSegment(t *testing.T, path, old, replacement string) {
	t.Helper()
	text, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(text), old)
	require.NoError(t, os.WriteFile(path, []byte(strings.ReplaceAll(string(text), old, replacement)), 0o600))
}

func TestReplayWithOtherRulesStopsAtTheFirstActionThatDiffers(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())
	quiet := oneRule(t, `whenever({ name: "launch-worker", each: "linear.ticket", if: () => false, then: () => [] });`)

	out, err := h.run("replay", "--rules", quiet)

	require.NoError(t, err)
	assert.Contains(t, out, "difference at")
	assert.Regexp(t, `recorded: wants launch ENG-1`, out)
	assert.Contains(t, out, "replayed: nothing like it")
}

func TestReplayWithTheSameRulesMatches(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())

	cycles, matched := replayCounts(t, h, "--rules", h.rulesDir)

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}

func TestReplayUsesTheRulesTheLiveRunHadWhenTheFilesChanged(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	entry, err := os.ReadFile(filepath.Join(h.rulesDir, "dispatch.ts"))
	require.NoError(t, err)
	files, err := filepath.Glob(filepath.Join(h.rulesDir, "*.ts"))
	require.NoError(t, err)
	for _, f := range files {
		if strings.HasSuffix(f, ".test.ts") {
			continue
		}
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, filepath.Base(f)), b, 0o600))
	}
	require.NotEmpty(t, entry)
	h.rulesDir = dir
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err = h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())
	// A rules file that no longer loads must not matter to a replay of the run that used the old one.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dispatch.ts"), []byte("not typescript {"), 0o600))

	cycles, matched := replayCounts(t, h)

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}

func TestReplayOfHistoryWithoutAReadExitsTwo(t *testing.T) {
	h := newHarness(t)
	w, err := history.Start(filepath.Join(h.state, "events"), h.clock.Now(), history.Header{Why: "start"}, history.Snapshot{})
	require.NoError(t, err)
	require.NoError(t, w.Append(history.Cycle{Line: history.LineCycle, Now: h.clock.Now().UnixMilli()}))
	require.NoError(t, w.Close(true))

	out, err := h.run("replay")

	require.ErrorContains(t, err, "holds no record of the world its cycles read")
	assert.Equal(t, 2, dispatch.ExitCode(err))
	assert.Empty(t, out)
}

func TestReplayOnlyRunsTheCyclesInTheStretchAsked(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	h.advance(10 * time.Minute)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	_, err = h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())
	all, _ := replayCounts(t, h)

	later, matched := replayCounts(t, h, "--from", h.clock.Now().Add(-time.Minute).Format(time.RFC3339))

	assert.Positive(t, later)
	assert.Less(t, later, all)
	assert.Equal(t, later, matched)
}

func TestReplayRejectsATimeItCannotRead(t *testing.T) {
	h := newHarness(t)

	_, err := h.run("replay", "--from", "yesterday")

	require.ErrorContains(t, err, `--from "yesterday" is not a time`)
}

func TestTraceShowsWhyATicketWasHeldAndWhatItReceived(t *testing.T) {
	h := newHarness(t)
	h.tickets(
		blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Review"}),
		ticket("ENG-3", "In Progress", "dispatch"),
	)
	_, err := h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())

	out, err := h.run("trace", "eng-2")

	require.NoError(t, err)
	assert.Regexp(t, `cycle #\d+ \(start\)  `, out)
	assert.Regexp(t, `note-blocker-hold  wants comment`, out)
	assert.Regexp(t, `receipt  comment ENG-2 done`, out)
	assert.NotContains(t, out, "ENG-3")
}

func TestTracePrintsACycleOnlyWhenWhatItDecidedChanged(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	h.advance(10 * time.Minute)
	require.NoError(t, h.stopLoop())

	out, err := h.run("trace", "ENG-1")

	require.NoError(t, err)
	// One cycle wanted the launch and the next found it covered. The resync after ten minutes changed nothing.
	assert.Equal(t, 2, strings.Count(out, "cycle #"), out)
}

func TestTraceExitsOneWhenHistoryHasNoRecordOfTheTicket(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())

	out, err := h.run("trace", "ENG-77")

	require.ErrorContains(t, err, "history has no record of ENG-77")
	assert.Equal(t, 1, dispatch.ExitCode(err))
	assert.Empty(t, out)
}

func TestTraceExitsOneWithNoHistoryAtAll(t *testing.T) {
	h := newHarness(t)

	_, err := h.run("trace", "ENG-1")

	require.ErrorContains(t, err, "history has no record of ENG-1")
	assert.Equal(t, 1, dispatch.ExitCode(err))
}

func TestTraceShowsAnEventARuleDropped(t *testing.T) {
	h := newHarness(t)
	now := h.clock.Now().UnixMilli()
	w, err := history.Start(filepath.Join(h.state, "events"), h.clock.Now(), history.Header{Why: "start"}, history.Snapshot{})
	require.NoError(t, err)
	require.NoError(t, w.Append(history.Cycle{
		Line: history.LineCycle, Now: now, Why: "event",
		Held:  []history.Entry{{Rule: "comment-command", Subject: "ENG-9", Reason: "herdr unread: herdr down", Dropped: true}},
		Reads: map[string][]string{"comment-command/ENG-9": {"worker=unknown"}},
	}))
	require.NoError(t, w.Close(true))

	out, err := h.run("trace", "ENG-9")

	require.NoError(t, err)
	assert.Contains(t, out, "comment-command  dropped: herdr unread: herdr down  read worker=unknown")
}

func TestStateResetStartsASegmentWithAnEmptyLedger(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())
	require.NotEmpty(t, h.ledgerNow())

	out, err := h.run("state", "reset")

	require.NoError(t, err)
	assert.Contains(t, out, "with an empty ledger")
	assert.Empty(t, h.ledgerNow())
	seg, ok, err := history.Latest(filepath.Join(h.state, "events"))
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "reset", seg.Header.Why)
	assert.True(t, seg.Clean)
}

func TestStateResetKeepsWhatTheWorldRemembers(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())
	before, _, err := history.Latest(filepath.Join(h.state, "events"))
	require.NoError(t, err)
	remembered, err := before.Load()
	require.NoError(t, err)
	require.NotEmpty(t, remembered.Memory.Facts)

	_, err = h.run("state", "reset")
	require.NoError(t, err)

	after, _, err := history.Latest(filepath.Join(h.state, "events"))
	require.NoError(t, err)
	assert.Equal(t, remembered.Memory.Facts, after.Snapshot.World.Facts)
}

func TestStateResetExitsOneWhileRunHoldsTheLock(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)

	_, err = h.run("state", "reset")

	require.ErrorContains(t, err, "holds the lock")
	assert.Equal(t, 1, dispatch.ExitCode(err))
}

func TestStateResetQuietRecordsTheMessagesTheFirstCycleWantsAsSent(t *testing.T) {
	h := newHarness(t)
	h.tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Review"}))
	_, err := h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())
	require.Len(t, comments(h), 1)

	out, err := h.run("state", "reset", "--quiet")
	require.NoError(t, err)
	require.NoError(t, h.startLoop(dispatch.Options{}))
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "receipt for the messages the first cycle wants")
	assert.Len(t, comments(h), 1)
	flagged := h.receipts("comment", "ENG-2")
	require.NotEmpty(t, flagged)
	assert.True(t, flagged[0].Reset)
	assert.Equal(t, ledger.Done, flagged[0].Outcome)
}

func TestStateResetWithoutQuietSendsTheMessagesAgain(t *testing.T) {
	h := newHarness(t)
	h.tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Review"}))
	_, err := h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())

	_, err = h.run("state", "reset")
	require.NoError(t, err)
	require.NoError(t, h.startLoop(dispatch.Options{}))
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Len(t, comments(h), 2)
}

func TestStateResetRejectsAnythingButReset(t *testing.T) {
	h := newHarness(t)

	_, err := h.run("state", "wipe")

	require.ErrorContains(t, err, "state takes reset")
}

// A restart over a segment this version wrote must see no difference from before the change.
func TestRestartLoadsASegmentThatHoldsReadLines(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())
	before := len(h.launches())

	h.restart()
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, before, len(h.launches()))
	seg, ok, err := history.Latest(filepath.Join(h.state, "events"))
	require.NoError(t, err)
	require.True(t, ok)
	assert.NotEmpty(t, seg.Header.Rules)
	assert.NotZero(t, seg.Header.Started)
}

func segmentLines(t *testing.T, path, line string) int {
	t.Helper()
	text, err := os.ReadFile(path)
	require.NoError(t, err)
	return strings.Count(string(text), `"line":"`+line+`"`)
}

func TestReplayRunsCyclesAcrossSegmentRotation(t *testing.T) {
	h := newHarness(t)
	h.config("[history]\nrotate = \"1m\"\nkeep = \"24h\"\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	h.advance(6 * time.Minute)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"), blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Review"}))
	h.advance(6 * time.Minute)
	h.advance(6 * time.Minute)
	require.NoError(t, h.stopLoop())
	paths, err := history.Segments(filepath.Join(h.state, "events"))
	require.NoError(t, err)
	require.Greater(t, len(paths), 1)

	cycles, matched := replayCounts(t, h)

	assert.Equal(t, cycles, matched)
	var written int
	for _, p := range paths {
		written += segmentLines(t, p, "cycle")
	}
	assert.Equal(t, written, cycles)
	// A segment begun by rotation replays from its own snapshot.
	last, _ := replayCounts(t, h, "--from", h.clock.Now().Add(-time.Second).Format(time.RFC3339))
	assert.Positive(t, last)
	assert.Less(t, last, cycles)
}

func TestReplayRunsASegmentThatHasNoStopLine(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())
	path := newestSegment(t, h)
	text, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(text)), "\n")
	require.Contains(t, lines[len(lines)-1], `"line":"stop"`)
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines[:len(lines)-1], "\n")+"\n"), 0o600))

	cycles, matched := replayCounts(t, h)

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}

func TestReplaySkipsARuleCallThatTimedOutInTheLiveRun(t *testing.T) {
	for _, test := range []struct {
		name    string
		timeout string
		wantErr bool
	}{
		{"TimedOut", "true", false},
		{"Threw", "false", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Review"}))
			_, err := h.run("tick")
			require.NoError(t, err)
			require.NoError(t, h.stopLoop())
			// Replay doesn't spend a rule's time budget, so whatever the rule decided that cycle isn't compared.
			throw := `{"line":"cycle","throws":[{"rule":"note-blocker-hold","subject":"ENG-2","error":"ran out of time","timeout":` + test.timeout + `}],`
			editSegment(t, newestSegment(t, h), `{"line":"cycle",`, throw)

			out, err := h.run("replay")

			if test.wantErr {
				require.Error(t, err)
				assert.Equal(t, 1, dispatch.ExitCode(err))
				assert.Contains(t, out, "threw for ENG-2")
				return
			}
			require.NoError(t, err)
			assert.Regexp(t, replayed, out)
		})
	}
}

func TestReplayKeepsTheTicketsAWorkerSlotWasReservedFor(t *testing.T) {
	h := newHarness(t)
	h.config("[dispatch]\nmax_in_progress = 1\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())
	cycles, matched := replayCounts(t, h)
	require.Equal(t, cycles, matched)
	// A launch under way holds a slot, so the cycle would have waited both tickets had it been recorded so.
	editSegment(t, newestSegment(t, h), `{"line":"read",`, `{"line":"read","reserved":["ENG-1","ENG-2"],`)

	out, err := h.run("replay")

	require.Error(t, err)
	assert.Equal(t, 1, dispatch.ExitCode(err))
	assert.Contains(t, out, "2 of 1 in progress")
}

func TestReplayWritesAReadAndACycleOnlyWhenTheyDifferFromTheLast(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	path := newestSegment(t, h)
	reads, cycles := segmentLines(t, path, "read"), segmentLines(t, path, "cycle")
	require.Positive(t, reads)

	// The resync reads the same world and the rules decide the same thing.
	h.advance(10 * time.Minute)
	h.advance(10 * time.Minute)
	require.NoError(t, h.stopLoop())

	assert.Equal(t, reads, segmentLines(t, path, "read"))
	assert.Equal(t, cycles, segmentLines(t, path, "cycle"))
}

func TestReplayStopsAtTheTimeGivenWithTo(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	before := h.clock.Now()
	h.advance(10 * time.Minute)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	_, err = h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())
	all, _ := replayCounts(t, h)

	upTo, matched := replayCounts(t, h, "--to", before.Add(time.Minute).Format(time.RFC3339))

	assert.Positive(t, upTo)
	assert.Less(t, upTo, all)
	assert.Equal(t, upTo, matched)
}

func TestTraceShowsAThrowAndARuleThatRanOutOfTime(t *testing.T) {
	h := newHarness(t)
	now := h.clock.Now().UnixMilli()
	w, err := history.Start(filepath.Join(h.state, "events"), h.clock.Now(), history.Header{Why: "start"}, history.Snapshot{})
	require.NoError(t, err)
	require.NoError(t, w.Append(history.Cycle{
		Line: history.LineCycle, Now: now,
		Throws: []history.Throw{{Rule: "launch-worker", Subject: "ENG-4", Error: "boom"}},
	}))
	require.NoError(t, w.Append(history.Cycle{
		Line: history.LineCycle, Now: now + 1,
		Throws: []history.Throw{{Rule: "launch-worker", Subject: "ENG-4", Error: "too slow", Timeout: true}},
	}))
	require.NoError(t, w.Close(true))

	out, err := h.run("trace", "ENG-4")

	require.NoError(t, err)
	assert.Contains(t, out, "launch-worker  threw: boom")
	assert.Contains(t, out, "launch-worker  ran out of time: too slow")
}

func TestTraceReadsASegmentWrittenBeforeReadLinesAndSaysOfAReceiptInIt(t *testing.T) {
	h := newHarness(t)
	now := h.clock.Now()
	w, err := history.Start(filepath.Join(h.state, "events"), now, history.Header{Why: "start"}, history.Snapshot{})
	require.NoError(t, err)
	require.NoError(t, w.Append(history.Cycle{
		Line: history.LineCycle, Now: now.UnixMilli(),
		Wants: []history.Entry{{Rule: "launch-worker", Kind: "launch", Subject: "ENG-6"}},
	}))
	require.NoError(t, w.Append(history.Receipt{Line: history.LineReceipt, Receipt: ledger.Receipt{
		Kind: "launch", Subject: "ENG-6", Outcome: ledger.Done, Started: now.UnixMilli(), Ended: now.UnixMilli(),
	}}))
	require.NoError(t, w.Close(true))

	out, err := h.run("trace", "ENG-6")

	require.NoError(t, err)
	assert.Contains(t, out, "launch-worker  wants launch")
	assert.Contains(t, out, "receipt  launch ENG-6 done")
}

func TestReplaySkipsASegmentFromBeforeReadLinesAndReplaysTheNewerOnes(t *testing.T) {
	h := newHarness(t)
	old, err := history.Start(filepath.Join(h.state, "events"), h.clock.Now().Add(-time.Hour), history.Header{Why: "start"}, history.Snapshot{})
	require.NoError(t, err)
	require.NoError(t, old.Append(history.Cycle{Line: history.LineCycle, Now: h.clock.Now().Add(-time.Hour).UnixMilli()}))
	require.NoError(t, old.Close(true))
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err = h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())

	out, stderr, err := h.runContext(t.Context(), "replay")

	require.NoError(t, err)
	found := replayed.FindStringSubmatch(out)
	require.NotNil(t, found, out)
	assert.Equal(t, found[1], found[2])
	assert.Contains(t, stderr, "dispatch: skipped")
	assert.Contains(t, stderr, "holds no record of the world its cycles read")
}

func TestReplayIgnoresACycleACrashLeftUndecided(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	_, err = h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())
	path := newestSegment(t, h)
	text, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(text)), "\n")
	last := -1
	for i, line := range lines {
		if strings.HasPrefix(line, `{"line":"read"`) {
			last = i
		}
	}
	require.Positive(t, last)
	// The crash came after the read and the memory change were written and before the cycle line.
	keep := lines[:last+1]
	for _, line := range lines[last+1:] {
		if !strings.HasPrefix(line, `{"line":"world"`) {
			break
		}
		keep = append(keep, line)
	}
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(keep, "\n")+"\n"), 0o600))

	cycles, matched := replayCounts(t, h)

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}

func TestReplayWithNoHistoryExitsOne(t *testing.T) {
	h := newHarness(t)

	_, err := h.run("replay")

	require.ErrorContains(t, err, "there is no history to read")
	assert.Equal(t, 1, dispatch.ExitCode(err))
}

func TestTraceDoesNotRepeatADecisionWhenASegmentRotates(t *testing.T) {
	h := newHarness(t)
	h.config("[history]\nrotate = \"1m\"\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	for range 3 {
		h.advance(6 * time.Minute)
	}
	require.NoError(t, h.stopLoop())
	paths, err := history.Segments(filepath.Join(h.state, "events"))
	require.NoError(t, err)
	require.Greater(t, len(paths), 2)

	out, err := h.run("trace", "ENG-1")

	require.NoError(t, err)
	// One cycle wanted the launch and the next found it covered. Rotation changed nothing after that.
	assert.Equal(t, 2, strings.Count(out, "cycle #"), out)
}

func TestTracePrintsTheReceiptsOfSegmentsAlreadyDeleted(t *testing.T) {
	h := newHarness(t)
	w, err := history.Start(filepath.Join(h.state, "events"), h.clock.Now(), history.Header{Why: "rotate"}, history.Snapshot{
		Ledger: history.LedgerState{Receipts: []ledger.Receipt{{Kind: "launch", Subject: "ENG-9", Once: ledger.Once{"launch", nil, 3.0}, Outcome: ledger.Done, Detail: "ok: workspace w3", Started: 1, Ended: 2}}},
	})
	require.NoError(t, err)
	require.NoError(t, w.Close(true))

	out, err := h.run("trace", "ENG-9")

	require.NoError(t, err)
	assert.Contains(t, out, "receipt  launch ENG-9 done once [launch - 3]: ok: workspace w3")
}

func TestStateResetQuietRefusesWhileAPoolIsUnread(t *testing.T) {
	h := newHarness(t)
	h.addRepo("tiger", filepath.Join(h.dir, "moved"))
	h.tickets(ticket("ENG-1", "In Progress", "tiger"))

	_, err := h.run("state", "reset", "--quiet")

	require.ErrorContains(t, err, "cannot record the messages the first cycle wants while a source is down")
	paths, err := history.Segments(filepath.Join(h.state, "events"))
	require.NoError(t, err)
	assert.Empty(t, paths)
}

func TestReplayStillComparesMemoryInTheLastCycleOfASegmentThatHasNoStopLine(t *testing.T) {
	h := newHarness(t)
	h.tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Review"}))
	_, err := h.run("tick")
	require.NoError(t, err)
	// The title changes what memory holds and leaves the rules deciding what they did, so no cycle line follows.
	renamed := blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Review"})
	renamed.Title = "ENG-2 renamed"
	h.tickets(renamed)
	_, err = h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())
	path := newestSegment(t, h)
	text, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(text)), "\n")
	last := -1
	for i, line := range lines {
		if strings.HasPrefix(line, `{"line":"read"`) {
			last = i
		}
	}
	require.Positive(t, last)
	require.Contains(t, lines[last], "ENG-2 renamed")
	for _, line := range lines[last+1:] {
		require.NotContains(t, line, `{"line":"cycle"`)
	}
	// The crash left no stop line, and the blocker was Done as far as the last read says.
	lines[last] = strings.ReplaceAll(lines[last], `"Status":"In Review"`, `"Status":"Done"`)
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines[:len(lines)-1], "\n")+"\n"), 0o600))

	out, err := h.run("replay")

	require.Error(t, err)
	assert.Equal(t, 1, dispatch.ExitCode(err))
	assert.Contains(t, out, "memory: recorded")
}

func TestReplayOfARangeWithNoCyclesIsNotAnUnreplayableSegment(t *testing.T) {
	h := newHarness(t)
	old, err := history.Start(filepath.Join(h.state, "events"), h.clock.Now().Add(-time.Hour), history.Header{Why: "start"}, history.Snapshot{})
	require.NoError(t, err)
	require.NoError(t, old.Append(history.Cycle{Line: history.LineCycle, Now: h.clock.Now().Add(-time.Hour).UnixMilli()}))
	require.NoError(t, old.Close(true))
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err = h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())

	out, _, err := h.runContext(t.Context(), "replay", "--from", h.clock.Now().Add(time.Hour).Format(time.RFC3339))

	require.NoError(t, err)
	assert.Equal(t, "0 cycles, 0 match\n", out)
}

func TestReplayExitsTwoWhenTheOnlySegmentsWithCyclesAreFromBeforeReadLines(t *testing.T) {
	h := newHarness(t)
	old, err := history.Start(filepath.Join(h.state, "events"), h.clock.Now().Add(-2*time.Hour), history.Header{Why: "start"}, history.Snapshot{})
	require.NoError(t, err)
	require.NoError(t, old.Append(history.Cycle{Line: history.LineCycle, Now: h.clock.Now().Add(-2 * time.Hour).UnixMilli()}))
	require.NoError(t, old.Close(true))
	// A reset starts a segment that has no cycle, which says nothing about the world.
	empty, err := history.Start(filepath.Join(h.state, "events"), h.clock.Now().Add(-time.Hour), history.Header{Why: "reset"}, history.Snapshot{})
	require.NoError(t, err)
	require.NoError(t, empty.Close(true))

	_, err = h.run("replay")

	require.ErrorContains(t, err, "holds no record of the world its cycles read")
	assert.Equal(t, 2, dispatch.ExitCode(err))
}

func TestTracePrintsACycleOnlyWhenTheDecisionChangedNotWhatTheRuleRead(t *testing.T) {
	h := newHarness(t)
	now := h.clock.Now().UnixMilli()
	w, err := history.Start(filepath.Join(h.state, "events"), h.clock.Now(), history.Header{Why: "start"}, history.Snapshot{})
	require.NoError(t, err)
	held := []history.Entry{{Rule: "launch-worker", Subject: "ENG-9", Reason: "herdr unread: herdr down"}}
	require.NoError(t, w.Append(history.Cycle{Line: history.LineCycle, Now: now, Held: held, Reads: map[string][]string{"launch-worker/ENG-9": {"worker=unknown"}}}))
	require.NoError(t, w.Append(history.Cycle{Line: history.LineCycle, Now: now + 1000, Held: held, Reads: map[string][]string{"launch-worker/ENG-9": {"worker=unknown", "failures=1"}}}))
	require.NoError(t, w.Close(true))

	out, err := h.run("trace", "ENG-9")

	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(out, "cycle #"), out)
}

func TestTraceShowsAResetReceiptAsNeverSent(t *testing.T) {
	h := newHarness(t)
	w, err := history.Start(filepath.Join(h.state, "events"), h.clock.Now(), history.Header{Why: "reset"}, history.Snapshot{
		Ledger: history.LedgerState{Receipts: []ledger.Receipt{{Kind: "nudge", Subject: "ENG-9", Once: ledger.Once{"stall", 5.0}, Outcome: ledger.Done, Detail: "state reset", Started: 1, Ended: 2, Reset: true}}},
	})
	require.NoError(t, err)
	require.NoError(t, w.Close(true))

	out, err := h.run("trace", "ENG-9")

	require.NoError(t, err)
	assert.Contains(t, out, "receipt  nudge ENG-9 done (reset, never sent) once [stall 5]")
}

// failingWriter rejects every write, as a closed pipe does.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestTraceFailsWhenItCannotWriteItsReport(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())
	opts := h.options(dispatch.Options{})
	opts.Stdout, opts.Stderr = failingWriter{}, failingWriter{}

	err = dispatch.Run(t.Context(), []string{"trace", "ENG-1"}, opts)

	require.ErrorContains(t, err, "broken pipe")
}

func TestReplayWithOtherRulesNoticesAnActionWithTheSameKeyButOtherText(t *testing.T) {
	h := newHarness(t)
	greeting := func(text string) string {
		return oneRule(t, `whenever({ name: "greet", each: "linear.ticket", if: () => true, then: (t, ctx) => ctx.linear.queueComment(t, "`+text+`", { once: ["greet"] }) });`)
	}
	h.rulesDir = greeting("hello")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())
	require.Equal(t, []string{"hello"}, comments(h))

	out, err := h.run("replay", "--rules", greeting("goodbye"))

	require.NoError(t, err)
	assert.Contains(t, out, "difference at")
	assert.Contains(t, out, `recorded: wants comment ENG-1 once [greet] args {"text":"hello"}`)
	assert.Contains(t, out, `replayed: wants comment ENG-1 once [greet] args {"text":"goodbye"}`)
}

func TestStateResetQuietRefusesWhileARuleFails(t *testing.T) {
	h := newHarness(t)
	h.rulesDir = oneRule(t, `whenever({ name: "broken", each: "linear.ticket", if: () => { throw new Error("boom"); }, then: () => {} });`)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	_, err := h.run("state", "reset", "--quiet")

	require.ErrorContains(t, err, "while rule broken fails for ENG-1")
	paths, err := history.Segments(filepath.Join(h.state, "events"))
	require.NoError(t, err)
	assert.Empty(t, paths)
}

func TestTraceKnowsATicketNoRuleHasWantedAnythingFor(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "working")
	_, err := h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())

	out, err := h.run("trace", "ENG-1")

	require.NoError(t, err)
	assert.Contains(t, out, "wants nothing for ENG-1")
}

func TestHistoryKeepsAChildsMemoryWhileTheChildrenOfItsParentAreUnread(t *testing.T) {
	h := newHarness(t)
	// The cap keeps ENG-2 in Todo, so only the children read knows it exists.
	h.config("[dispatch]\nmax_in_progress = 1\n")
	h.tickets(parent("ENG-1", "In Progress"), ticket("ENG-9", "In Progress", "dispatch"))
	h.worker("ENG-9", "working")
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"))
	_, err := h.run("tick")
	require.NoError(t, err)

	h.linear.failChildren()
	_, err = h.run("tick")
	require.NoError(t, err)
	require.NoError(t, h.stopLoop())

	text, err := os.ReadFile(newestSegment(t, h))
	require.NoError(t, err)
	worlds := 0
	for _, line := range strings.Split(string(text), "\n") {
		if !strings.HasPrefix(line, `{"line":"world"`) {
			continue
		}
		worlds++
		var changed history.World
		require.NoError(t, json.Unmarshal([]byte(line), &changed))
		for _, key := range changed.Gone {
			assert.NotContains(t, key, "ENG-2")
		}
	}
	require.Positive(t, worlds)
}

const childCommentRule = `
whenever({ name: "greet-child", each: "linear.ticket", if: (t) => t.id === "ENG-2",
  then: (t, ctx) => ctx.linear.queueComment(t, "hello", { once: ["greet", t.statusSince] }) });`

func TestAChildKeepsItsOnceKeyAcrossAFailedReadOfItsParentsChildren(t *testing.T) {
	for _, test := range []struct {
		name    string
		restart bool
	}{
		{"SameLoop", false},
		{"AfterRestart", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.rulesDir = oneRule(t, childCommentRule)
			h.config("[dispatch]\nmax_in_progress = 1\n")
			h.tickets(parent("ENG-1", "In Progress"), ticket("ENG-9", "In Progress", "dispatch"))
			h.worker("ENG-9", "working")
			h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"))
			h.tick()
			first := h.receipts("comment", "ENG-2")
			require.Len(t, first, 1)

			// The key's statusSince is the time dispatch first saw ENG-2, so it must not
			// move to the time the children were read again.
			h.linear.failChildren()
			h.advance(time.Hour)
			h.tick()
			if test.restart {
				h.restart()
			}
			h.linear.FailOn(lineartest.OnChildren, false)
			h.advance(time.Hour)
			h.tick()

			again := h.receipts("comment", "ENG-2")
			require.Len(t, again, 1)
			assert.Equal(t, first[0].Once, again[0].Once)
			comments := 0
			for _, change := range h.linear.mutations() {
				if change.Kind == lineartest.Comment && change.TicketID == "uuid-ENG-2" {
					comments++
				}
			}
			assert.Equal(t, 1, comments)
		})
	}
}

func TestAChildOfAParentWhoseChildrenAreUnreadIsRememberedAcrossARestart(t *testing.T) {
	h := newHarness(t)
	h.config("[dispatch]\nmax_in_progress = 1\n")
	h.tickets(parent("ENG-1", "In Progress"), ticket("ENG-9", "In Progress", "dispatch"))
	h.worker("ENG-9", "working")
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"))
	h.tick()
	h.linear.failChildren()
	h.tick()

	h.restart()
	h.tick()
	require.NoError(t, h.stopLoop())

	text, err := os.ReadFile(newestSegment(t, h))
	require.NoError(t, err)
	for _, line := range strings.Split(string(text), "\n") {
		if !strings.HasPrefix(line, `{"line":"world"`) {
			continue
		}
		var changed history.World
		require.NoError(t, json.Unmarshal([]byte(line), &changed))
		for _, key := range changed.Gone {
			assert.NotContains(t, key, "ENG-2")
		}
	}
}

func TestAChildThatLeftTheParentIsForgottenOnceTheChildrenAreRead(t *testing.T) {
	h := newHarness(t)
	h.config("[dispatch]\nmax_in_progress = 1\n")
	h.tickets(parent("ENG-1", "In Progress"), ticket("ENG-9", "In Progress", "dispatch"))
	h.worker("ENG-9", "working")
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"))
	h.tick()

	h.linear.setChildren(child("ENG-3", "ENG-1", "Todo"))
	h.tick()
	require.NoError(t, h.stopLoop())

	text, err := os.ReadFile(newestSegment(t, h))
	require.NoError(t, err)
	assert.Regexp(t, `"gone":\[[^\]]*ENG-2`, string(text))
}

func TestHistoryKeepsAChildsMemoryThroughALinearOutageBeforeTheChildrenFail(t *testing.T) {
	h := newHarness(t)
	h.config("[dispatch]\nmax_in_progress = 1\n")
	h.tickets(parent("ENG-1", "In Progress"), ticket("ENG-9", "In Progress", "dispatch"))
	h.worker("ENG-9", "working")
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"))
	h.tick()
	// Linear is unread across the restart, so no parent exists to list its children.
	h.linear.FailOn(lineartest.OnManaged, true)
	h.restart()
	h.linear.FailOn(lineartest.OnManaged, false)
	h.linear.failChildren()
	h.advance(5 * time.Minute)
	h.tick()
	require.NoError(t, h.stopLoop())

	text, err := os.ReadFile(newestSegment(t, h))
	require.NoError(t, err)
	for _, line := range strings.Split(string(text), "\n") {
		if !strings.HasPrefix(line, `{"line":"world"`) {
			continue
		}
		var changed history.World
		require.NoError(t, json.Unmarshal([]byte(line), &changed))
		for _, key := range changed.Gone {
			assert.False(t, strings.Contains(key, "linear.ticket/ENG-2/") || strings.Contains(key, "herdr.worker/ENG-2/"), key)
		}
	}
}
