package dispatch_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
)

func TestTickEscalatesAProgramThatExitedBeforeTheKickoffNamingRexItsExitAndItsScreen(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.RealLauncher()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.tools.Herdr.NameMultiplexer("Rex")
	d.tools.Herdr.Fail(herdrtest.OpKickoff, &herdr.ProgramExited{Exit: "exit status 1", Screen: "dispatch: no real claude found on PATH"})

	res := d.Run("tick")
	require.ErrorContains(t, res.Err, "ENG-1 launch failed: program_exited: Claude exited (exit status 1) before Rex showed the kickoff running")
	d.Advance(time.Second)
	res = d.Run("tick")

	assert.Contains(t, res.Stdout, "ENG-1 escalated: its worker failed to launch 2 times in a row")
	changes := d.Linear.Changes()
	require.Len(t, changes, 2)
	assert.Contains(t, changes[1], "Claude exited (exit status 1) before Rex showed the kickoff running. Its screen ended with: dispatch: no real claude found on PATH.")
	assert.NotContains(t, changes[1], "herdr")
	explain := d.Run("explain", "ENG-1")
	require.NoError(t, explain.Err)
	assert.Contains(t, explain.Stdout, "before Rex showed the kickoff running")
}

func TestTickEscalatesAProgramStillRunningAtTheKickoffNamingRex(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.RealLauncher()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.tools.Herdr.NameMultiplexer("Rex")
	d.tools.Herdr.Fail(herdrtest.OpKickoff, herdr.ErrKickoffUnconfirmed)

	_ = d.Run("tick")
	d.Advance(time.Second)
	_ = d.Run("tick")

	changes := d.Linear.Changes()
	require.Len(t, changes, 2)
	assert.Contains(t, changes[1], "Claude started but Rex never showed the kickoff running")
	assert.NotContains(t, changes[1], "herdr")
}

func TestTickEscalationNamesHerdrWhenHerdrIsTheMultiplexer(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.RealLauncher()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.tools.Herdr.Fail(herdrtest.OpKickoff, &herdr.ProgramExited{Exit: "killed"})

	_ = d.Run("tick")
	d.Advance(time.Second)
	_ = d.Run("tick")

	changes := d.Linear.Changes()
	require.Len(t, changes, 2)
	assert.Contains(t, changes[1], "Claude exited (killed) before herdr showed the kickoff running")
}

func TestExplainNamesRexWhenTheRunCouldNotReadIt(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.tools.Herdr.NameMultiplexer("Rex")
	d.tools.Herdr.Fail(herdrtest.OpRead, errors.New("Rex is not running"))

	run := d.Run("tick")
	explain := d.Run("explain", "ENG-1")

	assert.Contains(t, run.Stdout+run.Stderr+errText(run.Err), "Rex unread: Rex is not running")
	assert.NotContains(t, explain.Stdout, "herdr")
	assert.Contains(t, explain.Stdout, "Rex")
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
