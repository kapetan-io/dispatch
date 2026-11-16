//go:build claude

package harness_test

// Package exception: these tests run a real, logged-in Claude Code under herdr-core, which no fake stands in for.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/dispatchdb"
	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/harness"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
	"github.com/kapetan-io/dispatch/internal/journal"
	"github.com/kapetan-io/dispatch/internal/lineartest"
	"github.com/kapetan-io/dispatch/internal/loop"
	"github.com/kapetan-io/dispatch/internal/pullrequesttest"
	"github.com/kapetan-io/dispatch/internal/sessionlog"
	"github.com/kapetan-io/dispatch/internal/treehouse"
	"github.com/kapetan-io/dispatch/internal/workers"
)

const turnTimeout = 3 * time.Minute

// backgroundTask is the sleep the Stop scenario starts in the background.
const backgroundTask = "sleep 601"

// liveTicket holds the lease on the worker's tree. Dispatch stores a session's Stop only in the
// worker row naming its tree, and no launch inside `dispatch run` makes that row here, because the
// test starts the worker itself. The first pool read of a fresh state directory makes one for
// every tree leased to a ticket id.
const liveTicket = "LIVE-1"

// liveWorker is a real Claude Code started the way dispatch starts a worker on the Herdr app host,
// in a first-run folder. The app host runs the program directly, so no shell command line sits above
// Claude for the launcher's Unrun check to misread (see CLAUDE.md).
type liveWorker struct {
	session     *herdrtest.CoreSession
	core        *herdr.Core
	pane        string
	id          string
	cwd         string
	log         string
	trace       string
	seenSession string
	state       string
	config      string
	program     herdr.Program
	// rows is the worker rows in the dispatch.db the running dispatch opened. Only one process may
	// open the file, and no dispatch command prints a Stop record's fields or a closing message's transcript.
	rows *workers.Store
}

// hookCall is one run of a plugin hook: the dispatch subcommand it ran and the input Claude Code gave it.
type hookCall struct {
	Hook  string          `json:"hook"`
	Input json.RawMessage `json:"input"`
}

func (h hookCall) field(name string) string {
	var in map[string]any
	_ = json.Unmarshal(h.Input, &in)
	if v, ok := in[name].(string); ok {
		return v
	}
	return ""
}

