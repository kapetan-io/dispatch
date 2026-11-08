package dispatch_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
)

func fakeClear(session, tree string) string {
	return fmt.Sprintf(`{"sid":%q,"where":%q,"cleared":true}`, session, tree)
}

func (d *Dispatch) workerSessionOf(ticket string) string {
	d.t.Helper()
	snap, err := d.tools.Herdr.Read(context.Background())
	require.NoError(d.t, err)
	agent, ok := snap.SessionIn("w-" + ticket)
	require.True(d.t, ok)
	return agent.Session
}

func TestFollowClearMovesAWorkersSessionRecordsAndLabelToTheNewID(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule), FakeHarness: true})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "")
	pane := d.SessionOf("ENG-1", "fake", "idle")
	tree := d.worktreeOf("ENG-1")
	d.StartBeforeTheEvents()
	defer d.FromTerminal(pane)()
	require.NoError(t, d.Hook("record-stop", fakeStop(tree, 1)).Err)
	d.HookAndWaitForCommand("wake", fmt.Sprintf(`{"sid":"worker","where":%q,"typed":"/dispatch:pr-respond"}`, tree), tree, "worker", "/dispatch:pr-respond")

	res := d.Hook("follow-clear", fakeClear("cleared", tree))

	require.NoError(t, res.Err)
	assert.Empty(t, res.Stdout)
	assert.Equal(t, "cleared", d.workerSessionOf("ENG-1"))
	assert.True(t, d.StopRecorded(tree, "cleared"))
	assert.Equal(t, 1, d.CommandsLogged(tree, "cleared"))
	assert.False(t, d.StopRecorded(tree, "worker"))
	assert.Zero(t, d.CommandsLogged(tree, "worker"))
}

func TestFollowClearPutsTheRecordsBackWhenTheHostRefusesTheNewLabel(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule), FakeHarness: true})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "")
	pane := d.SessionOf("ENG-1", "fake", "idle")
	tree := d.worktreeOf("ENG-1")
	d.StartBeforeTheEvents()
	defer d.FromTerminal(pane)()
	require.NoError(t, d.Hook("record-stop", fakeStop(tree, 1)).Err)
	d.tools.Herdr.Fail(herdrtest.OpFollowSession, errors.New("host refused the label"))

	require.NoError(t, d.Hook("follow-clear", fakeClear("first", tree)).Err)

	assert.Equal(t, "worker", d.workerSessionOf("ENG-1"))
	assert.True(t, d.StopRecorded(tree, "worker"))
	assert.False(t, d.StopRecorded(tree, "first"))

	d.tools.Herdr.Fail(herdrtest.OpFollowSession, nil)
	require.NoError(t, d.Hook("follow-clear", fakeClear("second", tree)).Err)

	assert.Equal(t, "second", d.workerSessionOf("ENG-1"))
	assert.True(t, d.StopRecorded(tree, "second"))
}

func TestFollowClearKeepsTheRecordsWithTheNewIDWhenTheLabelLandedButTheCallFailed(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule), FakeHarness: true})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "")
	pane := d.SessionOf("ENG-1", "fake", "idle")
	tree := d.worktreeOf("ENG-1")
	d.StartBeforeTheEvents()
	defer d.FromTerminal(pane)()
	require.NoError(t, d.Hook("record-stop", fakeStop(tree, 1)).Err)
	d.tools.Herdr.Fail(herdrtest.OpFollowSessionLanded, errors.New("deadline exceeded"))

	require.NoError(t, d.Hook("follow-clear", fakeClear("first", tree)).Err)

	assert.Equal(t, "first", d.workerSessionOf("ENG-1"))
	assert.True(t, d.StopRecorded(tree, "first"))
	assert.False(t, d.StopRecorded(tree, "worker"))
}

func TestFollowClearMovesTheRecordsEvenWhenNoDispatchRuns(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule), FakeHarness: true})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "")
	pane := d.SessionOf("ENG-1", "fake", "idle")
	tree := d.worktreeOf("ENG-1")
	d.StartBeforeTheEvents()
	defer d.FromTerminal(pane)()
	require.NoError(t, d.Hook("record-stop", fakeStop(tree, 1)).Err)
	d.HookAndWaitForCommand("wake", fmt.Sprintf(`{"sid":"worker","where":%q,"typed":"/dispatch:pr-respond"}`, tree), tree, "worker", "/dispatch:pr-respond")
	d.Stop()

	require.NoError(t, d.HookWhileDown("follow-clear", fakeClear("cleared", tree)).Err)

	assert.True(t, d.StopRecorded(tree, "cleared"))
	assert.Equal(t, 1, d.CommandsLogged(tree, "cleared"))
	assert.Equal(t, "cleared", d.workerSessionOf("ENG-1"))
}

func TestFollowClearKeepsAFollowerOfTheSameProcessFollowing(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{FakeHarness: true})
	before, after := d.Tools().Harness.Open(), d.Tools().Harness.Open()
	d.FakeSession(before)
	d.FakeProcess("process-1")
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	require.NoError(t, d.Run("watch", "ENG-1").Err)
	d.Tools().Harness.Exit(before)
	d.FakeSession(after)

	require.NoError(t, d.Hook("follow-clear", fakeClear(after, d.worktreeOf("ENG-1"))).Err)
	d.Tick()

	require.Len(t, d.Tools().Harness.Delivered(after), 1)
	assert.Contains(t, d.Tools().Harness.Delivered(after)[0], "ENG-1 is ready for review")
	assert.Empty(t, d.LoopStderr())
}

