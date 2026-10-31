package dispatch_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
)

const fakeTable = `
[harness.fake-fast]
kind = "fake"
model = "tiny"
`

func TestLaunchStartsTheProgramOfAHarnessKindThatIsNotClaude(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, FakeHarness: true, Config: fakeTable})
	d.Trees.Pooled()

	out := d.launch("ENG-1", "thrawn01/eng-1", "--harness", "harness.fake-fast")

	assert.Equal(t, "ok", out.result["status"])
	require.Len(t, d.fakes().Herdr.Started(), 1)
	program := d.fakes().Herdr.Started()[0].Program
	assert.Equal(t, "fake", program.AgentKind)
	assert.Equal(t, []string{"--model", "tiny"}, program.Argv)
	assert.Empty(t, program.PathFirst)
	assert.Equal(t, []string{"w2:p2|/dispatch:build ENG-1"}, d.fakes().Herdr.Kicked())
	assert.Equal(t, []herdr.Startup{program.Startup}, d.fakes().Herdr.KickedWith())
	snap, err := d.fakes().Herdr.Read(context.Background())
	require.NoError(t, err)
	require.Len(t, snap.Agents, 1)
	assert.Equal(t, "fake", snap.Agents[0].Kind)
}

func TestTickLaunchesTheProgramOfAHarnessKindThatIsNotClaude(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Launcher: builtinLauncher, FakeHarness: true, Config: fakeTable + "[rules]\nworker = \"harness.fake-fast\"\n"})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Trees.Pooled()

	d.Tick()

	require.Len(t, d.fakes().Herdr.Started(), 1)
	assert.Equal(t, "fake", d.fakes().Herdr.Started()[0].Program.AgentKind)
	assert.Equal(t, "tiny", modelOf(d.fakes().Herdr.Started()[0].Program))
}

func TestLaunchNamesTheProgramInTheReasonItNeverStarted(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, FakeHarness: true, Config: fakeTable})
	d.Trees.Pooled()
	d.fakes().Herdr.Fail(herdrtest.OpKickoff, herdr.ErrNeverStarted)

	out := d.launch("ENG-1", "thrawn01/eng-1", "--harness", "harness.fake-fast")

	assert.Equal(t, "injection_unconfirmed", out.result["status"])
	assert.Contains(t, out.result["detail"], "Fake never started, because its launch command still showed at the shell prompt after Enter was pressed on it")
}

func TestLaunchNamesClaudeInTheReasonItNeverStarted(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.fakes().Herdr.Fail(herdrtest.OpKickoff, herdr.ErrNeverStarted)

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Contains(t, out.result["detail"], "Claude never started, because its launch command still showed at the shell prompt after Enter was pressed on it, and herdr")
}

func TestConfigRefusesTheFakeKindUntilItsHarnessIsRegistered(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: fakeTable})

	res := d.Run("tick")

	require.ErrorContains(t, res.Err, `harness.fake-fast: kind "fake" is not a harness this dispatch drives (claude-code)`)
}

func TestLaunchKicksOffWithTheStartupScreensOfClaudeCode(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()

	d.launch("ENG-1", "thrawn01/eng-1")

	require.Len(t, d.fakes().Herdr.KickedWith(), 1)
	startup := d.fakes().Herdr.KickedWith()[0]
	assert.Len(t, startup.Answers, 3)
	assert.Equal(t, "❯", startup.Ready.String())
	assert.Equal(t, "❯ *claude +--", startup.Unrun.String())
}
