package dispatch_test

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"maps"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/history"
	"github.com/kapetan-io/dispatch/internal/httpservice"
	"github.com/kapetan-io/dispatch/internal/httpservicetest"
	"github.com/kapetan-io/dispatch/internal/kickoffs"
	"github.com/kapetan-io/dispatch/internal/launcher"
	"github.com/kapetan-io/dispatch/internal/ledger"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/lineartest"
	"github.com/kapetan-io/dispatch/internal/loop"
	"github.com/kapetan-io/dispatch/internal/sessionlog"
	"github.com/kapetan-io/dispatch/internal/stops"
	"github.com/kapetan-io/dispatch/internal/tooltest"
	"github.com/kapetan-io/dispatch/internal/treehouse"
	"github.com/kapetan-io/dispatch/internal/world"
)

type keySource int

const (
	// optionKey hands the key to dispatch through Options, as most tests need.
	optionKey keySource = iota
	// configuredKey leaves the key to the config the test writes, such as api_key_command or the
	// keychain entry it names. The environment's LINEAR_API_KEY is cleared.
	configuredKey
	// noKey leaves no key anywhere. The keychain lookup is pointed at an entry that doesn't exist, so
	// a key on the developer's machine can't answer. The test must not set [plugins.linear] itself.
	noKey
)

type launcherKind int

const (
	// fakeLauncher answers launches from the launcher fake, as most tests need.
	fakeLauncher launcherKind = iota
	// builtinLauncher leases a tree and starts the worker through the herdr and treehouse fakes.
	builtinLauncher
)

// dispatchOptions is what a test changes about the machine dispatch runs on.
type dispatchOptions struct {
	// Config is TOML appended to the generated config.toml.
	Config string
	// OwnRepos leaves out the generated [repos.dispatch] table, for a Config that writes its own
	// [repos] tables. Config with a [repos.dispatch] table and no OwnRepos fails as a duplicate.
	OwnRepos bool
	Launcher launcherKind
	// Rules maps file names to rule files. Nil runs the shipped rules/.
	Rules map[string]string
	Key   keySource
	// Env sets environment variables for this test's commands, over HOME and an empty LINEAR_API_KEY.
	// Tests set them here and never call t.Setenv, so each test has its own environment.
	Env map[string]string
	// Dir is the working directory commands run in. Empty leaves it at the test process's. Tests set it
	// here and never call t.Chdir.
	Dir string
	// RepoSettings is TOML lines added to the dispatch repo's [repos.dispatch] table.
	RepoSettings string
	// ExactRules keeps the [rules] table as Config wrote it, without the shipped keys filled in.
	ExactRules bool
	// RuleTimeout and ActionTimeout shorten how long a rule call and an action may take.
	RuleTimeout   dispatch.RuleTimeout
	ActionTimeout time.Duration
}

// Dispatch is dispatch running on a machine of fakes: Linear, herdr, treehouse, GitHub and the
// notifier. Tests drive it through the command line and read back what the fakes saw.
type Dispatch struct {
	Clock    *fakeClock
	Linear   *Linear
	Workers  *Workers
	Trees    *Trees
	GitHub   *GitHub
	Alerts   *Alerts
	Services *Services
	// Repo is the dispatch repo, a directory the config lists under [repos.dispatch].
	Repo string
	// Home is the HOME every command and hook runs under, a directory of the test's own.
	Home string
	// env is the environment every command runs with, and cwd the directory it runs in. Empty cwd
	// runs in the test process's.
	env environment.Environment
	cwd string

	t             *testing.T
	configuredKey bool
	dir           string
	state         string
	extra         string
	repos         []repoEntry
	linear        *linearHarness
	tools         *tooltest.Tools
	// builtinLauncher leaves Options.Launcher empty, so a tick starts workers
	// with the launcher built into dispatch, over tools.Herdr and tools.Treehouse.
	builtinLauncher bool
	// builtinAlerts leaves Options.Alerts empty, so alerts go through the
	// notifier dispatch picks for the operating system.
	builtinAlerts bool
	// running is the loop started by the first command that needs one, and started every loop
	// this test started, so a failing test can show what they printed.
	running *runningLoop
	started []*runningLoop
	// earlier answers Stop reports until a loop starts, standing in for the dispatch that
	// ran before this one and left its records in the state directory.
	earlier net.Listener
	// rulesDir is the directory of rules the loop runs.
	rulesDir string
	// exactRules keeps the [rules] table as the test wrote it, without filling in the shipped keys.
	exactRules bool
	// http is the services a rule's ctx.http reaches. Nil leaves Options.HTTP to its default.
	http *httpservicetest.Fake
	// timeouts carries the RuleTimeout and ActionTimeout a test set, over every command.
	timeouts dispatch.Options
	// brokenLoop is true while a fake loop holds the socket.
	brokenLoop bool
	// loopUp is running != nil, readable from the goroutines later starts.
	loopUp atomic.Bool
	// loopCalls counts the connections the fake loop accepted.
	loopCalls atomic.Int32
}

const ruleHeader = "import { whenever, on, known, minutes, hours } from \"dispatch\";\n"

// rule is a rules directory holding dispatch.ts with just this body.
func rule(body string) map[string]string {
	return map[string]string{"dispatch.ts": ruleHeader + body}
}

// shippedRulesAnd is the shipped rules/ with one more rule file that dispatch.ts imports, for a
// test that needs the shipped behavior and one rule that reports something the shipped rules don't.
func shippedRulesAnd(t *testing.T, body string) map[string]string {
	t.Helper()
	paths, err := filepath.Glob("rules/*.ts")
	require.NoError(t, err)
	files := map[string]string{}
	for _, path := range paths {
		if strings.HasSuffix(path, ".test.ts") {
			continue
		}
		text, err := os.ReadFile(path)
		require.NoError(t, err)
		files[filepath.Base(path)] = string(text)
	}
	files["dispatch.ts"] += "\nimport \"./added\";\n"
	files["added.ts"] = ruleHeader + body
	return files
}

// Result is everything a user sees from one command.
type Result struct {
	Stdout, Stderr string
	Err            error
	// Code is the exit code, dispatch.ExitCode(Err), and 0 when Err is nil.
	Code int
}

func newDispatch(t *testing.T, opts dispatchOptions) *Dispatch {
	t.Helper()
	// Resolved so a /var versus /private/var symlink cannot hide a match between a path a test gives
	// a fake and the one dispatch finds through git.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	// A unix socket path is capped near 104 bytes on macOS, which t.TempDir() overruns.
	state, err := os.MkdirTemp("/tmp", "dsp")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(state) })
	// Each test runs the shipped rules from its own copy, because rules init and check write the
	// generated declarations into the rules directory, which would rewrite the committed ones.
	shipped := filepath.Join(dir, "shipped-rules")
	require.NoError(t, os.CopyFS(shipped, os.DirFS("rules")))
	d := &Dispatch{
		t:        t,
		dir:      dir,
		Repo:     filepath.Join(dir, "repo"),
		state:    state,
		rulesDir: shipped,
		tools:    tooltest.New(t),
		Clock:    &fakeClock{now: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)},
	}
	t.Cleanup(func() { _ = d.stopLoop() })
	t.Cleanup(func() {
		if t.Failed() {
			for _, r := range d.started {
				t.Logf("loop stderr:\n%s", r.err.String())
			}
		}
	})
	t.Cleanup(func() {
		if d.earlier != nil {
			_ = d.earlier.Close()
		}
	})
	d.linear = &linearHarness{Workspace: d.tools.Linear}
	d.tools.Linear.Now = d.Clock.Now
	require.NoError(t, os.MkdirAll(d.Repo, 0o755))
	d.addRepo("dispatch", d.Repo)
	d.Linear, d.Workers, d.Trees = &Linear{d: d}, &Workers{d: d}, &Trees{d: d}
	d.GitHub, d.Alerts, d.Services = &GitHub{d: d}, &Alerts{d: d}, &Services{d: d}
	if opts.Config != "" {
		d.config(opts.Config)
	}
	d.exactRules = opts.ExactRules
	if opts.RepoSettings != "" {
		d.repoSettings("dispatch", opts.RepoSettings)
	}
	if opts.OwnRepos {
		d.repos = nil
	}
	d.builtinLauncher = opts.Launcher == builtinLauncher
	if opts.Rules != nil {
		d.rulesDir = rulesDir(t, opts.Rules)
	}
	if opts.Key != optionKey {
		d.useConfiguredKey(t)
	}
	if opts.Key == noKey {
		d.config(opts.Config + "\n[plugins.linear]\napi_key_keychain = \"dispatch-test-no-such-entry\"\n")
	}
	d.Home = t.TempDir()
	d.env = environment.Process().With("HOME", d.Home).With("LINEAR_API_KEY", "")
	for _, name := range slices.Sorted(maps.Keys(opts.Env)) {
		if name == "HOME" {
			d.Home = opts.Env[name]
		}
		d.env = d.env.With(name, opts.Env[name])
	}
	d.cwd = opts.Dir
	d.timeouts = dispatch.Options{RuleTimeout: opts.RuleTimeout, ActionTimeout: opts.ActionTimeout}
	return d
}

// Run runs one command the way main does. tick, tell, watch and unwatch start the loop first.
func (d *Dispatch) Run(args ...string) Result {
	d.t.Helper()
	return d.result(d.runWith(context.Background(), dispatch.Options{}, args...))
}

// RunWith is Run with Options the test sets, such as a rule time budget.
func (d *Dispatch) RunWith(extra dispatch.Options, args ...string) Result {
	d.t.Helper()
	return d.result(d.runWith(context.Background(), extra, args...))
}

// later starts a command in the background and returns what waits for its result, so a test can
// move the clock while the command waits on a deadline.
func later[T any](run func() T) (wait func() T) {
	done := make(chan T, 1)
	go func() { done <- run() }()
	return func() T { return <-done }
}

// expiring runs a command that starts the same deadline in every cycle it runs, such as a rule
// that spins, and fires that deadline each time until the command returns. It does not use
// Expire, which waits for the loop to settle, and a spinning rule keeps the loop busy.
func expiring[T any](d *Dispatch, what string, run func() T) T {
	done := later(run)
	finished := make(chan T, 1)
	go func() { finished <- done() }()
	for {
		select {
		case result := <-finished:
			return result
		case <-time.After(time.Millisecond):
			d.Clock.expire(what)
		}
	}
}

// Tools is the fakes behind dispatch, for a test to make one fail or to read what it recorded.
func (d *Dispatch) Tools() *tooltest.Tools { return d.tools }

// RunIn runs one command from dir, as an operator's shell would.
func (d *Dispatch) RunIn(dir string, args ...string) Result {
	d.t.Helper()
	return d.runIn(context.Background(), dir, dispatch.Options{}, args...)
}

func (d *Dispatch) runIn(ctx context.Context, dir string, extra dispatch.Options, args ...string) Result {
	d.t.Helper()
	extra.Dir = dir
	return d.result(d.runWith(ctx, extra, args...))
}

// ConfigPath is the config.toml every command reads.
func (d *Dispatch) ConfigPath() string { return filepath.Join(d.dir, "config.toml") }

// Hook runs a hook command the way Claude Code does, with stdin as the hook's input. A Stop
// report that arrives before any loop runs is kept for the first read, as it is after a restart.
func (d *Dispatch) Hook(command, stdin string) Result {
	d.t.Helper()
	return d.hook(dispatch.Options{Stdin: strings.NewReader(stdin)}, command)
}

func (d *Dispatch) hook(extra dispatch.Options, command string) Result {
	d.t.Helper()
	if d.brokenLoop {
		return d.raw(extra, command)
	}
	return d.result(d.runWith(context.Background(), extra, command))
}

// HookWhileDown is Hook with no dispatch running and none to run before it, as on a machine where
// dispatch never started.
func (d *Dispatch) HookWhileDown(command, stdin string) Result {
	d.t.Helper()
	return d.raw(dispatch.Options{Stdin: strings.NewReader(stdin)}, command)
}

