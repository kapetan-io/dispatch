package dispatch_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/lineartest"
)

// RunOnTerminal runs `dispatch run` with its stdout on a pseudo-terminal until the startup screen
// shows the next resync, and returns everything the terminal received, with \r\n made \n.
func (d *Dispatch) RunOnTerminal() string {
	d.t.Helper()
	screen, stop := d.startOnTerminal()
	require.Eventually(d.t, func() bool { return strings.Contains(screen(), "next resync") }, 15*time.Second, 10*time.Millisecond)
	require.NoError(d.t, stop())
	return screen()
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
	assert.Contains(t, out, "RULES    28 loaded\n")
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
	assert.Contains(t, out, "start: 2 tracked tickets, 28 rules")
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
			wantOut: []string{"CONFIG   ", "RULES    28 loaded\n", "READING  linear, herdr, github, treehouse\n"},
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
	// The terminal form leaves out the start line the plain form keeps for logs, and says what it
	// restored from history on a line of its own.
	assert.NotContains(t, out, " start: ")
	assert.Contains(t, sgr.ReplaceAllString(out, ""), " HISTORY  0 receipts from no earlier history\n")
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

// endOfRun is what startOnTerminal writes to the terminal after the run returns.
const endOfRun = "-- end of run --"

// startOnTerminal starts `dispatch run` with its stdout on a pseudo-terminal and returns what the
// terminal has received so far, with \r\n made \n, and what stops the run and returns its error.
func (d *Dispatch) startOnTerminal() (screen func() string, stop func() error) {
	d.t.Helper()
	control, terminal, err := openPTY()
	if errors.Is(err, errors.ErrUnsupported) {
		d.t.Skip("no pseudo-terminal on this OS")
	}
	require.NoError(d.t, err)
	received := &syncBuffer{}
	go func() { _, _ = io.Copy(received, control) }()

	ctx, cancel := context.WithCancel(context.Background())
	d.followers = d.freshFollowers()
	opts := d.options(dispatch.Options{})
	opts.Stdout, opts.Stderr = terminal, &syncBuffer{}
	var runErr error
	done := make(chan struct{})
	go func() {
		runErr = dispatch.Run(ctx, []string{"run"}, opts)
		close(done)
	}()
	// A test that fails before calling stop still ends its run, before its directories are removed.
	d.t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
		}
		_ = terminal.Close()
		_ = control.Close()
	})
	stop = func() error {
		cancel()
		<-done
		err := runErr
		// The terminal delivers bytes in order, so once this line arrives everything the run wrote has too.
		_, _ = terminal.WriteString(endOfRun + "\n")
		require.Eventually(d.t, func() bool { return strings.Contains(received.String(), endOfRun) }, 15*time.Second, time.Millisecond)
		_ = terminal.Close()
		_ = control.Close()
		return err
	}
	screen = func() string {
		return strings.ReplaceAll(strings.ReplaceAll(received.String(), "\r\n", "\n"), endOfRun+"\n", "")
	}
	return screen, stop
}

var sgr = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestRunFadesTheLogoFromCyanToMagentaOneStepPerColumn(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))

	lines := strings.Split(d.RunOnTerminal(), "\n")

	var plain []string
	for _, line := range lines {
		plain = append(plain, sgr.ReplaceAllString(line, ""))
	}
	top := slices.IndexFunc(plain, func(line string) bool {
		return strings.HasPrefix(line, "  ██████╗ ██╗███████╗")
	})
	require.GreaterOrEqual(t, top, 0)
	// The signal line sits under the six rows of block letters, and the tagline under it.
	require.Greater(t, len(plain), top+7)
	assert.Equal(t, "  ━━━━━━━━━━━●━━━━━━━━━━━━━━━●━━━━━━━━━━━━━━━━━━●━━━━━━━━━━━━▶", plain[top+6])
	assert.Contains(t, plain[top+7], "event-driven reconcile loop for agents")

	for _, row := range lines[top : top+7] {
		cells := regexp.MustCompile(`\x1b\[38;2;(\d+;\d+;\d+)m([^\x1b])`).FindAllStringSubmatch(row, -1)
		columns := []rune(strings.TrimPrefix(sgr.ReplaceAllString(row, ""), "  "))
		require.Len(t, cells, len(columns))
		assert.Equal(t, "0;210;255", cells[0][1])
		assert.Equal(t, "230;80;220", cells[len(cells)-1][1])
		for i := 1; i < len(cells); i++ {
			assert.NotEqual(t, cells[i-1][1], cells[i][1])
		}
	}
}

