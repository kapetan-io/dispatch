package dispatch_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/httpservicetest"
)

var replayed = regexp.MustCompile(`^(\d+) cycles?, (\d+) match\n$`)

func replayTotals(t *testing.T, out string) (cycles, matched int) {
	t.Helper()
	found := replayed.FindStringSubmatch(out)
	require.NotNil(t, found)
	cycles, _ = strconv.Atoi(found[1])
	matched, _ = strconv.Atoi(found[2])
	return cycles, matched
}

// replayCounts runs replay on a harness and returns the cycles it ran again and how many matched.
// commands_test.go still reads its history through it.
func replayCounts(t *testing.T, d *Dispatch, args ...string) (cycles, matched int) {
	t.Helper()
	out, err := d.output(append([]string{"replay"}, args...)...)
	require.NoError(t, err)
	return replayTotals(t, out)
}

// replayOf runs replay and returns the cycles it ran again and how many matched.
func replayOf(t *testing.T, d *Dispatch, args ...string) (cycles, matched int) {
	t.Helper()
	res := d.Run(append([]string{"replay"}, args...)...)
	require.NoError(t, res.Err)
	return replayTotals(t, res.Stdout)
}

func shippedRules(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs("rules")
	require.NoError(t, err)
	return path
}

func TestReplayMatchesTheLiveRun(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()

	cycles, matched := replayOf(t, d)

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}

// Each case leaves the same lines in the world's memory as its first tick and changes what the
// rules decide, so only a replay over the recorded read can match.
func TestReplayMatchesWhatMemoryAloneCannotTellApart(t *testing.T) {
	for _, test := range []struct {
		name       string
		unreadPool bool
		setup      func(d *Dispatch)
	}{
		{"BlockersCutShort", false, func(d *Dispatch) {
			d.Linear.Tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), true, [2]string{"ENG-1", "Done"}))
		}},
		{"AnotherOpenBlocker", false, func(d *Dispatch) {
			d.Linear.Tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Progress"}))
			d.Tick()
			d.Linear.Tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-5", "In Progress"}))
		}},
		{"AnUnreadPool", true, func(d *Dispatch) {
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "tiger"))
		}},
		{"ChildrenUnread", false, func(d *Dispatch) {
			d.Linear.Tickets(parent("ENG-1", "In Progress"))
			d.Linear.Children(child("ENG-2", "ENG-1", "Todo"))
			d.Linear.FailChildren(true)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var opts dispatchOptions
			if test.unreadPool {
				opts.Config = fmt.Sprintf("[repos.tiger]\npath = %q\n", filepath.Join(t.TempDir(), "moved"))
			}
			d := newDispatch(t, opts)
			test.setup(d)
			d.Tick()
			d.Stop()

			cycles, matched := replayOf(t, d)

			assert.Positive(t, cycles)
			assert.Equal(t, cycles, matched)
		})
	}
}

func TestReplayPrintsTheFirstDifferenceAndExitsOne(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Review"}))
	d.Tick()
	d.Stop()
	// Unreachable from the CLI: a replay of the recorded rules differs only when history was damaged.
	d.EditHistory(`"rule":"note-blocker-hold"`, `"rule":"another-rule"`)

	res := d.Run("replay")

	require.Error(t, res.Err)
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "difference at")
	assert.Contains(t, res.Stdout, "recorded: wants comment ENG-2 once")
	assert.Contains(t, res.Stdout, "(another-rule)")
	assert.Contains(t, res.Stdout, "(note-blocker-hold)")
}

func TestReplayNamesTheMemoryItFoundDifferent(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Review"}))
	d.Tick()
	d.Stop()
	// Unreachable from the CLI: the blocker was Done when the cycle ran, as far as replay can tell.
	d.EditHistory(`"Status":"In Review"`, `"Status":"Done"`)

	res := d.Run("replay")

	require.Error(t, res.Err)
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "memory: recorded")
}

func TestReplayWithOtherRulesStopsAtTheFirstActionThatDiffers(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()
	quiet := oneRule(t, `whenever({ name: "launch-worker", each: "linear.ticket", if: () => false, then: () => [] });`)

	res := d.Run("replay", "--rules", quiet)

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "difference at")
	assert.Regexp(t, `recorded: wants launch ENG-1`, res.Stdout)
	assert.Contains(t, res.Stdout, "replayed: nothing like it")
}

func TestReplayWithTheSameRulesMatches(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()

	cycles, matched := replayOf(t, d, "--rules", shippedRules(t))

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}

func TestReplayUsesTheRulesTheLiveRunHadWhenTheFilesChanged(t *testing.T) {
	files := map[string]string{}
	paths, err := filepath.Glob("rules/*.ts")
	require.NoError(t, err)
	for _, path := range paths {
		if strings.HasSuffix(path, ".test.ts") {
			continue
		}
		text, err := os.ReadFile(path)
		require.NoError(t, err)
		files[filepath.Base(path)] = string(text)
	}
	d := newDispatch(t, dispatchOptions{Rules: files})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()
	// A rules file that no longer loads must not matter to a replay of the run that used the old one.
	d.EditRules("dispatch.ts", "not typescript {")

	cycles, matched := replayOf(t, d)

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}

func TestReplayOfHistoryWithoutAReadExitsTwo(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	// Unreachable from the CLI: every cycle a tick runs records the read it ran over.
	d.WroteHistory(pastRun{Cycles: []pastCycle{{}}})

	res := d.Run("replay")

	require.ErrorContains(t, res.Err, "holds no record of the world its cycles read")
	assert.Equal(t, 2, res.Code)
	assert.Empty(t, res.Stdout)
}

func TestReplayOnlyRunsTheCyclesInTheStretchAsked(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Advance(10 * time.Minute)
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Tick()
	d.Stop()
	all, _ := replayOf(t, d)

	later, matched := replayOf(t, d, "--from", d.Clock.Now().Add(-time.Minute).Format(time.RFC3339))

	assert.Positive(t, later)
	assert.Less(t, later, all)
	assert.Equal(t, later, matched)
}

func TestReplayRejectsATimeItCannotRead(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})

	res := d.Run("replay", "--from", "yesterday")

	require.ErrorContains(t, res.Err, `--from "yesterday" is not a time`)
}

