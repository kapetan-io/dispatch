package dispatch_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
)

// carryOnMessage is what a launch types into a resumed session that already took its kickoff.
const carryOnMessage = "Dispatch restarted your terminal and resumed this conversation. Carry on from where you stopped."

// kickedText is the nth text a launch typed and saw running, oldest first.
func kickedText(d *Dispatch, n int) string {
	d.t.Helper()
	kicked := d.fakes().Multiplexer.Kicked()
	require.Greater(d.t, len(kicked), n)
	_, text, _ := strings.Cut(kicked[n], "|")
	return text
}

func TestARelaunchThatResumesTheSessionTypesTheCarryOnMessageInPlaceOfTheKickoff(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.StartBeforeTheEvents()
	require.NoError(t, d.launch("ENG-1", "br-eng-1").err)
	session := d.fakes().Multiplexer.Started()[0].Program.Session
	d.fakes().Multiplexer.LoseTerminals()
	d.Clock.Advance(time.Minute)

	out := d.launch("ENG-1", "br-eng-1")

	require.NoError(t, out.err)
	assert.Equal(t, "ok", out.result["status"])
	assert.Contains(t, out.result["detail"], ", resumed the worker's session without retyping the kickoff")
	require.Len(t, d.fakes().Multiplexer.Kicked(), 2)
	assert.Equal(t, carryOnMessage, kickedText(d, 1))
	snap, err := d.fakes().Multiplexer.Read(context.Background())
	require.NoError(t, err)
	require.Len(t, snap.Agents, 1)
	assert.Equal(t, session, snap.Agents[0].Session)
	// The record still names the kickoff the session took, so a second restart resumes the same way.
	kickoff, found := d.kickoffRecord("ENG-1")
	require.True(t, found)
	assert.Equal(t, "/dispatch:build ENG-1", kickoff.Text)
	assert.Equal(t, session, kickoff.Session)
	assert.Equal(t, d.Clock.Now().UnixMilli(), kickoff.At)
	assert.False(t, kickoff.Open)

	d.fakes().Multiplexer.LoseTerminals()
	d.Clock.Advance(time.Minute)
	require.NoError(t, d.launch("ENG-1", "br-eng-1").err)

	assert.Equal(t, carryOnMessage, kickedText(d, 2))
}

// A resumed session whose kickoff was typed but never confirmed may not hold it, so it is typed
// again. A Stop of that session after the kickoff shows it ran, so the session gets the message.
func TestARelaunchThatResumesASessionWhoseKickoffIsStillOpen(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		stopped bool
		want    string
	}{
		{"TypesTheKickoffAgain", false, "/dispatch:build ENG-1"},
		{"TypesTheCarryOnMessageAfterAStopOfThatSession", true, carryOnMessage},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
			tree := d.Trees.Pooled()
			d.StartBeforeTheEvents()
			// The kickoff never shows as running and the workspace will not close, so the session
			// stays with its kickoff record open.
			d.fakes().Multiplexer.Fail(herdrtest.OpKickoff, herdr.ErrKickoffUnconfirmed)
			d.fakes().Multiplexer.Fail(herdrtest.OpCloseWorkspace, fmt.Errorf("herdr workspace close failed"))
			require.Error(t, d.launch("ENG-1", "br-eng-1").err)
			session := d.fakes().Multiplexer.Started()[0].Program.Session
			kickoff, found := d.kickoffRecord("ENG-1")
			require.True(t, found)
			require.True(t, kickoff.Open)
			require.Equal(t, session, kickoff.Session)
			d.Clock.Advance(time.Minute)
			if test.stopped {
				input := fmt.Sprintf(`{"session_id":%q,"cwd":%q,"background_tasks":[]}`, session, tree)
				require.NoError(t, d.Hook("record-stop", input).Err)
			}
			d.fakes().Multiplexer.Fail(herdrtest.OpKickoff, nil)
			d.fakes().Multiplexer.Fail(herdrtest.OpCloseWorkspace, nil)
			d.fakes().Multiplexer.LoseTerminals()
			d.Clock.Advance(time.Minute)

			out := d.launch("ENG-1", "br-eng-1")

			require.NoError(t, out.err)
			require.Len(t, d.fakes().Multiplexer.Started(), 2)
			assert.Equal(t, test.want, kickedText(d, 0))
		})
	}
}

func TestARelaunchThatStartsANewSessionTypesTheKickoff(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.StartBeforeTheEvents()
	require.NoError(t, d.launch("ENG-1", "br-eng-1").err)
	pane, _, _ := strings.Cut(d.fakes().Multiplexer.Kicked()[0], "|")
	// A Claude that exits is not resumed, so the relaunch starts a new session.
	d.fakes().Multiplexer.ExitAgent(pane)
	d.Clock.Advance(time.Minute)

	out := d.launch("ENG-1", "br-eng-1")

	require.NoError(t, out.err)
	assert.NotContains(t, out.result["detail"], "resumed")
	assert.Equal(t, "/dispatch:build ENG-1", kickedText(d, 1))
	started := d.fakes().Multiplexer.Started()
	require.Len(t, started, 2)
	kickoff, found := d.kickoffRecord("ENG-1")
	require.True(t, found)
	assert.Equal(t, started[1].Program.Session, kickoff.Session)
}