// startLiveWorker starts the worker and returns once Claude took the kickoff, which asks it to write
// its messaging socket to a file. A real dispatch loop runs over a scratch state directory, and the
// plugin's hooks reach it through the logging program, so a test sees both what Claude Code sent and
// what dispatch did with it.
func startLiveWorker(t *testing.T) *liveWorker {
	t.Helper()
	w := &liveWorker{}
	// Runs after the scratch core's own cleanup, which removes its socket whether or not the core stopped.
	t.Cleanup(func() {
		left, err := herdrtest.CoreLeftovers(os.Getpid())
		require.NoError(t, err)
		assert.Empty(t, left)
		if w.id != "" {
			// The session id is on Claude's command line, and the Stop scenario's sleep runs a number of seconds nothing else does.
			assert.True(t, gone(w.id))
			assert.True(t, gone(backgroundTask))
			// The scratch core's session name is in the path of the holder process it starts.
			assert.True(t, gone(w.session.Env.Get("HC_SESSION")))
		}
	})
	ctx := context.Background()
	// Backstop for the updater, which once repointed the maintainer's ~/.local/bin/claude at a
	// directory under the scratch core's XDG directories that the test then deleted.
	launcher := filepath.Join(os.Getenv("HOME"), ".local", "bin", "claude")
	before := launcherState(launcher)
	t.Cleanup(func() { assert.Equal(t, before, launcherState(launcher)) })
	// Cleanups run last in first out, so the transcript removal registered here runs after the core stops Claude.
	repo, tree := fixedCheckout(t)
	w.session = herdrtest.StartCoreSession(t)
	w.cwd = tree
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	w.log = filepath.Join(root, "hooks.log")
	w.trace = filepath.Join(root, "kickoff.trace")
	// macOS caps a socket path at about 100 bytes, which the test's temp dir can pass.
	w.state, err = os.MkdirTemp("/tmp", "dsp")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(w.state) })
	// Local tickets give the ask scenarios a ticket to build without a Linear account.
	config := filepath.Join(root, "config.toml")
	w.config = config
	require.NoError(t, os.WriteFile(config, []byte(fmt.Sprintf("[repos.live]\npath = %q\n\n[history]\ndir = %q\n\n[plugins.herdr]\nhost = \"app\"\n\n[plugins.local]\n", repo, filepath.Join(w.state, "events"))), 0o644))

	build := func(pkg, dir, name string) string {
		require.NoError(t, os.Mkdir(filepath.Join(root, dir), 0o755))
		bin := filepath.Join(root, dir, name)
		out, err := exec.Command("go", "build", "-o", bin, pkg).CombinedOutput()
		t.Log(string(out))
		require.NoError(t, err)
		return bin
	}
	// The claude shim and the hooks run the real dispatch over the scratch state, the hooks through the logging
	// program. The real one is not named dispatch, because it puts its own directory first on the worker's PATH.
	bin := build("github.com/kapetan-io/dispatch/internal/herdrtest/driftdispatch", "real", "driftdispatch")
	stub := filepath.Dir(build("github.com/kapetan-io/dispatch/internal/herdrtest/hooklog", "stub", "dispatch"))
	plugin, err := filepath.Abs("../..")
	require.NoError(t, err)

	w.id = newSessionID(t)
	w.program = harness.ClaudeCode{}.Program(harness.Start{Model: "haiku", PluginRoot: plugin, Session: w.id})
	w.program.PathFirst = append([]string{stub}, w.program.PathFirst...)
	w.program.Env = map[string]string{
		"HOOKLOG": w.log, "HOOKLOG_REAL": bin, "DISABLE_AUTOUPDATER": "1",
		"DISPATCH_DRIFT_CONFIG": config, "DISPATCH_DRIFT_STATE": w.state, "DISPATCH_DRIFT_RESTORE_XDG": "1",
	}
	// The scratch core hands its terminals scratch XDG directories, which the claude shim puts back.
	for _, name := range []string{"XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		if value, ok := os.LookupEnv(name); ok {
			w.program.Env["DISPATCH_DRIFT_REAL_"+name] = value
		}
	}
	w.core = w.session.Core().WithKinds("claude").WithTrace(w.trace).WithDispatchBin(bin)

	loopCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	opened := make(chan *dispatchdb.File, 1)
	go func() {
		done <- dispatch.Run(loopCtx, []string{"run"}, dispatch.Options{
			ConfigPath: config, StateDir: w.state, Multiplexer: w.core, LinearKey: lineartest.Key, Linear: lineartest.New(nil).Connect,
			PullRequests: pullrequesttest.New(&journal.Journal{}), Stdout: os.Stderr, Stderr: os.Stderr,
			Database: dispatchdb.Hooks{Opened: func(file *dispatchdb.File) { opened <- file }},
		})
	}()
	t.Cleanup(func() {
		stop()
		<-done
	})
	require.Eventually(t, func() bool {
		resp, err := loop.Ask(ctx, w.state, loop.Request{Op: loop.OpIdle})
		return err == nil && !resp.Busy
	}, 30*time.Second, 50*time.Millisecond)
	w.rows = workers.New(<-opened)

	space, err := w.core.OpenWorkspace(ctx, herdr.NewWorkspace{Path: w.cwd})
	require.NoError(t, err)
	started, err := w.core.StartWorker(ctx, herdr.Worker{Workspace: space, Dir: w.cwd, Label: "live", Name: "live", PluginRoot: plugin, Program: w.program})
	require.NoError(t, err)
	w.pane = started.Pane

	sessionFile := filepath.Join(root, "socket")
	err = w.core.Kickoff(ctx, w.pane, "Run this shell command, then reply with the word done: printenv CLAUDE_CODE_SESSION_ID > "+sessionFile, w.program.Startup)
	if err != nil {
		t.Logf("terminal:\n%s\nscreen:\n%s", w.session.Run(t, "get", w.pane), w.screen(t))
	}
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(sessionFile)
		w.seenSession = strings.TrimSpace(string(b))
		return err == nil && w.seenSession != ""
	}, turnTimeout, 500*time.Millisecond)
	// A Bash tool sees the session id the launch chose, which is what followers send by.
	require.Equal(t, w.id, w.seenSession)
	return w
}