func TestWatchAgainFromAResumedProcessKeepsOneFollowerThatUnwatchRemoves(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{FakeHarness: true})
	session := d.Tools().Harness.Open()
	d.FakeSession(session)
	d.FakeProcess("process-1")
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	require.NoError(t, d.Run("watch", "ENG-1").Err)
	d.FakeProcess("process-2")
	require.NoError(t, d.Run("watch", "ENG-1").Err)

	d.Tick()

	assert.Len(t, d.Tools().Harness.Delivered(session), 1)

	require.NoError(t, d.Run("unwatch", "ENG-1").Err)
	d.GitHub.PullRequest("ENG-1", cleanPR("a2"))
	d.Tick()

	assert.Len(t, d.Tools().Harness.Delivered(session), 1)
}

func TestFollowClearMovesAFollowerThatWatchedAgainAfterAResume(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{FakeHarness: true})
	before, after := d.Tools().Harness.Open(), d.Tools().Harness.Open()
	d.FakeSession(before)
	d.FakeProcess("process-1")
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	require.NoError(t, d.Run("watch", "ENG-1").Err)
	d.FakeProcess("process-2")
	require.NoError(t, d.Run("watch", "ENG-1").Err)
	d.Tools().Harness.Exit(before)
	d.FakeSession(after)

	require.NoError(t, d.Hook("follow-clear", fakeClear(after, d.worktreeOf("ENG-1"))).Err)
	d.Tick()

	assert.Len(t, d.Tools().Harness.Delivered(after), 1)
}

func TestFollowClearLeavesAFollowerOfAnotherProcessAlone(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{FakeHarness: true})
	mine, other := d.Tools().Harness.Open(), d.Tools().Harness.Open()
	d.FakeSession(mine)
	d.FakeProcess("process-1")
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	require.NoError(t, d.Run("watch", "ENG-1").Err)
	d.FakeProcess("process-2")
	d.FakeSession(other)

	require.NoError(t, d.Hook("follow-clear", fakeClear(other, d.worktreeOf("ENG-1"))).Err)
	d.Tick()

	assert.Len(t, d.Tools().Harness.Delivered(mine), 1)
	assert.Empty(t, d.Tools().Harness.Delivered(other))
}

func TestFollowClearDoesNothingForAnInputThatIsNoClear(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule), FakeHarness: true})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "")
	pane := d.SessionOf("ENG-1", "fake", "idle")
	tree := d.worktreeOf("ENG-1")
	d.StartBeforeTheEvents()
	defer d.FromTerminal(pane)()

	for _, input := range []string{
		fmt.Sprintf(`{"sid":"cleared","where":%q}`, tree),
		`not json`,
		fakeClear("", tree),
	} {
		res := d.Hook("follow-clear", input)
		require.NoError(t, res.Err)
		assert.Empty(t, res.Stdout)
		assert.Empty(t, res.Stderr)
	}
	assert.Equal(t, "worker", d.workerSessionOf("ENG-1"))
}

func TestFollowClearIgnoresAClearInASessionOfAnotherAgentKind(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule), FakeHarness: true})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "")
	pane := d.SessionOf("ENG-1", "fake", "idle")
	tree := d.worktreeOf("ENG-1")
	d.StartBeforeTheEvents()
	restore := d.FromTerminal(pane)
	require.NoError(t, d.Hook("record-stop", fakeStop(tree, 1)).Err)
	restore()
	d.tools.Herdr.AddAgent(herdr.Agent{Kind: "pi", Status: "idle", Session: "other", PaneID: "pi:p1"})
	defer d.FromTerminal("pi:p1")()

	res := d.Hook("follow-clear", fakeClear("cleared", tree))

	require.NoError(t, res.Err)
	assert.Empty(t, res.Stderr)
	assert.Equal(t, "worker", d.workerSessionOf("ENG-1"))
	assert.True(t, d.StopRecorded(tree, "worker"))
	assert.False(t, d.StopRecorded(tree, "cleared"))
}

func claudeClear(session, tree, source string) string {
	return fmt.Sprintf(`{"session_id":%q,"cwd":%q,"hook_event_name":"SessionStart","source":%q}`, session, tree, source)
}

func TestFollowClearOfARealClaudePayloadLetsTheNextNudgeReachTheClearedSession(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "idle")
	tree := d.worktreeOf("ENG-1")
	d.Workers.SessionExits("ENG-1")
	d.openWorkerSession("ENG-1", "cleared")
	require.ErrorContains(t, d.Run("tell", "ENG-1", "before").Err, "nothing sent")
	defer d.FromTerminal("w-ENG-1:p1")()

	require.NoError(t, d.Hook("follow-clear", claudeClear("cleared", tree, "clear")).Err)

	require.NoError(t, d.Run("tell", "ENG-1", "after").Err)
	assert.Equal(t, []string{"after"}, d.Workers.Received("ENG-1"))
	assert.Equal(t, "cleared", d.workerSessionOf("ENG-1"))
}

func TestFollowClearIgnoresCompactStartupAndResume(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "idle")
	tree := d.worktreeOf("ENG-1")
	defer d.FromTerminal("w-ENG-1:p1")()

	for _, source := range []string{"compact", "startup", "resume"} {
		require.NoError(t, d.Hook("follow-clear", claudeClear("other", tree, source)).Err)
	}

	assert.Equal(t, "worker", d.workerSessionOf("ENG-1"))
}
