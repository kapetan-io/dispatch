//go:build claude

package harness_test

// Package exception: these tests run a real, logged-in Claude Code under herdr-core, which no fake stands in for.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/harness"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
)

const turnTimeout = 3 * time.Minute

// liveWorker is a real Claude Code started the way dispatch starts a worker on the Herdr app host,
// in a first-run folder. The app host runs the program directly, so no shell command line sits above
// Claude for the launcher's Unrun check to misread (see CLAUDE.md).
type liveWorker struct {
	session *herdrtest.CoreSession
	core    *herdr.Core
	pane    string
	id      string
	cwd     string
	log     string
	trace   string
	socket  string
	program herdr.Program
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
// its messaging socket to a file.
func startLiveWorker(t *testing.T) *liveWorker {
	t.Helper()
	w := &liveWorker{}
	// Runs after the scratch core's own cleanup, which removes its socket whether or not the core stopped.
	t.Cleanup(func() {
		left, err := herdrtest.CoreLeftovers(os.Getpid())
		require.NoError(t, err)
		assert.Empty(t, left)
		if w.id != "" {
			assert.Error(t, exec.Command("pgrep", "-f", w.id).Run())
		}
	})
	ctx := context.Background()
	// Backstop for DISABLE_AUTOUPDATER below. Under the scratch core's XDG directories the updater once
	// repointed the maintainer's ~/.local/bin/claude at a directory the test then deleted.
	launcher := filepath.Join(os.Getenv("HOME"), ".local", "bin", "claude")
	before, _ := os.Readlink(launcher)
	t.Cleanup(func() {
		after, _ := os.Readlink(launcher)
		if before != "" && after != before {
			_ = os.Remove(launcher)
			_ = os.Symlink(before, launcher)
		}
		assert.Equal(t, before, after)
	})
	w.session = herdrtest.StartCoreSession(t)

	_, w.cwd = herdrtest.NewCheckout(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	w.log = filepath.Join(root, "hooks.log")
	w.trace = filepath.Join(root, "kickoff.trace")
	build := func(pkg, dir string) string {
		require.NoError(t, os.Mkdir(filepath.Join(root, dir), 0o755))
		bin := filepath.Join(root, dir, "dispatch")
		out, err := exec.Command("go", "build", "-o", bin, pkg).CombinedOutput()
		require.NoError(t, err, string(out))
		return bin
	}
	// The plugin's claude shim runs the real dispatch, while its hooks find the logging one first on PATH.
	bin := build("github.com/kapetan-io/dispatch/cmd/dispatch", "real")
	stub := filepath.Dir(build("github.com/kapetan-io/dispatch/internal/herdrtest/hooklog", "stub"))
	plugin, err := filepath.Abs("../..")
	require.NoError(t, err)

	w.id = newSessionID(t)
	w.program = harness.ClaudeCode{}.Program(harness.Start{Model: "haiku", PluginRoot: plugin, Session: w.id})
	w.program.PathFirst = append([]string{stub}, w.program.PathFirst...)
	w.program.Env = map[string]string{"HOOKLOG": w.log, "DISABLE_AUTOUPDATER": "1"}
	w.core = w.session.Core().WithKinds("claude").WithTrace(w.trace).WithDispatchBin(bin)

	space, err := w.core.OpenWorkspace(ctx, herdr.NewWorkspace{Path: w.cwd})
	require.NoError(t, err)
	started, err := w.core.StartWorker(ctx, herdr.Worker{Workspace: space, Dir: w.cwd, Label: "live", Name: "live", PluginRoot: plugin, Program: w.program})
	require.NoError(t, err)
	w.pane = started.Pane

	socketFile := filepath.Join(root, "socket")
	if err := w.core.Kickoff(ctx, w.pane, "Run this shell command, then reply with the word done: printenv CLAUDE_CODE_MESSAGING_SOCKET > "+socketFile, w.program.Startup); err != nil {
		t.Fatalf("%v\nterminal:\n%s\nscreen:\n%s", err, w.session.Run(t, "get", w.pane), w.screen(t))
	}
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(socketFile)
		w.socket = strings.TrimSpace(string(b))
		return err == nil && w.socket != ""
	}, turnTimeout, 500*time.Millisecond)
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

// await returns the skip-th call (from 0) of hook whose hook_event_name is event, once it arrives.
func (w *liveWorker) await(t *testing.T, hook, event string, skip int, within time.Duration) hookCall {
	t.Helper()
	var found hookCall
	require.True(t, poll(within, func() bool {
		n := 0
		for _, call := range w.hooks(t) {
			if call.Hook == hook && call.field("hook_event_name") == event {
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
	if !poll(within, func() bool {
		agent, found, err := w.core.AgentAt(context.Background(), w.pane)
		last = agent
		return err == nil && found && agent.Status == want
	}) {
		t.Fatalf("herdr never showed %q, last saw %q\nscreen:\n%s", want, last.Status, w.screen(t))
	}
}

// typeLine sends text through hc, not Prompt, so herdr gets no say in whether the worker is ready for it.
func (w *liveWorker) typeLine(t *testing.T, text string) {
	t.Helper()
	w.session.Run(t, "send", w.pane, text)
}

func (w *liveWorker) screen(t *testing.T) string {
	t.Helper()
	return w.session.Run(t, "read", w.pane)
}

// TestClaudeCodeLaunchesAWorkerAndTakesTheKickoff fails when a worker's flags, its first-run
// folder dialog or the kickoff typed into the input box stop working on the installed Claude Code.
func TestClaudeCodeLaunchesAWorkerAndTakesTheKickoff(t *testing.T) {
	w := startLiveWorker(t)

	trace, err := os.ReadFile(w.trace)
	require.NoError(t, err)
	// The folder is new to Claude, so it asks to trust it before it takes the kickoff.
	assert.Regexp(t, `(?i)trust this folder`, string(trace))
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

	w.typeLine(t, "Start `sleep 600` as a background task with the Bash tool's run_in_background option, then reply with the word started.")
	busy := w.await(t, "record-stop", "Stop", 1, turnTimeout)
	stop, _, ok = harness.ClaudeCode{}.Stop(busy.Input)
	require.True(t, ok)
	assert.Equal(t, 1, stop.Running)
}

// TestClaudeCodeFiresNotificationWhenIdleAndUserPromptSubmitWhenTextIsTyped fails when the hooks
// that wake a worker's loop stop firing, or stop carrying the typed prompt.
func TestClaudeCodeFiresNotificationWhenIdleAndUserPromptSubmitWhenTextIsTyped(t *testing.T) {
	w := startLiveWorker(t)

	// idle_prompt fires about 60 seconds after a turn ends with nobody typing.
	idle := w.await(t, "wake", "Notification", 0, turnTimeout)
	assert.Equal(t, "idle_prompt", idle.field("notification_type"))
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
	// A nudge into an open popup would answer the question, so the adapter refuses it.
	assert.ErrorIs(t, w.core.Prompt(context.Background(), w.pane, "dispatch nudge"), herdr.ErrAgentBlocked)
}

// TestClaudeCodeTakesAMessageDeliveredToItsSession fails when the frame Deliver writes stops
// reaching the session as a prompt. The sender gets no receipt, so arrival is seen in the
// prompt the session's hook receives.
func TestClaudeCodeTakesAMessageDeliveredToItsSession(t *testing.T) {
	w := startLiveWorker(t)
	w.status(t, "done", turnTimeout)
	before := w.count(t, "wake", "UserPromptSubmit")

	require.NoError(t, harness.ClaudeCode{}.Deliver(context.Background(), w.socket, "dispatch event: reply with the word received."))

	got := w.await(t, "wake", "UserPromptSubmit", before, time.Minute)
	assert.Contains(t, got.field("prompt"), "dispatch event: reply with the word received.")
}