func TestTraceShowsWhyATicketWasHeldAndWhatItReceived(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(
		blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Review"}),
		ticket("ENG-3", "In Progress", "dispatch"),
	)
	d.Tick()
	d.Stop()

	res := d.Run("trace", "eng-2")

	require.NoError(t, res.Err)
	assert.Regexp(t, `cycle #\d+ \(start\)  `, res.Stdout)
	assert.Regexp(t, `note-blocker-hold  wants comment`, res.Stdout)
	assert.Regexp(t, `receipt  comment ENG-2 done`, res.Stdout)
	assert.NotContains(t, res.Stdout, "ENG-3")
}

func TestTracePrintsACycleOnlyWhenWhatItDecidedChanged(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Advance(10 * time.Minute)
	d.Stop()

	res := d.Run("trace", "ENG-1")

	require.NoError(t, res.Err)
	// One cycle wanted the launch and the next found it covered. The resync after ten minutes changed nothing.
	assert.Equal(t, 2, strings.Count(res.Stdout, "cycle #"), res.Stdout)
}

func TestTraceExitsOneWhenHistoryHasNoRecordOfTheTicket(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()

	res := d.Run("trace", "ENG-77")

	require.ErrorContains(t, res.Err, "history has no record of ENG-77")
	assert.Equal(t, 1, res.Code)
	assert.Empty(t, res.Stdout)
}

func TestTraceExitsOneWithNoHistoryAtAll(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})

	res := d.Run("trace", "ENG-1")

	require.ErrorContains(t, res.Err, "history has no record of ENG-1")
	assert.Equal(t, 1, res.Code)
}

func TestTraceShowsAnEventARuleDropped(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	// Unreachable from the CLI: a dropped decision needs an event lost while herdr was unread.
	d.WroteHistory(pastRun{Cycles: []pastCycle{{
		Why:   "event",
		Held:  []pastEntry{{Rule: "comment-command", Subject: "ENG-9", Reason: "herdr unread: herdr down", Dropped: true}},
		Reads: map[string][]string{"comment-command/ENG-9": {"worker=unknown"}},
	}}})

	res := d.Run("trace", "ENG-9")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "comment-command  dropped: herdr unread: herdr down  read worker=unknown")
}

func TestStateResetStartsASegmentWithAnEmptyLedger(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()
	require.Len(t, d.Workers.Launched(), 1)

	res := d.Run("state", "reset")
	d.Tick()

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "with an empty ledger")
	assert.Len(t, d.Workers.Launched(), 2)
}

// The ticket's once key is stamped with the time dispatch first saw it, so a key that survives a
// reset shows the world's memory survived.
func TestStateResetKeepsWhatTheWorldRemembers(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(childCommentRule)})
	d.Linear.Tickets(ticket("ENG-2", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()
	d.Advance(time.Hour)

	require.NoError(t, d.Run("state", "reset").Err)
	d.Tick()

	assert.Len(t, d.Receipts("ENG-2"), 1)
	assert.Equal(t, []string{"ENG-2: hello", "ENG-2: hello"}, d.Linear.Comments())
}

func TestStateResetExitsOneWhileRunHoldsTheLock(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()

	res := d.Run("state", "reset")

	require.ErrorContains(t, res.Err, "holds the lock")
	assert.Equal(t, 1, res.Code)
}

func TestStateResetQuietRecordsTheMessagesTheFirstCycleWantsAsSent(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Review"}))
	d.Tick()
	d.Stop()
	require.Len(t, d.Linear.Comments(), 1)
	d.Advance(time.Minute)

	res := d.Run("state", "reset", "--quiet")
	require.NoError(t, res.Err)
	d.Restart()
	d.Tick()

	assert.Contains(t, res.Stdout, "receipt for the messages the first cycle wants")
	assert.Len(t, d.Linear.Comments(), 1)
	flagged := d.Receipts("ENG-2")
	require.NotEmpty(t, flagged)
	assert.True(t, flagged[0].Reset)
	assert.Equal(t, "done", flagged[0].State)
}

func TestStateResetWithoutQuietSendsTheMessagesAgain(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Review"}))
	d.Tick()
	d.Stop()

	require.NoError(t, d.Run("state", "reset").Err)
	d.Restart()
	d.Tick()

	assert.Len(t, d.Linear.Comments(), 2)
}

func TestStateResetRejectsAnythingButReset(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})

	res := d.Run("state", "wipe")

	require.ErrorContains(t, res.Err, "state takes reset")
}

// A restart over a segment this version wrote must see no difference from before the change.
func TestRestartLoadsASegmentThatHoldsReadLines(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()
	before := len(d.Workers.Launched())

	d.Restart()
	d.Tick()

	assert.Equal(t, before, len(d.Workers.Launched()))
}

func TestReplayRunsCyclesAcrossSegmentRotation(t *testing.T) {
	const rotating = "[history]\nrotate = \"1m\"\nkeep = \"24h\"\n"
	run := func(t *testing.T, config string) *Dispatch {
		d := newDispatch(t, dispatchOptions{Config: config})
		d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
		d.Tick()
		d.Advance(6 * time.Minute)
		d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"), blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Review"}))
		d.Advance(6 * time.Minute)
		d.Advance(6 * time.Minute)
		d.Stop()
		return d
	}
	d := run(t, rotating)

	cycles, matched := replayOf(t, d)

	assert.Equal(t, cycles, matched)
	// A segment begun by rotation opens with a cycle of its own, so the rotated run replays more than the unrotated one does.
	steady, _ := replayOf(t, run(t, ""))
	assert.Greater(t, cycles, steady)
	// A segment begun by rotation replays from its own snapshot.
	last, _ := replayOf(t, d, "--from", d.Clock.Now().Add(-time.Second).Format(time.RFC3339))
	assert.Positive(t, last)
	assert.Less(t, last, cycles)
}

func TestReplayRunsASegmentThatHasNoStopLine(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()
	d.CrashedBeforeStopLine()

	cycles, matched := replayOf(t, d)

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
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Review"}))
			d.Tick()
			d.Stop()
			// Unreachable from the CLI: a rule that runs out of time needs the real rule time budget.
			throw := `{"line":"cycle","throws":[{"rule":"note-blocker-hold","subject":"ENG-2","error":"ran out of time","timeout":` + test.timeout + `}],`
			d.EditHistory(`{"line":"cycle",`, throw)

			res := d.Run("replay")

			if test.wantErr {
				require.Error(t, res.Err)
				assert.Equal(t, 1, res.Code)
				assert.Contains(t, res.Stdout, "threw for ENG-2")
				return
			}
			require.NoError(t, res.Err)
			assert.Regexp(t, replayed, res.Stdout)
		})
	}
}

