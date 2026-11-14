package dispatch_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// closingMessageLimit and busyExpiry are the 64 KiB a closing message is cut at and the 60 minutes
// a Stop that left a background task running still counts as a turn.
const (
	closingMessageLimit = 64 * 1024
	busyExpiry          = 60 * time.Minute
)

// transcript makes the file Claude's Stop hook names as transcript_path, holding one user entry.
func (d *Dispatch) transcript(name string) string {
	d.t.Helper()
	path := filepath.Join(d.dir, name+".jsonl")
	require.NoError(d.t, os.WriteFile(path, []byte(`{"type":"user","timestamp":"2026-01-01T00:00:00.000Z"}`+"\n"), 0o644))
	return path
}

// take appends a user entry stamped at the clock's time plus after, as Claude writes one when its
// session takes a message, whether or not a hook reported it.
func (d *Dispatch) take(path string, after time.Duration) {
	d.t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(d.t, err)
	defer func() { require.NoError(d.t, f.Close()) }()
	at := d.Clock.Now().Add(after).UTC().Format("2006-01-02T15:04:05.000Z")
	_, err = fmt.Fprintf(f, `{"type":"user","timestamp":%q}`+"\n", at)
	require.NoError(d.t, err)
}

func TestTurnPrintsTheTurnTheWorkerWaitsAtAndItsClosingMessage(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	const said = "Should we retry for 24 hours?\n\nI recommend yes.\n"
	d.Workers.Stop("ENG-1", stopTasks{Said: said, Transcript: d.transcript("t1")})
	at := d.Clock.Now().UnixMilli()

	res := d.Run("turn", "ENG-1")

	require.NoError(t, res.Err)
	assert.Equal(t, fmt.Sprintf("ENG-1 turn %d ended %s\n%s", at, d.Clock.Now().UTC().Format(time.RFC3339), said), res.Stdout)

	res = d.Run("turn", "eng-1", "--json")

	require.NoError(t, res.Err)
	var got struct {
		Ticket  string `json:"ticket"`
		At      int64  `json:"at"`
		Message string `json:"message"`
		Cut     bool   `json:"cut"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.Stdout), &got))
	assert.Equal(t, "ENG-1", got.Ticket)
	assert.Equal(t, at, got.At)
	assert.Equal(t, said, got.Message)
	assert.False(t, got.Cut)
}

func TestTurnCutsAClosingMessageAt64KiB(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "idle")
	// A 3-byte rune straddles the limit, so a cut at the limit would print half of it.
	long := strings.Repeat("a", closingMessageLimit-1) + "€" + "tail"
	d.Workers.Stop("ENG-1", stopTasks{Said: long, Transcript: d.transcript("t1")})

	res := d.Run("turn", "ENG-1", "--json")

	require.NoError(t, res.Err)
	var got struct {
		Message string `json:"message"`
		Cut     bool   `json:"cut"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.Stdout), &got))
	assert.Equal(t, strings.Repeat("a", closingMessageLimit-1), got.Message)
	assert.True(t, utf8.ValidString(got.Message))
	assert.True(t, got.Cut)
}

func TestTurnRefusesWhileTheWorkerIsInATurn(t *testing.T) {
	t.Parallel()
	const refusal = "ENG-1's worker is in a turn; nothing to read until it ends"
	t.Run("HerdrReadsWorking", func(t *testing.T) {
		d := newDispatch(t, dispatchOptions{})
		d.Workers.Running("ENG-1", "idle")
		d.Workers.Stop("ENG-1", stopTasks{Said: "q?", Transcript: d.transcript("t1")})
		d.Workers.SetStatus("ENG-1", "working")

		res := d.Run("turn", "ENG-1")

		require.ErrorContains(t, res.Err, refusal)
		assert.Equal(t, 1, res.Code)
		assert.Empty(t, res.Stdout)
	})
	t.Run("PlainMessageReportedAfterTheStopWhileHerdrReadsIdle", func(t *testing.T) {
		d := newDispatch(t, dispatchOptions{})
		d.Workers.Running("ENG-1", "idle")
		d.Workers.Stop("ENG-1", stopTasks{Said: "q?", Transcript: d.transcript("t1")})
		d.Advance(time.Second)
		require.NoError(t, d.Prompt(d.Workers.Tree("ENG-1"), "keep going").Err)

		require.ErrorContains(t, d.Run("turn", "ENG-1").Err, refusal)
	})
	t.Run("MessageOnlyTheTranscriptShows", func(t *testing.T) {
		d := newDispatch(t, dispatchOptions{})
		d.Workers.Running("ENG-1", "idle")
		path := d.transcript("t1")
		d.Workers.Stop("ENG-1", stopTasks{Said: "q?", Transcript: path})
		// The hook report for the message was lost, so the session log holds nothing of it.
		d.take(path, time.Second)

		res := d.Run("turn", "ENG-1")

		require.ErrorContains(t, res.Err, refusal)
		assert.Empty(t, res.Stdout)
	})
	t.Run("TranscriptTheHarnessCannotRead", func(t *testing.T) {
		d := newDispatch(t, dispatchOptions{})
		d.Workers.Running("ENG-1", "idle")
		d.Workers.Stop("ENG-1", stopTasks{Said: "q?", Transcript: filepath.Join(d.dir, "missing.jsonl")})

		require.ErrorContains(t, d.Run("turn", "ENG-1").Err, refusal)
	})
	t.Run("StopThatNamedNoTranscript", func(t *testing.T) {
		d := newDispatch(t, dispatchOptions{})
		d.Workers.Running("ENG-1", "idle")
		d.Workers.Stop("ENG-1", stopTasks{Said: "q?"})

		require.ErrorContains(t, d.Run("turn", "ENG-1").Err, refusal)
	})
	t.Run("BackgroundTaskRunningUntilItExpires", func(t *testing.T) {
		d := newDispatch(t, dispatchOptions{})
		d.Workers.Running("ENG-1", "idle")
		d.Workers.Stop("ENG-1", stopTasks{Running: 1, Said: "q?", Transcript: d.transcript("t1")})
		d.Advance(busyExpiry - time.Minute)

		require.ErrorContains(t, d.Run("turn", "ENG-1").Err, refusal)

		d.Advance(2 * time.Minute)

		res := d.Run("turn", "ENG-1")

		require.NoError(t, res.Err)
		assert.Contains(t, res.Stdout, "\nq?")
	})
}

