package dispatch_test

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// askedWorker is a worker on a Needs Human ticket that ended a turn with a question, and the
// turn's time as dispatch turn prints it.
func askedWorker(t *testing.T) (d *Dispatch, at string, transcript string) {
	t.Helper()
	d = newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	transcript = d.transcript("t1")
	d.Workers.Stop("ENG-1", stopTasks{Said: "retry for 24 hours?", Transcript: transcript})
	return d, strconv.FormatInt(d.Clock.Now().UnixMilli(), 10), transcript
}

func TestTellTurnDeliversOnceToAWorkerOnANeedsHumanTicket(t *testing.T) {
	t.Parallel()
	d, at, _ := askedWorker(t)

	res := d.Run("tell", "eng-1", "--turn", at, "Decision for your question: yes")

	require.NoError(t, res.Err)
	assert.Equal(t, "told ENG-1\n", res.Stdout)
	assert.Equal(t, []string{"Decision for your question: yes"}, d.Workers.Received("ENG-1"))
	assert.Equal(t, []string{"done"}, d.nudgeStates("ENG-1"))
}

func TestTellTurnRepeatedExitsZeroAndSendsNothing(t *testing.T) {
	t.Parallel()
	d, at, _ := askedWorker(t)
	require.NoError(t, d.Run("tell", "ENG-1", "--turn", at, "yes").Err)

	res := d.Run("tell", "ENG-1", "--turn", at, "yes")

	require.NoError(t, res.Err)
	assert.Equal(t, fmt.Sprintf("already told ENG-1 for turn %s\n", at), res.Stdout)
	assert.Equal(t, []string{"yes"}, d.Workers.Received("ENG-1"))
}

func TestTellTurnRepeatedAfterRestartingDispatchSendsNothing(t *testing.T) {
	t.Parallel()
	d, at, _ := askedWorker(t)
	require.NoError(t, d.Run("tell", "ENG-1", "--turn", at, "yes").Err)

	d.Restart()
	res := d.Run("tell", "ENG-1", "--turn", at, "yes")

	require.NoError(t, res.Err)
	assert.Equal(t, fmt.Sprintf("already told ENG-1 for turn %s\n", at), res.Stdout)
	assert.Equal(t, []string{"yes"}, d.Workers.Received("ENG-1"))
}

func TestTellTurnStaysToldAfterALaterTurn(t *testing.T) {
	t.Parallel()
	d, at, transcript := askedWorker(t)
	require.NoError(t, d.Run("tell", "ENG-1", "--turn", at, "yes").Err)
	d.Advance(time.Minute)
	d.Workers.Stop("ENG-1", stopTasks{Said: "next?", Transcript: transcript})

	res := d.Run("tell", "ENG-1", "--turn", at, "yes")

	require.NoError(t, res.Err)
	assert.Equal(t, fmt.Sprintf("already told ENG-1 for turn %s\n", at), res.Stdout)
	assert.Equal(t, []string{"yes"}, d.Workers.Received("ENG-1"))
}

func TestTellTurnStaysToldAfterTheSessionRelaunches(t *testing.T) {
	t.Parallel()
	d, at, _ := askedWorker(t)
	require.NoError(t, d.Run("tell", "ENG-1", "--turn", at, "yes").Err)

	d.Workers.SetSession("ENG-1", "restarted")
	d.Workers.SessionExits("ENG-1")
	d.Workers.SessionOpens("ENG-1")
	res := d.Run("tell", "ENG-1", "--turn", at, "yes")

	require.NoError(t, res.Err)
	assert.Equal(t, fmt.Sprintf("already told ENG-1 for turn %s\n", at), res.Stdout)
	assert.Equal(t, []string{"yes"}, d.Workers.Received("ENG-1"))
}

func TestTellTurnRefusesAToldTurnWhoseWorkerIsNowBlocked(t *testing.T) {
	t.Parallel()
	d, at, _ := askedWorker(t)
	require.NoError(t, d.Run("tell", "ENG-1", "--turn", at, "yes").Err)
	d.Workers.SetStatus("ENG-1", "blocked")

	res := d.Run("tell", "ENG-1", "--turn", at, "yes")

	require.ErrorContains(t, res.Err, "ENG-1's worker is blocked on a question")
	assert.Equal(t, []string{"yes"}, d.Workers.Received("ENG-1"))
}