// HookWithoutConfig runs a hook on a machine that has no config.toml.
func (d *Dispatch) HookWithoutConfig(command, stdin string) Result {
	d.t.Helper()
	opts := d.options(dispatch.Options{Stdin: strings.NewReader(stdin)})
	opts.ConfigPath = filepath.Join(d.dir, "missing.toml")
	return d.run(opts, command)
}

func (d *Dispatch) raw(extra dispatch.Options, command string) Result {
	return d.run(d.options(extra), command)
}

func (d *Dispatch) run(opts dispatch.Options, args ...string) Result {
	var stdout, stderr bytes.Buffer
	opts.Stdout, opts.Stderr = &stdout, &stderr
	err := dispatch.Run(context.Background(), args, opts)
	return d.result(stdout.String(), stderr.String(), err)
}

func (d *Dispatch) result(stdout, stderr string, err error) Result {
	res := Result{Stdout: stdout, Stderr: stderr, Err: err}
	if err != nil {
		res.Code = dispatch.ExitCode(err)
	}
	return res
}

// Tick runs `dispatch tick` and requires it to succeed.
func (d *Dispatch) Tick() string {
	d.t.Helper()
	res := d.Run("tick")
	require.NoError(d.t, res.Err)
	return res.Stdout
}

// Receipt is one action dispatch took, as `dispatch trace --json` reports it.
type Receipt struct {
	Kind    string
	Subject string
	// State is "attempted" or the action's outcome, such as "done".
	State  string
	Once   string
	Detail string
	// Reset marks a receipt that `state reset --quiet` wrote for something that was never sent.
	Reset bool
}

// Receipts is what dispatch did for the ticket, one receipt per kind, subject and once key at its
// final state, oldest first. A ticket that history has no record of has none.
func (d *Dispatch) Receipts(ticket string) []Receipt {
	d.t.Helper()
	res := d.Run("explain", ticket, "--json")
	if res.Code == 1 && strings.Contains(res.Err.Error(), "history has no record of") {
		return nil
	}
	require.NoError(d.t, res.Err)
	var found []Receipt
	index := map[string]int{}
	for _, text := range strings.Split(res.Stdout, "\n") {
		if text == "" {
			continue
		}
		var line struct {
			Type, Kind, Subject, State, Once, Detail string
			Reset                                    bool
		}
		require.NoError(d.t, json.Unmarshal([]byte(text), &line))
		if line.Type != "receipt" {
			continue
		}
		receipt := Receipt{Kind: line.Kind, Subject: line.Subject, State: line.State, Once: line.Once, Detail: line.Detail, Reset: line.Reset}
		key := line.Kind + "|" + line.Subject + "|" + line.Once
		if at, ok := index[key]; ok {
			found[at] = receipt
			continue
		}
		index[key] = len(found)
		found = append(found, receipt)
	}
	return found
}

// Restart stops the loop if one runs and starts it again, as after a config edit.
func (d *Dispatch) Restart() { d.t.Helper(); d.restart() }

// Stop stops the loop. The next tick, tell, watch or Restart starts a new one.
func (d *Dispatch) Stop() { d.t.Helper(); require.NoError(d.t, d.stopLoop()) }

// StartBeforeTheEvents starts the loop ahead of the tickets, workers and pull requests a test
// sets up, because a session follows a ticket only while a dispatch runs.
func (d *Dispatch) StartBeforeTheEvents() { d.t.Helper(); d.startBeforeTheEvents() }

// Advance moves the clock and waits for the loop to settle.
func (d *Dispatch) Advance(by time.Duration) { d.t.Helper(); d.advance(by) }

// AdvanceLagging is Advance for a machine whose wall clock runs lag behind the timers.
func (d *Dispatch) AdvanceLagging(by, lag time.Duration) {
	d.t.Helper()
	d.moving(func() int { return d.Clock.AdvanceLagging(by, lag) })
}

// BrokenLoop puts a loop on the socket that fails the way a loop can't be made to fail: it
// closes without answering, answers garbage, answers with an error, or never answers. A stale
// socket file is a loop that died and left its socket behind.
func (d *Dispatch) BrokenLoop(fault loopFault) {
	d.t.Helper()
	require.Nil(d.t, d.running)
	ln, err := net.Listen("unix", loop.SocketPath(d.state))
	require.NoError(d.t, err)
	d.brokenLoop = true
	if fault == staleSocket {
		ln.(*net.UnixListener).SetUnlinkOnClose(false)
		require.NoError(d.t, ln.Close())
		return
	}
	d.t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			d.loopCalls.Add(1)
			go func() {
				defer func() { _ = conn.Close() }()
				switch fault {
				case answersGarbage:
					_, _ = conn.Write([]byte("not json\n"))
				case answersWithAnError:
					_ = json.NewEncoder(conn).Encode(loop.Response{Code: loop.CodeFailed, Error: "disk full"})
				case neverAnswers:
					_, _ = io.Copy(io.Discard, conn)
				}
			}()
		}
	}()
}

// LoopCalls counts the connections the loop made by BrokenLoop accepted.
func (d *Dispatch) LoopCalls() int { return int(d.loopCalls.Load()) }

type loopFault int

const (
	staleSocket loopFault = iota
	closesWithoutAnswering
	answersGarbage
	answersWithAnError
	neverAnswers
)

// CrashedEarlierRun writes history the command line can't produce, as a run that crashed between
// an action and its receipt leaves it: a segment with the receipts and no stop line. The next loop
// start resumes from it (unreachable from the CLI: a fault the product can't inject).
func (d *Dispatch) CrashedEarlierRun(receipts ...earlierReceipt) {
	d.t.Helper()
	written := make([]ledger.Receipt, 0, len(receipts))
	for _, r := range receipts {
		written = append(written, ledger.Receipt{Kind: r.Kind, Subject: r.Subject, Rule: r.Rule, Once: ledger.Once(r.Once),
			Outcome: ledger.Outcome(r.Outcome), Started: r.Started, Ended: r.Ended})
	}
	d.seedHistory(nil, written...)
}

// HistoryDatedAhead writes a closed run dated after the clock, as a clock that stepped back
// leaves behind (unreachable from the CLI: dispatch never writes history from the future).
func (d *Dispatch) HistoryDatedAhead(by time.Duration) {
	d.t.Helper()
	require.Nil(d.t, d.running)
	at := d.Clock.Now().Add(by)
	w, err := history.Start(filepath.Join(d.state, "events"), at,
		history.Header{Why: "start", At: at.UnixMilli()}, history.Snapshot{At: at.UnixMilli()})
	require.NoError(d.t, err)
	require.NoError(d.t, w.Close(true))
}

// UnconfirmedLaunch marks the ticket's tree as one whose launch started Claude at the time given
// and has not returned (unreachable from the CLI: a test cannot kill a launch halfway).
func (w *Workers) UnconfirmedLaunch(ticket string, at time.Time) {
	w.d.t.Helper()
	markLaunch(w.d.t, w.d.state, w.d.Trees.PathOf(ticket), at)
}

// CorruptStopRecords overwrites the records Stop hooks left, or only the newest one, with text
// that is not a record (unreachable from the CLI: a disk fault the product can't inject).
func (w *Workers) CorruptStopRecords(newestOnly bool) {
	w.d.t.Helper()
	records, err := filepath.Glob(filepath.Join(w.d.state, "stops", "*", "*.json"))
	require.NoError(w.d.t, err)
	require.NotEmpty(w.d.t, records)
	if newestOnly {
		require.Greater(w.d.t, len(records), 1)
		newest, newestBody := "", ""
		for _, record := range records {
			body, err := os.ReadFile(record)
			require.NoError(w.d.t, err)
			if string(body) > newestBody {
				newest, newestBody = record, string(body)
			}
		}
		records = []string{newest}
	}
	for _, record := range records {
		require.NoError(w.d.t, os.WriteFile(record, []byte("not json"), 0o644))
	}
}

// Unconfirmed marks the ticket's tree as one whose launch never confirmed its kickoff (unreachable
// from the CLI: a test cannot kill a launch halfway).
func (w *Workers) Unconfirmed(ticket string) {
	w.d.t.Helper()
	markLaunch(w.d.t, w.d.state, w.Tree(ticket), w.d.Clock.Now())
}

// PathOf is where the tree of a ticket's worker is.
func (t *Trees) PathOf(ticket string) string { return filepath.Join(t.d.dir, "trees", ticket) }

// Tree is the path of the tree Running leases for a ticket.
func (w *Workers) Tree(ticket string) string { return filepath.Join(w.d.dir, "trees", ticket) }

// StateFiles reads every file dispatch keeps in its state directory, except sockets, to check what
// a secret did not reach.
func (d *Dispatch) StateFiles() []string {
	d.t.Helper()
	var bodies []string
	require.NoError(d.t, filepath.Walk(d.state, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || strings.HasSuffix(path, ".sock") {
			return err
		}
		body, rerr := os.ReadFile(path)
		require.NoError(d.t, rerr)
		bodies = append(bodies, string(body))
		return nil
	}))
	return bodies
}

// HalfWrittenSegment adds a history segment that has its header and no snapshot, as a run that
// crashed while rotating leaves it (unreachable from the CLI: a crash between two writes).
func (d *Dispatch) HalfWrittenSegment() {
	d.t.Helper()
	events := filepath.Join(d.state, "events")
	require.NoError(d.t, os.MkdirAll(events, 0o700))
	name := "events-" + d.Clock.Now().Add(-30*time.Minute).UTC().Format("20060102T150405Z") + ".jsonl"
	require.NoError(d.t, os.WriteFile(filepath.Join(events, name), []byte(`{"line":"header","why":"start","at":1}`+"\n"), 0o600))
}

// EmptyFirstSegment adds a history file with nothing in it, as a first run that crashed before
// it wrote its header leaves it (unreachable from the CLI: a crash between two writes).
func (d *Dispatch) EmptyFirstSegment() {
	d.t.Helper()
	events := filepath.Join(d.state, "events")
	require.NoError(d.t, os.MkdirAll(events, 0o700))
	require.NoError(d.t, os.WriteFile(filepath.Join(events, "events-20260101T000000Z.jsonl"), nil, 0o600))
}

// earlierReceipt is a receipt CrashedEarlierRun writes. Outcome is "done" or empty for an action
// that was attempted and never finished.
type earlierReceipt struct {
	Kind, Subject, Rule, Outcome string
	Once                         earlierOnce
	Started, Ended               int64
}

// earlierOnce is the once key of an earlierReceipt: a topic, then the values that must change
// before the action runs again.
type earlierOnce []any

// HookAndWait is Hook for a hook that reports to the loop, and returns once the loop has taken the
// report. It starts a loop first when none runs, because the report has nowhere to go otherwise.
func (d *Dispatch) HookAndWait(command, stdin string) Result {
	h := d
	h.t.Helper()
	if h.running == nil {
		require.NoError(h.t, h.startLoop(dispatch.Options{}))
	}
	before, err := loop.Ask(context.Background(), h.state, loop.Request{Op: loop.OpIdle})
	require.NoError(h.t, err)
	res := d.Hook(command, stdin)
	require.Eventually(h.t, func() bool {
		resp, err := loop.Ask(context.Background(), h.state, loop.Request{Op: loop.OpIdle})
		return err == nil && !resp.Busy && resp.Idle > before.Idle
	}, 20*time.Second, 2*time.Millisecond)
	return res
}

// HookAndWaitForCommand is HookAndWait that also waits for the report of a slash command to reach the
// session's log, because an idle pass that began before the report arrived can end HookAndWait's wait first.
// A prompt that is no command, or a log that can't be read, only gets HookAndWait.
func (d *Dispatch) HookAndWaitForCommand(command, stdin, tree, session, prompt string) Result {
	d.t.Helper()
	before, readErr := sessionlog.Read(d.state, tree, session)
	res := d.HookAndWait(command, stdin)
	if _, isCommand := sessionlog.ParseCommand(prompt); !isCommand || readErr != nil {
		return res
	}
	require.Eventually(d.t, func() bool {
		after, err := sessionlog.Read(d.state, tree, session)
		return err != nil || len(after.Commands) > len(before.Commands)
	}, 20*time.Second, 2*time.Millisecond)
	return res
}

