package dispatch_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
)

const (
	rexSession     = "session:s1"
	rexBlock       = "block:b1"
	userPane       = rexSession + "/" + rexBlock
	oldInstance    = "aaaaaaaa"
	newInstance    = "bbbbbbbb"
	claudeSession  = "11111111-0000-4000-8000-000000000001"
	anotherSession = "22222222-0000-4000-8000-000000000002"
)

func sessionStart(session, cwd string) string {
	return `{"session_id":"` + session + `","cwd":"` + cwd + `","hook_event_name":"SessionStart","source":"startup"}`
}

func sessionEnd(session, reason string) string {
	return `{"session_id":"` + session + `","hook_event_name":"SessionEnd","reason":"` + reason + `"}`
}

// paneHook runs a pane record hook in the user's Rex pane, against the Rex server given.
func paneHook(d *Dispatch, rex *herdrtest.RexPanes, command, stdin string) Result {
	d.t.Helper()
	return d.hook(dispatch.Options{Stdin: strings.NewReader(stdin), RexPanes: rex}, command)
}

func restore(d *Dispatch, rex *herdrtest.RexPanes, args ...string) Result {
	d.t.Helper()
	return d.RunWith(dispatch.Options{RexPanes: rex}, append([]string{"rex", "restore"}, args...)...)
}

// paneRecord is the pane record file as it is on disk.
type paneRecord struct {
	Session  string    `json:"session"`
	CWD      string    `json:"cwd"`
	Instance string    `json:"instance"`
	At       time.Time `json:"at"`
}

func recordPath(d *Dispatch, pane string) string {
	return filepath.Join(d.state, "panes", filepath.FromSlash(pane)+".json")
}

// recorded writes the pane record a hook left before the Rex restart.
func recorded(d *Dispatch, pane, session, cwd string) {
	d.t.Helper()
	b, err := json.Marshal(paneRecord{Session: session, CWD: cwd, Instance: oldInstance})
	require.NoError(d.t, err)
	require.NoError(d.t, os.MkdirAll(filepath.Dir(recordPath(d, pane)), 0o755))
	require.NoError(d.t, os.WriteFile(recordPath(d, pane), b, 0o600))
}

func readRecord(d *Dispatch, pane string) (paneRecord, bool) {
	d.t.Helper()
	b, err := os.ReadFile(recordPath(d, pane))
	if err != nil {
		return paneRecord{}, false
	}
	var record paneRecord
	require.NoError(d.t, json.Unmarshal(b, &record))
	return record, true
}

func TestRecordStartWritesThePaneRecordOfTheUsersRexPane(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	defer d.FromRexBlock(rexSession, rexBlock)()

	res := paneHook(d, herdrtest.NewRexPanes(oldInstance), "record-start", sessionStart(claudeSession, "/work"))

	require.NoError(t, res.Err)
	assert.Empty(t, res.Stdout)
	assert.Empty(t, res.Stderr)
	record, ok := readRecord(d, userPane)
	require.True(t, ok)
	assert.Equal(t, claudeSession, record.Session)
	assert.Equal(t, "/work", record.CWD)
	assert.Equal(t, oldInstance, record.Instance)
	assert.Equal(t, d.Clock.Now().UTC(), record.At)
	assert.FileExists(t, filepath.Join(d.state, "panes", rexSession, rexBlock+".json"))
}

func TestRecordStartWritesNoPaneRecord(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		setup func(d *Dispatch, rex *herdrtest.RexPanes)
		stdin string
	}{
		{"OutsideRex", func(*Dispatch, *herdrtest.RexPanes) {}, sessionStart(claudeSession, "/work")},
		{"InAWorker", func(d *Dispatch, _ *herdrtest.RexPanes) {
			d.env = d.env.With("REX_SESSION", rexSession).With("REX_BLOCK", rexBlock).With("AUTONOMOUS_WORKER", "1")
		}, sessionStart(claudeSession, "/work")},
		{"WhenRexIsNotRunning", func(d *Dispatch, rex *herdrtest.RexPanes) {
			d.env = d.env.With("REX_SESSION", rexSession).With("REX_BLOCK", rexBlock)
			rex.Down()
		}, sessionStart(claudeSession, "/work")},
		{"FromInputThatNamesNoSession", func(d *Dispatch, _ *herdrtest.RexPanes) {
			d.env = d.env.With("REX_SESSION", rexSession).With("REX_BLOCK", rexBlock)
		}, `{"cwd":"/work"}`},
		{"FromInputThatIsNotJSON", func(d *Dispatch, _ *herdrtest.RexPanes) {
			d.env = d.env.With("REX_SESSION", rexSession).With("REX_BLOCK", rexBlock)
		}, `not json`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{})
			rex := herdrtest.NewRexPanes(oldInstance)
			test.setup(d, rex)

			res := paneHook(d, rex, "record-start", test.stdin)

			require.NoError(t, res.Err)
			assert.Empty(t, res.Stderr)
			assert.NoDirExists(t, filepath.Join(d.state, "panes"))
		})
	}
}

