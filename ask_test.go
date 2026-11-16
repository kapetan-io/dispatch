package dispatch_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
)

const (
	askedLine   = "asked. Say why in chat and end your turn.\n"
	askedNotice = "ENG-1's worker asked a question in its workspace: ticket ENG-1"
)

// Ask runs `dispatch ask` as the ticket's worker session, in its tree, as Claude Code runs a
// command for it. A loop starts first when none runs.
func (w *Workers) Ask(ticket string) Result {
	w.d.t.Helper()
	return w.d.askAs(workerSession(ticket), w.d.worktreeOf(ticket), true)
}

// AskWhileDown is Ask with no dispatch running.
func (w *Workers) AskWhileDown(ticket string) Result {
	w.d.t.Helper()
	return w.d.askAs(workerSession(ticket), w.d.worktreeOf(ticket), false)
}

func (d *Dispatch) askAs(session, dir string, running bool, args ...string) Result {
	d.t.Helper()
	saved := d.env
	d.env = d.env.With("CLAUDE_CODE_SESSION_ID", session)
	defer func() { d.env = saved }()
	if !running {
		return d.run(d.options(dispatch.Options{Dir: dir}), append([]string{"ask"}, args...)...)
	}
	return d.RunIn(dir, append([]string{"ask"}, args...)...)
}

// Say has the person type a message that is no command into the ticket's worker session, and
// returns once the loop has logged it.
func (w *Workers) Say(ticket, text string) {
	w.d.t.Helper()
	before := w.messagesTaken(ticket)
	w.Type(ticket, text)
	require.Eventually(w.d.t, func() bool { return w.messagesTaken(ticket) > before }, 20*time.Second, 5*time.Millisecond)
}

func TestAskShowsRedAndNotifiesFollowersOnceWithoutMovingTheTicket(t *testing.T) {
	t.Parallel()
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_SESSION_ID": socket}})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	require.NoError(t, d.Run("watch", "ENG-1").Err)

	res := d.Workers.Ask("ENG-1")
	require.NoError(t, res.Err)
	assert.Equal(t, askedLine, res.Stdout)
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Workers.SetStatus("ENG-1", "idle")
	d.Tick()
	d.Advance(5 * time.Minute)
	d.Tick()

	assert.Equal(t, "w-ENG-1|🔴 ENG-1", d.Workers.lastRename(t))
	assert.Contains(t, next(t, lines), "dispatch event (worker asked a question): "+askedNotice)
	assert.Equal(t, []string{askedNotice}, d.Followed.Told())
	assert.Empty(t, d.Linear.Moves())
	assert.Empty(t, lines)
}

func TestAskShowsRedAndNotifiesFollowersOfALocalTicket(t *testing.T) {
	t.Parallel()
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Config: localOn, Env: map[string]string{"CLAUDE_CODE_SESSION_ID": socket}})
	d.StartBeforeTheEvents()
	d.fileLocal("In Progress")
	d.Workers.Running("LOC-1", "working")
	require.NoError(t, d.Run("watch", "LOC-1").Err)

	require.NoError(t, d.Workers.Ask("LOC-1").Err)
	d.Workers.Stop("LOC-1", stopTasks{})
	d.Workers.SetStatus("LOC-1", "idle")
	d.Tick()

	assert.Equal(t, "w-LOC-1|🔴 LOC-1", d.Workers.lastRename(t))
	assert.Contains(t, next(t, lines), "dispatch event (worker asked a question): LOC-1's worker asked a question in its workspace: work")
	assert.Equal(t, "In Progress", d.statusOf("LOC-1"))
}

func TestAskShowsRedOverAReadyPR(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))

	require.NoError(t, d.Workers.Ask("ENG-1").Err)
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Workers.SetStatus("ENG-1", "idle")
	d.Tick()

	assert.Equal(t, "w-ENG-1|🔴 ENG-1", d.Workers.lastRename(t))
	assert.Equal(t, []string{askedNotice}, d.Followed.Told())
}

func TestAskingWorkerKeepsItsPlaceUnderMaxInProgress(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 1\n"})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	require.NoError(t, d.Workers.Ask("ENG-1").Err)
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Workers.SetStatus("ENG-1", "idle")
	d.Tick()
	require.Equal(t, "w-ENG-1|🔴 ENG-1", d.Workers.lastRename(t))

	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	out := d.Tick()

	assert.Contains(t, out, "ENG-2 queued: launch, 1 of 1 in progress")
	assert.Empty(t, d.Workers.Launched())
}