func TestReplayKeepsTheTicketsAWorkerSlotWasReservedFor(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 1\n"})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()
	cycles, matched := replayOf(t, d)
	require.Equal(t, cycles, matched)
	// Unreachable from the CLI: a launch under way holds a slot, so the cycle would have waited both tickets had it been recorded so.
	d.EditHistory(`{"line":"read",`, `{"line":"read","reserved":["ENG-1","ENG-2"],`)

	res := d.Run("replay")

	require.Error(t, res.Err)
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "2 of 1 in progress")
}

// A resync that reads the same world and decides the same thing writes no cycle, so the cycles
// replay can run stay the same.
func TestReplayWritesACycleOnlyWhenItDiffersFromTheLast(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	before, _ := replayOf(t, d)

	d.Advance(10 * time.Minute)
	d.Advance(10 * time.Minute)
	d.Stop()

	cycles, matched := replayOf(t, d)
	assert.Positive(t, before)
	assert.Equal(t, before, cycles)
	assert.Equal(t, cycles, matched)
}

func TestReplayStopsAtTheTimeGivenWithTo(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	before := d.Clock.Now()
	d.Advance(10 * time.Minute)
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Tick()
	d.Stop()
	all, _ := replayOf(t, d)

	upTo, matched := replayOf(t, d, "--to", before.Add(time.Minute).Format(time.RFC3339))

	assert.Positive(t, upTo)
	assert.Less(t, upTo, all)
	assert.Equal(t, upTo, matched)
}

func TestTraceShowsAThrowAndARuleThatRanOutOfTime(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	// Unreachable from the CLI: a rule that runs out of time needs the real rule time budget.
	d.WroteHistory(pastRun{Cycles: []pastCycle{
		{Throws: []pastThrow{{Rule: "launch-worker", Subject: "ENG-4", Error: "boom"}}},
		{Throws: []pastThrow{{Rule: "launch-worker", Subject: "ENG-4", Error: "too slow", Timeout: true}}},
	}})

	res := d.Run("trace", "ENG-4")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "launch-worker  threw: boom")
	assert.Contains(t, res.Stdout, "launch-worker  ran out of time: too slow")
}

func TestTraceReadsASegmentWrittenBeforeReadLinesAndSaysOfAReceiptInIt(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	// Unreachable from the CLI: segments from before dispatch recorded reads exist only on disk.
	d.WroteHistory(pastRun{
		Cycles:   []pastCycle{{Wants: []pastEntry{{Rule: "launch-worker", Kind: "launch", Subject: "ENG-6"}}}},
		Receipts: []pastReceipt{{Kind: "launch", Subject: "ENG-6", Done: true}},
	})

	res := d.Run("trace", "ENG-6")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "launch-worker  wants launch")
	assert.Contains(t, res.Stdout, "receipt  launch ENG-6 done")
}

func TestReplaySkipsASegmentFromBeforeReadLinesAndReplaysTheNewerOnes(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	// Unreachable from the CLI: segments from before dispatch recorded reads exist only on disk.
	d.WroteHistory(pastRun{Ago: time.Hour, Cycles: []pastCycle{{}}})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()

	res := d.Run("replay")

	require.NoError(t, res.Err)
	cycles, matched := replayTotals(t, res.Stdout)
	assert.Equal(t, cycles, matched)
	assert.Contains(t, res.Stderr, "dispatch: skipped")
	assert.Contains(t, res.Stderr, "holds no record of the world its cycles read")
}

func TestReplayIgnoresACycleACrashLeftUndecided(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Tick()
	d.Stop()
	d.CrashedBeforeLastCycleLine()

	cycles, matched := replayOf(t, d)

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}

func TestReplayWithNoHistoryExitsOne(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})

	res := d.Run("replay")

	require.ErrorContains(t, res.Err, "there is no history to read")
	assert.Equal(t, 1, res.Code)
}

func TestTraceDoesNotRepeatADecisionWhenASegmentRotates(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: "[history]\nrotate = \"1m\"\n"})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	for range 3 {
		d.Advance(6 * time.Minute)
	}
	d.Stop()

	// Each rotation opens a segment with its own cycle, so an unrotated run replays 2 cycles.
	rotated, _ := replayOf(t, d)
	require.Greater(t, rotated, 2)
	res := d.Run("trace", "ENG-1")

	require.NoError(t, res.Err)
	// One cycle wanted the launch and the next found it covered. Rotation changed nothing after that.
	assert.Equal(t, 2, strings.Count(res.Stdout, "cycle #"), res.Stdout)
}

func TestTracePrintsTheReceiptsOfSegmentsAlreadyDeleted(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	// Unreachable from the CLI: pruning keeps old receipts only in the snapshot of a newer segment.
	d.WroteHistory(pastRun{Why: "rotate", Carried: []pastReceipt{{
		Kind: "launch", Subject: "ENG-9", Once: []any{"launch", nil, 3.0}, Done: true, Detail: "ok: workspace w3",
	}}})

	res := d.Run("trace", "ENG-9")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "receipt  launch ENG-9 done once [launch - 3]: ok: workspace w3")
}

func TestStateResetQuietRefusesWhilePoolIsUnread(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: fmt.Sprintf("[repos.tiger]\npath = %q\n", filepath.Join(t.TempDir(), "moved"))})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "tiger"))

	res := d.Run("state", "reset", "--quiet")

	require.ErrorContains(t, res.Err, "cannot record the messages the first cycle wants while a source is down")
	assert.ErrorContains(t, d.Run("replay").Err, "there is no history to read")
}

func TestReplayStillComparesMemoryInTheLastCycleOfASegmentThatHasNoStopLine(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Review"}))
	d.Tick()
	// The title changes what memory holds and leaves the rules deciding what they did, so no cycle line follows.
	renamed := blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Review"})
	renamed.Title = "ENG-2 renamed"
	d.Linear.Tickets(renamed)
	d.Tick()
	d.Stop()
	// Unreachable from the CLI: the crash left no stop line, and the blocker was Done as far as the last read says.
	d.CrashedBeforeStopLine()
	d.EditLastRead(`"Status":"In Review"`, `"Status":"Done"`)

	res := d.Run("replay")

	require.Error(t, res.Err)
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "memory: recorded")
}