func newSessionID(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("uuidgen").Output()
	require.NoError(t, err)
	return strings.ToLower(strings.TrimSpace(string(out)))
}

func (w *liveWorker) hooks(t *testing.T) []hookCall {
	t.Helper()
	raw, err := os.ReadFile(w.log)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var calls []hookCall
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var call hookCall
		require.NoError(t, json.Unmarshal([]byte(line), &call))
		calls = append(calls, call)
	}
	return calls
}

// await returns the skip-th call (from 0) of hook whose hook_event_name is event and that where accepts, once it arrives.
func (w *liveWorker) await(t *testing.T, hook, event string, skip int, within time.Duration, where ...func(hookCall) bool) hookCall {
	t.Helper()
	var found hookCall
	require.True(t, poll(within, func() bool {
		n := 0
		for _, call := range w.hooks(t) {
			if call.Hook == hook && call.field("hook_event_name") == event && (len(where) == 0 || where[0](call)) {
				if n == skip {
					found = call
					return true
				}
				n++
			}
		}
		return false
	}))
	return found
}

// poll checks cond on the test's goroutine, where a failed require inside it still stops the test.
func poll(within time.Duration, cond func() bool) bool {
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		if cond() {
			return true
		}
	}
	return false
}

func (w *liveWorker) count(t *testing.T, hook, event string) int {
	t.Helper()
	n := 0
	for _, call := range w.hooks(t) {
		if call.Hook == hook && call.field("hook_event_name") == event {
			n++
		}
	}
	return n
}

func (w *liveWorker) agent(t *testing.T) herdr.Agent {
	t.Helper()
	agent, ok, err := w.core.AgentAt(context.Background(), w.pane)
	require.NoError(t, err)
	require.True(t, ok)
	return agent
}

func (w *liveWorker) status(t *testing.T, want string, within time.Duration) {
	t.Helper()
	var last herdr.Agent
	reached := poll(within, func() bool {
		agent, found, err := w.core.AgentAt(context.Background(), w.pane)
		last = agent
		return err == nil && found && agent.Status == want
	})
	if !reached {
		t.Logf("last status %q\nscreen:\n%s", last.Status, w.screen(t))
	}
	require.True(t, reached)
}

// typeLine sends text through hc, not Prompt, so herdr gets no say in whether the worker is ready for it.
func (w *liveWorker) typeLine(t *testing.T, text string) {
	t.Helper()
	w.session.Run(t, "send", w.pane, text)
}

// row is the worker row naming the worker's tree, with its Stop records and closing messages.
// It is empty until the first pool read made it.
func (w *liveWorker) row(t *testing.T) workers.Worker {
	t.Helper()
	row, _, err := w.rows.OnTree(w.cwd)
	require.NoError(t, err)
	return row
}

func (w *liveWorker) screen(t *testing.T) string {
	t.Helper()
	return w.session.Run(t, "read", w.pane)
}

// TestClaudeCodeLaunchesAWorkerAndTakesTheKickoff fails when a worker's flags or the kickoff typed
// into the input box stop working on the installed Claude Code. The trust and bypass-permissions
// dialogs are not checked, because the real Claude config has accepted them already.
func TestClaudeCodeLaunchesAWorkerAndTakesTheKickoff(t *testing.T) {
	w := startLiveWorker(t)

	trace, err := os.ReadFile(w.trace)
	require.NoError(t, err)
	assert.Contains(t, string(trace), "typing")
	assert.Equal(t, "claude", w.agent(t).Kind)
}