func TestAWorkerThatStopsWithoutAskingIsIdleAndOneThatAskedOnlyAsks(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		ask   bool
		label string
		told  []string
	}{
		{name: "WithoutAsk", label: "w-ENG-1|💤 ENG-1", told: []string{idleNotice}},
		{name: "WithAsk", ask: true, label: "w-ENG-1|🔴 ENG-1", told: []string{askedNotice}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
			d.Workers.Running("ENG-1", "working")
			if test.ask {
				require.NoError(t, d.Workers.Ask("ENG-1").Err)
			}
			d.Workers.Stop("ENG-1", stopTasks{})
			d.Workers.SetStatus("ENG-1", "idle")

			d.Tick()
			d.Advance(5 * time.Minute)
			d.Tick()

			assert.Equal(t, test.label, d.Workers.lastRename(t))
			assert.Equal(t, test.told, d.Followed.Told())
		})
	}
}

func TestAWorkerThatAskedIsGreenWhileItIsStillWorking(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")

	require.NoError(t, d.Workers.Ask("ENG-1").Err)
	d.Tick()

	assert.Equal(t, []string{"w-ENG-1|🟢 ENG-1"}, d.Workers.Renames())
	assert.Empty(t, d.Followed.Told())
}

func TestAskTwiceInATurnRecordsOneAskAndNotifiesOnce(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")

	first := d.Workers.Ask("ENG-1")
	d.Advance(time.Second)
	second := d.Workers.Ask("ENG-1")
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Workers.SetStatus("ENG-1", "idle")
	d.Tick()

	require.NoError(t, first.Err)
	require.NoError(t, second.Err)
	assert.Equal(t, askedLine, second.Stdout)
	assert.Equal(t, 1, d.Workers.asksRecorded("ENG-1"))
	assert.Equal(t, []string{askedNotice}, d.Followed.Told())
}

func TestAskWhileDispatchIsDownExits3AndRecordsNothing(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")

	res := d.Workers.AskWhileDown("ENG-1")

	require.Error(t, res.Err)
	assert.Equal(t, 3, res.Code)
	assert.EqualError(t, res.Err, "not running")
	assert.Empty(t, res.Stdout)
	assert.NoDirExists(t, d.sessionLogs())
}

func TestAskRefusesToRunOutsideAWorkerSession(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		session string
		dir     func(d *Dispatch) string
		wantErr string
	}{
		{name: "NoSession", session: "", dir: func(d *Dispatch) string { return d.worktreeOf("ENG-1") },
			wantErr: "ask runs inside a dispatch worker's session"},
		{name: "OutsideAWorkerTree", session: "worker", dir: func(d *Dispatch) string { return d.Repo },
			wantErr: "ask runs inside a dispatch worker's session"},
		{name: "ATreeNoWorkerRunsIn", session: "worker-ENG-9", dir: func(d *Dispatch) string { return d.worktreeOf("ENG-9") },
			wantErr: "ask runs inside a dispatch worker's session"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
			d.Workers.Running("ENG-1", "working")
			dir := test.dir(d)
			d.Tick()

			res := d.askAs(test.session, dir, true)

			require.EqualError(t, res.Err, test.wantErr)
			assert.Equal(t, 1, res.Code)
			assert.Empty(t, res.Stdout)
			assert.NoDirExists(t, d.sessionLogs())
		})
	}
}

func TestAskSurvivesARestartWithoutASecondNotification(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	require.NoError(t, d.Workers.Ask("ENG-1").Err)
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Workers.SetStatus("ENG-1", "idle")
	d.Tick()
	require.Equal(t, []string{askedNotice}, d.Followed.Told())

	d.Restart()
	d.Workers.LabelWorkspace("w-ENG-1", "ENG-1")
	d.Tick()

	assert.Equal(t, "w-ENG-1|🔴 ENG-1", d.Workers.lastRename(t))
	assert.Equal(t, []string{askedNotice}, d.Followed.Told())
}

func TestAWorkerWhoseSessionEndsWhileAskingIsRelaunched(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	require.NoError(t, d.Workers.Ask("ENG-1").Err)
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Workers.SetStatus("ENG-1", "idle")
	d.Tick()
	require.Equal(t, "w-ENG-1|🔴 ENG-1", d.Workers.lastRename(t))

	d.tools.Multiplexer.ExitAgent("w-ENG-1:p1")
	d.Tick()

	assert.Equal(t, "w-ENG-1|🟢 ENG-1", d.Workers.lastRename(t))
	assert.Len(t, d.Workers.Launched(), 1)
}