func TestTellTurnRefusesATurnTheWorkerMovedPast(t *testing.T) {
	t.Parallel()
	const refusal = "ENG-1's worker has moved past that turn; nothing sent"
	t.Run("MessageReportedAfterTheTurn", func(t *testing.T) {
		d, at, _ := askedWorker(t)
		d.Advance(time.Second)
		require.NoError(t, d.Prompt(d.Workers.Tree("ENG-1"), "the user answered").Err)

		res := d.Run("tell", "ENG-1", "--turn", at, "yes")

		require.ErrorContains(t, res.Err, refusal)
		assert.Equal(t, 1, res.Code)
		assert.Empty(t, res.Stdout)
		assert.Empty(t, d.Workers.Received("ENG-1"))
	})
	t.Run("MessageOnlyTheTranscriptShows", func(t *testing.T) {
		d, at, transcript := askedWorker(t)
		// The hook report for the message was lost, so the session log holds nothing of it.
		d.take(transcript, time.Second)

		res := d.Run("tell", "ENG-1", "--turn", at, "yes")

		require.ErrorContains(t, res.Err, refusal)
		assert.Empty(t, d.Workers.Received("ENG-1"))
	})
	t.Run("ALaterTurnEnded", func(t *testing.T) {
		d, at, transcript := askedWorker(t)
		d.Advance(time.Minute)
		d.Workers.Stop("ENG-1", stopTasks{Said: "second?", Transcript: transcript})

		res := d.Run("tell", "ENG-1", "--turn", at, "yes")

		require.ErrorContains(t, res.Err, refusal)
		assert.Empty(t, d.Workers.Received("ENG-1"))
	})
}

func TestTellTurnRefusesAWorkerThatIsWorking(t *testing.T) {
	t.Parallel()
	const refusal = "ENG-1's worker is working; nothing sent"
	t.Run("HerdrReadsWorkingWithNothingRecorded", func(t *testing.T) {
		d, at, _ := askedWorker(t)
		d.Workers.SetStatus("ENG-1", "working")

		res := d.Run("tell", "ENG-1", "--turn", at, "yes")

		require.ErrorContains(t, res.Err, refusal)
		assert.Equal(t, 1, res.Code)
		assert.Empty(t, d.Workers.Received("ENG-1"))
	})
	t.Run("BackgroundTaskStillRunning", func(t *testing.T) {
		d := newDispatch(t, dispatchOptions{})
		d.Workers.Running("ENG-1", "idle")
		d.Workers.Stop("ENG-1", stopTasks{Running: 1, Said: "q?", Transcript: d.transcript("t1")})

		res := d.Run("tell", "ENG-1", "--turn", strconv.FormatInt(d.Clock.Now().UnixMilli(), 10), "yes")

		require.ErrorContains(t, res.Err, refusal)
		assert.Empty(t, d.Workers.Received("ENG-1"))
	})
	t.Run("TranscriptTheHarnessCannotRead", func(t *testing.T) {
		d := newDispatch(t, dispatchOptions{})
		d.Workers.Running("ENG-1", "idle")
		d.Workers.Stop("ENG-1", stopTasks{Said: "q?"})

		res := d.Run("tell", "ENG-1", "--turn", strconv.FormatInt(d.Clock.Now().UnixMilli(), 10), "yes")

		require.ErrorContains(t, res.Err, refusal)
		assert.Empty(t, d.Workers.Received("ENG-1"))
	})
}

func TestTellTurnRefusesWhenThereIsNoTurnToAnswer(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		prepare func(d *Dispatch)
		turn    string
		wantErr string
	}{
		{"NoWorker", func(d *Dispatch) {}, "5", "ENG-1 has no worker"},
		{"Popup", func(d *Dispatch) { d.Workers.Running("ENG-1", "blocked") }, "5", "ENG-1's worker is blocked on a question; answer it in its workspace"},
		{"NoTurnEndedYet", func(d *Dispatch) { d.Workers.Running("ENG-1", "idle") }, "5", "ENG-1's worker has no turn that ended at 5"},
		{"UnknownTime", func(d *Dispatch) {
			d.Workers.Running("ENG-1", "idle")
			d.Workers.Stop("ENG-1", stopTasks{Said: "q?", Transcript: d.transcript("t1")})
		}, "5", "ENG-1's worker has no turn that ended at 5"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			test.prepare(d)

			res := d.Run("tell", "ENG-1", "--turn", test.turn, "yes")

			require.ErrorContains(t, res.Err, test.wantErr)
			assert.Equal(t, 1, res.Code)
			assert.Empty(t, res.Stdout)
			assert.Empty(t, d.Workers.Received("ENG-1"))
		})
	}
}