// WaitForCommands waits until the session's log holds count commands. A hook ignores a failed hand-off
// to the loop, so a command it dropped never gets there, and a test that waits here fails on the drop
// instead of on a later step that the drop starves.
func (d *Dispatch) WaitForCommands(tree, session string, count int) {
	d.t.Helper()
	require.Eventually(d.t, func() bool {
		log, err := sessionlog.Read(d.state, tree, session)
		return err == nil && len(log.Commands) == count
	}, 20*time.Second, 5*time.Millisecond)
}

// HookTogether runs the hook count times at once, as Claude sessions do. The options are made before
// any hook runs, because Dispatch rewrites config.toml for every command.
func (d *Dispatch) HookTogether(count int, command, stdin string) []Result {
	d.t.Helper()
	all := make([]dispatch.Options, count)
	for i := range all {
		all[i] = d.options(dispatch.Options{Stdin: strings.NewReader(stdin)})
	}
	results := make([]Result, count)
	var wg sync.WaitGroup
	for i := range all {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var stdout, stderr bytes.Buffer
			all[i].Stdout, all[i].Stderr = &stdout, &stderr
			err := dispatch.Run(context.Background(), []string{command}, all[i])
			results[i] = d.result(stdout.String(), stderr.String(), err)
		}()
	}
	wg.Wait()
	return results
}

// LoseHistory deletes the history directory, as an operator clearing dispatch's memory by hand does
// (unreachable from the CLI: no command deletes the directory).
func (d *Dispatch) LoseHistory() {
	d.t.Helper()
	require.NoError(d.t, os.RemoveAll(filepath.Join(d.state, "events")))
}

// CorruptSessionLog appends text to the end of the only session log there is, as a writer that
// died mid-line leaves it (unreachable from the CLI: a fault the product can't inject).
func (d *Dispatch) CorruptSessionLog(text string) {
	d.t.Helper()
	appendToSessionLog(d.t, d.state, text)
}

// BlockSessionLogs makes the session log directory unwritable, so a Stop report can't be logged
// (unreachable from the CLI: a fault the product can't inject).
func (d *Dispatch) BlockSessionLogs() {
	d.t.Helper()
	require.NoError(d.t, os.WriteFile(sessionlog.Dir(d.state), nil, 0o644))
}

// LoseSessionLogs deletes every session log, to show a command that reads history alone doesn't need them.
func (d *Dispatch) LoseSessionLogs() {
	d.t.Helper()
	require.NoError(d.t, os.RemoveAll(sessionlog.Dir(d.state)))
}

// StateEntries counts the entries in dispatch's state directory, to show a command left no file there.
func (d *Dispatch) StateEntries() int {
	d.t.Helper()
	entries, err := os.ReadDir(d.state)
	require.NoError(d.t, err)
	return len(entries)
}

// pastEntry is a decision a rule made in a cycle that WroteHistory writes.
type pastEntry struct {
	Rule, Subject, Kind, Reason string
	Once                        []any
	Dropped                     bool
}

// pastThrow is a rule call that threw, or ran out of time, in a written cycle.
type pastThrow struct {
	Rule, Subject, Error string
	Timeout              bool
}

// pastExchange is an HTTP exchange in a written cycle. Unknown is why it has no answer.
type pastExchange struct{ Service, Path, Rule, Ticket, Unknown string }

// pastCycle is one cycle of a written run, without the read of the world a loop records.
type pastCycle struct {
	Why                           string
	Wants, Waiting, Held, Covered []pastEntry
	Throws                        []pastThrow
	Reads                         map[string][]string
	HTTP                          []pastExchange
}

// pastReceipt is a receipt in a written run. Done false leaves it attempted, with no outcome.
type pastReceipt struct {
	Kind, Subject, Rule, Detail string
	Once                        []any
	Done, Reset                 bool
}

// pastRun is a history segment WroteHistory writes.
type pastRun struct {
	// Why is "start" unless set.
	Why string
	// Ago is how long before now the segment began.
	Ago    time.Duration
	Cycles []pastCycle
	// Carried are the receipts the segment's snapshot holds from segments already deleted.
	Carried []pastReceipt
	// Receipts follow the cycles as receipt lines.
	Receipts []pastReceipt
	// Crashed leaves the segment with no stop line.
	Crashed bool
}

// WroteHistory writes a segment the command line can't produce: cycles with no recorded read of the
// world, or decisions a tick never makes (unreachable from the CLI: a fault the product can't inject).
func (d *Dispatch) WroteHistory(run pastRun) {
	d.t.Helper()
	require.Nil(d.t, d.running)
	at := d.Clock.Now().Add(-run.Ago)
	ledgerOf := func(past []pastReceipt) []ledger.Receipt {
		var out []ledger.Receipt
		for _, p := range past {
			receipt := ledger.Receipt{Kind: p.Kind, Subject: p.Subject, Rule: p.Rule, Once: p.Once, Detail: p.Detail, Started: at.UnixMilli(), Reset: p.Reset}
			if p.Done {
				receipt.Outcome, receipt.Ended = ledger.Done, at.UnixMilli()
			}
			out = append(out, receipt)
		}
		return out
	}
	entries := func(past []pastEntry) []history.Entry {
		var out []history.Entry
		for _, p := range past {
			out = append(out, history.Entry{Rule: p.Rule, Subject: p.Subject, Kind: p.Kind, Reason: p.Reason, Once: p.Once, Dropped: p.Dropped})
		}
		return out
	}
	w, err := history.Start(filepath.Join(d.state, "events"), at, history.Header{Why: cmp.Or(run.Why, "start"), At: at.UnixMilli()},
		history.Snapshot{At: at.UnixMilli(), Ledger: history.LedgerState{Receipts: ledgerOf(run.Carried)}})
	require.NoError(d.t, err)
	for i, c := range run.Cycles {
		cycle := history.Cycle{Line: history.LineCycle, Now: at.Add(time.Duration(i) * time.Second).UnixMilli(), Why: c.Why,
			Wants: entries(c.Wants), Waiting: entries(c.Waiting), Held: entries(c.Held), Covered: entries(c.Covered), Reads: c.Reads}
		for _, t := range c.Throws {
			cycle.Throws = append(cycle.Throws, history.Throw{Rule: t.Rule, Subject: t.Subject, Error: t.Error, Timeout: t.Timeout})
		}
		for _, x := range c.HTTP {
			cycle.HTTP = append(cycle.HTTP, httpservice.Exchange{Service: x.Service, Path: x.Path, Rule: x.Rule, Ticket: x.Ticket, Unknown: x.Unknown})
		}
		require.NoError(d.t, w.Append(cycle))
	}
	for _, r := range ledgerOf(run.Receipts) {
		require.NoError(d.t, w.Append(history.Receipt{Line: history.LineReceipt, Receipt: r}))
	}
	require.NoError(d.t, w.Close(!run.Crashed))
}

func (d *Dispatch) newestSegment() (string, []string) {
	d.t.Helper()
	paths, err := history.Segments(filepath.Join(d.state, "events"))
	require.NoError(d.t, err)
	require.NotEmpty(d.t, paths)
	path := paths[len(paths)-1]
	text, err := os.ReadFile(path)
	require.NoError(d.t, err)
	return path, strings.Split(strings.TrimSpace(string(text)), "\n")
}

func (d *Dispatch) rewriteNewestSegment(lines []string) {
	d.t.Helper()
	path, _ := d.newestSegment()
	require.NoError(d.t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600))
}

// lastRead is the index of the newest read line in lines.
func lastRead(t *testing.T, lines []string) int {
	t.Helper()
	last := -1
	for i, line := range lines {
		if strings.HasPrefix(line, `{"line":"read"`) {
			last = i
		}
	}
	require.Positive(t, last)
	return last
}

// EditHistory replaces text in the newest history segment, as a different dispatch version or a
// damaged disk leaves it (unreachable from the CLI: history is written only by the loop).
func (d *Dispatch) EditHistory(old, replacement string) {
	d.t.Helper()
	_, lines := d.newestSegment()
	text := strings.Join(lines, "\n")
	require.Contains(d.t, text, old)
	d.rewriteNewestSegment(strings.Split(strings.ReplaceAll(text, old, replacement), "\n"))
}

// CrashedBeforeStopLine drops the stop line of the newest segment, as a crash leaves it (unreachable
// from the CLI: a fault the product can't inject).
func (d *Dispatch) CrashedBeforeStopLine() {
	d.t.Helper()
	_, lines := d.newestSegment()
	require.Contains(d.t, lines[len(lines)-1], `"line":"stop"`)
	d.rewriteNewestSegment(lines[:len(lines)-1])
}

// CrashedBeforeLastCycleLine ends the newest segment where a crash left it: after the last read
// and the memory changes written with it, and before the cycle line (unreachable from the CLI: a
// crash between two writes).
func (d *Dispatch) CrashedBeforeLastCycleLine() {
	d.t.Helper()
	_, lines := d.newestSegment()
	last := lastRead(d.t, lines)
	keep := lines[:last+1]
	for _, line := range lines[last+1:] {
		if !strings.HasPrefix(line, `{"line":"world"`) {
			break
		}
		keep = append(keep, line)
	}
	d.rewriteNewestSegment(keep)
}

// EditLastRead replaces text in the newest read of the world only, in a segment whose last cycle
// wrote no cycle line (unreachable from the CLI: history is written only by the loop).
func (d *Dispatch) EditLastRead(old, replacement string) {
	d.t.Helper()
	_, lines := d.newestSegment()
	last := lastRead(d.t, lines)
	for _, line := range lines[last+1:] {
		require.NotContains(d.t, line, `{"line":"cycle"`)
	}
	require.Contains(d.t, lines[last], old)
	lines[last] = strings.ReplaceAll(lines[last], old, replacement)
	d.rewriteNewestSegment(lines)
}

type brokenPipe struct{}

func (brokenPipe) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

// RunWithBrokenOutput runs a command whose stdout and stderr reject every write, as a closed pipe does.
func (d *Dispatch) RunWithBrokenOutput(args ...string) Result {
	d.t.Helper()
	opts := d.options(dispatch.Options{})
	opts.Stdout, opts.Stderr = brokenPipe{}, brokenPipe{}
	return d.result("", "", dispatch.Run(context.Background(), args, opts))
}

// EditRules rewrites a file of the rules directory the test gave, after a run used it.
func (d *Dispatch) EditRules(name, text string) {
	d.t.Helper()
	require.NoError(d.t, os.WriteFile(filepath.Join(d.rulesDir, name), []byte(text), 0o644))
}

// Linear is the Linear workspace: tickets, statuses and comments.
type Linear struct{ d *Dispatch }

// Tickets seeds the tickets that are started and assigned to the key's owner.
func (l *Linear) Tickets(tickets ...fakeTicket) { l.d.tickets(tickets...) }

// AcceptKey makes Linear reject every key but key from now on.
func (l *Linear) AcceptKey(key string) { l.d.linear.AcceptKey(key) }

// Children sets the children Linear returns for open parents.
func (l *Linear) Children(tickets ...fakeTicket) { l.d.linear.setChildren(tickets...) }

// Finished seeds tickets Linear knows that are not started, such as Done or Todo ones, which only
// a lookup by identifier finds.
func (l *Linear) Finished(tickets ...fakeTicket) { l.d.linear.setElsewhere(tickets...) }

// FailChildren makes Linear fail the reads of a parent's children while on is true.
func (l *Linear) FailChildren(on bool) { l.d.linear.FailOn(lineartest.OnChildren, on) }

// FailManaged makes Linear fail the read of the started tickets while on is true.
func (l *Linear) FailManaged(on bool) { l.d.linear.FailOn(lineartest.OnManaged, on) }