// TestClaudeCodeRunsThePluginsHooksAndAddsSessionStartOutputToTheSession fails when the plugin's
// hooks.json stops loading or Claude Code stops showing SessionStart output to the model.
func TestClaudeCodeRunsThePluginsHooksAndAddsSessionStartOutputToTheSession(t *testing.T) {
	w := startLiveWorker(t)

	start := w.await(t, "ticket-hint", "SessionStart", 0, time.Minute)
	assert.Equal(t, w.id, start.field("session_id"))

	w.status(t, "done", turnTimeout)
	answer := filepath.Join(t.TempDir(), "word")
	w.typeLine(t, "Run this shell command, replacing WORD with the secret word from the session note: printf %s WORD > "+answer)
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(answer)
		return err == nil && string(b) == "PINEAPPLE7"
	}, turnTimeout, 500*time.Millisecond)
}

// TestClaudeCodeStopHookCarriesTheSessionTheTreeAndTheBackgroundTasks fails when the Stop hook
// input loses session_id, cwd or background_tasks, or stops listing a running task as running.
func TestClaudeCodeStopHookCarriesTheSessionTheTreeAndTheBackgroundTasks(t *testing.T) {
	w := startLiveWorker(t)

	// The kickoff turn ended with no background task.
	idle := w.await(t, "record-stop", "Stop", 0, turnTimeout)
	stop, cwd, ok := harness.ClaudeCode{}.Stop(idle.Input)
	require.True(t, ok)
	assert.Equal(t, w.id, stop.Session)
	assert.Equal(t, w.cwd, cwd)
	assert.True(t, stop.Reported)
	assert.Equal(t, 0, stop.Running)

	// dispatch's own record of that Stop is what the worker's label reads.
	var record workers.Record
	require.True(t, poll(10*time.Second, func() bool {
		var found bool
		record, found = w.row(t).Stop(w.id)
		return found
	}))
	assert.True(t, record.Reported)
	assert.False(t, record.Busy(time.Now()))

	// The same Stop left the closing message in the worker row, with the transcript path the
	// harness reads to tell whether the session took a message after it.
	closing, found := w.row(t).ClosingAt(w.id, record.At.UnixMilli())
	require.True(t, found)
	assert.Equal(t, stop.Message, closing.Message)
	assert.NotEmpty(t, closing.Transcript)

	w.typeLine(t, "Start `"+backgroundTask+"` as a background task with the Bash tool's run_in_background option, then reply with the word started.")
	busy := w.await(t, "record-stop", "Stop", 1, turnTimeout)
	stop, _, ok = harness.ClaudeCode{}.Stop(busy.Input)
	require.True(t, ok)
	assert.Equal(t, 1, stop.Running)
	require.True(t, poll(10*time.Second, func() bool {
		var found bool
		record, found = w.row(t).Stop(w.id)
		return found && record.Running == 1
	}))
	assert.True(t, record.Busy(time.Now()))
	closing, found = w.row(t).ClosingAt(w.id, record.At.UnixMilli())
	require.True(t, found)
	assert.Equal(t, stop.Message, closing.Message)
	assert.Contains(t, strings.ToLower(closing.Message), "started")
}

// TestClaudeCodeTakesATypedAndADeliveredMessageAsMessagesAfterTheStop fails when a message typed into
// a worker, or one dispatch delivers, stops adding a time to the session log, or stops showing in the
// transcript the harness reads as a message after the turn that ended before it.
func TestClaudeCodeTakesATypedAndADeliveredMessageAsMessagesAfterTheStop(t *testing.T) {
	w := startLiveWorker(t)
	claude := harness.ClaudeCode{Sessions: harness.SessionsDir(environment.Process())}
	var closing workers.Closing
	require.True(t, poll(time.Minute, func() bool {
		row := w.row(t)
		record, found := row.Stop(w.id)
		if !found {
			return false
		}
		closing, found = row.ClosingAt(w.id, record.At.UnixMilli())
		return found
	}))
	took, err := claude.TookMessageAfter(closing.Transcript, closing.At)
	require.NoError(t, err)
	assert.False(t, took)

	w.typeLine(t, "Reply with the word typed.")
	require.True(t, poll(time.Minute, func() bool {
		log, err := sessionlog.Read(w.state, w.cwd, w.id)
		return err == nil && len(log.Messages) == 1
	}))
	w.status(t, "done", turnTimeout)
	took, err = claude.TookMessageAfter(closing.Transcript, closing.At)
	require.NoError(t, err)
	assert.True(t, took)

	require.NoError(t, claude.Deliver(context.Background(), w.id, "dispatch event: reply with the word received."))
	require.True(t, poll(time.Minute, func() bool {
		log, err := sessionlog.Read(w.state, w.cwd, w.id)
		return err == nil && len(log.Messages) == 2
	}))
}