func TestReplayOfARangeWithNoCyclesIsNotAnUnreplayableSegment(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	// Unreachable from the CLI: segments from before dispatch recorded reads exist only on disk.
	d.WroteHistory(pastRun{Ago: time.Hour, Cycles: []pastCycle{{}}})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()

	res := d.Run("replay", "--from", d.Clock.Now().Add(time.Hour).Format(time.RFC3339))

	require.NoError(t, res.Err)
	assert.Equal(t, "0 cycles, 0 match\n", res.Stdout)
}

func TestReplayExitsTwoWhenTheOnlySegmentsWithCyclesAreFromBeforeReadLines(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	// Unreachable from the CLI: segments from before dispatch recorded reads exist only on disk.
	d.WroteHistory(pastRun{Ago: 2 * time.Hour, Cycles: []pastCycle{{}}})
	// A reset starts a segment that has no cycle, which says nothing about the world.
	d.WroteHistory(pastRun{Ago: time.Hour, Why: "reset"})

	res := d.Run("replay")

	require.ErrorContains(t, res.Err, "holds no record of the world its cycles read")
	assert.Equal(t, 2, res.Code)
}

func TestTracePrintsACycleOnlyWhenTheDecisionChangedNotWhatTheRuleRead(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	held := []pastEntry{{Rule: "launch-worker", Subject: "ENG-9", Reason: "herdr unread: herdr down"}}
	// Unreachable from the CLI: it needs a rule whose reads change while its decision does not.
	d.WroteHistory(pastRun{Cycles: []pastCycle{
		{Held: held, Reads: map[string][]string{"launch-worker/ENG-9": {"worker=unknown"}}},
		{Held: held, Reads: map[string][]string{"launch-worker/ENG-9": {"worker=unknown", "failures=1"}}},
	}})

	res := d.Run("trace", "ENG-9")

	require.NoError(t, res.Err)
	assert.Equal(t, 1, strings.Count(res.Stdout, "cycle #"), res.Stdout)
}

func TestTraceShowsAResetReceiptAsNeverSent(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Review"}))
	d.Tick()
	d.Stop()
	d.Advance(time.Minute)
	require.NoError(t, d.Run("state", "reset", "--quiet").Err)

	res := d.Run("trace", "ENG-2")

	require.NoError(t, res.Err)
	assert.Regexp(t, `receipt  comment ENG-2 done \(reset, never sent\) once `, res.Stdout)
}

func TestTraceFailsWhenItCannotWriteItsReport(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()

	res := d.RunWithBrokenOutput("trace", "ENG-1")

	require.ErrorContains(t, res.Err, "broken pipe")
}

func TestReplayWithOtherRulesNoticesAnActionWithTheSameKeyButOtherText(t *testing.T) {
	greeting := func(text string) string {
		return `whenever({ name: "greet", each: "linear.ticket", if: () => true, then: (t, ctx) => ctx.linear.queueComment(t, "` + text + `", { once: ["greet"] }) });`
	}
	d := newDispatch(t, dispatchOptions{Rules: rule(greeting("hello"))})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()
	require.Equal(t, []string{"ENG-1: hello"}, d.Linear.Comments())

	res := d.Run("replay", "--rules", oneRule(t, greeting("goodbye")))

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "difference at")
	assert.Contains(t, res.Stdout, `recorded: wants comment ENG-1 once [greet] args {"text":"hello"}`)
	assert.Contains(t, res.Stdout, `replayed: wants comment ENG-1 once [greet] args {"text":"goodbye"}`)
}

func TestStateResetQuietRefusesWhileARuleFails(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(`whenever({ name: "broken", each: "linear.ticket", if: () => { throw new Error("boom"); }, then: () => {} });`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("state", "reset", "--quiet")

	require.ErrorContains(t, res.Err, "while rule broken fails for ENG-1")
	assert.ErrorContains(t, d.Run("replay").Err, "there is no history to read")
}

func TestTraceKnowsATicketNoRuleHasWantedAnythingFor(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.Tick()
	d.Stop()

	res := d.Run("trace", "ENG-1")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "wants nothing for ENG-1")
}

// The cap keeps ENG-2 in Todo, so only the children read knows it exists. A child dispatch forgot
// would be met again with a new first-seen time, and so a new once key.
func childrenOfAParent(t *testing.T) *Dispatch {
	t.Helper()
	d := newDispatch(t, dispatchOptions{Rules: rule(childCommentRule), Config: "[dispatch]\nmax_in_progress = 1\n"})
	d.Linear.Tickets(parent("ENG-1", "In Progress"), ticket("ENG-9", "In Progress", "dispatch"))
	d.Workers.Running("ENG-9", "working")
	d.Linear.Children(child("ENG-2", "ENG-1", "Todo"))
	return d
}

func commentsOn(d *Dispatch, id string) int {
	var count int
	for _, text := range d.Linear.Comments() {
		if strings.HasPrefix(text, id+":") {
			count++
		}
	}
	return count
}

func TestAChildKeepsItsOnceKeyAcrossAFailedReadOfItsParentsChildren(t *testing.T) {
	for _, test := range []struct {
		name    string
		restart bool
	}{
		{"SameLoop", false},
		{"AfterRestart", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := childrenOfAParent(t)
			d.Tick()
			first := d.Receipts("ENG-2")
			require.Len(t, first, 1)

			// The key's statusSince is the time dispatch first saw ENG-2, so it must not
			// move to the time the children were read again.
			d.Linear.FailChildren(true)
			d.Advance(time.Hour)
			d.Tick()
			if test.restart {
				d.Restart()
				d.Tick()
			}
			d.Linear.FailChildren(false)
			d.Advance(time.Hour)
			d.Tick()

			again := d.Receipts("ENG-2")
			require.Len(t, again, 1)
			assert.Equal(t, first[0].Once, again[0].Once)
			assert.Equal(t, 1, commentsOn(d, "ENG-2"))
		})
	}
}