// Moves lists the status changes Linear accepted, as "ENG-1 -> In Review".
func (l *Linear) Moves() []string {
	var out []string
	for _, c := range l.d.linear.mutations() {
		if c.Kind == lineartest.Move {
			out = append(out, strings.TrimPrefix(c.TicketID, "uuid-")+" -> "+stateName(c.StateID))
		}
	}
	return out
}

// Comments lists the comments Linear accepted, as "ENG-1: <body>".
func (l *Linear) Comments() []string {
	var out []string
	for _, c := range l.d.linear.mutations() {
		if c.Kind == lineartest.Comment {
			out = append(out, strings.TrimPrefix(c.TicketID, "uuid-")+": "+c.Body)
		}
	}
	return out
}

func stateName(id string) string {
	for _, name := range []string{"Backlog", "Todo", "In Progress", "In Review", "Needs Human", "Done", "Canceled", "Duplicate"} {
		if lineartest.StateID(name) == id {
			return name
		}
	}
	return id
}

// Workers is herdr: the workspaces, agents and prompts of the workers.
type Workers struct{ d *Dispatch }

// Running gives a ticket a leased tree and a herdr workspace on it. An empty status means the
// workspace has no Claude agent.
func (w *Workers) Running(ticket, status string) { w.d.worker(ticket, status) }

// RunningIn is Running for a tree at a chosen path.
func (w *Workers) RunningIn(ticket, tree, status string) {
	w.d.workerOn(ticket, tree, "w-"+ticket, status)
}

// SetStatus changes the herdr status of a worker made with Running.
func (w *Workers) SetStatus(ticket, status string) { w.d.setWorkerStatus(ticket, status) }

// SetSession changes the Claude session id herdr reports for a worker made with Running, as a
// relaunch does. An empty id is a worker herdr reports no session for.
func (w *Workers) SetSession(ticket, session string) { w.d.setWorkerSession(ticket, session) }

// Prompts lists what was typed into workers, as "<pane>|<text>".
func (w *Workers) Prompts() []string { return w.d.prompts() }

// Launched lists the launch requests dispatch made, as "<repo> <ticket> <branch> --model <model>"
// plus " --kickoff <text>" when one was set.
func (w *Workers) Launched() []string { return w.d.launches() }

// Closed lists the workspaces that were closed.
func (w *Workers) Closed() []string { return w.d.closes() }

// stopTasks is the background tasks a Stop hook reports.
type stopTasks struct {
	// Running is how many background tasks still run.
	Running int
	// Unreported leaves the field out, as a Claude Code that predates it does.
	Unreported bool
}

// StopInput is what Claude Code hands the Stop hook for the worker session of ticket.
func (w *Workers) StopInput(ticket string, tasks stopTasks) string {
	w.d.t.Helper()
	return w.StopInputFrom("worker", ticket, tasks)
}

// StopInputFrom is StopInput for the Claude session with the given id.
func (w *Workers) StopInputFrom(session, ticket string, tasks stopTasks) string {
	w.d.t.Helper()
	return w.d.stopInput(session, ticket, tasks.json())
}

func (s stopTasks) json() string {
	if s.Unreported {
		return ""
	}
	running := make([]string, s.Running)
	for i := range running {
		running[i] = `{"type":"shell","status":"running"}`
	}
	return `"background_tasks":[` + strings.Join(running, ",") + `]`
}

// behindClock is a clock that reads an interval before Dispatch.Clock.
type behindClock struct {
	*fakeClock
	by time.Duration
}

func (c behindClock) Now() time.Time { return c.fakeClock.Now().Add(-c.by) }

// LateStop is Stop for a report that waited past its hook's timeout, so the loop gets it after
// reports stamped later (unreachable from the CLI: a delay the product can't inject).
func (w *Workers) LateStop(ticket string, tasks stopTasks, late time.Duration) {
	w.d.t.Helper()
	extra := dispatch.Options{Stdin: strings.NewReader(w.StopInput(ticket, tasks)), Clock: behindClock{w.d.Clock, late}}
	require.NoError(w.d.t, w.d.hook(extra, "record-stop").Err)
}

// Stop ends a turn of the worker session of ticket: it runs the Stop hook in the ticket's tree
// and requires it to succeed.
func (w *Workers) Stop(ticket string, tasks stopTasks) {
	w.d.t.Helper()
	w.StopFrom("worker", ticket, tasks)
}

// StopFrom is Stop for the Claude session with the given id.
func (w *Workers) StopFrom(session, ticket string, tasks stopTasks) {
	w.d.t.Helper()
	res := w.d.Hook("record-stop", w.StopInputFrom(session, ticket, tasks))
	require.NoError(w.d.t, res.Err)
}

// Trees is the treehouse pool over real git worktrees.
type Trees struct{ d *Dispatch }

// CleanLeased leases the ticket a clean, pushed tree that has no herdr workspace.
func (t *Trees) CleanLeased(ticket string) {
	t.d.lease(ticket, t.d.gitTree(ticket))
}

// Untracked makes the ticket's tree a clean, pushed clone with a file git has never seen, and
// returns the tree's path.
func (t *Trees) Untracked(ticket, name string) string {
	path := t.d.gitTree(ticket)
	t.d.untracked(path, name)
	return path
}

// Pushed gives the ticket's tree a remote that has its branch, so the tree can go back to the pool.
// Run a Stop hook for the ticket first, because that is what makes the tree a worktree of the
// dispatch repo.
func (t *Trees) Pushed(ticket string) string {
	h := t.d
	remote := filepath.Join(h.dir, "remotes", ticket+".git")
	require.NoError(h.t, os.MkdirAll(filepath.Dir(remote), 0o755))
	hintGit(h.t, h.dir, "init", "-q", "--bare", remote)
	tree := filepath.Join(h.dir, "trees", ticket)
	hintGit(h.t, tree, "remote", "add", "origin", remote)
	hintGit(h.t, tree, "push", "-q", "--set-upstream", "origin", "br-"+ticket)
	return tree
}

// Returned lists the trees given back to the pool, as "<repo>|<tree>|<holder>".
func (t *Trees) Returned() []string { return t.d.returns() }

// GitHub is the pull requests, reviews, checks and merges.
type GitHub struct{ d *Dispatch }

// PullRequest gives the ticket's branch an open pull request in this state.
func (g *GitHub) PullRequest(ticket string, pr prState) { g.d.pr(ticket, pr) }

// Alerts is the notifications dispatch shows.
type Alerts struct{ d *Dispatch }

// Shown lists the notification texts shown.
func (a *Alerts) Shown() []string { return a.d.notifications() }

// Services is the HTTP services a rule's ctx.http reaches.
type Services struct{ d *Dispatch }

// Sentry is the fake sentry service, which a rule reaches as ctx.http "sentry".
// It adds its [http.sentry] table to the config the test gave newDispatch.
func (s *Services) Sentry() *httpservicetest.Fake {
	h := s.d
	given := h.extra
	fake := h.sentry()
	h.config(given + sentryConfig)
	return fake
}

func (d *Dispatch) turnsRule() {
	d.t.Helper()
	d.config("[dispatch]\nrules = " + `"` + oneRule(d.t, turnsRule) + `"` + "\n")
}

func sessionLogs(t *testing.T, stateDir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(sessionlog.Dir(stateDir), func(path string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		out = append(out, path)
		return nil
	})
	if !os.IsNotExist(err) {
		require.NoError(t, err)
	}
	return out
}

