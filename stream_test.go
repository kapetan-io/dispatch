package dispatch_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
)

// stateRule comments the worker's state once per state, so a test reads what the rules saw.
const stateRule = `
whenever({ name: "state", each: "ticket", if: () => true,
  then: (t, ctx) => {
    const state = ctx.worker.of(t).state;
    return ctx.ticket.queueComment(t, "state " + state, { once: ["state", t.id, state] });
  } });`

// startStreaming starts the loop over a streaming herdr and returns once the stream is live and the loop has
// settled, so a change the test makes next is the only thing the loop is waiting on.
func (d *Dispatch) startStreaming() {
	d.t.Helper()
	d.Restart()
	d.streamLive(1)
}

// streamLive waits for the nth stream to name its first panes and for the loop to take them.
func (d *Dispatch) streamLive(n int) {
	d.t.Helper()
	require.Eventually(d.t, func() bool { return d.tools.Herdr.Streams() >= n }, 5*time.Second, time.Millisecond)
	d.settleLoop()
}

func (d *Dispatch) herdrCalls(name string) int { return len(d.tools.Calls("herdr", name)) }

// readLines is every read line in the newest history segment.
func (d *Dispatch) readLines() []string {
	d.t.Helper()
	_, lines := d.newestSegment()
	var reads []string
	for _, line := range lines {
		if strings.HasPrefix(line, `{"line":"read"`) {
			reads = append(reads, line)
		}
	}
	return reads
}

func streamingWorker(t *testing.T) *Dispatch {
	t.Helper()
	d := newDispatch(t, dispatchOptions{Streaming: true})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.startStreaming()
	return d
}

func TestStreamShowsAnIdleWorkerAndTellsItsFollowerWithoutAResync(t *testing.T) {
	t.Parallel()
	d := streamingWorker(t)
	d.Workers.Stop("ENG-1", stopTasks{})
	d.settleLoop()
	reads, panes := d.herdrCalls("read"), d.herdrCalls("agent-at")

	d.Workers.SetStatus("ENG-1", "idle")

	require.Eventually(t, func() bool { return slices.Contains(d.Workers.Renames(), "w-ENG-1|💤 ENG-1") }, 5*time.Second, 5*time.Millisecond)
	d.settleLoop()
	assert.Contains(t, d.Followed.Told(), "ENG-1's worker is idle with nothing running: ticket ENG-1")
	assert.Equal(t, reads, d.herdrCalls("read"))
	assert.Equal(t, panes+1, d.herdrCalls("agent-at"))
}

func TestStreamTellsTheFollowerAWorkerIsBlocked(t *testing.T) {
	t.Parallel()
	d := streamingWorker(t)
	reads := d.herdrCalls("read")

	d.Workers.SetStatus("ENG-1", "blocked")

	require.Eventually(t, func() bool {
		return slices.Contains(d.Followed.Told(), "ENG-1 is waiting on your answer in its workspace")
	}, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, reads, d.herdrCalls("read"))
}

func TestStreamShowsAWorkerGoneWhenItsClaudeExitsOrItsTabOrWorkspaceCloses(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		gone func(d *Dispatch)
	}{
		{"ClaudeExits", func(d *Dispatch) { d.tools.Herdr.ExitAgent("w-ENG-1:p1") }},
		{"TabCloses", func(d *Dispatch) { require.NoError(d.t, d.tools.Herdr.CloseTab(context.Background(), "w-ENG-1:t1")) }},
		{"WorkspaceCloses", func(d *Dispatch) { d.Workers.Gone("ENG-1") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{Streaming: true, Rules: rule(stateRule)})
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
			d.Workers.Running("ENG-1", "working")
			d.startStreaming()
			require.Equal(t, "state working", lastReading(t, d))

			test.gone(d)

			require.Eventually(t, func() bool { return lastReadingOf(d, "ENG-1") == "state none" }, 5*time.Second, 5*time.Millisecond)
		})
	}
}

func TestStreamLeavesAWorkerAloneForAChangeToAPaneItNoLongerHolds(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Streaming: true, Rules: rule(stateRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.startStreaming()
	d.Workers.Gone("ENG-1")
	require.Eventually(t, func() bool { return lastReadingOf(d, "ENG-1") == "state none" }, 5*time.Second, 5*time.Millisecond)

	d.settleLoop()
	calls := len(d.tools.Journal.Calls())

	d.tools.Herdr.Changed("w-ENG-1:p1")

	d.settleLoop()
	assert.Equal(t, calls, len(d.tools.Journal.Calls()))
}

func TestStreamAppliesAChangeThatArrivesDuringAReadAfterThatRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Streaming: true, Rules: rule(stateRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.startStreaming()
	d.tools.Herdr.SetStatusAfterSnapshot("w-ENG-1:p1", "blocked")

	require.NoError(t, d.TickAfterASecond())

	require.Eventually(t, func() bool { return lastReadingOf(d, "ENG-1") == "state blocked" }, 5*time.Second, 5*time.Millisecond)
	d.settleLoop()
	assert.Equal(t, []string{"ENG-1: state working", "ENG-1: state blocked"}, d.Linear.Comments())
}

func TestStreamIgnoresAChangeToAPaneThatHoldsNoWorker(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Streaming: true})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.startStreaming()
	calls, receipts := len(d.tools.Journal.Calls()), len(d.Receipts("ENG-1"))

	d.tools.Herdr.Changed("a-pane-of-the-users:p1")

	d.settleLoop()
	assert.Equal(t, calls, len(d.tools.Journal.Calls()))
	assert.Len(t, d.Receipts("ENG-1"), receipts)
}