// launchWithKickoff queues a launch for each title an In Progress ticket has had, with the kickoff
// "/dispatch:build <id> <title>" for a title other than "first", the way the restack relaunch
// sends a kickoff of its own.
const launchWithKickoff = `
whenever({ name: "launch", each: "ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.worker.queueLaunch(t, {
    once: ["launch", t.id, t.title],
    kickoff: t.title === "first" ? undefined : "/dispatch:build " + t.id + " " + t.title,
  }) });`

func TestARelaunchThatResumesTheSessionTypesAKickoffItWasNeverSent(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(launchWithKickoff)})
	d.Trees.Pooled()
	d.StartBeforeTheEvents()
	require.NoError(t, d.launch("ENG-1", "br-eng-1", "--title", "first").err)
	session := d.fakes().Multiplexer.Started()[0].Program.Session
	d.fakes().Multiplexer.LoseTerminals()
	d.Clock.Advance(time.Minute)

	out := d.launch("ENG-1", "br-eng-1", "--title", "restack")

	require.NoError(t, out.err)
	assert.NotContains(t, out.result["detail"], "resumed")
	assert.Equal(t, "/dispatch:build ENG-1 restack", kickedText(d, 1))
	kickoff, found := d.kickoffRecord("ENG-1")
	require.True(t, found)
	assert.Equal(t, "/dispatch:build ENG-1 restack", kickoff.Text)
	assert.Equal(t, session, kickoff.Session)
}

func TestAResumedWorkerThatNeverShowsTheCarryOnMessageRunningIsClosed(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.StartBeforeTheEvents()
	require.NoError(t, d.launch("ENG-1", "br-eng-1").err)
	d.fakes().Multiplexer.LoseTerminals()
	d.fakes().Multiplexer.Fail(herdrtest.OpKickoff, herdr.ErrKickoffUnconfirmed)
	d.Clock.Advance(time.Minute)

	out := d.launch("ENG-1", "br-eng-1")

	assert.Equal(t, "injection_unconfirmed", out.result["status"])
	assert.Equal(t, []string{"w2"}, d.fakes().Multiplexer.ClosedWorkspaces())
}

func TestARelaunchThatResumesASessionWithNoKickoffRecordTypesTheKickoff(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	d.StartBeforeTheEvents()
	require.NoError(t, d.launch("ENG-1", "br-eng-1").err)
	session := d.fakes().Multiplexer.Started()[0].Program.Session
	d.forgetKickoff(tree)
	d.fakes().Multiplexer.LoseTerminals()
	d.Clock.Advance(time.Minute)

	out := d.launch("ENG-1", "br-eng-1")

	require.NoError(t, out.err)
	assert.NotContains(t, out.result["detail"], "resumed")
	assert.Equal(t, "/dispatch:build ENG-1", kickedText(d, 1))
	snap, err := d.fakes().Multiplexer.Read(context.Background())
	require.NoError(t, err)
	require.Len(t, snap.Agents, 1)
	assert.Equal(t, session, snap.Agents[0].Session)
}

// The resumed session's Stops from before the relaunch say nothing of the carry-on message, so only
// a Stop of that session after it confirms the worker; until then close-unconfirmed-worker
// applies to it as to a new session.
func TestAResumedWorkerWhoseCarryOnMessageIsUnconfirmed(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		stopped bool
		closed  []string
	}{
		{"IsClosedDespiteAnEarlierStopOfItsSession", false, []string{"w2"}},
		{"IsKeptOnceItsSessionStops", true, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
			tree := d.Trees.Pooled()
			d.StartBeforeTheEvents()
			require.NoError(t, d.launch("ENG-1", "br-eng-1").err)
			session := d.fakes().Multiplexer.Started()[0].Program.Session
			stop := fmt.Sprintf(`{"session_id":%q,"cwd":%q,"background_tasks":[]}`, session, tree)
			require.NoError(t, d.Hook("record-stop", stop).Err)
			d.fakes().Multiplexer.LoseTerminals()
			// The carry-on message never shows as running and the workspace will not close, so the
			// resumed session stays with its kickoff record open.
			d.fakes().Multiplexer.Fail(herdrtest.OpKickoff, herdr.ErrKickoffUnconfirmed)
			d.fakes().Multiplexer.Fail(herdrtest.OpCloseWorkspace, fmt.Errorf("herdr workspace close failed"))
			d.Clock.Advance(time.Minute)
			out := d.launch("ENG-1", "br-eng-1")
			require.Equal(t, "injection_unconfirmed", out.result["status"])
			attempts := d.tools.Calls("herdr", "kickoff")
			require.Len(t, attempts, 2)
			_, typed, _ := strings.Cut(attempts[1], "|")
			require.Equal(t, carryOnMessage, typed)
			kickoff, found := d.kickoffRecord("ENG-1")
			require.True(t, found)
			require.True(t, kickoff.Open)
			require.Equal(t, session, kickoff.Session)
			d.fakes().Multiplexer.Fail(herdrtest.OpKickoff, nil)
			d.fakes().Multiplexer.Fail(herdrtest.OpCloseWorkspace, nil)
			if test.stopped {
				snap, err := d.fakes().Multiplexer.Read(context.Background())
				require.NoError(t, err)
				require.Len(t, snap.Agents, 1)
				// The restart ended the hooks' pane too, so the Stop runs in the resumed worker's.
				restore := d.FromPane(snap.Agents[0].PaneID)
				d.Clock.Advance(time.Second)
				require.NoError(t, d.Hook("record-stop", stop).Err)
				restore()
			}

			d.Advance(10 * time.Minute)
			d.Tick()

			assert.Equal(t, test.closed, d.fakes().Multiplexer.ClosedWorkspaces())
		})
	}
}