func TestRecordStartStampsTheRunningRexServerOnASessionResumedAfterARestart(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	defer d.FromRexBlock(rexSession, rexBlock)()
	recorded(d, userPane, claudeSession, "/work")

	require.NoError(t, paneHook(d, herdrtest.NewRexPanes(newInstance), "record-start", sessionStart(claudeSession, "/work")).Err)

	record, ok := readRecord(d, userPane)
	require.True(t, ok)
	assert.Equal(t, newInstance, record.Instance)
}

func TestRecordStopWritesThePaneRecordOfASessionThatHasNone(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	defer d.FromRexBlock(rexSession, rexBlock)()

	require.NoError(t, paneHook(d, herdrtest.NewRexPanes(oldInstance), "record-stop", sessionStart(claudeSession, "/work")).Err)

	record, ok := readRecord(d, userPane)
	require.True(t, ok)
	assert.Equal(t, claudeSession, record.Session)
	assert.Equal(t, "/work", record.CWD)
	assert.Equal(t, oldInstance, record.Instance)
}

func TestRecordStopReplacesAPaneRecordThatNamesAnotherSession(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	defer d.FromRexBlock(rexSession, rexBlock)()
	recorded(d, userPane, anotherSession, "/elsewhere")

	require.NoError(t, paneHook(d, herdrtest.NewRexPanes(newInstance), "record-stop", sessionStart(claudeSession, "/work")).Err)

	record, ok := readRecord(d, userPane)
	require.True(t, ok)
	assert.Equal(t, claudeSession, record.Session)
	assert.Equal(t, "/work", record.CWD)
	assert.Equal(t, newInstance, record.Instance)
}

func TestRecordStopLeavesAPaneRecordThatNamesItsSession(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	defer d.FromRexBlock(rexSession, rexBlock)()
	recorded(d, userPane, claudeSession, "/work")
	rex := herdrtest.NewRexPanes(newInstance)
	// A turn end that asked Rex would fail here, so the record staying shows it did not ask.
	rex.Down()

	require.NoError(t, paneHook(d, rex, "record-stop", sessionStart(claudeSession, "/work")).Err)

	record, ok := readRecord(d, userPane)
	require.True(t, ok)
	assert.Equal(t, oldInstance, record.Instance)
}

func TestRecordEnd(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		ending string
		reason string
		kept   bool
	}{
		{"RemovesThePaneRecordWhenTheUserLeavesThePrompt", claudeSession, "prompt_input_exit", false},
		{"RemovesThePaneRecordWhenTheUserLogsOut", claudeSession, "logout", false},
		{"KeepsThePaneRecordWhenTheSessionEndsForAnotherReason", claudeSession, "other", true},
		{"KeepsThePaneRecordOnAClear", claudeSession, "clear", true},
		{"KeepsThePaneRecordOfANewerSession", anotherSession, "prompt_input_exit", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{})
			defer d.FromRexBlock(rexSession, rexBlock)()
			recorded(d, userPane, claudeSession, "/work")

			res := paneHook(d, herdrtest.NewRexPanes(oldInstance), "record-end", sessionEnd(test.ending, test.reason))

			require.NoError(t, res.Err)
			assert.Empty(t, res.Stderr)
			_, ok := readRecord(d, userPane)
			assert.Equal(t, test.kept, ok)
		})
	}
}

func TestRecordEndDoesNothingOutsideRex(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	recorded(d, userPane, claudeSession, "/work")

	require.NoError(t, paneHook(d, herdrtest.NewRexPanes(oldInstance), "record-end", sessionEnd(claudeSession, "prompt_input_exit")).Err)

	_, ok := readRecord(d, userPane)
	assert.True(t, ok)
}

