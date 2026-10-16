package dispatch_test

import (
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