func TestStreamMakesNoCallForAChangeAfterAFailedReadOfTheMultiplexer(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Streaming: true})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.startStreaming()
	d.tools.Herdr.Fail(herdrtest.OpRead, errors.New("the server is down"))
	require.NoError(t, d.TickAfterASecond())
	d.settleLoop()
	calls := len(d.tools.Journal.Calls())

	d.Workers.SetStatus("ENG-1", "idle")

	d.settleLoop()
	assert.Equal(t, calls, len(d.tools.Journal.Calls()))
}

func TestStreamDownIsReportedAtEachTickAndReopenedWhenTheMultiplexerIsBack(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Streaming: true})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.tools.Herdr.Fail(herdrtest.OpStream, herdr.ErrRexNotRunning)
	d.Restart()
	const downLine = "dispatch: the worker stream ended: Rex is not running, and dispatch never starts it: open Rex; workers update at each resync until it reopens\n"
	waitForDown := func(n int) {
		require.Eventually(t, func() bool { return strings.Count(d.loopErr(), downLine) == n }, 5*time.Second, time.Millisecond)
	}
	waitForDown(1)

	require.NoError(t, d.TickAfterASecond())
	waitForDown(2)

	d.tools.Herdr.Fail(herdrtest.OpStream, nil)
	require.NoError(t, d.TickAfterASecond())
	d.streamLive(1)
	assert.Equal(t, 3, d.herdrCalls("changes"))
	d.Workers.SetStatus("ENG-1", "blocked")
	require.Eventually(t, func() bool {
		return slices.Contains(d.Followed.Told(), "ENG-1 is waiting on your answer in its workspace")
	}, 5*time.Second, 5*time.Millisecond)
}

func TestStreamDroppedIsReportedOnceAndDoesNotReopenInPlace(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Streaming: true})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.startStreaming()

	d.tools.Herdr.DropStream(herdr.ErrRexEventsEnded)

	require.Eventually(t, func() bool {
		return strings.Contains(d.loopErr(), "the worker stream ended: rex events ended: the server disconnected; workers update at each resync until it reopens")
	}, 5*time.Second, time.Millisecond)
	d.settleLoop()
	assert.Equal(t, 1, d.herdrCalls("changes"))
}

func TestStreamStoppedWithTheLoopPrintsNoStreamDownLine(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Streaming: true})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.startStreaming()
	d.Stop()

	assert.NotContains(t, d.loopErr(), "the worker stream ended")
}

func TestReplayMatchesARunWhoseWorldsCameFromTheStream(t *testing.T) {
	t.Parallel()
	d := streamingWorker(t)
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Workers.SetStatus("ENG-1", "idle")
	require.Eventually(t, func() bool { return slices.Contains(d.Workers.Renames(), "w-ENG-1|💤 ENG-1") }, 5*time.Second, 5*time.Millisecond)
	d.Workers.SetStatus("ENG-1", "blocked")
	require.Eventually(t, func() bool {
		return slices.Contains(d.Followed.Told(), "ENG-1 is waiting on your answer in its workspace")
	}, 5*time.Second, 5*time.Millisecond)
	d.settleLoop()
	d.Stop()

	cycles, matched := replayOf(t, d)

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}

// workersOf is the workers part of a read line, which holds what the rules decide a worker is.
func workersOf(t *testing.T, line string) string {
	t.Helper()
	_, after, ok := strings.Cut(line, `"workers":`)
	require.True(t, ok)
	before, _, _ := strings.Cut(after, `,"sessions":`)
	return before
}