func TestRexRestoreResumesARecordedSessionInItsPane(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	recorded(d, userPane, claudeSession, "/Users/me/My Notes")
	rex := herdrtest.NewRexPanes(newInstance)
	rex.Add(herdr.RexPane{ID: userPane, Session: "claude", Window: "Dr Claude", AtPrompt: true})

	res := restore(d, rex)

	require.NoError(t, res.Err)
	assert.Equal(t, []string{"cd '/Users/me/My Notes' && claude --resume " + claudeSession}, rex.Typed(userPane))
	assert.Equal(t, "resumed       claude / Dr Claude  "+claudeSession+"\n", res.Stdout)
}

func TestRexRestoreTypesOnceOverTwoRuns(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	recorded(d, userPane, claudeSession, "/work")
	rex := herdrtest.NewRexPanes(newInstance)
	rex.Add(herdr.RexPane{ID: userPane, Session: "claude", Window: "Dr Claude", AtPrompt: true})
	require.NoError(t, restore(d, rex).Err)

	res := restore(d, rex)

	require.NoError(t, res.Err)
	assert.Len(t, rex.Typed(userPane), 1)
	assert.Equal(t, "busy          claude / Dr Claude\n", res.Stdout)
}

func TestRexRestoreTypesNothingIntoAResumedPaneWhoseSessionExited(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	defer d.FromRexBlock(rexSession, rexBlock)()
	recorded(d, userPane, claudeSession, "/work")
	rex := herdrtest.NewRexPanes(newInstance)
	rex.Add(herdr.RexPane{ID: userPane, Session: "claude", Window: "Dr Claude", AtPrompt: true})
	require.NoError(t, restore(d, rex).Err)
	// The resumed session's SessionStart restamps the record, and then the session ends without a quit.
	require.NoError(t, paneHook(d, rex, "record-start", sessionStart(claudeSession, "/work")).Err)
	rex.Restart(newInstance)

	res := restore(d, rex)

	require.NoError(t, res.Err)
	assert.Len(t, rex.Typed(userPane), 1)
	assert.Equal(t, "idle          claude / Dr Claude  run: cd '/work' && claude --resume "+claudeSession+"\n", res.Stdout)
}

func TestRexRestoreLeavesAPaneAlone(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		pane     herdr.RexPane
		instance string
		cwd      string
		want     string
	}{
		{
			name:     "ThatRunsAProgram",
			pane:     herdr.RexPane{ID: userPane, Session: "claude", Window: "Dr Claude"},
			instance: newInstance, cwd: "/work",
			want: "busy          claude / Dr Claude\n",
		},
		{
			name:     "ThatHoldsAWorker",
			pane:     herdr.RexPane{ID: userPane, Session: "dispatch", Window: "ENG-1 title", AtPrompt: true, Worker: true},
			instance: newInstance, cwd: "/work",
			want: "worker        dispatch / ENG-1 title\n",
		},
		{
			name:     "WhoseRecordIsFromTheRunningRexServer",
			pane:     herdr.RexPane{ID: userPane, Session: "claude", Window: "Dr Claude", AtPrompt: true},
			instance: oldInstance, cwd: "/work",
			want: "idle          claude / Dr Claude  run: cd '/work' && claude --resume " + claudeSession + "\n",
		},
		{
			name:     "WhoseDirectoryHoldsAQuote",
			pane:     herdr.RexPane{ID: userPane, Session: "claude", Window: "Dr Claude", AtPrompt: true},
			instance: newInstance, cwd: "/it's",
			want: "skipped       claude / Dr Claude  run: cd '/it's' && claude --resume " + claudeSession + "\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{})
			recorded(d, userPane, claudeSession, test.cwd)
			rex := herdrtest.NewRexPanes(test.instance)
			rex.Add(test.pane)

			res := restore(d, rex)

			require.NoError(t, res.Err)
			assert.Empty(t, rex.Typed(userPane))
			assert.Equal(t, test.want, res.Stdout)
			_, ok := readRecord(d, userPane)
			assert.True(t, ok)
		})
	}
}

func TestRexRestoreNeverTouchesAPaneWithNoRecord(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	rex := herdrtest.NewRexPanes(newInstance)
	rex.Add(herdr.RexPane{ID: userPane, Session: "claude", Window: "Dr Claude", AtPrompt: true})

	res := restore(d, rex)

	require.NoError(t, res.Err)
	assert.Empty(t, rex.Typed(userPane))
	assert.Empty(t, res.Stdout)
}