func TestAChildThatLeftTheParentIsForgottenOnceTheChildrenAreRead(t *testing.T) {
	d := childrenOfAParent(t)
	d.Tick()
	d.Linear.Children(child("ENG-3", "ENG-1", "Todo"))
	d.Advance(time.Hour)
	d.Tick()

	d.Linear.Children(child("ENG-2", "ENG-1", "Todo"))
	d.Advance(time.Hour)
	d.Tick()

	assert.Len(t, d.Receipts("ENG-2"), 2)
	assert.Equal(t, 2, commentsOn(d, "ENG-2"))
}

func TestAChildIsRememberedThroughALinearOutageBeforeTheChildrenFail(t *testing.T) {
	d := childrenOfAParent(t)
	d.Tick()
	// Linear is unread across the restart, so no parent exists to list its children.
	d.Linear.FailManaged(true)
	d.Restart()
	d.Linear.FailManaged(false)
	d.Linear.FailChildren(true)
	d.Advance(5 * time.Minute)
	d.Tick()

	d.Linear.FailChildren(false)
	d.Advance(time.Hour)
	d.Tick()

	assert.Len(t, d.Receipts("ENG-2"), 1)
	assert.Equal(t, 1, commentsOn(d, "ENG-2"))
}

func traceJSON(t *testing.T, out string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(out), "\n") {
		var line map[string]any
		require.NoError(t, json.Unmarshal([]byte(raw), &line))
		lines = append(lines, line)
	}
	return lines
}

func TestTraceJSONPrintsOneObjectForEveryLineTheTextFormPrints(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(countRule)})
	d.Services.Sentry().Respond("sentry", "GET", "/count", 200, `{"open":3}`)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()

	text := d.Run("trace", "ENG-1")
	res := d.Run("trace", "ENG-1", "--json")

	require.NoError(t, text.Err)
	require.NoError(t, res.Err)
	textLines := strings.Split(strings.TrimSpace(text.Stdout), "\n")
	lines := traceJSON(t, res.Stdout)
	require.Len(t, lines, len(textLines))
	for i, line := range lines {
		switch {
		case strings.Contains(textLines[i], " receipt  "):
			assert.Equal(t, "receipt", line["type"])
		case strings.Contains(textLines[i], "  http  "):
			assert.Equal(t, "http", line["type"])
		default:
			assert.Equal(t, "cycle", line["type"])
		}
	}
}

func TestTraceJSONPrintsACycleLineWithWhatTheRuleWantedAndRead(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()

	res := d.Run("trace", "ENG-1", "--json")

	require.NoError(t, res.Err)
	var wants map[string]any
	for _, line := range traceJSON(t, res.Stdout) {
		if line["type"] == "cycle" && line["decision"] == "wants" && line["kind"] == "launch" {
			wants = line
		}
	}
	require.NotNil(t, wants)
	assert.Equal(t, "launch-worker", wants["rule"])
	assert.Equal(t, "ENG-1", wants["subject"])
	assert.Equal(t, "start", wants["why"])
	assert.Equal(t, "2026-10-09T12:00:00Z", wants["time"])
	assert.EqualValues(t, 1, wants["cycle"])
	assert.NotEmpty(t, wants["once"])
	assert.NotEmpty(t, wants["read"])
}

func TestTraceJSONPrintsAReceiptFirstAttemptedAndThenWithItsOutcome(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()

	res := d.Run("trace", "ENG-1", "--json")
	text := d.Run("trace", "ENG-1")

	require.NoError(t, res.Err)
	require.NoError(t, text.Err)
	var launches []map[string]any
	for _, line := range traceJSON(t, res.Stdout) {
		if line["type"] == "receipt" && line["kind"] == "launch" {
			launches = append(launches, line)
		}
	}
	require.Len(t, launches, 2)
	assert.Equal(t, "attempted", launches[0]["state"])
	assert.Equal(t, "done", launches[1]["state"])
	assert.Equal(t, "ENG-1", launches[1]["subject"])
	assert.Equal(t, "launch-worker", launches[1]["rule"])
	assert.Equal(t, "2026-10-09T12:00:00Z", launches[1]["time"])
	assert.NotEmpty(t, launches[1]["detail"])
	assert.Contains(t, text.Stdout, "launch ENG-1 done once "+launches[1]["once"].(string))
	assert.Equal(t, []Receipt{{
		Kind: "launch", Subject: "ENG-1", State: "done", Once: launches[1]["once"].(string), Detail: launches[1]["detail"].(string),
	}}, d.Receipts("ENG-1"))
}

func TestTraceJSONPrintsAReceiptWithNoOutcomeAsAttempted(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	// Unreachable from the CLI: a receipt with no outcome comes from a run that crashed between an action and its result.
	d.WroteHistory(pastRun{Crashed: true, Carried: []pastReceipt{{Kind: "comment", Subject: "ENG-9", Once: []any{"note", "ENG-9"}, Rule: "note"}}})

	receipts := d.Receipts("ENG-9")

	require.Len(t, receipts, 1)
	assert.Equal(t, "attempted", receipts[0].State)
	assert.Equal(t, "comment", receipts[0].Kind)
}

func TestTraceJSONPrintsTheExchangesOfATicket(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(countRule)})
	d.Services.Sentry().Respond("sentry", "GET", "/count", 200, `{"open":3}`)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()

	res := d.Run("trace", "ENG-1", "--json")

	require.NoError(t, res.Err)
	var exchange map[string]any
	for _, line := range traceJSON(t, res.Stdout) {
		if line["type"] == "http" {
			exchange = line
		}
	}
	require.NotNil(t, exchange)
	assert.Equal(t, "count", exchange["rule"])
	assert.Equal(t, "sentry", exchange["service"])
	assert.Equal(t, "/count", exchange["path"])
	assert.EqualValues(t, 200, exchange["status"])
	assert.Equal(t, map[string]any{"open": float64(3)}, exchange["body"])
}

func TestTraceJSONExitsOneWhenHistoryHasNoRecordOfTheTicket(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()

	res := d.Run("trace", "ENG-77", "--json")

	require.ErrorContains(t, res.Err, "history has no record of ENG-77")
	assert.Equal(t, 1, res.Code)
	assert.Empty(t, res.Stdout)
	assert.Empty(t, d.Receipts("ENG-77"))
}