func TestTickAfterAStreamChangeLeavesTheWorkerAsTheStreamReadIt(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		change func(d *Dispatch)
		state  string
	}{
		{"Idle", func(d *Dispatch) { d.Workers.SetStatus("ENG-1", "idle") }, "idle"},
		{"ClaudeExits", func(d *Dispatch) { d.tools.Herdr.ExitAgent("w-ENG-1:p1") }, "none"},
		{"TabCloses", func(d *Dispatch) { require.NoError(d.t, d.tools.Herdr.CloseTab(context.Background(), "w-ENG-1:t1")) }, "none"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{Streaming: true})
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
			d.Workers.Running("ENG-1", "working")
			d.startStreaming()
			test.change(d)
			require.Eventually(t, func() bool {
				reads := d.readLines()
				return strings.Contains(workersOf(t, reads[len(reads)-1]), `"State":"`+test.state+`"`)
			}, 5*time.Second, 5*time.Millisecond)
			d.settleLoop()
			before := d.readLines()

			require.NoError(t, d.TickAfterASecond())

			after := d.readLines()
			assert.Equal(t, workersOf(t, before[len(before)-1]), workersOf(t, after[len(after)-1]))
		})
	}
}

func TestStreamShowsANudgedWorkerIdleOnceItWorkedAndKeepsItWorkingWhileItReadsIdle(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Streaming: true})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{})
	d.startStreaming()
	require.Equal(t, []string{"w-ENG-1|💤 ENG-1"}, d.Workers.Renames())
	d.Advance(time.Second)
	require.NoError(t, d.Run("tell", "ENG-1", "carry on").Err)
	require.Eventually(t, func() bool { return len(d.Workers.Renames()) == 2 }, 5*time.Second, 5*time.Millisecond)

	// A change that finds the worker still idle before Claude took the nudge keeps it working.
	panes := d.herdrCalls("agent-at")
	d.tools.Herdr.Changed("w-ENG-1:p1")
	require.Eventually(t, func() bool { return d.herdrCalls("agent-at") > panes }, 5*time.Second, time.Millisecond)
	d.settleLoop()
	assert.Len(t, d.Workers.Renames(), 2)

	panes = d.herdrCalls("agent-at")
	d.Workers.SetStatus("ENG-1", "working")
	require.Eventually(t, func() bool { return d.herdrCalls("agent-at") > panes }, 5*time.Second, time.Millisecond)
	d.settleLoop()
	d.Workers.SetStatus("ENG-1", "idle")

	require.Eventually(t, func() bool {
		renames := d.Workers.Renames()
		return len(renames) > 2 && renames[len(renames)-1] == "w-ENG-1|💤 ENG-1"
	}, 5*time.Second, 5*time.Millisecond)
}

func TestStreamTellsTheFollowerAWorkerIsIdleOnceTheStopRecordOfItsTurnLands(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Streaming: true})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	// The previous turn ended with a background task still running.
	d.Workers.Stop("ENG-1", stopTasks{Running: 1})
	d.startStreaming()

	// Claude reports idle just before its Stop hook lands the record of the turn that ended.
	d.Workers.SetStatus("ENG-1", "idle")
	d.settleLoop()
	require.NotContains(t, d.Followed.Told(), "ENG-1's worker is idle with nothing running: ticket ENG-1")
	d.Advance(time.Second)
	d.Workers.Stop("ENG-1", stopTasks{})

	require.Eventually(t, func() bool {
		return slices.Contains(d.Followed.Told(), "ENG-1's worker is idle with nothing running: ticket ENG-1")
	}, 5*time.Second, 5*time.Millisecond)
}

func TestStreamDownLeavesWorkersToTheNextTickWhichReopensIt(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Streaming: true})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.startStreaming()
	d.tools.Herdr.Fail(herdrtest.OpStream, herdr.ErrRexNotRunning)
	d.tools.Herdr.DropStream(herdr.ErrRexEventsEnded)
	require.Eventually(t, func() bool { return strings.Contains(d.loopErr(), "the worker stream ended") }, 5*time.Second, time.Millisecond)

	d.Workers.SetStatus("ENG-1", "blocked")
	d.settleLoop()
	require.NotContains(t, d.Followed.Told(), "ENG-1 is waiting on your answer in its workspace")
	d.tools.Herdr.Fail(herdrtest.OpStream, nil)
	require.NoError(t, d.TickAfterASecond())

	assert.Contains(t, d.Followed.Told(), "ENG-1 is waiting on your answer in its workspace")
	d.streamLive(2)
	assert.Equal(t, 2, d.herdrCalls("changes"))
}

func TestStreamNamesMadeDuringAFailedReadAreLeftToTheNextFullRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Streaming: true})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.startStreaming()
	d.tools.Herdr.Fail(herdrtest.OpRead, errors.New("the server is down"))
	require.NoError(t, d.TickAfterASecond())
	d.settleLoop()

	d.Workers.SetStatus("ENG-1", "blocked")
	d.settleLoop()
	d.tools.Herdr.Fail(herdrtest.OpRead, nil)
	require.NoError(t, d.TickAfterASecond())

	assert.Contains(t, d.Followed.Told(), "ENG-1 is waiting on your answer in its workspace")
}