// A resumed launch whose carry-on message is never confirmed leaves an open kickoff record dated
// after every Stop of the session. The session still took the kickoff, so the next resume of it
// types the carry-on message again.
func TestASecondResumeAfterAnUnconfirmedCarryOnMessageTypesItAgain(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.StartBeforeTheEvents()
	require.NoError(t, d.launch("ENG-1", "br-eng-1").err)
	session := d.fakes().Multiplexer.Started()[0].Program.Session
	d.fakes().Multiplexer.LoseTerminals()
	d.fakes().Multiplexer.Fail(herdrtest.OpKickoff, herdr.ErrKickoffUnconfirmed)
	d.fakes().Multiplexer.Fail(herdrtest.OpCloseWorkspace, fmt.Errorf("herdr workspace close failed"))
	d.Clock.Advance(time.Minute)
	require.Equal(t, "injection_unconfirmed", d.launch("ENG-1", "br-eng-1").result["status"])
	kickoff, found := d.kickoffRecord("ENG-1")
	require.True(t, found)
	require.True(t, kickoff.Open)
	d.fakes().Multiplexer.Fail(herdrtest.OpKickoff, nil)
	d.fakes().Multiplexer.Fail(herdrtest.OpCloseWorkspace, nil)
	d.fakes().Multiplexer.LoseTerminals()
	d.Clock.Advance(time.Minute)

	out := d.launch("ENG-1", "br-eng-1")

	require.NoError(t, out.err)
	assert.Equal(t, carryOnMessage, kickedText(d, 1))
	snap, err := d.fakes().Multiplexer.Read(context.Background())
	require.NoError(t, err)
	require.Len(t, snap.Agents, 1)
	assert.Equal(t, session, snap.Agents[0].Session)
}

// A launch whose harness fails to start puts back the record it found, which still says the
// resumed session ran the kickoff, so the resume after it types the carry-on message.
func TestAResumeAfterAFailedStartKeepsTheKickoffTaken(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.StartBeforeTheEvents()
	require.NoError(t, d.launch("ENG-1", "br-eng-1").err)
	d.fakes().Multiplexer.LoseTerminals()
	d.fakes().Multiplexer.Fail(herdrtest.OpKickoff, herdr.ErrKickoffUnconfirmed)
	d.fakes().Multiplexer.Fail(herdrtest.OpCloseWorkspace, fmt.Errorf("herdr workspace close failed"))
	d.Clock.Advance(time.Minute)
	require.Equal(t, "injection_unconfirmed", d.launch("ENG-1", "br-eng-1").result["status"])
	d.fakes().Multiplexer.Fail(herdrtest.OpKickoff, nil)
	d.fakes().Multiplexer.LoseTerminals()
	d.fakes().Multiplexer.Fail(herdrtest.OpStartAgent, fmt.Errorf("herdr agent start failed"))
	d.Clock.Advance(time.Minute)
	require.Equal(t, "agent_start_failed", d.launch("ENG-1", "br-eng-1").result["status"])
	kickoff, found := d.kickoffRecord("ENG-1")
	require.True(t, found)
	require.True(t, kickoff.Open)
	require.True(t, kickoff.Taken)
	d.fakes().Multiplexer.Fail(herdrtest.OpStartAgent, nil)
	d.fakes().Multiplexer.Fail(herdrtest.OpCloseWorkspace, nil)
	d.Clock.Advance(time.Minute)

	out := d.launch("ENG-1", "br-eng-1")

	require.NoError(t, out.err)
	assert.Equal(t, carryOnMessage, kickedText(d, 1))
}
