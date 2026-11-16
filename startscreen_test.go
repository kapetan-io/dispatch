package dispatch_test

import (
	"context"
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
	"github.com/kapetan-io/dispatch/internal/linear"
)

// RunOnTerminal runs `dispatch run` with its stdout on a pseudo-terminal until the startup screen
// shows the next resync, and returns what the terminal received, with the terminal's \r\n made \n.
func (d *Dispatch) RunOnTerminal() string {
	d.t.Helper()
	control, terminal, err := openPTY()
	require.NoError(d.t, err)
	defer func() { _ = control.Close() }()
	screen := &syncBuffer{}
	go func() { _, _ = io.Copy(screen, control) }()

	ctx, cancel := context.WithCancel(context.Background())
	d.followers = d.freshFollowers()
	opts := d.options(dispatch.Options{})
	opts.Stdout, opts.Stderr = terminal, &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- dispatch.Run(ctx, []string{"run"}, opts) }()
	require.Eventually(d.t, func() bool { return strings.Contains(screen.String(), "next resync") }, 15*time.Second, 10*time.Millisecond)
	// The ticket lines follow the resync line in the same burst of writes.
	time.Sleep(50 * time.Millisecond)
	cancel()
	require.NoError(d.t, <-done)
	_ = terminal.Close()
	return strings.ReplaceAll(screen.String(), "\r\n", "\n")
}

func TestRunPrintsThePlainStartupScreenOutsideATerminal(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "working")

	d.StartBeforeTheEvents()
	require.Eventually(t, func() bool { return strings.Contains(d.LoopOut(), " start: ") }, 15*time.Second, 10*time.Millisecond)
	out := d.LoopOut()
	d.Stop()

	assert.Contains(t, out, "CONFIG   "+d.ConfigPath()+"\n")
	assert.Contains(t, out, "RULES    27 loaded\n")
	assert.Contains(t, out, "READING  linear, herdr, github, treehouse\n")
	assert.Contains(t, out, "SOURCES  linear     team ENG, label ai, 2 tickets\n")
	assert.Contains(t, out, "         local      off\n")
	assert.Contains(t, out, "         herdr      1 workspace, 1 agent\n")
	assert.Contains(t, out, "         github     0 pull requests on tracked tickets\n")
	assert.Contains(t, out, "         treehouse  1 tree, 1 leased\n")
	assert.Contains(t, out, "         rex        off\n")
	assert.Contains(t, out, "REPOS    dispatch\n")
	assert.Contains(t, out, "WATCHING 2 tickets   next resync "+d.Clock.Now().Add(5*time.Minute).Local().Format(time.TimeOnly)+"\n")
	assert.Contains(t, out, "         ENG-1  In Progress  ticket ENG-1\n")
	assert.Contains(t, out, "         ENG-2  In Review  ticket ENG-2\n")
	// Today's start line stays for the logs that grep it.
	assert.Contains(t, out, "start: 2 tracked tickets, 27 rules")
	assert.NotContains(t, out, "\x1b[")
	assert.NotContains(t, out, "██")
	assert.NotRegexp(t, regexp.MustCompile(`[🟢🔴⚪💤▶]`), out)
	// The sources are named before the read and their results printed after it, in that order.
	assert.Less(t, strings.Index(out, "CONFIG"), strings.Index(out, "READING"))
	assert.Less(t, strings.Index(out, "READING"), strings.Index(out, "SOURCES"))
	assert.Less(t, strings.Index(out, "SOURCES"), strings.Index(out, "WATCHING"))
}

func TestRunMarksTheSourcesWhoseFirstReadFailed(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Tools().Multiplexer.Fail(herdrtest.OpRead, errors.New("herdr is down"))
	d.Tools().Treehouse.FailTrees(errors.New("exit status 1"))

	d.StartBeforeTheEvents()
	require.Eventually(t, func() bool { return strings.Contains(d.LoopOut(), " start: ") }, 15*time.Second, 10*time.Millisecond)
	out := d.LoopOut()

	assert.Contains(t, out, "         herdr      failed\n")
	assert.Contains(t, out, "herdr is down")
	assert.Contains(t, out, "         treehouse  failed\n")
	assert.Contains(t, out, "exit status 1")
	assert.Contains(t, out, "REPOS    dispatch (unread)\n")
	// The sources that read still show what they found, and dispatch keeps running.
	assert.Contains(t, out, "SOURCES  linear     team ENG, label ai, 1 ticket\n")
	assert.Contains(t, out, "         ENG-1  In Review  ticket ENG-1\n")
	d.Tick()
	d.Stop()
}

