package dispatch_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
)

func TestConfigRejectsAHostThatIsNeitherTuiNorApp(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, host string
	}{
		{"Unknown", "x"},
		{"Empty", ""},
		{"WrongCase", "App"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Config: "[plugins.herdr]\nhost = \"" + test.host + "\"\n"})

			res := d.Run("cycle")

			require.ErrorContains(t, res.Err, `plugins.herdr.host must be "tui" or "app", not "`+test.host+`"`)
		})
	}
}

func TestLaunchUnderTheAppHostWithNoHcNamesTheReadError(t *testing.T) {
	t.Parallel()
	bin := t.TempDir()
	git, err := exec.LookPath("git")
	require.NoError(t, err)
	require.NoError(t, os.Symlink(git, filepath.Join(bin, "git")))
	d := newDispatch(t, dispatchOptions{
		Launcher:  builtinLauncher,
		RealHerdr: true,
		Config:    "[plugins.herdr]\nhost = \"app\"\n",
		Env:       map[string]string{"DISPATCH_PLUGIN_ROOT": "/plugin", "DISPATCH_LAUNCH_PREFLIGHT_TIMEOUT": "0", "PATH": bin},
	})
	d.Trees.Pooled()

	launched := later(func() launchOutput { return d.launch("ENG-1", "thrawn01/eng-1") })
	assert.Equal(t, time.Nanosecond, d.Deadline("launch preflight"))
	d.Expire("launch preflight")
	out := launched()

	assert.Equal(t, "herdr_unavailable", out.result["status"])
	assert.Contains(t, out.result["detail"], "herdr server not answering")
	assert.Contains(t, out.result["detail"], `"hc"`)
	assert.Empty(t, d.fakes().Calls("treehouse", "lease"))
}

func TestLaunchReportsAlreadyRunningWhenHerdrFindsASessionOnTheTree(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.fakes().Herdr.Fail(herdrtest.OpStartTab, herdr.ErrSessionRunning)

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "already_running", out.result["status"])
	assert.Empty(t, d.fakes().Herdr.ClosedWorkspaces())
	assert.Empty(t, d.fakes().Calls("treehouse", "return"))
	assert.Empty(t, d.fakes().Herdr.Kicked())
}

func TestLaunchStartsClaudeOnTheSessionItChose(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()

	out := d.launch("ENG-1", "thrawn01/eng-1")

	require.Equal(t, "ok", out.result["status"])
	program := d.fakes().Herdr.Started()[0].Program
	assert.Regexp(t, `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`, program.Session)
	assert.Equal(t, "--session-id", program.SessionFlag)
	assert.NotContains(t, program.Argv, "--session-id")
	assert.NotContains(t, program.Argv, program.Session)
}

func TestTickFailsANudgeToAWorkerThatReportsNoStatus(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.PullRequestFailsAfterEntry(prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	d.fakes().Herdr.Fail(herdrtest.OpPrompt, herdr.ErrStatusUnknown)

	err := d.TickAfterASecond()

	require.ErrorContains(t, err, "ENG-1 nudge failed: status unknown, nothing typed")
	assert.Empty(t, d.Workers.Prompts())
}

func TestRecordStopLandsTheTurnEndOfASessionInAnAppTerminal(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule), FakeHarness: true})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "")
	terminal := d.SessionOf("ENG-1", "fake", "idle")
	d.StartBeforeTheEvents()

	defer d.FromTerminal(terminal)()
	require.NoError(t, d.Hook("record-stop", fakeStop(d.worktreeOf("ENG-1"), 1)).Err)

	d.Tick()
	assert.Equal(t, []string{"ENG-1: turns  of 0", "ENG-1: turns running of 1"}, d.Linear.Comments())
	assert.Equal(t, []string{terminal}, d.fakes().Calls("herdr", "agent-at"))
}

func TestRecordStopAsksTheAppFirstAndThePaneWhenTheAppDoesNotKnowTheTerminal(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule), FakeHarness: true})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "")
	pane := d.SessionOf("ENG-1", "fake", "idle")
	d.StartBeforeTheEvents()

	defer d.FromPane(pane)()
	restore := d.FromTerminal("t404")
	defer restore()
	d.env = d.env.With("HERDR_PANE_ID", pane)
	require.NoError(t, d.Hook("record-stop", fakeStop(d.worktreeOf("ENG-1"), 1)).Err)

	d.Tick()
	assert.Equal(t, []string{"ENG-1: turns  of 0", "ENG-1: turns running of 1"}, d.Linear.Comments())
	assert.Equal(t, []string{"t404", pane}, d.fakes().Calls("herdr", "agent-at"))
}

func TestTickClosesAWorkerStuckAtAStartupDialogAfterItsLauncherDied(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "unknown")
	d.Workers.UnconfirmedLaunch("ENG-1", d.Clock.Now())

	d.Tick()
	d.Advance(6 * time.Minute)
	d.Tick()

	assert.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())
	assert.Empty(t, d.Workers.Prompts())
}

func TestTickLeavesAWorkerThatReportsNoStatusAloneOnceItsKickoffRan(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "unknown")

	d.Tick()
	d.Advance(6 * time.Minute)
	d.Tick()

	assert.Empty(t, d.Workers.Closed())
}

func TestRuleTypesLeaveOutTheHost(t *testing.T) {
	t.Parallel()
	types, err := os.ReadFile("rules/types/dispatch.d.ts")
	require.NoError(t, err)

	assert.NotContains(t, string(types), "host")
}

func TestConfigAcceptsTheTuiAndAppHosts(t *testing.T) {
	t.Parallel()
	for _, host := range []string{"tui", "app"} {
		t.Run(host, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Config: "[plugins.herdr]\nhost = \"" + host + "\"\n"})

			res := d.Run("tick")

			require.NoError(t, res.Err)
		})
	}
}

func TestRecordStopWritesNothingFromATerminalTheAppDoesNotKnowAsAWorker(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(turnsRule), FakeHarness: true})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "")
	d.SessionOf("ENG-1", "fake", "idle")
	d.StartBeforeTheEvents()

	defer d.FromTerminal("t404")()
	require.NoError(t, d.Hook("record-stop", fakeStop(d.worktreeOf("ENG-1"), 1)).Err)

	d.Tick()
	assert.Equal(t, []string{"ENG-1: turns  of 0"}, d.Linear.Comments())
}

func TestTickLeavesAWorkingWorkerAloneWhileItsLaunchIsUnconfirmed(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"working", "blocked"} {
		t.Run(status, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
			d.Workers.Running("ENG-1", status)
			d.Workers.UnconfirmedLaunch("ENG-1", d.Clock.Now())

			d.Tick()
			d.Advance(6 * time.Minute)
			d.Tick()

			assert.Empty(t, d.Workers.Closed())
		})
	}
}

func TestANudgeToAWorkerThatReportsNoStatusLeavesNoSendForAPersonsCommandToMatch(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(commandsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.CheckedOut("ENG-1", "idle")
	d.fakes().Herdr.Fail(herdrtest.OpPrompt, herdr.ErrStatusUnknown)
	require.Error(t, d.Run("tell", "ENG-1", "/merge-it 42").Err)
	require.Empty(t, d.Workers.Prompts())

	d.Workers.Type("ENG-1", "/merge-it 42")
	d.Tick()

	assert.Equal(t, "commands merge-it|42|false|person of 1", lastReading(t, d))
}