func TestAWorkerThatMovesItsOwnTicketToNeedsHumanStillNotifiesFollowers(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.Tick()

	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Workers.SetStatus("ENG-1", "idle")
	d.Tick()

	assert.Equal(t, "w-ENG-1|🔴 ENG-1", d.Workers.lastRename(t))
	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1"}, d.Followed.Told())
}

func TestASecondAskAfterAnAnswerShowsRedAndNotifiesAgain(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	require.NoError(t, d.Workers.Ask("ENG-1").Err)
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Workers.SetStatus("ENG-1", "idle")
	d.Tick()
	require.Equal(t, "w-ENG-1|🔴 ENG-1", d.Workers.lastRename(t))

	d.Advance(time.Minute)
	d.Workers.Say("ENG-1", "use the second option")
	d.Workers.SetStatus("ENG-1", "working")
	d.Tick()
	require.Equal(t, "w-ENG-1|🟢 ENG-1", d.Workers.lastRename(t))
	d.Advance(time.Minute)
	require.NoError(t, d.Workers.Ask("ENG-1").Err)
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Workers.SetStatus("ENG-1", "idle")
	d.Tick()

	assert.Equal(t, "w-ENG-1|🔴 ENG-1", d.Workers.lastRename(t))
	assert.Equal(t, []string{askedNotice, askedNotice}, d.Followed.Told())
	assert.Equal(t, 2, d.Workers.asksRecorded("ENG-1"))
}

func TestAMessageEndsTheAsk(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	require.NoError(t, d.Workers.Ask("ENG-1").Err)
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Workers.SetStatus("ENG-1", "idle")
	d.Tick()
	require.Equal(t, "w-ENG-1|🔴 ENG-1", d.Workers.lastRename(t))

	// Herdr has not yet read the worker working when the message lands.
	d.Advance(time.Minute)
	d.Workers.Say("ENG-1", "use the second option")
	d.Tick()

	assert.Equal(t, "w-ENG-1|🟢 ENG-1", d.Workers.lastRename(t))
}

func TestRulesReadAnAskingWorkerAsIdle(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(`
whenever({
  name: "see-ask",
  each: "ticket",
  if:   (t, ctx) => ctx.worker.of(t).state === "idle" && ctx.worker.of(t).asking,
  then: (t, ctx) => ctx.notify.queueSend(t, "asked at " + ctx.worker.of(t).askedAt, { once: ["see", t.id] }),
});
`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	asked := d.Clock.Now()
	require.NoError(t, d.Workers.Ask("ENG-1").Err)
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Workers.SetStatus("ENG-1", "idle")

	d.Tick()

	assert.Equal(t, []string{fmt.Sprint("asked at ", asked.UnixMilli())}, d.Followed.Told())
}

// The command does not check that its session is the tree's worker, and only the worker's log is read.
func TestAskFromAnotherSessionInTheTreeLeavesTheWorkerAlone(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")

	require.NoError(t, d.askAs("stranger", d.worktreeOf("ENG-1"), true).Err)
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Workers.SetStatus("ENG-1", "idle")
	d.Tick()

	assert.Equal(t, "w-ENG-1|💤 ENG-1", d.Workers.lastRename(t))
	assert.Equal(t, []string{idleNotice}, d.Followed.Told())
}

func TestAskWithAnArgumentPrintsTheUsageThatListsAsk(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.Tick()

	res := d.askAs(workerSession("ENG-1"), d.worktreeOf("ENG-1"), true, "ENG-1")

	require.EqualError(t, res.Err, "ask takes no arguments")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stderr, "       dispatch ask\n")
	assert.NoDirExists(t, d.sessionLogs())
}

func TestAnAskingWorkerWhoseTicketMovesToNeedsHumanShowsRedAndNotifiesFollowersOfIt(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	require.NoError(t, d.Workers.Ask("ENG-1").Err)
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Workers.SetStatus("ENG-1", "idle")
	d.Tick()
	require.Equal(t, []string{askedNotice}, d.Followed.Told())

	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Tick()

	assert.Equal(t, "w-ENG-1|🔴 ENG-1", d.Workers.lastRename(t))
	assert.Equal(t, []string{askedNotice, "ENG-1 is in Needs Human: ticket ENG-1"}, d.Followed.Told())
}