// appendToSessionLog writes text to the end of the only session log there is.
func appendToSessionLog(t *testing.T, stateDir, text string) {
	t.Helper()
	logs := sessionLogs(t, stateDir)
	require.Len(t, logs, 1)
	f, err := os.OpenFile(logs[0], os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(text)
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

const (
	time1 = "2026-10-09T10:00:00Z"
	time2 = "2026-10-09T11:00:00Z"
	time3 = "2026-10-09T12:00:00Z"
)

func activityPrompt(by string) string {
	return fmt.Sprintf("w-ENG-1:p1|/dispatch:pr-respond PR %s for ENG-1 has new activity, %s. Read it and handle it. If it asks for nothing, say nothing.", prURL1, by)
}

const (
	autoMergeSetting = "auto_merge = true\n"
	mergedText1      = "ENG-1 was approved and merged: " + prURL1
	mergeCall1       = prURL1 + "|a1"
	mergeRefused     = "Pull request is not mergeable: the base branch policy prohibits the merge"
)

func approvedPR(head string) prState {
	pr := cleanPR(head)
	pr.approved = []string{head}
	return pr
}

// blockedBy gives a standalone ticket blockers, each a blocker identifier and status.
func blockedBy(t fakeTicket, more bool, blockers ...[2]string) fakeTicket {
	t.Blockers = nil
	for _, b := range blockers {
		t.Blockers = append(t.Blockers, linear.Blocker{Identifier: b[0], Status: b[1], Abandoned: stateType(b[1]) == "canceled"})
	}
	t.BlockersTruncated = more
	return t
}

func comments(d *Dispatch) []string {
	var bodies []string
	for _, m := range d.linear.mutations() {
		if m.Kind == lineartest.Comment {
			bodies = append(bodies, m.Body)
		}
	}
	return bodies
}

const (
	comment1 = "2026-10-09T00:09:00.000Z"
	comment2 = "2026-10-09T00:20:00.000Z"
)

func withComment(t fakeTicket, createdAt string) fakeTicket {
	t.LastComment = createdAt
	return t
}

func designTicket(id, status string) fakeTicket {
	return withLabel(ticket(id, status, "dispatch"), "design")
}

// duration matches Go's duration formatting, which prints 0s under a
// millisecond, 12ms under a second, and 3.477s from a second up.
const duration = `(?:\d+h)?(?:\d+m)?\d+(?:\.\d+)?(?:ms|s)`

const (
	needsHumanID = "state-needs-human"
	viewerID     = "viewer-1"
)

// useConfiguredKey makes runs find the Linear key the way a user's machine
// does, instead of through Options.LinearKey.
func (d *Dispatch) useConfiguredKey(t *testing.T) {
	d.configuredKey = true
}

func (d *Dispatch) config(extra string) {
	d.extra = extra
}

type repoEntry struct{ label, dir, settings string }

// addRepo adds a [repos.<label>] table to the config.
func (d *Dispatch) addRepo(label, dir string) {
	d.repos = append(d.repos, repoEntry{label: label, dir: dir})
}

// resetRepos drops every repo added after the dispatch repo.
func (d *Dispatch) resetRepos() {
	d.repos = d.repos[:1]
}

// repoSettings appends extra TOML lines to the table of a repo, such as
// "auto_merge = true\n". The dispatch repo is always configured.
func (d *Dispatch) repoSettings(label, extra string) {
	for i := range d.repos {
		if d.repos[i].label == label {
			d.repos[i].settings += extra
		}
	}
}

func (d *Dispatch) reposTOML() string {
	var out strings.Builder
	for _, repo := range d.repos {
		fmt.Fprintf(&out, "\n[repos.%s]\npath = %q\n%s", repo.label, repo.dir, repo.settings)
	}
	return out.String()
}

func (d *Dispatch) output(args ...string) (string, error) {
	stdout, _, err := d.runContext(context.Background(), args...)
	return stdout, err
}

func (d *Dispatch) runContext(ctx context.Context, args ...string) (string, string, error) {
	return d.runWith(ctx, dispatch.Options{}, args...)
}

// writeConfig replaces config.toml in one step, so a launch reading it while a concurrent launch
// writes it sees one whole file.
func (d *Dispatch) writeConfig(body string) {
	file, err := os.CreateTemp(d.dir, "config-*.toml")
	require.NoError(d.t, err)
	_, err = file.WriteString(body)
	require.NoError(d.t, err)
	require.NoError(d.t, file.Close())
	require.NoError(d.t, os.Rename(file.Name(), filepath.Join(d.dir, "config.toml")))
}

// options is the Options every command runs over: the fakes.
func (d *Dispatch) options(extra dispatch.Options) dispatch.Options {
	d.writeConfig(d.withRules(d.withHistory(d.extra)) + d.historyTOML() + d.reposTOML())
	extra.ConfigPath = filepath.Join(d.dir, "config.toml")
	extra.StateDir = d.state
	extra.Env = d.env
	if extra.Dir == "" {
		extra.Dir = d.cwd
	}
	if extra.Clock == nil {
		extra.Clock = d.Clock
	}
	extra.Linear = d.linear.Connect
	if !d.configuredKey {
		extra.LinearKey = lineartest.Key
	}
	extra.Herdr = d.tools.Herdr
	extra.Treehouse = d.tools.Treehouse
	extra.PullRequests = d.tools.PullRequests
	if d.http != nil {
		extra.HTTP = d.http
	}
	if !d.builtinAlerts {
		extra.Alerts = d.tools.Alerts
	}
	if !d.builtinLauncher {
		extra.Launcher = d.tools.Launcher
	}
	if extra.RulesDir == "" && !strings.Contains(d.extra, "\nrules = ") {
		extra.RulesDir = d.rulesDir
	}
	extra.LaunchPace = time.Millisecond
	if extra.RuleTimeout == (dispatch.RuleTimeout{}) {
		extra.RuleTimeout = d.timeouts.RuleTimeout
	}
	if extra.ActionTimeout == 0 {
		extra.ActionTimeout = d.timeouts.ActionTimeout
	}
	return extra
}

// withRules gives the config the [rules] keys the shipped rules read, at today's
// values, unless the test set them. A test's own [rules] table keeps its keys.
func (d *Dispatch) withRules(extra string) string {
	if d.exactRules {
		return extra
	}
	defaults := []string{
		"worker_model = \"sonnet\"", "design_model = \"opus\"", "launch_failure_limit = 2", "nudge_limit = 2", "blocked_renotify_minutes = 15",
	}
	if !strings.Contains(extra, "[rules]") {
		return extra + "\n[rules]\n" + strings.Join(defaults, "\n") + "\n"
	}
	var missing []string
	for _, d := range defaults {
		key, _, _ := strings.Cut(d, " ")
		if !strings.Contains(extra, "\n"+key+" ") && !strings.Contains(extra, "\n"+key+"=") {
			missing = append(missing, d)
		}
	}
	return strings.Replace(extra, "[rules]\n", "[rules]\n"+strings.Join(missing, "\n")+"\n", 1)
}

// historyTOML points history at the state directory, unless the test set it. A
// test that sets only other [history] keys still gets the directory, so no test
// writes into the user's real history.
func (d *Dispatch) historyTOML() string {
	if strings.Contains(d.extra, "[history]") {
		return ""
	}
	return fmt.Sprintf("\n[history]\ndir = %q\n", filepath.Join(d.state, "events"))
}

// withHistory adds the history directory to a test's own [history] table.
func (d *Dispatch) withHistory(extra string) string {
	if !strings.Contains(extra, "[history]") || strings.Contains(extra, "\ndir ") || strings.Contains(extra, "\ndir=") {
		return extra
	}
	return strings.Replace(extra, "[history]\n", fmt.Sprintf("[history]\ndir = %q\n", filepath.Join(d.state, "events")), 1)
}

// runWith runs a command with the fakes, over the Options given. A
// command that needs the loop starts one first.
func (d *Dispatch) runWith(ctx context.Context, extra dispatch.Options, args ...string) (string, string, error) {
	if len(args) > 0 && args[0] == "record-stop" && d.running == nil {
		d.answerStopsOfAnEarlierRun()
	}
	if len(args) > 0 && slices.Contains([]string{"tick", "tell", "watch", "unwatch"}, args[0]) && d.running == nil {
		if err := d.startLoop(extra); err != nil {
			return "", "", err
		}
		// The loop acted on what it read at start, before this tick asked, so the first
		// tick of a test asks for everything since the start.
		if len(args) <= 2 && args[0] == "tick" {
			return d.tickSinceStart(ctx, args[1:])
		}
	}
	var stdout, stderr bytes.Buffer
	opts := d.options(extra)
	opts.Stdout, opts.Stderr = &stdout, &stderr
	err := dispatch.Run(ctx, args, opts)
	return stdout.String(), stderr.String(), err
}

// answerStopsOfAnEarlierRun stores the Stop reports that arrive before any loop runs. A
// worker's Stop is in the state directory when a restarted dispatch first reads, so the first
// read sees it, as it does a Stop that came in while a loop ran.
func (d *Dispatch) answerStopsOfAnEarlierRun() {
	if d.earlier != nil {
		return
	}
	ln, err := net.Listen("unix", loop.SocketPath(d.state))
	require.NoError(d.t, err)
	d.earlier = ln
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			var req loop.Request
			if json.NewDecoder(conn).Decode(&req) == nil && req.Op == loop.OpStop {
				s := req.Stop
				rec := stops.Record{At: time.UnixMilli(s.At).UTC(), Reported: s.Reported, Running: s.Running}
				_ = stops.Write(d.state, s.Tree, s.Session, rec)
				_ = sessionlog.AppendTurn(d.state, s.Tree, s.Session, sessionlog.Turn{At: s.At, Running: s.Running > 0})
			}
			_ = json.NewEncoder(conn).Encode(loop.Response{})
			_ = conn.Close()
		}
	}()
}

// tickSinceStart is `dispatch tick [ticket]` for the tick that starts the loop: it reports
// every action since the start and reads nothing again, so one tick is one attempt.
func (d *Dispatch) tickSinceStart(ctx context.Context, args []string) (string, string, error) {
	req := loop.Request{Op: loop.OpSettle}
	if len(args) == 1 {
		req.Ticket = strings.ToUpper(args[0])
	}
	resp, err := loop.Ask(ctx, d.state, req)
	if err != nil {
		return "", "", err
	}
	var out strings.Builder
	for _, line := range resp.Lines {
		out.WriteString(line + "\n")
	}
	if resp.Code != loop.CodeSettled {
		return out.String(), "", errors.New(resp.Error)
	}
	return out.String(), "", nil
}

// syncBuffer is a buffer the loop writes to while a test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type runningLoop struct {
	cancel context.CancelFunc
	done   chan error
	out    *syncBuffer
	err    *syncBuffer
}

// startLoop runs `dispatch run` in the background over the fakes and
// returns once it answers on its socket, or with the error that stopped it.
func (d *Dispatch) startLoop(extra dispatch.Options) error {
	d.t.Helper()
	if d.earlier != nil {
		_ = d.earlier.Close()
		d.earlier = nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &runningLoop{cancel: cancel, done: make(chan error, 1), out: &syncBuffer{}, err: &syncBuffer{}}
	d.started = append(d.started, r)
	opts := d.options(extra)
	opts.Stdout, opts.Stderr = r.out, r.err
	go func() { r.done <- dispatch.Run(ctx, []string{"run"}, opts) }()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-r.done:
			cancel()
			return err
		default:
		}
		if resp, err := loop.Ask(ctx, d.state, loop.Request{Op: loop.OpIdle}); err == nil && !resp.Busy {
			d.running = r
			d.loopUp.Store(true)
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	return errors.New("the loop did not start in 20 seconds")
}

// startBeforeTheEvents starts the loop ahead of the tickets, workers and pull requests a
// test sets up, because a session follows a ticket only while a dispatch runs and hears
// events that come after it asks.
func (d *Dispatch) startBeforeTheEvents() {
	d.t.Helper()
	require.NoError(d.t, d.startLoop(dispatch.Options{}))
}

// stopLoop stops the loop and returns what it stopped with.
func (d *Dispatch) stopLoop() error {
	if d.running == nil {
		return nil
	}
	r := d.running
	d.running = nil
	d.loopUp.Store(false)
	r.cancel()
	select {
	case err := <-r.done:
		return err
	case <-time.After(30 * time.Second):
		return errors.New("the loop did not stop in 30 seconds")
	}
}

// restart stops the loop and starts it again, as a user does after editing config.toml or a rule.
func (d *Dispatch) restart() {
	d.t.Helper()
	require.NoError(d.t, d.stopLoop())
	require.NoError(d.t, d.startLoop(dispatch.Options{}))
}

// loopOut is what the running loop has printed to stdout.
func (d *Dispatch) loopOut() string {
	if d.running == nil {
		return ""
	}
	return d.running.out.String()
}

// loopErr is what the running loop has printed to stderr.
func (d *Dispatch) loopErr() string {
	if d.running == nil {
		return ""
	}
	return d.running.err.String()
}

func (d *Dispatch) tick() string {
	d.t.Helper()
	out, err := d.output("tick")
	require.NoError(d.t, err)
	return out
}

// advance moves the clock forward, which fires the resync when it is due, and
// waits until the loop has settled.
func (d *Dispatch) advance(by time.Duration) {
	d.t.Helper()
	d.moving(func() int { return d.Clock.Advance(by) })
}

// moving runs move, which changes the clock and returns how many timers it fired, and waits until
// the loop has settled after a timer fired.
func (d *Dispatch) moving(move func() int) {
	d.t.Helper()
	var before loop.Response
	if d.loopUp.Load() {
		var err error
		before, err = loop.Ask(context.Background(), d.state, loop.Request{Op: loop.OpIdle})
		require.NoError(d.t, err)
	}
	fired := move()
	if !d.loopUp.Load() || fired == 0 {
		return
	}
	require.Eventually(d.t, func() bool {
		resp, err := loop.Ask(context.Background(), d.state, loop.Request{Op: loop.OpIdle})
		return err == nil && !resp.Busy && resp.Idle > before.Idle
	}, 20*time.Second, 2*time.Millisecond)
}

// Deadline waits up to 20 seconds for the work to start the deadline on what, such as "launch ENG-1"
// or "rule spins if", and returns how far the clock is from passing it.
func (d *Dispatch) Deadline(what string) time.Duration {
	d.t.Helper()
	var left time.Duration
	require.Eventually(d.t, func() bool {
		var ok bool
		left, ok = d.Clock.untilDeadline(what)
		return ok
	}, 20*time.Second, time.Millisecond)
	return left
}

// DeadlineRunning is true while the work has a deadline on what that the clock has not passed.
func (d *Dispatch) DeadlineRunning(what string) bool {
	_, ok := d.Clock.untilDeadline(what)
	return ok
}

// Expire fires the deadline on what, as the clock passing it does, and waits until the loop has settled.
func (d *Dispatch) Expire(what string) {
	d.t.Helper()
	d.Deadline(what)
	d.moving(func() int { return d.Clock.expire(what) })
}

// seedHistory writes a segment as an earlier run left it, with no stop line as after
// a crash, for the next loop start to resume from. Call it before the loop starts.
func (d *Dispatch) seedHistory(mem *world.Memory, receipts ...ledger.Receipt) {
	d.t.Helper()
	require.Nil(d.t, d.running)
	if mem == nil {
		mem = world.NewMemory()
	}
	var resync int
	for _, rc := range receipts {
		resync = max(resync, rc.Resync)
	}
	w, err := history.Start(filepath.Join(d.state, "events"), d.Clock.Now().Add(-time.Hour),
		history.Header{Why: "start", At: d.Clock.Now().Add(-time.Hour).UnixMilli()},
		history.Snapshot{At: d.Clock.Now().Add(-time.Hour).UnixMilli(), World: mem, Ledger: history.LedgerState{Receipts: receipts}, Resync: resync})
	require.NoError(d.t, err)
	require.NoError(d.t, w.Close(false))
}

// planRow matches the plan row that gives a ticket a state, such as held or waiting.
func planRow(state, ticket string) string {
	return `(?m)^` + state + `\s+` + ticket + `\s`
}

// receipts lists the receipts of one kind the loop has written, oldest first. An empty
// subject matches every subject.
func (d *Dispatch) receipts(kind, subject string) []ledger.Receipt {
	d.t.Helper()
	var found []ledger.Receipt
	for _, r := range d.ledgerNow() {
		if r.Kind == kind && (subject == "" || r.Subject == subject) {
			found = append(found, r)
		}
	}
	return found
}

// ledgerNow is the ledger the newest history segment holds, which is what the loop has done.
func (d *Dispatch) ledgerNow() []ledger.Receipt {
	d.t.Helper()
	seg, ok, err := history.Latest(filepath.Join(d.state, "events"))
	require.NoError(d.t, err)
	require.True(d.t, ok)
	st, err := seg.Load()
	require.NoError(d.t, err)
	return st.Ledger.Entries
}

// fakeClock is the time the loop and the tools see. It moves only through Advance.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) NewTimer(d time.Duration) dispatch.Timer { return c.NewDeadline("", d) }

// NewDeadline starts a timer a test can expire by the name of the work it bounds.
func (c *fakeClock) NewDeadline(what string, d time.Duration) dispatch.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{clock: c, c: make(chan time.Time, 1), at: c.now.Add(d), what: what}
	c.timers = append(c.timers, t)
	return t
}