func TestRunPrintsTheLogoAndTheSourcesItWillReadBeforeTheLinearKeyArrives(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gate := filepath.Join(dir, "gate")
	command, counter := keyScript(t, dir, "while [ ! -f "+gate+" ]; do sleep 0.01; done\necho test-key")
	d := newDispatch(t, dispatchOptions{Key: configuredKey, Config: "[plugins.linear]\napi_key_command = \"" + command + "\"\n"})
	// A test that fails before opening the gate must not leave the key command looping.
	t.Cleanup(func() { _ = os.WriteFile(gate, nil, 0o600) })
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))

	screen, stop := d.startOnTerminal()
	require.Eventually(t, func() bool { return runCount(t, counter) > 0 && strings.Contains(screen(), "READING") }, 15*time.Second, 10*time.Millisecond)
	waiting := screen()

	assert.Contains(t, sgr.ReplaceAllString(waiting, ""), "██████╗ ██╗███████╗")
	assert.Contains(t, waiting, "CONFIG")
	assert.Contains(t, sgr.ReplaceAllString(waiting, ""), "READING  linear, herdr, github, treehouse\n")
	assert.NotContains(t, waiting, "SOURCES")
	assert.Zero(t, d.Linear.Calls())

	require.NoError(t, os.WriteFile(gate, nil, 0o600))
	require.Eventually(t, func() bool { return strings.Contains(screen(), "next resync") }, 15*time.Second, 10*time.Millisecond)
	require.NoError(t, stop())
	after := screen()

	// The first read's lines follow the ones already printed, and nothing moves the cursor back.
	assert.True(t, strings.HasPrefix(after, waiting))
	assert.NotContains(t, after, "\r")
	assert.NotRegexp(t, regexp.MustCompile(`\x1b[^\[]|\x1b\[[0-9;?]*[^0-9;?m]`), after)
}

func TestRunNamesTheSourcesWhileTheFirstReadWaitsOnLinear(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	release := d.Tools().Linear.Hold(lineartest.OnManaged)
	defer release()

	out := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	d.followers = d.freshFollowers()
	opts := d.options(dispatch.Options{})
	opts.Stdout, opts.Stderr = out, &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- dispatch.Run(ctx, []string{"run"}, opts) }()

	require.Eventually(t, func() bool { return d.Tools().Linear.Held(lineartest.OnManaged) > 0 }, 15*time.Second, 10*time.Millisecond)
	assert.Contains(t, out.String(), "CONFIG   "+d.ConfigPath()+"\n")
	assert.Contains(t, out.String(), "READING  linear, herdr, github, treehouse\n")
	assert.NotContains(t, out.String(), "SOURCES")

	release()
	require.Eventually(t, func() bool { return strings.Contains(out.String(), " start: ") }, 15*time.Second, 10*time.Millisecond)
	assert.Contains(t, out.String(), "SOURCES  linear     team ENG, label ai, 1 ticket\n")
	cancel()
	require.NoError(t, <-done)
}

func TestRunMarksAFailedSourceRedInATerminal(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Tools().Multiplexer.Fail(herdrtest.OpRead, errors.New("herdr is down"))

	out := d.RunOnTerminal()

	assert.Contains(t, out, "🔴 herdr")
	assert.Regexp(t, regexp.MustCompile(`\x1b\[38;2;255;95;95m[^\x1b]*herdr is down[^\x1b]*\x1b\[0m`), out)
	// The sources that read still show what they found.
	assert.Contains(t, out, "🟢 linear     team ENG, label ai, 1 ticket\n")
	assert.Contains(t, out, "🟢 treehouse")
	assert.Contains(t, out, "   ENG-1  In Review  ticket ENG-1\n")
}

func TestRunShowsTheLogoInATerminalWhenTheStartStops(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nnot_a_key = 1\n"})

	screen, stop := d.startOnTerminal()
	require.Eventually(t, func() bool { return strings.Contains(screen(), "CONFIG") }, 15*time.Second, 10*time.Millisecond)
	err := stop()

	require.ErrorContains(t, err, "not_a_key")
	assert.Contains(t, sgr.ReplaceAllString(screen(), ""), "██████╗ ██╗███████╗")
	assert.Contains(t, screen(), "event-driven reconcile loop for agents")
	assert.NotContains(t, screen(), "SOURCES")
}

func TestRunShowsEveryConfiguredRepo(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.addRepo("webapp", t.TempDir())
	d.addRepo("api", t.TempDir())
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))

	d.StartBeforeTheEvents()
	require.Eventually(t, func() bool { return strings.Contains(d.LoopOut(), " start: ") }, 15*time.Second, 10*time.Millisecond)
	out := d.LoopOut()
	d.Stop()

	repos := regexp.MustCompile(`(?m)^REPOS    (.*)$`).FindStringSubmatch(out)
	require.Len(t, repos, 2)
	assert.ElementsMatch(t, []string{"dispatch", "webapp", "api"}, strings.Split(repos[1], "  "))
}

func TestRunShowsTheLabelAnInReviewWorkerKeepsWhileItsPRCannotBeRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{})
	// An earlier run read the PR as ready for review, and this start can't read it.
	require.NoError(t, d.Tools().Multiplexer.RenameWorkspace(context.Background(), "w-ENG-1", "👀 ENG-1"))
	d.GitHub.FailRead(errors.New("exit status 1"))

	out := d.RunOnTerminal()

	assert.Contains(t, out, "👀 ENG-1  ticket ENG-1\n")
}

func TestRunShowsWhichHistoryARestartPickedUpInATerminal(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.StartBeforeTheEvents()
	require.Eventually(t, func() bool { return len(d.Workers.Launched()) == 1 }, 15*time.Second, 10*time.Millisecond)
	d.Stop()

	out := sgr.ReplaceAllString(d.RunOnTerminal(), "")

	history := regexp.MustCompile(`(?m)^ HISTORY  (\d+) receipts? from (\S+)$`).FindStringSubmatch(out)
	require.Len(t, history, 3)
	assert.NotEqual(t, "0", history[1])
	assert.NotEqual(t, "no", history[2])
}