func TestTraceTakesTheJSONFlagBeforeOrAfterTheTicketAndNothingElse(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()

	after := d.Run("trace", "ENG-1", "--json")
	before := d.Run("trace", "--json", "eng-1")

	require.NoError(t, after.Err)
	require.NoError(t, before.Err)
	assert.Equal(t, after.Stdout, before.Stdout)
	for _, args := range [][]string{{"trace", "--json"}, {"trace", "ENG-1", "--json", "--json"}, {"trace", "ENG-1", "ENG-2", "--json"}} {
		res := d.Run(args...)

		require.ErrorContains(t, res.Err, "trace takes a ticket")
		assert.Empty(t, res.Stdout)
	}
}

func TestTraceJSONNamesEachDecisionARuleMade(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	// Unreachable from the CLI: the cycles are written by hand so that one deterministic history holds every decision value.
	d.WroteHistory(pastRun{Cycles: []pastCycle{
		{Waiting: []pastEntry{{Rule: "launch-worker", Subject: "ENG-4", Kind: "launch", Reason: "1 of 1 in progress"}}},
		{Held: []pastEntry{{Rule: "launch-worker", Subject: "ENG-4", Reason: "linear unread: down"}}},
		{Held: []pastEntry{{Rule: "comment-command", Subject: "ENG-4", Reason: "herdr unread", Dropped: true}}},
		{Throws: []pastThrow{{Rule: "launch-worker", Subject: "ENG-4", Error: "boom"}}},
		{Throws: []pastThrow{{Rule: "launch-worker", Subject: "ENG-4", Error: "too slow", Timeout: true}}},
		{Covered: []pastEntry{{Rule: "launch-worker", Subject: "ENG-4", Kind: "launch", Once: []any{"launch", 3.0}, Reason: "done 12:00"}}},
	}})

	res := d.Run("trace", "ENG-4", "--json")

	require.NoError(t, res.Err)
	lines := traceJSON(t, res.Stdout)
	require.Len(t, lines, 6)
	for i, want := range []map[string]any{
		{"decision": "waits", "kind": "launch", "detail": "1 of 1 in progress"},
		{"decision": "held", "detail": "linear unread: down"},
		{"decision": "dropped", "rule": "comment-command", "detail": "herdr unread"},
		{"decision": "threw", "detail": "boom"},
		{"decision": "timeout", "detail": "too slow"},
		{"decision": "covered", "kind": "launch", "once": "[launch 3]", "detail": "done 12:00"},
	} {
		assert.Equal(t, "cycle", lines[i]["type"])
		assert.Equal(t, "ENG-4", lines[i]["subject"])
		for field, value := range want {
			assert.Equal(t, value, lines[i][field])
		}
	}
}

func TestTraceJSONPrintsATicketNoRuleHasWantedAnythingForAsNothing(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.Tick()
	d.Stop()

	res := d.Run("trace", "ENG-1", "--json")

	require.NoError(t, res.Err)
	lines := traceJSON(t, res.Stdout)
	require.NotEmpty(t, lines)
	assert.Equal(t, "nothing", lines[0]["decision"])
	assert.Equal(t, "ENG-1", lines[0]["subject"])
}

func TestTraceJSONMarksAReceiptStateResetRecordedAsNeverSent(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Review"}))
	d.Tick()
	d.Stop()
	d.Advance(time.Minute)
	require.NoError(t, d.Run("state", "reset", "--quiet").Err)

	res := d.Run("trace", "ENG-2", "--json")

	require.NoError(t, res.Err)
	var reset map[string]any
	for _, line := range traceJSON(t, res.Stdout) {
		if line["type"] == "receipt" && line["reset"] == true {
			reset = line
		}
	}
	require.NotNil(t, reset)
	assert.Equal(t, "comment", reset["kind"])
	assert.Equal(t, "done", reset["state"])
	assert.Equal(t, "state reset", reset["detail"])
	receipts := d.Receipts("ENG-2")
	require.NotEmpty(t, receipts)
	assert.True(t, receipts[len(receipts)-1].Reset)
}

func TestTraceJSONPrintsAnExchangeWithNoAnswerAsUnknown(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(countRule)})
	d.Services.Sentry().Respond("sentry", "GET", "/count", 503, "")
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Run("tick")

	res := d.Run("trace", "ENG-1", "--json")

	require.NoError(t, res.Err)
	var found map[string]any
	for _, line := range traceJSON(t, res.Stdout) {
		if line["type"] == "http" {
			found = line
		}
	}
	require.NotNil(t, found)
	assert.Equal(t, "GET sentry /count: returned 503", found["unknown"])
	assert.NotContains(t, found, "status")
	assert.NotContains(t, found, "body")
}

func TestTraceRefusesATicketAfterAnEmptyArgument(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()

	res := d.Run("trace", "", "ENG-1")

	require.ErrorContains(t, res.Err, "trace takes a ticket")
	assert.Empty(t, res.Stdout)
}

// readKeptRules fetches in the first cycle and reads the kept answer in a later one.
const readKeptRules = `
whenever({ name: "fetch", each: "linear.ticket", if: (t, ctx) => !ctx.ledger.done(t, ["fetch", t.id]),
  then: (t, ctx) => { ctx.http.get("sentry", "/count"); return ctx.linear.queueComment(t, "fetched", { once: ["fetch", t.id] }); } });
whenever({ name: "use-kept", each: "linear.ticket", if: (t, ctx) => ctx.ledger.done(t, ["fetch", t.id]) && known(() => ctx.http.last("sentry", "/count")),
  then: (t, ctx) => ctx.linear.queueComment(t, "kept " + (ctx.http.last("sentry", "/count").body as any).open, { once: ["kept", t.id] }) });`

// httpHistory runs rules that call sentry for a few ticks and stops the loop, leaving history to replay.
func httpHistory(t *testing.T, body string) (*Dispatch, *httpservicetest.Fake) {
	t.Helper()
	d := newDispatch(t, dispatchOptions{Rules: rule(body)})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "GET", "/count", 200, `{"open":3}`)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Advance(time.Minute)
	d.Tick()
	d.Stop()
	return d, sentry
}

func TestReplayMatchesARunWhoseRuleCalledGet(t *testing.T) {
	d, sentry := httpHistory(t, countRule)
	live := len(sentry.Requests())

	cycles, matched := replayOf(t, d)

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
	assert.Len(t, sentry.Requests(), live)
}