// untilDeadline is how far the clock must move to pass the deadline for what, or false when
// no deadline for it is running.
func (c *fakeClock) untilDeadline(what string) (time.Duration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, t := range c.timers {
		if !t.done && t.what == what {
			return max(t.at.Sub(c.now), 0), true
		}
	}
	return 0, false
}

// expire fires the first live deadline named what and leaves the time alone, so deadlines due at
// the same instant, such as a rule's budget and the request inside it, pass one at a time.
func (c *fakeClock) expire(what string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range c.timers {
		if !t.done && t.what == what {
			t.done = true
			t.c <- c.now
			return 1
		}
	}
	return 0
}

// Skew moves the clock without firing a timer, as a machine that slept through a resync does.
func (c *fakeClock) Skew(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Advance moves the clock and fires the timers that are due, returning how many.
func (c *fakeClock) Advance(d time.Duration) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	fired := 0
	for _, t := range c.timers {
		if !t.done && !c.now.Before(t.at) {
			t.done = true
			t.c <- c.now
			fired++
		}
	}
	return fired
}

// AdvanceLagging fires the timers that are due after d while the wall time moves only d less lag.
func (c *fakeClock) AdvanceLagging(d, lag time.Duration) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d - lag)
	fired := 0
	for _, t := range c.timers {
		if !t.done && !c.now.Add(lag).Before(t.at) {
			t.done = true
			t.c <- c.now
			fired++
		}
	}
	return fired
}

type fakeTimer struct {
	clock *fakeClock
	c     chan time.Time
	at    time.Time
	done  bool
	what  string
}

func (t *fakeTimer) C() <-chan time.Time { return t.c }

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	was := !t.done
	t.done = true
	return was
}

func (d *Dispatch) tickets(tickets ...fakeTicket) {
	d.linear.setTickets(tickets)
}

// worker gives a ticket a leased tree and a herdr workspace on it. An empty
// status means the workspace has no Claude agent.
func (d *Dispatch) worker(ticket, status string) {
	d.workerOn(ticket, filepath.Join(d.dir, "trees", ticket), "w-"+ticket, status)
}

// workerOn is worker for a tree at a chosen path and workspace id.
func (d *Dispatch) workerOn(ticket, tree, id, status string) {
	d.tools.Treehouse.Seed(d.Repo, treehouse.Tree{Path: tree, Status: "leased", LeaseHolder: ticket})

	d.tools.Herdr.AddWorkspace(herdr.Workspace{ID: id, Label: ticket, CheckoutPath: tree})
	if status == "" {
		return
	}
	d.tools.Herdr.AddTab(herdr.Tab{ID: id + ":t1", Label: ticket, WorkspaceID: id})
	d.tools.Herdr.AddPane(herdr.Pane{ID: id + ":p1", TabID: id + ":t1", WorkspaceID: id})
	d.tools.Herdr.AddAgent(herdr.Agent{Kind: "claude", Status: status, Session: "worker", PaneID: id + ":p1", TabID: id + ":t1", WorkspaceID: id})
}

// setWorkerStatus changes the herdr status of a worker added with worker.
func (d *Dispatch) setWorkerStatus(ticket, status string) {
	d.tools.Herdr.SetStatus("w-"+ticket+":p1", status)
}

// setWorkerSession changes the Claude session id herdr reports for a worker
// added with worker, as a relaunch does.
func (d *Dispatch) setWorkerSession(ticket, session string) {
	d.tools.Herdr.SetSession("w-"+ticket+":p1", session)
}

// blockAfterSnapshot has the worker stop to ask a question after dispatch's
// snapshot, so herdr refuses a prompt the snapshot says is fine.
func (d *Dispatch) blockAfterSnapshot(ticket string) {
	d.tools.Herdr.BlockAfterSnapshot("w-" + ticket + ":p1")
}

// vanishAfterSnapshot closes the worker's pane after dispatch's snapshot, so herdr no longer
// knows a pane the snapshot lists.
func (d *Dispatch) vanishAfterSnapshot(ticket string) {
	d.tools.Herdr.VanishAfterSnapshot("w-" + ticket + ":p1")
}

// unblock undoes blockAfterSnapshot.
func (d *Dispatch) unblock(ticket string) {
	d.tools.Herdr.Unblock("w-" + ticket + ":p1")
}

// prompts lists what was typed into workers, as "<pane>|<text>".
func (d *Dispatch) prompts() []string {
	return d.tools.Herdr.Prompted()
}

// notifications lists the macOS notification texts shown.
func (d *Dispatch) notifications() []string {
	return d.tools.Alerts.Shown()
}