// TestClaudeCodeFiresNotificationWhenIdleAndUserPromptSubmitWhenTextIsTyped fails when the hooks
// that wake a worker's loop stop firing, or stop carrying the typed prompt that dispatch logs.
func TestClaudeCodeFiresNotificationWhenIdleAndUserPromptSubmitWhenTextIsTyped(t *testing.T) {
	w := startLiveWorker(t)

	// idle_prompt fires about 60 seconds after a turn ends with nobody typing.
	idle := w.await(t, "wake", "Notification", 0, turnTimeout, func(call hookCall) bool { return call.field("notification_type") == "idle_prompt" })
	assert.Equal(t, w.id, idle.field("session_id"))
	assert.Equal(t, w.cwd, idle.field("cwd"))

	before := w.count(t, "wake", "UserPromptSubmit")
	w.typeLine(t, "Reply with the word typed.")
	typed := w.await(t, "wake", "UserPromptSubmit", before, time.Minute)
	prompt, cwd, ok := harness.ClaudeCode{}.Prompt(typed.Input)
	require.True(t, ok)
	assert.Equal(t, w.id, prompt.Session)
	assert.Equal(t, w.cwd, cwd)
	assert.Equal(t, "Reply with the word typed.", prompt.Text)

	// A slash command reaches the running dispatch through the same hook, which logs it as typed by a person.
	w.status(t, "done", turnTimeout)
	w.typeLine(t, "/dispatch:principles")
	require.True(t, poll(time.Minute, func() bool {
		log, err := sessionlog.Read(w.state, w.cwd, w.id)
		return err == nil && len(log.Commands) == 1 && log.Commands[0].Name == "dispatch:principles" && log.Commands[0].By == sessionlog.ByPerson
	}))
}

// TestHerdrReadsARealWorkersStatusAndSession fails when herdr stops reading a real Claude's
// screen as working during a turn and while a typed message runs, done after it, or blocked at a
// question popup, or reports a session id other than the one the Stop hook carries.
func TestHerdrReadsARealWorkersStatusAndSession(t *testing.T) {
	w := startLiveWorker(t)

	// The app host calls a turn that ended with nobody watching "done", where the herdr TUI says "idle".
	w.status(t, "done", turnTimeout)
	assert.Equal(t, w.await(t, "record-stop", "Stop", 0, time.Minute).field("session_id"), w.agent(t).Session)

	// A message the user types runs as a turn, so herdr must show it working.
	w.typeLine(t, "Run `sleep 20`, then reply with the word done.")
	w.status(t, "working", 30*time.Second)
	w.status(t, "done", turnTimeout)

	w.typeLine(t, "Use the AskUserQuestion tool to ask me one question, which color I want, with the options Crimson and Cobalt. Do nothing else until I answer.")
	w.status(t, "blocked", turnTimeout)
	assert.Contains(t, w.screen(t), "Enter to select")
}

// TestClaudeCodeTakesAMessageWrittenToItsMessagingSocket fails when the frame Deliver writes stops
// reaching the session as a prompt. It checks the frame, not `dispatch watch`, which forwards what
// it is told to the same socket. The sender gets no receipt, so arrival is seen in the prompt the
// session's hook receives.
func TestClaudeCodeTakesAMessageWrittenToItsMessagingSocket(t *testing.T) {
	w := startLiveWorker(t)
	w.status(t, "done", turnTimeout)
	before := w.count(t, "wake", "UserPromptSubmit")

	require.NoError(t, harness.ClaudeCode{Sessions: harness.SessionsDir(environment.Process())}.Deliver(context.Background(), w.id, "dispatch event: reply with the word received."))

	got := w.await(t, "wake", "UserPromptSubmit", before, time.Minute)
	assert.Contains(t, got.field("prompt"), "dispatch event: reply with the word received.")
}