func TestTurnPrintsAnIdleWorkersTurnWhileATranscriptMessageIsOlderThanIt(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "idle")
	path := d.transcript("t1")
	d.take(path, -time.Second)
	d.Workers.Stop("ENG-1", stopTasks{Said: "q?", Transcript: path})

	res := d.Run("turn", "ENG-1")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "\nq?")
}

func TestTurnRefusesWhenThereIsNothingToRead(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		prepare func(d *Dispatch)
		wantErr string
	}{
		{"NoWorker", func(d *Dispatch) {}, "ENG-1 has no worker"},
		{"Popup", func(d *Dispatch) { d.Workers.Running("ENG-1", "blocked") }, "ENG-1's worker is blocked on a question; answer it in its workspace"},
		{"NoTurnYet", func(d *Dispatch) { d.Workers.Running("ENG-1", "idle") }, "ENG-1's worker has ended no turn yet"},
		{"NoClosingMessage", func(d *Dispatch) {
			d.Workers.Running("ENG-1", "idle")
			d.Workers.Stop("ENG-1", stopTasks{Transcript: d.transcript("t1")})
		}, "ENG-1's turn has no closing message; read its workspace"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			test.prepare(d)

			res := d.Run("turn", "ENG-1")

			require.ErrorContains(t, res.Err, test.wantErr)
			assert.Equal(t, 1, res.Code)
			assert.Empty(t, res.Stdout)
		})
	}
}

func TestTurnNeverPrintsAnEarlierTurnsMessageForALaterTurn(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "idle")
	path := d.transcript("t1")
	d.Workers.Stop("ENG-1", stopTasks{Said: "first question?", Transcript: path})
	d.Advance(time.Minute)
	d.Workers.Stop("ENG-1", stopTasks{Transcript: path})

	require.ErrorContains(t, d.Run("turn", "ENG-1").Err, "turn has no closing message")
}

func TestTurnPrintsTheLatestTurnWhenTheWorkerEndedSeveral(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "idle")
	path := d.transcript("t1")
	d.Workers.Stop("ENG-1", stopTasks{Said: "first?", Transcript: path})
	d.Advance(time.Minute)
	d.Workers.Stop("ENG-1", stopTasks{Said: "second?", Transcript: path})

	res := d.Run("turn", "ENG-1")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "\nsecond?")
}

func TestTurnFailsWithExitCode3WhenNoDispatchRuns(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})

	res := d.RunWhileDown("turn", "ENG-1")

	require.ErrorContains(t, res.Err, "not running")
	assert.Equal(t, 3, res.Code)
}

func TestTurnTakesATicketAndOnlyTheJSONFlag(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})

	for _, args := range [][]string{{"turn"}, {"turn", "ENG-1", "ENG-2"}, {"turn", "ENG-1", "--text"}} {
		res := d.Run(args...)

		require.ErrorContains(t, res.Err, "turn takes a ticket")
		assert.Contains(t, res.Stderr, "dispatch turn <ticket> [--json]")
	}
}

func TestAReturnedTreeKeepsNoClosingMessage(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{Said: "q?", Transcript: d.transcript("t1")})
	require.Len(t, closingFiles(t, d.state), 1)
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	d.Tick()

	require.NotEmpty(t, d.Trees.Returned())
	assert.Empty(t, closingFiles(t, d.state))
}

func closingFiles(t *testing.T, stateDir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(stateDir, "sessions", "*", "*.closing"))
	require.NoError(t, err)
	return files
}