func (d *Dispatch) lines(name string) []string {
	b, err := os.ReadFile(filepath.Join(d.dir, name))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(d.t, err)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// labelWorkspace sets the label herdr reports for a workspace.
func (d *Dispatch) labelWorkspace(id, label string) {
	d.tools.Herdr.SetWorkspaceLabel(id, label)
}

// renames lists the workspace renames, as "<workspace>|<label>".
func (d *Dispatch) renames() []string {
	return d.tools.Herdr.WorkspaceRenames()
}

// tabRenames lists the tab renames, as "<tab>|<label>".
func (d *Dispatch) tabRenames() []string {
	return d.tools.Herdr.TabRenames()
}

// labelTab sets the label herdr reports for a worker's tab.
func (d *Dispatch) labelTab(ticket, label string) {
	d.tools.Herdr.SetTabLabel("w-"+ticket+":t1", label)
}

// closes lists the herdr workspaces that were closed.
func (d *Dispatch) closes() []string {
	return d.tools.Herdr.ClosedWorkspaces()
}

// returns lists the treehouse return calls, as "<repo>|<tree>|<holder>".
func (d *Dispatch) returns() []string {
	return d.tools.Calls("treehouse", "return")
}

// pool makes the dispatch repo a git repo that treehouse pools, with one tree
// in it, and returns the tree's path.
func (d *Dispatch) pool() string {
	tree := filepath.Join(d.dir, "tree")
	require.NoError(d.t, os.WriteFile(filepath.Join(d.Repo, "treehouse.toml"), []byte("max_trees = 4\n"), 0o644))
	copyPlainRepo(d.t, d.Repo)
	hintGit(d.t, d.Repo, "worktree", "add", "-q", "--detach", tree)
	d.tools.Treehouse.AddTree(d.Repo, tree)
	return tree
}

func (d *Dispatch) launchResult(result string) {
	var r launcher.Result
	require.NoError(d.t, json.Unmarshal([]byte(result), &r))
	d.tools.Launcher.Answer(r)
}

// launches lists the launch requests the dispatcher made, as
// "<repo> <ticket> <branch> --model <model>" plus " --kickoff <text>" when one was set.
func (d *Dispatch) launches() []string {
	var found []string
	for _, r := range d.tools.Launcher.Launched() {
		line := fmt.Sprintf("%s %s %s --model %s", r.RepoDir, r.Ticket, r.Branch, r.Model)
		if r.Kickoff != "" {
			line += " --kickoff " + r.Kickoff
		}
		found = append(found, line+"")
	}
	return found
}

func (d *Dispatch) removeFile(name string) {
	require.NoError(d.t, os.Remove(filepath.Join(d.dir, name)))
}

func (d *Dispatch) writeFile(name, content string) {
	require.NoError(d.t, os.WriteFile(filepath.Join(d.dir, name), []byte(content), 0o644))
}

func (d *Dispatch) writeJSON(name string, v any) {
	b, err := json.Marshal(v)
	require.NoError(d.t, err)
	d.writeFile(name, string(b))
}

func (d *Dispatch) readFile(name string) []byte {
	b, err := os.ReadFile(filepath.Join(d.dir, name))
	require.NoError(d.t, err)
	return b
}

// fakeTicket is one ticket in the Linear workspace.
type fakeTicket = lineartest.Issue

// ticket builds an ai-labeled ENG ticket in status, labeled with repo when
// repo is not empty.
func ticket(id, status, repo string) fakeTicket {
	labels := []linear.Label{{Name: "ai"}}
	if repo != "" {
		labels = append(labels, linear.Label{Name: repo, Group: "repo"})
	}
	return fakeTicket{
		Ticket: linear.Ticket{
			ID:         "uuid-" + id,
			Identifier: id,
			Title:      "ticket " + id,
			URL:        "https://linear.app/team/issue/" + id,
			Branch:     "thrawn01/" + strings.ToLower(id),
			Status:     status,
			Team:       "ENG",
			Labels:     labels,
		},
		Assignee: viewerID,
	}
}

func withLabel(t fakeTicket, name string) fakeTicket {
	t.Labels = append(slices.Clone(t.Labels), linear.Label{Name: name})
	return t
}

// linearHarness seeds the Linear workspace from three groups of tickets, so a
// test can replace one group without restating the others.
type linearHarness struct {
	*lineartest.Workspace
	managed, elsewhere, children []fakeTicket
}

func (l *linearHarness) seed() {
	var all []fakeTicket
	seen := map[string]bool{}
	for _, group := range [][]fakeTicket{l.managed, l.elsewhere, l.children} {
		for _, t := range group {
			if !seen[t.Identifier] {
				seen[t.Identifier] = true
				all = append(all, t)
			}
		}
	}
	l.Seed(all...)
}

// setTickets sets the tickets that are started and assigned to the key's owner.
func (l *linearHarness) setTickets(tickets []fakeTicket) {
	l.managed = tickets
	l.seed()
}

// setElsewhere sets the tickets Linear knows that are not started, such as a
// Done or Todo ticket, which only a lookup by identifier finds.
func (l *linearHarness) setElsewhere(tickets ...fakeTicket) {
	l.elsewhere = tickets
	l.seed()
}

// setChildren sets the children Linear returns for open parents.
func (l *linearHarness) setChildren(tickets ...fakeTicket) {
	l.children = tickets
	l.seed()
}

// mutations lists the moves and comments Linear accepted.
func (l *linearHarness) mutations() []lineartest.Change { return l.Changes() }

// calls counts the requests Linear answered.
func (l *linearHarness) calls() int { return l.Calls() }

// lookups counts the lookups of tickets by identifier.
func (l *linearHarness) lookups() int { return l.CallsTo(lineartest.OnLeased) }

// childReadParents lists the parent ids each children read asked for.
func (l *linearHarness) childReadParents() [][]string { return l.Reads() }

func (l *linearHarness) failMoves() { l.FailOn(lineartest.OnMove, true) }

func (l *linearHarness) unsuccessfulMoves() { l.FailOn(lineartest.OnUnsuccessfulMove, true) }

func (l *linearHarness) unsuccessfulComments() { l.FailOn(lineartest.OnUnsuccessfulComment, true) }

func (l *linearHarness) failComments(on bool) { l.FailOn(lineartest.OnComment, on) }

func (l *linearHarness) failLookups() { l.FailOn(lineartest.OnLeased, true) }

func (l *linearHarness) allowLookups() { l.FailOn(lineartest.OnLeased, false) }

func (l *linearHarness) failChildren() { l.FailOn(lineartest.OnChildren, true) }

// overflowManaged adds enough started tickets to push a read of managed
// tickets onto a second page.
func (l *linearHarness) overflowManaged() {
	for n := range linear.PageSize {
		l.managed = append(l.managed, ticket(fmt.Sprintf("ENG-%d", 1000+n), "In Progress", "dispatch"))
	}
	l.seed()
}

func loadExample(t *testing.T, body string) (config.Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return config.Load(path, environment.Process())
}

const (
	draftSetting    = "open_pr_as = \"draft\"\n"
	draftReadyText1 = "ENG-1's draft is ready for your review: " + prURL1
	approvedReady1  = "ENG-1 is approved and ready to merge: " + prURL1
)

const childCommentRule = `
whenever({ name: "greet-child", each: "ticket", if: (t) => t.id === "ENG-2",
  then: (t, ctx) => ctx.ticket.queueComment(t, "hello", { once: ["greet", t.statusSince] }) });`

const (
	sentryToken  = "sntrys_tok_4f9a1c"
	sentryConfig = "\n[http.sentry]\nbase_url = \"https://sentry.test/api/0\"\ntoken_command = \"op read op://dev/sentry/token\"\n"
	unresolved   = "/issues/?query=is:unresolved"
)

// sentry gives Dispatch the sentry service its config names.
func (d *Dispatch) sentry() *httpservicetest.Fake {
	d.config(sentryConfig)
	d.http = httpservicetest.New(map[string]config.HTTPService{"sentry": {BaseURL: "https://sentry.test/api/0"}})
	d.http.Token("sentry", sentryToken)
	return d.http
}

var (
	dispatchBin     string
	dispatchBinErr  error
	dispatchBinOnce sync.Once
)

// buildDispatch returns the directory holding the dispatch binary TestMain compiled, and compiles it itself if TestMain has not.
func buildDispatch(t *testing.T) string {
	t.Helper()
	dispatchBinOnce.Do(compileDispatch)
	require.NoError(t, dispatchBinErr)
	return dispatchBin
}

// compileDispatch builds ./cmd/dispatch into dispatchBin and records a failure in dispatchBinErr.
func compileDispatch() {
	var err error
	if dispatchBin, err = os.MkdirTemp("", "dispatch-bin"); err != nil {
		dispatchBinErr = err
		return
	}
	build := exec.Command("go", "build", "-o", filepath.Join(dispatchBin, "dispatch"), "./cmd/dispatch")
	// The Go toolchain records its own telemetry under HOME, which is not dispatch writing there.
	build.Env = append(os.Environ(), "HOME="+dispatchBin)
	if out, err := build.CombinedOutput(); err != nil {
		dispatchBinErr = fmt.Errorf("while building dispatch: %w\n%s", err, out)
	}
}

// child builds a child of parent. blockedBy pairs a blocker's identifier with
// its status.
func child(id, parent, status string, blockedBy ...[2]string) fakeTicket {
	t := ticket(id, status, "dispatch")
	t.Parent = parent
	for _, b := range blockedBy {
		t.Blockers = append(t.Blockers, linear.Blocker{Identifier: b[0], Status: b[1], Abandoned: stateType(b[1]) == "canceled"})
	}
	return t
}

func stateType(status string) string {
	if status == "Canceled" || status == "Duplicate" {
		return "canceled"
	}
	return "started"
}

// parent builds a ticket that becomes a parent once a test gives it children
// with setChildren, as Linear derives it.
func parent(id, status string) fakeTicket {
	return ticket(id, status, "dispatch")
}

// parentsRealLauncher makes launches create a herdr workspace, so a launched worker
// counts against the cap the way it does in production. The fake launcher leaves no trace.
func parentsRealLauncher(d *Dispatch) {
	d.builtinLauncher = true
	d.pool()
}

const readyText1 = "ENG-1 is ready for review: " + prURL1

const requiredOnly = "ignore_optional_checks = true\n"

const prURL1 = "https://github.com/kapetan-io/dispatch/pull/7"

func prPrompt(problem string) string {
	return fmt.Sprintf("w-ENG-1:p1|/dispatch:pr-respond PR %s for ENG-1 has %s. Handle it, and push.", prURL1, problem)
}

// prState is the part of a PR the dispatcher reads.
type prState struct {
	head        string
	checks      string
	decision    string
	mergeable   string
	unresolved  int
	resolved    int
	moreThreads bool
	// approved lists the commits a review approves.
	approved []string
	// changesOn lists the commits a review requests changes on. A decision of
	// CHANGES_REQUESTED alone adds a request on the head. dispatch reads the
	// reviews, not the decision field.
	changesOn []string
	// fork marks a PR opened from a fork.
	fork bool
	// noChecks marks a PR whose head has no checks, which GitHub reports as a null rollup.
	noChecks bool
	// forksAhead is how many fork PRs on the same branch name GitHub lists ahead of this PR in its page.
	forksAhead int
	// forkPages is how many full pages of fork PRs on the same branch name GitHub lists before the page
	// holding this PR. The cursor of the nth is "c<n>".
	forkPages int
	// endlessForks makes every page after the first point at itself as the next, so this PR is never reached.
	endlessForks bool
	// draft marks a PR that has not been opened for review.
	draft bool
	// threads are the newest comments of unresolved review threads.
	threads []activity
	// reviews and comments are the PR's activity, oldest first.
	reviews  []activity
	comments []activity
	// opinions are the reviewers' latest approvals and change requests, with who wrote them.
	opinions []opinion
	// conversations are unresolved review threads with several comments, oldest first.
	conversations [][]activity
	// reviewsBefore and commentsBefore are the cursors of older pages, "" when
	// the window reaches the start.
	reviewsBefore, commentsBefore string
	// contexts are the checks behind the rollup, which GitHub reports only when
	// asked by PR number. A PR with none leaves the checks answer unset, so a
	// read that asks for it fails.
	contexts []checkContext
}

// checkContext is one check on the head: a CheckRun with a state of
// "SUCCESS", "FAILURE" or "IN_PROGRESS", or a StatusContext, which a state of "EXPECTED" or "ERROR" makes.
type checkContext struct {
	name, state string
	required    bool
}

// opinion is a reviewer's latest approval or change request on a commit.
type opinion struct {
	state, commit, author string
}

// activity is a review (with a state) or a conversation comment.
type activity struct {
	state  string
	at     string
	author string
	body   string
	// submitted is when a review left draft, "" for the same time as at.
	submitted string
}

func activityNodes(list []activity, before string) map[string]any {
	out := []map[string]any{}
	for _, a := range list {
		n := map[string]any{"createdAt": a.at, "author": map[string]string{"login": a.author}}
		if a.state != "" {
			n["state"] = a.state
			n["body"] = a.body
			n["submittedAt"] = cmp.Or(a.submitted, a.at)
		}
		out = append(out, n)
	}
	return map[string]any{"nodes": out, "pageInfo": map[string]any{"hasPreviousPage": before != "", "startCursor": before}}
}

func cleanPR(head string) prState {
	return prState{head: head, checks: "SUCCESS", decision: "APPROVED", mergeable: "MERGEABLE"}
}

// pr makes the ticket's branch have an open PR in this state.
func (d *Dispatch) pr(ticket string, p prState) {
	if p.contexts != nil {
		d.respondChecks(p.head, p.contexts, false)
	}
	threads := []map[string]any{}
	for range p.unresolved {
		threads = append(threads, map[string]any{"isResolved": false})
	}
	for _, a := range p.threads {
		threads = append(threads, map[string]any{"isResolved": false, "comments": activityNodes([]activity{a}, "")})
	}
	for _, c := range p.conversations {
		threads = append(threads, map[string]any{"isResolved": false, "comments": activityNodes(c, "")})
	}
	for range p.resolved {
		threads = append(threads, map[string]any{"isResolved": true})
	}
	reviews := []map[string]any{}
	for _, oid := range p.approved {
		reviews = append(reviews, map[string]any{"state": "APPROVED", "commit": map[string]string{"oid": oid}})
	}
	changesOn := p.changesOn
	if p.decision == "CHANGES_REQUESTED" && changesOn == nil {
		changesOn = []string{p.head}
	}
	for _, oid := range changesOn {
		reviews = append(reviews, map[string]any{"state": "CHANGES_REQUESTED", "commit": map[string]string{"oid": oid}})
	}
	for _, o := range p.opinions {
		reviews = append(reviews, map[string]any{"state": o.state, "commit": map[string]string{"oid": o.commit}, "author": map[string]string{"login": o.author}})
	}
	var rollup any = map[string]string{"state": p.checks}
	if p.noChecks {
		rollup = nil
	}
	own := map[string]any{
		"number":                   7,
		"url":                      prURL1,
		"isCrossRepository":        p.fork,
		"headRefOid":               p.head,
		"mergeable":                p.mergeable,
		"isDraft":                  p.draft,
		"reviewDecision":           p.decision,
		"commits":                  map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{"statusCheckRollup": rollup}}}},
		"latestOpinionatedReviews": map[string]any{"nodes": reviews},
		"reviews":                  activityNodes(p.reviews, p.reviewsBefore),
		"comments":                 activityNodes(p.comments, p.commentsBefore),
		"reviewThreads":            map[string]any{"nodes": threads, "pageInfo": map[string]bool{"hasNextPage": p.moreThreads}},
	}
	d.respondPages(ticket, p, own)
}

// respondPages sets the pages of the ticket's PR query: the worker's PR, behind the fork PRs p asks for.
func (d *Dispatch) respondPages(ticket string, p prState, own map[string]any) {
	const pageSize = 20
	forks := func(count int) []any {
		nodes := []any{}
		for range count {
			fork := maps.Clone(own)
			fork["isCrossRepository"], fork["headRefOid"], fork["url"] = true, "evil", "https://github.com/kapetan-io/dispatch/pull/99"
			nodes = append(nodes, fork)
		}
		return nodes
	}
	page := func(nodes []any, next string) map[string]any {
		pullRequests := map[string]any{"nodes": nodes}
		if next != "" {
			pullRequests["pageInfo"] = map[string]any{"hasNextPage": true, "endCursor": next}
		}
		return map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequests": pullRequests}}}
	}
	if p.forkPages == 0 {
		d.respond(ticket, "", page(append(forks(p.forksAhead), own), ""))
		return
	}
	d.respond(ticket, "", page(forks(pageSize), "c1"))
	for n := 1; n <= p.forkPages; n++ {
		cursor := fmt.Sprintf("c%d", n)
		switch {
		case p.endlessForks:
			d.respond(ticket, cursor, page(forks(pageSize), cursor))
		case n < p.forkPages:
			d.respond(ticket, cursor, page(forks(pageSize), fmt.Sprintf("c%d", n+1)))
		default:
			d.respond(ticket, cursor, page([]any{own}, ""))
		}
	}
}

// respondChecks sets what GitHub answers when asked for the checks behind PR 7's rollup.
func (d *Dispatch) respondChecks(head string, contexts []checkContext, more bool) {
	nodes := []map[string]any{}
	for _, c := range contexts {
		node := map[string]any{"__typename": "CheckRun", "name": c.name, "isRequired": c.required, "status": "COMPLETED", "conclusion": c.state}
		switch c.state {
		case "IN_PROGRESS":
			node["status"], node["conclusion"] = "IN_PROGRESS", nil
		case "EXPECTED", "ERROR":
			node = map[string]any{"__typename": "StatusContext", "context": c.name, "isRequired": c.required, "state": c.state}
		}
		nodes = append(nodes, node)
	}
	d.tools.PullRequests.RespondChecks(7, map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{"commits": map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{"oid": head, "statusCheckRollup": map[string]any{"contexts": map[string]any{"nodes": nodes, "pageInfo": map[string]bool{"hasNextPage": more}}}}}}}}}}})
}

// respondOlder sets what GitHub answers for the page of the PR's reviews or
// comments that ends before the cursor. kind is "reviews" or "comments".
func (d *Dispatch) respondOlder(kind, before string, items []activity, earlier string) {
	d.tools.PullRequests.RespondOlder(7, kind, before, map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{kind: activityNodes(items, earlier)}}}})
}

// prBranch is the branch Linear names for a ticket.
func prBranch(ticket string) string {
	return "thrawn01/" + strings.ToLower(ticket)
}