// TestClaudeCodeResumedWithASessionIdReportsItsStopUnderThatId fails when a Claude started with
// `--resume <id>` reports its Stop under another session id. The worker row's kickoff record holds
// the id dispatch chose at the launch, so a resumed worker's Stop must carry the same one.
func TestClaudeCodeResumedWithASessionIdReportsItsStopUnderThatId(t *testing.T) {
	w := startLiveWorker(t)
	ctx := context.Background()
	first := w.await(t, "record-stop", "Stop", 0, turnTimeout).field("session_id")
	require.NotEmpty(t, first)

	// A core restart ends the worker's terminal with reason server_stop, the one loss StartWorker resumes from.
	w.session.Restart(t)
	require.True(t, poll(10*time.Second, func() bool {
		_, found, err := w.core.AgentAt(ctx, w.pane)
		return err == nil && !found
	}))
	before := w.count(t, "record-stop", "Stop")
	plugin, err := filepath.Abs("../..")
	require.NoError(t, err)
	space, err := w.core.OpenWorkspace(ctx, herdr.NewWorkspace{Path: w.cwd})
	require.NoError(t, err)
	started, err := w.core.StartWorker(ctx, herdr.Worker{Workspace: space, Dir: w.cwd, Label: "live", Name: "live", PluginRoot: plugin, Program: w.program})
	require.NoError(t, err)
	w.pane = started.Pane
	require.NoError(t, w.core.Kickoff(ctx, w.pane, "Reply with the word resumed.", w.program.Startup))

	second := w.await(t, "record-stop", "Stop", before, turnTimeout)
	// The resumed Claude is still running, with the flag and the id on its command line.
	require.NoError(t, exec.Command("pgrep", "-f", "--", "--resume "+first).Run())
	assert.Equal(t, first, second.field("session_id"))
	assert.Equal(t, w.cwd, second.field("cwd"))
}

// askTimeout covers a build turn, which loads skills and reads the ticket before it asks.
const askTimeout = 8 * time.Minute

// productCallTicket is a ticket whose limit no design states, which the build skill reserves to the user.
const productCallTicket = "## What it delivers\n\n* A failed webhook delivery is retried at most N times, then dropped.\n\nNo design says what N is, and nothing in the repo sets it."

// fileTicket files a local ticket in Todo, so no rule launches a worker on it, and returns its id.
func (w *liveWorker) fileTicket(t *testing.T, title, description string) string {
	t.Helper()
	var out strings.Builder
	require.NoError(t, dispatch.Run(context.Background(), []string{"ticket", "create", "local", "--repo", "live", "--title", title, "--description", description},
		dispatch.Options{ConfigPath: w.config, StateDir: w.state, Stdout: &out, Stderr: os.Stderr}))
	return strings.TrimSpace(out.String())
}