func TestTellTurnRetriesAfterAFailedDelivery(t *testing.T) {
	t.Parallel()
	d, at, _ := askedWorker(t)
	// A follower notified about the Needs Human move would take an unaddressed failure.
	d.Workers.FailNextDeliveryTo(workerSession("ENG-1"), errors.New("broken pipe"))

	require.ErrorContains(t, d.Run("tell", "ENG-1", "--turn", at, "yes").Err, "nothing sent")

	res := d.Run("tell", "ENG-1", "--turn", at, "yes")

	require.NoError(t, res.Err)
	assert.Equal(t, "told ENG-1\n", res.Stdout)
	assert.Equal(t, []string{"yes"}, d.Workers.Received("ENG-1"))
}

func TestTellWithoutTurnStillSendsEveryTime(t *testing.T) {
	t.Parallel()
	d, _, _ := askedWorker(t)

	require.NoError(t, d.Run("tell", "ENG-1", "yes").Err)
	require.NoError(t, d.Run("tell", "ENG-1", "yes").Err)

	assert.Equal(t, []string{"yes", "yes"}, d.Workers.Received("ENG-1"))
}

func TestTellTurnRejectsBadArguments(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})

	for _, test := range []struct {
		args    []string
		wantErr string
	}{
		{[]string{"tell", "ENG-1", "--turn", "soon", "yes"}, "tell --turn takes the time the turn ended in milliseconds"},
		{[]string{"tell", "ENG-1", "--turn", "5"}, "tell takes a ticket"},
		{[]string{"tell", "ENG-1", "--when", "5", "yes"}, "tell takes a ticket"},
	} {
		t.Run(strings.Join(test.args[2:], "_"), func(t *testing.T) {
			res := d.Run(test.args...)

			require.ErrorContains(t, res.Err, test.wantErr)
			assert.Contains(t, res.Stderr, `dispatch tell <ticket> --turn <at> "<text>"`)
		})
	}
}

func TestTellTurnFailsWithExitCode3WhenNoDispatchRuns(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})

	res := d.RunWhileDown("tell", "ENG-1", "--turn", "5", "yes")

	require.ErrorContains(t, res.Err, "not running")
	assert.Equal(t, 3, res.Code)
}

func TestTellTurnSendsNothingWhenAnEarlierRunCrashedAfterRecordingTheAttempt(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	at := d.Clock.Now().UnixMilli()
	// A crash between the receipt and the send leaves an attempt with no outcome (unreachable from
	// the CLI: a test cannot kill dispatch halfway through a send).
	d.CrashedEarlierRun(earlierReceipt{
		Kind: "nudge", Subject: "ENG-1", Rule: "tell",
		Once: earlierOnce{"tell-turn", "ENG-1", at}, Started: at,
	})
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{Said: "q?", Transcript: d.transcript("t1")})

	res := d.Run("tell", "ENG-1", "--turn", strconv.FormatInt(at, 10), "yes")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "already told")
	assert.Empty(t, d.Workers.Received("ENG-1"))
}

func TestTellTurnNamesAMissingClosingMessage(t *testing.T) {
	t.Parallel()
	d, at, _ := askedWorker(t)
	// The Stop's closing message file is gone while its record stays (unreachable from the CLI: a
	// failed write or a hand-deleted file).
	for _, file := range closingFiles(t, d.state) {
		require.NoError(t, os.Remove(file))
	}

	res := d.Run("tell", "ENG-1", "--turn", at, "yes")

	require.ErrorContains(t, res.Err, fmt.Sprintf("ENG-1's worker has no closing message for turn %s; nothing sent", at))
	assert.Equal(t, 1, res.Code)
	assert.Empty(t, d.Workers.Received("ENG-1"))
}