// respond sets what GitHub answers for the ticket's PR query, for the page
// after cursor, or the first page when cursor is empty.
func (d *Dispatch) respond(ticket, cursor string, response any) {
	b, err := json.Marshal(response)
	require.NoError(d.t, err)
	d.tools.PullRequests.Respond(prBranch(ticket), cursor, json.RawMessage(b))
}

// closePR closes the ticket's PR without merging it.
func (d *Dispatch) closePR(ticket string) {
	d.tools.PullRequests.Close(prBranch(ticket))
}

// merges lists the PRs that merged, as url|head.
func (d *Dispatch) merges() []string {
	return d.tools.PullRequests.Merged()
}

// prReads lists the branches dispatch read a PR for, with ".cursor" after a
// page past the first.
func (d *Dispatch) prReads() []string {
	var reads []string
	for _, call := range d.tools.Calls("pullrequest", "read") {
		branch, cursor, _ := strings.Cut(call, "|")
		if cursor != "" {
			branch += "." + cursor
		}
		reads = append(reads, branch)
	}
	return reads
}

// rulesDir writes the files into a new directory and returns it.
func rulesDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	return dir
}

// oneRule is a rules directory holding dispatch.ts with just this body.
func oneRule(t *testing.T, body string) string {
	t.Helper()
	return rulesDir(t, rule(body))
}

func (d *Dispatch) rules(dir string, args ...string) (string, error) {
	d.t.Helper()
	stdout, _, err := d.runWith(context.Background(), dispatch.Options{RulesDir: dir}, append([]string{"rules"}, args...)...)
	return stdout, err
}

// Root tests drive dispatch the way an operator does, so a refactor below the command line leaves them
// untouched. A test case may import the adapters of the external tools and their fakes, the config
// and the environment, which an operator also sees. Any other internal package is dispatch's own
// state and logic. harness_test.go is the one file that waits on the loop and writes history fixtures.
func TestRootTestsImportNoDispatchState(t *testing.T) {
	t.Parallel()
	const internal = "github.com/kapetan-io/dispatch/internal/"
	allowed := []string{"config", "environment", "herdr", "herdrtest", "httpservicetest", "linear", "lineartest", "tooltest", "treehouse"}
	files, err := filepath.Glob("*_test.go")
	require.NoError(t, err)
	require.NotEmpty(t, files)

	for _, file := range files {
		if file == "harness_test.go" {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		require.NoError(t, err)
		for _, spec := range parsed.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			require.NoError(t, err)
			if name, ok := strings.CutPrefix(path, internal); ok {
				assert.Contains(t, allowed, name, file)
			}
		}
	}
}

// The test binary doubles as the tsc these tests put on PATH: a symlink named tsc
// points at it, and TestMain runs fakeTSCMain when it is started under that name.
func TestMain(m *testing.M) {
	// The race detector sleeps 1s at exit by default. The stand-in processes this binary re-execs inherit this setting.
	_ = os.Setenv("GORACE", strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	if filepath.Base(os.Args[0]) == "tsc" {
		os.Exit(fakeTSCMain(os.Args[1:]))
	}
	// A test that skips newDispatch inherits this HOME, so a write there fails the package
	// instead of landing in the real dispatch history.
	guard, err := os.MkdirTemp("", "dispatch-home")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// go build keeps its caches under HOME, so pin them before HOME moves.
	for _, name := range []string{"GOCACHE", "GOMODCACHE", "GOPATH"} {
		out, err := exec.Command("go", "env", name).Output()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		_ = os.Setenv(name, strings.TrimSpace(string(out)))
	}
	_ = os.Setenv("HOME", guard)
	// Compiled here so no test's measured time includes the build.
	dispatchBinOnce.Do(compileDispatch)
	code := m.Run()
	if dispatchBin != "" {
		_ = os.RemoveAll(dispatchBin)
	}
	if template, err := plainRepo(); err == nil {
		_ = os.RemoveAll(template)
	}
	if written, _ := os.ReadDir(guard); len(written) > 0 {
		fmt.Fprintf(os.Stderr, "FAIL: a test wrote under the home directory: %v\n", written)
		code = 1
	}
	_ = os.RemoveAll(guard)
	os.Exit(code)
}

// fakeTSCMain records its arguments and the config named by -p, prints what the
// test asked for, and exits with the code the test asked for.
func fakeTSCMain(args []string) int {
	record := os.Getenv("FAKE_TSC_RECORD")
	out := strings.Join(args, " ") + "\n"
	if len(args) == 2 {
		if conf, err := os.ReadFile(args[1]); err == nil {
			out += string(conf)
		}
	}
	if err := os.WriteFile(record, []byte(out), 0o644); err != nil {
		return 99
	}
	fmt.Print(os.Getenv("FAKE_TSC_OUTPUT"))
	code, _ := strconv.Atoi(os.Getenv("FAKE_TSC_EXIT"))
	return code
}

// fakeTSC puts a tsc on the PATH of d's commands and returns the file it records its arguments in.
func (d *Dispatch) fakeTSC(exit int, output string) (record string) {
	t := d.t
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	bin := t.TempDir()
	require.NoError(t, os.Symlink(exe, filepath.Join(bin, "tsc")))
	record = filepath.Join(t.TempDir(), "args")
	d.env = d.env.With("FAKE_TSC_RECORD", record).With("FAKE_TSC_OUTPUT", output).
		With("FAKE_TSC_EXIT", strconv.Itoa(exit)).
		With("PATH", bin+string(os.PathListSeparator)+d.env.Get("PATH"))
	return record
}

// recordStopFrom runs the Stop hook as the Claude session with the given id, in a real worktree of the
// harness repo. tasks is the background_tasks member of the hook input, or "" to leave it out.
func (d *Dispatch) recordStopFrom(session, ticket, tasks string) {
	d.t.Helper()
	_, _, err := d.runWith(context.Background(), dispatch.Options{Stdin: strings.NewReader(d.stopInput(session, ticket, tasks))}, "record-stop")
	require.NoError(d.t, err)
}

// stopInput is what Claude Code hands the Stop hook for a session in the ticket's tree.
func (d *Dispatch) stopInput(session, ticket, tasks string) string {
	d.t.Helper()
	tree := d.worktreeOf(ticket)
	input := fmt.Sprintf(`{"hook_event_name":"Stop","session_id":%q,"cwd":%q`, session, tree)
	if tasks != "" {
		input += "," + tasks
	}
	return input + "}"
}

// worktreeOf is the path of ticket's tree, made a real git worktree of d.Repo.
func (d *Dispatch) worktreeOf(ticket string) string {
	d.t.Helper()
	tree := filepath.Join(d.dir, "trees", ticket)
	if _, err := os.Stat(filepath.Join(d.Repo, ".git")); err != nil {
		copyPlainRepo(d.t, d.Repo)
	}
	if _, err := os.Stat(tree); err != nil {
		hintGit(d.t, d.Repo, "worktree", "add", "-q", "-b", "br-"+ticket, tree)
	}
	return tree
}

// gitTree makes a pool tree that is clean and pushed: a clone of a bare
// remote on main, at <dir>/trees/<name>, the path h.worker uses for a ticket
// named <name>.
func (d *Dispatch) gitTree(ticket string) string {
	path := filepath.Join(d.dir, "trees", ticket)
	remote := filepath.Join(d.dir, "remotes", ticket+".git")
	require.NoError(d.t, os.MkdirAll(filepath.Dir(remote), 0o755))
	d.git(d.dir, "init", "--bare", "--initial-branch=main", remote)
	require.NoError(d.t, os.MkdirAll(path, 0o755))
	d.git(path, "init", "--initial-branch=main")
	d.git(path, "remote", "add", "origin", remote)
	d.commit(path)
	d.push(path)
	return path
}

// lease holds a tree for a ticket with no herdr workspace on it.
func (d *Dispatch) lease(ticket, path string) {
	d.tools.Treehouse.Clear(d.Repo)
	d.tools.Treehouse.Seed(d.Repo, treehouse.Tree{Path: path, Status: "leased", LeaseHolder: ticket})
}

// dirty stages a new file in the tree.
func (d *Dispatch) dirty(path string) {
	d.untracked(path, "wip.txt")
	d.git(path, "add", "wip.txt")
}

// untracked leaves a file in the tree that git has never seen.
func (d *Dispatch) untracked(path, name string) {
	require.NoError(d.t, os.WriteFile(filepath.Join(path, name), []byte("wip"), 0o644))
}

// commit adds a commit that no remote has.
func (d *Dispatch) commit(path string) {
	d.git(path, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "work")
}

func (d *Dispatch) push(path string) {
	d.git(path, "push", "origin", "HEAD:main")
	d.git(path, "fetch", "origin")
}

func (d *Dispatch) git(dir string, args ...string) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	require.NoError(d.t, err, string(out))
}

func newHintRepo(t *testing.T) string {
	t.Helper()
	// Resolved so a /var versus /private/var symlink cannot hide a match.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	copyPlainRepo(t, dir)
	return dir
}

// plainRepo is a git repo on main with one empty commit, built once per test run. A test that needs
// a plain repo copies it, which costs a directory copy instead of three git processes.
var plainRepo = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "dispatch-plain-repo")
	if err != nil {
		return "", err
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		// Git detaches a background maintenance run after a commit, and its lock file can vanish
		// while copyPlainRepo copies the directory.
		{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "maintenance.auto=false", "-c", "gc.auto=0", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("git %v: %w: %s", args, err, out)
		}
	}
	return dir, nil
})

// copyPlainRepo makes dir a git repo with one empty commit. Tests that need a real worktree still
// add one to it.
func copyPlainRepo(t *testing.T, dir string) {
	t.Helper()
	template, err := plainRepo()
	require.NoError(t, err)
	require.NoError(t, os.CopyFS(dir, os.DirFS(template)))
}

func writeHintConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

func hintGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

// turnsRule comments each time what a worker's turns read as changes, one comment for each reading.
const turnsRule = `
whenever({ name: "turns", each: "ticket",
  if: (t) => t.status === "in_progress" || t.status === "in_review",
  then: (t, ctx) => {
    const w = ctx.worker.of(t);
    const body = known(() => w.turns)
      ? "turns " + w.turns.map((turn) => turn.running ? "running" : "idle").join(",") + " of " + w.turns.length
      : "turns unknown";
    return ctx.ticket.queueComment(t, body, { once: ["turns", t.id, body] });
  } });`

// markLaunch leaves the mark a launch killed before its kickoff would leave, because a
// test cannot kill a launch halfway.
func markLaunch(t *testing.T, state, tree string, at time.Time) {
	t.Helper()
	_, err := kickoffs.Mark(state, tree, at)
	require.NoError(t, err)
}

// sessionSocket stands in for a Claude Code session's inbox and collects the
// lines written to it. The path is short because macOS caps socket paths.
func sessionSocket(t *testing.T) (string, <-chan string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ds")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "1.sock")
	ln, err := net.Listen("unix", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	lines := make(chan string, 8)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				sc := bufio.NewScanner(conn)
				for sc.Scan() {
					lines <- sc.Text()
				}
			}()
		}
	}()
	return path, lines
}

func next(t *testing.T, lines <-chan string) string {
	t.Helper()
	select {
	case line := <-lines:
		return line
	case <-time.After(5 * time.Second):
		require.FailNow(t, "no message arrived")
		return ""
	}
}

const countRule = `whenever({ name: "count", each: "ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.ticket.queueComment(t, "open " + (ctx.http.get("sentry", "/count").body as any).open, { once: ["count", t.id] }) });`

// exists is true once path exists, for a test to wait on a file a script writes.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// SavedFromMain copies a history segment a build of main wrote, under testdata/, into this test's
// history directory, so the next loop start resumes from it as a restart does.
func (d *Dispatch) SavedFromMain(name string) {
	d.t.Helper()
	require.NoError(d.t, os.CopyFS(filepath.Join(d.state, "events"), os.DirFS(filepath.Join("testdata", name))))
}