func (w *liveWorker) ticketStatus(t *testing.T, id string) string {
	t.Helper()
	var out strings.Builder
	require.NoError(t, dispatch.Run(context.Background(), []string{"ticket", "show", id},
		dispatch.Options{ConfigPath: w.config, StateDir: w.state, Stdout: &out, Stderr: os.Stderr}))
	for _, line := range strings.Split(out.String(), "\n") {
		if rest, ok := strings.CutPrefix(line, "status"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// asked waits for the worker's session log to hold an ask. The log is kept under the session id
// `dispatch ask` read from the harness, and read here under the id the launch chose and the hooks
// report, so finding the ask proves the two agree, which the 🔴 label relies on.
func (w *liveWorker) asked(t *testing.T, within time.Duration) sessionlog.Log {
	t.Helper()
	var log sessionlog.Log
	reached := poll(within, func() bool {
		var err error
		log, err = sessionlog.Read(w.state, w.cwd, w.id)
		return err == nil && len(log.Asks) > 0
	})
	if !reached {
		t.Logf("screen:\n%s", w.screen(t))
	}
	require.True(t, reached)
	return log
}

// TestClaudeCodeBuildWorkerRunsDispatchAskOnATicketThatNeedsAProductCall fails when a build worker
// that needs the user's call ends its turn without running `dispatch ask`, or moves its ticket.
func TestClaudeCodeBuildWorkerRunsDispatchAskOnATicketThatNeedsAProductCall(t *testing.T) {
	w := startLiveWorker(t)
	w.status(t, "done", turnTimeout)
	id := w.fileTicket(t, "Cap retries of failed webhook deliveries", productCallTicket)
	stops := w.count(t, "record-stop", "Stop")
	before, err := sessionlog.Read(w.state, w.cwd, w.id)
	require.NoError(t, err)

	w.typeLine(t, "/dispatch:build "+id)
	log := w.asked(t, askTimeout)
	assert.Len(t, log.Asks, 1)

	// The ask ran in the turn the build started, before that turn ended. The hook log gets the Stop
	// before the running dispatch appends its turn line, so the turn line is polled for.
	assert.Equal(t, w.id, w.await(t, "record-stop", "Stop", stops, turnTimeout).field("session_id"))
	require.True(t, poll(time.Minute, func() bool {
		log, err = sessionlog.Read(w.state, w.cwd, w.id)
		return err == nil && len(log.Turns) > len(before.Turns)
	}))
	assert.Greater(t, log.Turns[len(before.Turns)].At, log.Asks[0].At)
	assert.True(t, log.AskOpen())
	assert.Equal(t, "Todo", w.ticketStatus(t, id))
}

// TestClaudeCodeWorkerWaitingOnTheUserRunsDispatchAskWhenAPRNudgeArrives fails when a worker that
// asked in chat without `dispatch ask` doesn't run it once a PR nudge arrives.
func TestClaudeCodeWorkerWaitingOnTheUserRunsDispatchAskWhenAPRNudgeArrives(t *testing.T) {
	w := startLiveWorker(t)
	w.status(t, "done", turnTimeout)
	id := w.fileTicket(t, "Cap retries of failed webhook deliveries", productCallTicket)
	const pr = "https://github.com/kapetan-io/dispatch-claude-live/pull/1"
	stops := w.count(t, "record-stop", "Stop")

	// The worker's last turn asked in chat, as a worker that forgot `dispatch ask` does.
	w.typeLine(t, "You are the dispatch build worker for "+id+", In Review with PR "+pr+". Ask me in chat whether a failed webhook delivery is retried at most 3 or 5 times, then end your turn without running any command.")
	w.await(t, "record-stop", "Stop", stops, turnTimeout)
	log, err := sessionlog.Read(w.state, w.cwd, w.id)
	require.NoError(t, err)
	require.Empty(t, log.Asks)
	w.status(t, "done", turnTimeout)

	// rules/texts.ts PR_NUDGE, the nudge for a PR that needs work.
	w.typeLine(t, "/dispatch:pr-respond PR "+pr+" for "+id+" has a failing check. Load the dispatch:pr-respond skill with the Skill tool, then handle it, and push.")
	w.asked(t, askTimeout)
	assert.Equal(t, w.id, w.await(t, "record-stop", "Stop", stops+1, turnTimeout).field("session_id"))
	// Still in Todo, where fileTicket put it: the worker asked without moving the ticket.
	assert.Equal(t, "Todo", w.ticketStatus(t, id))
}

// TestClaudeCodeFinishedBackgroundTaskLeavesTheAskOpen fails when the message Claude Code hands a
// session for its own finished background task stops reading as a task notice, which would end the
// worker's ask without the user answering.
func TestClaudeCodeFinishedBackgroundTaskLeavesTheAskOpen(t *testing.T) {
	w := startLiveWorker(t)
	w.status(t, "done", turnTimeout)

	w.typeLine(t, "Start `sleep 20` as a background task with the Bash tool's run_in_background option. Then run the shell command `\"${DISPATCH_BIN:-dispatch}\" ask`, then reply with the word waiting and end your turn.")
	log := w.asked(t, turnTimeout)
	asked := log.Asks[0].At

	require.True(t, poll(turnTimeout, func() bool {
		log, err := sessionlog.Read(w.state, w.cwd, w.id)
		if err != nil {
			return false
		}
		for _, message := range log.Messages {
			if message.Task && message.At > asked {
				return true
			}
		}
		return false
	}))
	w.status(t, "done", turnTimeout)
	log, err := sessionlog.Read(w.state, w.cwd, w.id)
	require.NoError(t, err)
	assert.Len(t, log.Asks, 1)
	assert.True(t, log.AskOpen())
}

// launcherState names what the maintainer's claude launcher points at, or its size and time when it is a file.
func launcherState(path string) string {
	if target, err := os.Readlink(path); err == nil {
		return target
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err.Error()
	}
	return fmt.Sprint(info.Size(), info.ModTime())
}

// gone reports whether no process has pattern on its command line within ten seconds. A pgrep that fails
// for any reason but finding nothing counts as not gone.
func gone(pattern string) bool {
	return poll(10*time.Second, func() bool {
		var exit *exec.ExitError
		err := exec.Command("pgrep", "-f", pattern).Run()
		return errors.As(err, &exit) && exit.ExitCode() == 1
	})
}

// fixedCheckout returns the same repo on every run, and the tree of its treehouse pool that
// liveTicket leases and never returns. Claude records trust per folder in the real ~/.claude.json,
// so a fresh folder per run would add an entry each time. The run's transcripts go when the test
// ends. It points TREEHOUSE_ROOT at the repo's pool, which the dispatch under test reads too.
func fixedCheckout(t *testing.T) (repo, tree string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(os.TempDir())
	require.NoError(t, err)
	root := filepath.Join(base, "dispatch-claude-live")
	repo = filepath.Join(root, "repo")
	t.Setenv("TREEHOUSE_ROOT", filepath.Join(root, "pool"))
	if _, err := os.Stat(filepath.Join(repo, "treehouse.toml")); err != nil {
		require.NoError(t, os.RemoveAll(root))
		require.NoError(t, os.MkdirAll(repo, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(repo, "treehouse.toml"), []byte("max_trees = 1\n"), 0o644))
		for _, args := range [][]string{
			{"-C", repo, "init", "-q", "-b", "main"},
			{"-C", repo, "-c", "user.name=drift", "-c", "user.email=drift@example.com", "commit", "-q", "--allow-empty", "-m", "init"},
		} {
			out, err := exec.Command("git", args...).CombinedOutput()
			t.Log(string(out))
			require.NoError(t, err)
		}
	}
	pool := treehouse.NewCLI("treehouse").WithEnv(environment.Process())
	trees, err := pool.Trees(context.Background(), repo)
	require.NoError(t, err)
	for _, held := range trees {
		if held.LeaseHolder == liveTicket {
			tree = held.Path
		}
	}
	if tree == "" {
		leased, err := pool.Lease(context.Background(), repo, liveTicket)
		require.NoError(t, err)
		tree = leased.Path
	}
	// A worker may write or commit in the tree, which every run reuses.
	head, err := exec.Command("git", "-C", tree, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	t.Cleanup(func() {
		for _, args := range [][]string{{"-C", tree, "reset", "-q", "--hard", strings.TrimSpace(string(head))}, {"-C", tree, "clean", "-qfdx"}} {
			out, err := exec.Command("git", args...).CombinedOutput()
			assert.NoError(t, err, string(out))
		}
	})
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	// Claude names a folder's transcript directory by turning every character but letters and digits into a dash.
	projects := filepath.Join(home, ".claude", "projects", regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(tree, "-"))
	t.Cleanup(func() { _ = os.RemoveAll(projects) })
	return repo, tree
}