func TestReplayMatchesALastReadOfAnAnswerAnEarlierCycleKept(t *testing.T) {
	d, _ := httpHistory(t, readKeptRules)
	require.Contains(t, strings.Join(d.Linear.Comments(), "\n"), "kept 3")

	cycles, matched := replayOf(t, d)

	assert.Greater(t, cycles, 1)
	assert.Equal(t, cycles, matched)
}

func TestReplayFromALaterCycleRebuildsWhatEarlierCyclesKept(t *testing.T) {
	d, _ := httpHistory(t, readKeptRules)

	res := d.Run("replay", "--from", d.Clock.Now().Add(-30*time.Second).Format(time.RFC3339))

	require.NoError(t, res.Err)
	assert.NotContains(t, res.Stdout, "difference at")
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
			d, sentry := httpHistory(t, countRule)
			live := len(sentry.Requests())

			res := d.Run("replay", "--rules", oneRule(t, test.body))

			require.NoError(t, res.Err)
			assert.Contains(t, res.Stdout, "difference at")
			assert.Len(t, sentry.Requests(), live)
		})
	}
}

func TestReplayWithOtherRulesMatchesWhenTheGetsAreTheSame(t *testing.T) {
	d, _ := httpHistory(t, countRule)

	cycles, matched := replayOf(t, d, "--rules", oneRule(t, countRule))

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}

func TestTraceShowsTheExchangesOfATicket(t *testing.T) {
	d, _ := httpHistory(t, countRule)

	res := d.Run("trace", "ENG-1")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, `http  count  GET sentry /count  200 {"open":3}`)
}

func TestReplayOfHistoryWithoutRuleAndTicketOnItsExchangesStillMatches(t *testing.T) {
	d, _ := httpHistory(t, countRule)
	// Unreachable from the CLI: exchanges recorded before they named a rule and ticket exist only on disk.
	d.EditHistory(`,"rule":"count","ticket":"ENG-1"`, ``)

	cycles, matched := replayOf(t, d)

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}

func TestStateResetQuietRecordsTheMessagesAnHTTPRuleWants(t *testing.T) {
	d, _ := httpHistory(t, countRule)
	require.Equal(t, []string{"ENG-1: open 3"}, d.Linear.Comments())

	res := d.Run("state", "reset", "--quiet")
	require.NoError(t, res.Err)
	d.Restart()
	d.Tick()

	assert.Contains(t, res.Stdout, "1 receipt for the messages the first cycle wants")
	assert.Equal(t, []string{"ENG-1: open 3"}, d.Linear.Comments())
}

func TestStateResetQuietRefusesWhenAServiceAnswerIsUnknown(t *testing.T) {
	d, sentry := httpHistory(t, countRule)
	sentry.Respond("sentry", "GET", "/count", 503, "")

	res := d.Run("state", "reset", "--quiet")

	require.ErrorContains(t, res.Err, "cannot record the messages the first cycle wants")
	assert.ErrorContains(t, res.Err, "sentry /count")
}

// clearedRules comments once the kept answer is gone, which only a resync or a tick brings about.
const clearedRules = `
whenever({ name: "fetch", each: "linear.ticket", if: (t, ctx) => !ctx.ledger.done(t, ["fetch", t.id]),
  then: (t, ctx) => { ctx.http.get("sentry", "/count"); return ctx.linear.queueComment(t, "fetched", { once: ["fetch", t.id] }); } });
whenever({ name: "cleared", each: "linear.ticket", if: (t, ctx) => ctx.ledger.done(t, ["fetch", t.id]) && !known(() => ctx.http.last("sentry", "/count")),
  then: (t, ctx) => ctx.linear.queueComment(t, "cleared", { once: ["cleared", t.id] }) });`

func TestReplayFromACycleLaterThanTheOneThatKeptTheAnswerRebuildsIt(t *testing.T) {
	d, _ := httpHistory(t, readKeptRules)
	start := d.Clock.Now().Add(-time.Minute)
	// Unreachable from the CLI: the cycle that reads the kept answer moves to 20s after the cycle that
	// fetched it, with no resync or tick between.
	d.EditHistory(
		fmt.Sprintf(`"now":%d,"why":"event"`, start.UnixMilli()),
		fmt.Sprintf(`"now":%d,"why":"event"`, start.Add(20*time.Second).UnixMilli()))

	res := d.Run("replay", "--from", start.Add(10*time.Second).Format(time.RFC3339))

	require.NoError(t, res.Err)
	assert.NotContains(t, res.Stdout, "difference at")
	assert.Regexp(t, `^3 cycles, 3 match\n$`, res.Stdout)
}

func TestReplayClearsWhatWasKeptAtATickTheLiveRunClearedItAt(t *testing.T) {
	d, _ := httpHistory(t, clearedRules)
	require.Equal(t, []string{"ENG-1: fetched", "ENG-1: cleared"}, d.Linear.Comments())

	cycles, matched := replayOf(t, d)

	assert.Greater(t, cycles, 2)
	assert.Equal(t, cycles, matched)
}

func TestReplayClearsWhatWasKeptAtAResyncTheLiveRunClearedItAt(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(clearedRules)})
	d.Services.Sentry().Respond("sentry", "GET", "/count", 200, `{"open":3}`)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Advance(5 * time.Minute)
	d.Stop()
	require.Equal(t, []string{"ENG-1: fetched", "ENG-1: cleared"}, d.Linear.Comments())

	cycles, matched := replayOf(t, d)

	assert.Greater(t, cycles, 2)
	assert.Equal(t, cycles, matched)
}

func TestReplayOfAGetThatFailedLiveHoldsTheRuleAgain(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(countRule)})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "GET", "/count", 503, ``)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()
	require.Empty(t, d.Linear.Comments())
	live := len(sentry.Requests())

	cycles, matched := replayOf(t, d)

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
	assert.Len(t, sentry.Requests(), live)
}