func TestRunShowsWhatItKnewWhenTheStartStops(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		opts    dispatchOptions
		wantOut []string
		wantErr string
	}{
		{
			name:    "bad config key",
			opts:    dispatchOptions{Config: "[dispatch]\nnot_a_key = 1\n"},
			wantOut: []string{"CONFIG   "},
			wantErr: "not_a_key",
		},
		{
			name:    "Linear refuses the key",
			opts:    dispatchOptions{Key: configuredKey, Config: "[plugins.linear]\napi_key_command = \"echo bad\"\n"},
			wantOut: []string{"CONFIG   ", "RULES    27 loaded\n", "READING  linear, herdr, github, treehouse\n"},
			wantErr: linear.ErrUnauthorized.Error(),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, test.opts)

			res := d.Run("run")

			require.Error(t, res.Err)
			require.ErrorContains(t, res.Err, test.wantErr)
			for _, want := range test.wantOut {
				assert.Contains(t, res.Stdout, want)
			}
			assert.NotContains(t, res.Stdout, "SOURCES")
		})
	}
}

func TestRunShowsTheTicketSystemsAndMultiplexerThisConfigUses(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[plugins.local]\n\n[plugins.rex]\n"})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))

	d.StartBeforeTheEvents()
	require.Eventually(t, func() bool { return strings.Contains(d.LoopOut(), " start: ") }, 15*time.Second, 10*time.Millisecond)
	out := d.LoopOut()
	d.Stop()

	assert.Contains(t, out, "READING  linear, local, rex, github, treehouse\n")
	assert.Contains(t, out, "         local      0 tickets\n")
	assert.Contains(t, out, "         rex        0 workspaces, 0 agents\n")
	assert.Contains(t, out, "         herdr      off\n")
}

func TestRunPrintsTheStartupScreenInATerminal(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "Needs Human", "dispatch"), ticket("ENG-3", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.Workers.Running("ENG-2", "blocked")

	out := d.RunOnTerminal()

	assert.Contains(t, out, "\x1b[38;2;0;210;255m█")
	assert.Contains(t, out, "\x1b[38;2;230;80;220m▶")
	assert.Contains(t, out, "event-driven reconcile loop for agents")
	assert.Contains(t, out, "🟢 linear     team ENG, label ai, 3 tickets\n")
	assert.Contains(t, out, "⚪ local      off\n")
	assert.Contains(t, out, "⚪ rex        off\n")
	assert.Contains(t, out, "REPOS")
	assert.Contains(t, out, "🟢 dispatch")
	assert.Contains(t, out, "▶ watching 3 tickets   next resync "+d.Clock.Now().Add(5*time.Minute).Local().Format(time.TimeOnly))
	// A ticket with a worker label shows its emoji, and one with no worker its status.
	assert.Contains(t, out, "🟢 ENG-1  ticket ENG-1\n")
	assert.Contains(t, out, "🔴 ENG-2  ticket ENG-2\n")
	assert.Contains(t, out, "   ENG-3  In Review  ticket ENG-3\n")
	// The terminal form leaves out the start line the plain form keeps for logs.
	assert.NotContains(t, out, " start: ")
}

func TestRunPrintsTheStartupScreenWithoutColorUnderNoColor(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"NO_COLOR": "1"}})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))

	out := d.RunOnTerminal()

	assert.NotContains(t, out, "\x1b[")
	assert.Contains(t, out, "██████╗ ██╗███████╗")
	assert.Contains(t, out, "🟢 linear     team ENG, label ai, 1 ticket\n")
	assert.Contains(t, out, "▶ watching 1 ticket")
}