func TestRexRestorePrintsTheLineForAPaneRexDidNotBringBackAndDropsItsRecord(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	recorded(d, userPane, claudeSession, "/work")

	res := restore(d, herdrtest.NewRexPanes(newInstance))

	require.NoError(t, res.Err)
	assert.Equal(t, "gone          "+userPane+"  run: cd '/work' && claude --resume "+claudeSession+"\n", res.Stdout)
	_, ok := readRecord(d, userPane)
	assert.False(t, ok)
}

func TestRexRestoreDryRunPrintsThePlanAndChangesNothing(t *testing.T) {
	t.Parallel()
	const gonePane = "session:s2/block:b2"
	d := newDispatch(t, dispatchOptions{})
	recorded(d, userPane, claudeSession, "/work")
	recorded(d, gonePane, anotherSession, "/elsewhere")
	rex := herdrtest.NewRexPanes(newInstance)
	rex.Add(herdr.RexPane{ID: userPane, Session: "claude", Window: "Dr Claude", AtPrompt: true})

	res := restore(d, rex, "--dry-run")

	require.NoError(t, res.Err)
	assert.Empty(t, rex.Typed(userPane))
	assert.Equal(t, "would resume  claude / Dr Claude  "+claudeSession+"\n"+
		"gone          "+gonePane+"  run: cd '/elsewhere' && claude --resume "+anotherSession+"\n", res.Stdout)
	_, ok := readRecord(d, gonePane)
	assert.True(t, ok)
}

func TestRexRestoreTypesNothingWhenRexIsNotRunning(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	recorded(d, userPane, claudeSession, "/work")
	rex := herdrtest.NewRexPanes(newInstance)
	rex.Add(herdr.RexPane{ID: userPane, Session: "claude", Window: "Dr Claude", AtPrompt: true})
	rex.Down()

	res := restore(d, rex)

	require.ErrorIs(t, res.Err, herdr.ErrRexNotRunning)
	assert.Equal(t, 1, res.Code)
	assert.Empty(t, rex.Typed(userPane))
	_, ok := readRecord(d, userPane)
	assert.True(t, ok)
}

func TestRexRestoreFailsWhenAResumedSessionDoesNotStart(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	recorded(d, userPane, claudeSession, "/work")
	rex := herdrtest.NewRexPanes(newInstance)
	rex.Add(herdr.RexPane{ID: userPane, Session: "claude", Window: "Dr Claude", AtPrompt: true})
	rex.Stuck(userPane)

	restored := later(func() Result { return restore(d, rex) })
	require.Eventually(t, func() bool { return len(rex.Typed(userPane)) == 1 }, 20*time.Second, time.Millisecond)
	assert.Equal(t, 15*time.Second, d.Deadline("rex-restore"))
	d.Expire("rex-restore")
	res := restored()

	require.ErrorContains(t, res.Err, "not resumed")
	assert.Equal(t, 1, res.Code)
	assert.Equal(t, "not started   claude / Dr Claude  "+claudeSession+"\n", res.Stdout)
}

func TestRexRestoreSkipsAFileThatIsNotAPaneRecordAndLeavesIt(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := filepath.Join(d.state, "panes", rexSession, rexBlock+".json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))
	rex := herdrtest.NewRexPanes(newInstance)
	rex.Add(herdr.RexPane{ID: userPane, Session: "claude", Window: "Dr Claude", AtPrompt: true})

	res := restore(d, rex)

	require.NoError(t, res.Err)
	assert.Empty(t, rex.Typed(userPane))
	assert.Contains(t, res.Stdout, "skipped       "+path+"  unreadable: ")
	assert.FileExists(t, path)
}

func TestRexRefusesArgumentsItDoesNotKnow(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		args []string
	}{
		{"NoSubcommand", []string{"rex"}},
		{"UnknownSubcommand", []string{"rex", "snapshot"}},
		{"UnknownFlag", []string{"rex", "restore", "--force"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{})

			res := d.Run(test.args...)

			require.ErrorContains(t, res.Err, "rex takes restore, or restore --dry-run")
			assert.Contains(t, res.Stderr, "dispatch rex restore [--dry-run]")
		})
	}
}

func TestPaneRecordHooksRunOnSessionStartAndSessionEndAndNeverFailTheSession(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("hooks/hooks.json")
	require.NoError(t, err)

	assert.Contains(t, string(body), `"SessionEnd"`)
	assert.Contains(t, string(body), "dispatch record-start 2>/dev/null; exit 0")
	assert.Contains(t, string(body), "dispatch record-end 2>/dev/null; exit 0")
}