func TestReplayWithOtherRulesCountsAnUnrecordedGetAsADifferenceEvenWhenTheActionsMatch(t *testing.T) {
	d, sentry := httpHistory(t, countRule)
	live := len(sentry.Requests())
	other := oneRule(t, `whenever({ name: "count", each: "linear.ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => { known(() => ctx.http.get("sentry", "/other")); return ctx.linear.queueComment(t, "open 3", { once: ["count", t.id] }); } });`)

	res := d.Run("replay", "--rules", other)

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "difference at")
	assert.Contains(t, res.Stdout, "GET sentry /other")
	assert.Len(t, sentry.Requests(), live)
}

func TestReplayMatchesAGetTwoTicketsEachMade(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(countRule)})
	d.Services.Sentry().Respond("sentry", "GET", "/count", 200, `{"open":3}`)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()
	require.Equal(t, []string{"ENG-1: open 3", "ENG-2: open 3"}, d.Linear.Comments())

	cycles, matched := replayOf(t, d)

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}

func TestTraceShowsOnlyTheExchangesOfTheTicketItIsGiven(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(`whenever({ name: "count", each: "linear.ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.linear.queueComment(t, "open " + (ctx.http.get("sentry", "/count/" + t.id).body as any).open, { once: ["count", t.id] }) });`)})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "GET", "/count/ENG-1", 200, `{"open":3}`)
	sentry.Respond("sentry", "GET", "/count/ENG-2", 200, `{"open":4}`)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()

	res1 := d.Run("trace", "ENG-1")
	res2 := d.Run("trace", "ENG-2")

	require.NoError(t, res1.Err)
	require.NoError(t, res2.Err)
	assert.Contains(t, res1.Stdout, "GET sentry /count/ENG-1")
	assert.NotContains(t, res1.Stdout, "/count/ENG-2")
	assert.Contains(t, res2.Stdout, "GET sentry /count/ENG-2")
	assert.NotContains(t, res2.Stdout, "/count/ENG-1")
}

func TestStateResetQuietWorksOnceTheServiceAnswersAfterRefusing(t *testing.T) {
	d, sentry := httpHistory(t, countRule)
	sentry.Respond("sentry", "GET", "/count", 503, "")

	require.Error(t, d.Run("state", "reset", "--quiet").Err)
	sentry.Respond("sentry", "GET", "/count", 200, `{"open":3}`)
	res := d.Run("state", "reset", "--quiet")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "1 receipt for the messages the first cycle wants")
}

// markRules says whether the answer fetched earlier is still kept.
const markRules = `
whenever({ name: "fetch", each: "linear.ticket", if: (t, ctx) => !ctx.ledger.done(t, ["fetch", t.id]),
  then: (t, ctx) => { ctx.http.get("sentry", "/count"); return ctx.linear.queueComment(t, "fetched", { once: ["fetch", t.id] }); } });
whenever({ name: "mark", each: "linear.ticket", if: (t, ctx) => ctx.ledger.done(t, ["fetch", t.id]),
  then: (t, ctx) => { const state = known(() => ctx.http.last("sentry", "/count")) ? "kept" : "empty"; return ctx.linear.queueComment(t, state, { once: ["mark", t.id, state] }); } });`

func TestReplayStartsTheSegmentAfterARestartWithNothingKept(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(markRules)})
	d.Services.Sentry().Respond("sentry", "GET", "/count", 200, `{"open":3}`)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Restart()
	d.Stop()
	require.Equal(t, []string{"ENG-1: fetched", "ENG-1: kept", "ENG-1: empty"}, d.Linear.Comments())

	cycles, matched := replayOf(t, d)

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}

func TestReplayMatchesALastReadAfterACycleThatWroteNoLinesRefilledWhatAResyncCleared(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(`
whenever({ name: "poll", each: "linear.ticket", if: (t, ctx) => t.status === "in_progress" && ctx.herdr.worker(t).state === "idle",
  then: (t, ctx) => { ctx.http.get("sentry", "/count"); return ctx.linear.queueComment(t, "polled", { once: ["poll", t.id] }); } });
whenever({ name: "working-reader", each: "linear.ticket", if: (t, ctx) => ctx.herdr.worker(t).state === "working" && known(() => ctx.http.last("sentry", "/count")),
  then: (t, ctx) => ctx.linear.queueComment(t, "working with " + (ctx.http.last("sentry", "/count").body as any).open, { once: ["working", t.id] }) });`)})
	d.Services.Sentry().Respond("sentry", "GET", "/count", 200, `{"open":3}`)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	// poll gets on every cycle while the worker is idle, so the cycle after a tick refills what the tick
	// cleared, and the cycle that sees the worker typed into (working) only reads it.
	d.Tick()
	d.Advance(time.Minute)
	d.Tick()
	// Nothing changed since the last tick, so the cycle after this one writes no line at all.
	d.Advance(time.Minute)
	d.Tick()

	require.NoError(t, d.Run("tell", "ENG-1", "hi").Err)
	require.Eventually(t, func() bool { return slices.Contains(d.Linear.Comments(), "ENG-1: working with 3") }, 5*time.Second, 5*time.Millisecond)
	d.Stop()

	cycles, matched := replayOf(t, d)

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}

func TestTraceShowsAnAnswerThatChangedWhileTheRuleDecidedTheSame(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(countRule)})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "GET", "/count", 200, `{"open":3}`)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Advance(time.Minute)
	d.Tick()
	sentry.Respond("sentry", "GET", "/count", 200, `{"open":4}`)
	d.Advance(time.Minute)
	d.Tick()
	d.Stop()

	res := d.Run("trace", "ENG-1")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, `GET sentry /count  200 {"open":3}`)
	assert.Contains(t, res.Stdout, `GET sentry /count  200 {"open":4}`)
}

func TestReplayWithOtherRulesMatchesTheSameGetsRegisteredInAnotherOrder(t *testing.T) {
	const first = `whenever({ name: "first", each: "linear.ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.linear.queueComment(t, "a " + (ctx.http.get("sentry", "/a").body as any).n, { once: ["a", t.id] }) });`
	const second = `whenever({ name: "second", each: "linear.ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.linear.queueComment(t, "b " + (ctx.http.get("sentry", "/b").body as any).n, { once: ["b", t.id] }) });`
	d := newDispatch(t, dispatchOptions{Rules: rule(first + "\n" + second)})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "GET", "/a", 200, `{"n":1}`)
	sentry.Respond("sentry", "GET", "/b", 200, `{"n":2}`)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()

	cycles, matched := replayOf(t, d, "--rules", oneRule(t, second+"\n"+first))

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}
