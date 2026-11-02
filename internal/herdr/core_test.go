package herdr_test

// Package exception: this package wraps the hc tool, so every test here is an adapter test of it.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/herdr"
)

const (
	envCoreReplies = "HC_TEST_REPLIES"
	envCoreLog     = "HC_TEST_LOG"
	envAppLog      = "HERDR_APP_TEST_LOG"
	envAppFails    = "HERDR_APP_TEST_FAILS"
)

// runCoreStandIn plays `hc connect`. It reads one request, appends it to the log, and prints the
// next canned reply for the request's method, repeating the last one when they run out.
func runCoreStandIn(args []string) int {
	if len(args) > 0 && args[0] == "do" {
		return runAppStandIn(args)
	}
	if len(args) != 1 || args[0] != "connect" {
		fmt.Fprintln(os.Stderr, "unexpected command line:", args)
		return 2
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	var request struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal([]byte(line), &request); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	f, err := os.OpenFile(os.Getenv(envCoreLog), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	_, _ = fmt.Fprintf(f, "%s %s\n", request.Method, request.Params)
	_ = f.Close()

	var replies map[string][]string
	if err := json.Unmarshal([]byte(os.Getenv(envCoreReplies)), &replies); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	canned := replies[request.Method]
	if len(canned) == 0 {
		fmt.Fprintln(os.Stderr, "unexpected method:", request.Method)
		return 2
	}
	// Each call is a new process, so the position is kept in a file beside the log.
	seen := countLines(os.Getenv(envCoreLog), request.Method)
	_, _ = fmt.Println(canned[min(seen, len(canned))-1])
	return 0
}

// runAppStandIn plays `herdr-app do`: it appends its command line to the app log and, when
// the test says so, fails.
func runAppStandIn(args []string) int {
	f, err := os.OpenFile(os.Getenv(envAppLog), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	_, _ = fmt.Fprintln(f, strings.Join(args[1:], " "))
	_ = f.Close()
	if os.Getenv(envAppFails) != "" {
		fmt.Fprintln(os.Stderr, "no window is open")
		return 1
	}
	return 0
}

func countLines(path, method string) int {
	b, _ := os.ReadFile(path)
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, method+" ") {
			n++
		}
	}
	return n
}

// hcCalls is what a test's stand-in hc was asked, in order.
type hcCalls struct{ path string }

// Params returns the params of every request for method.
func (c hcCalls) Params(t *testing.T, method string) []map[string]any {
	t.Helper()
	b, _ := os.ReadFile(c.path)
	var found []map[string]any
	for _, l := range strings.Split(string(b), "\n") {
		name, raw, ok := strings.Cut(l, " ")
		if !ok || name != method {
			continue
		}
		var params map[string]any
		require.NoError(t, json.Unmarshal([]byte(raw), &params))
		found = append(found, params)
	}
	return found
}

// AppLines lists every herdr-app command as one line each.
func (c hcCalls) AppLines() []string {
	b, _ := os.ReadFile(c.path + ".app")
	if len(b) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// Methods lists the method of every request, in order.
func (c hcCalls) Methods() []string {
	b, _ := os.ReadFile(c.path)
	var methods []string
	for _, l := range strings.Split(string(b), "\n") {
		if name, _, ok := strings.Cut(l, " "); ok {
			methods = append(methods, name)
		}
	}
	return methods
}

// coreStandIn is a Core whose hc is this test binary, answering each method with the replies given.
func coreStandIn(t *testing.T, replies map[string][]string) (*herdr.Core, hcCalls) {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	raw, err := json.Marshal(replies)
	require.NoError(t, err)
	log := t.TempDir() + "/calls"
	t.Setenv(envCoreReplies, string(raw))
	t.Setenv(envCoreLog, log)
	t.Setenv(envAppLog, log+".app")
	env := environment.Process()
	core := herdr.NewCore(exe).WithApp(exe).WithKinds("claude").WithEnv(env).WithPace(1)
	return core, hcCalls{path: log}
}

func ok(result string) string { return `{"jsonrpc":"2.0","id":1,"result":` + result + `}` }

func fails(kind, message string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"data":{"kind":%q},"message":%q}}`, kind, message)
}

type spec struct {
	address, name, state, reason, status, kind, program string
	finishedMS                                          uint64
	turn                                                int
	labels                                              map[string]string
}

// term is one terminal as hc lists it.
func term(s spec) string {
	labels, _ := json.Marshal(s.labels)
	status := "null"
	if s.status != "" {
		status = fmt.Sprintf(`{"state":%q,"kind":%q}`, s.status, s.kind)
	}
	foreground := "null"
	if s.program != "" {
		foreground = fmt.Sprintf(`{"argv":[%q,"--model","sonnet"],"name":"2.1.0"}`, s.program)
	}
	if s.state == "" {
		s.state = "live"
	}
	reason := "null"
	if s.reason != "" {
		reason = fmt.Sprintf("%q", s.reason)
	}
	return fmt.Sprintf(`{"address":%q,"name":%q,"state":%q,"reason":%s,"finished_ms":%d,"labels":%s,"status":%s,"turn":%d,"foreground":%s}`,
		s.address, s.name, s.state, reason, s.finishedMS, labels, status, s.turn, foreground)
}

func list(terminals ...string) string {
	return ok(`{"hidden_finished":0,"terminals":[` + strings.Join(terminals, ",") + `]}`)
}

func worker(address, space, tab, status string) spec {
	return spec{address: address, status: status, program: "/Users/me/.local/bin/claude", labels: map[string]string{
		"dispatch.tree": "/trees/1", "dispatch.session": "sess-" + address, "herdr.space": space, "herdr.tab": tab,
	}}
}

func TestCoreReadShowsWorkerTerminalsAsWorkspacesTabsPanesAndAgents(t *testing.T) {
	core, _ := coreStandIn(t, map[string][]string{"terminal.list": {list(
		term(worker("t1", "spA", "tbA", "idle")),
		term(spec{address: "t2", program: "/bin/zsh"}),
		term(spec{address: "t3", program: "/Users/me/.local/bin/claude", status: "idle"}),
		term(spec{address: "t4", state: "finished", labels: map[string]string{"dispatch.tree": "/trees/2", "herdr.space": "spB", "herdr.tab": "tbB"}}),
	)}})

	snap, err := core.Read(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []herdr.Workspace{{ID: "spA", CheckoutPath: "/trees/1"}, {ID: "spB", CheckoutPath: "/trees/2"}}, snap.Workspaces)
	assert.Equal(t, []herdr.Tab{{ID: "tbA", WorkspaceID: "spA"}, {ID: "tbB", WorkspaceID: "spB"}}, snap.Tabs)
	assert.Equal(t, []herdr.Pane{{ID: "t1", TabID: "tbA", WorkspaceID: "spA"}}, snap.Panes)
	assert.Equal(t, []herdr.Agent{{Kind: "claude", Status: "idle", PaneID: "t1", TabID: "tbA", WorkspaceID: "spA", Session: "sess-t1"}}, snap.Agents)
}

func TestCoreReadMapsClaudesReportToAWorkerStatus(t *testing.T) {
	for _, test := range []struct {
		report, kind, want string
	}{
		{"idle", "", "idle"},
		{"done", "", "done"},
		{"working", "", "working"},
		{"blocked", "question", "blocked"},
		{"blocked", "permission", "blocked"},
		{"blocked", "auth", "blocked"},
		{"blocked", "", "blocked"},
		{"error", "", "unknown"},
		{"", "", "unknown"},
	} {
		t.Run(test.report+test.kind, func(t *testing.T) {
			w := worker("t1", "spA", "tbA", test.report)
			w.kind = test.kind
			core, _ := coreStandIn(t, map[string][]string{"terminal.list": {list(term(w))}})

			snap, err := core.Read(context.Background())

			require.NoError(t, err)
			require.Len(t, snap.Agents, 1)
			assert.Equal(t, test.want, snap.Agents[0].Status)
		})
	}
}

func TestCoreReadFindsNoAgentWhereClaudeHasExited(t *testing.T) {
	shell := worker("t1", "spA", "tbA", "idle")
	shell.program = "/bin/zsh"
	finished := worker("t2", "spA", "tbA", "")
	finished.state, finished.program = "finished", ""
	core, _ := coreStandIn(t, map[string][]string{"terminal.list": {list(term(shell), term(finished))}})

	snap, err := core.Read(context.Background())

	require.NoError(t, err)
	assert.Empty(t, snap.Agents)
	assert.Equal(t, []herdr.Pane{{ID: "t1", TabID: "tbA", WorkspaceID: "spA"}}, snap.Panes)
	require.Len(t, snap.Workspaces, 1)
}

func TestCoreReadNamesHcWhenItIsMissing(t *testing.T) {
	core := herdr.NewCore("hc-that-is-not-installed").WithEnv(environment.Environment{"PATH=" + t.TempDir()})

	_, err := core.Read(context.Background())

	require.ErrorContains(t, err, "hc-that-is-not-installed")
}

func TestCoreTranslatesHcRefusalsToThePortsCodes(t *testing.T) {
	for _, test := range []struct {
		kind, code string
	}{
		{"not_found", "agent_not_found"},
		{"blocked", "agent_blocked"},
		{"status_mismatch", "status_mismatch"},
		{"finished", "finished"},
	} {
		t.Run(test.kind, func(t *testing.T) {
			core, _ := coreStandIn(t, map[string][]string{
				"terminal.get":  {ok(term(worker("t1", "spA", "tbA", "idle")))},
				"terminal.send": {fails(test.kind, "refused")},
			})

			err := core.Prompt(context.Background(), "t1", "go on")

			var refusal *herdr.Refusal
			require.ErrorAs(t, err, &refusal)
			assert.Equal(t, test.code, refusal.Code)
			assert.Equal(t, "hc:terminal.send", refusal.Command)
			assert.True(t, herdr.NothingTyped(err))
		})
	}
}

func TestCorePromptTypesOnlyIntoAWorkerItCanDescribe(t *testing.T) {
	for _, test := range []struct {
		name, status string
		wantIf       any
	}{
		{"Idle", "idle", "idle"},
		{"Done", "done", nil},
		{"Working", "working", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			core, calls := coreStandIn(t, map[string][]string{
				"terminal.get":  {ok(term(worker("t1", "spA", "tbA", test.status)))},
				"terminal.send": {ok(`{"turn":1,"started":true}`)},
			})

			require.NoError(t, core.Prompt(context.Background(), "t1", "go on"))

			sends := calls.Params(t, "terminal.send")
			require.Len(t, sends, 1)
			assert.Equal(t, map[string]any{"terminal": "t1", "text": "go on"}, withoutIf(sends[0]))
			assert.Equal(t, test.wantIf, sends[0]["if"])
		})
	}
}

func withoutIf(params map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range params {
		if k != "if" {
			out[k] = v
		}
	}
	return out
}

func TestCorePromptRefusesWithNothingTypedWhenItCannotTell(t *testing.T) {
	for _, test := range []struct {
		name   string
		get    string
		wantIs error
		code   string
	}{
		{"NoStatus", ok(term(worker("t1", "spA", "tbA", ""))), herdr.ErrStatusUnknown, ""},
		{"ErrorStatus", ok(term(worker("t1", "spA", "tbA", "error"))), herdr.ErrStatusUnknown, ""},
		{"Blocked", ok(term(worker("t1", "spA", "tbA", "blocked"))), herdr.ErrAgentBlocked, "agent_blocked"},
		{"NotAWorker", ok(term(spec{address: "t1", program: "/Users/me/.local/bin/claude", status: "idle"})), nil, "agent_not_found"},
		{"NoSuchTerminal", fails("not_found", `no terminal "t1"`), nil, "agent_not_found"},
	} {
		t.Run(test.name, func(t *testing.T) {
			core, calls := coreStandIn(t, map[string][]string{"terminal.get": {test.get}})

			err := core.Prompt(context.Background(), "t1", "go on")

			require.Error(t, err)
			if test.wantIs != nil {
				require.ErrorIs(t, err, test.wantIs)
			}
			var refusal *herdr.Refusal
			if test.code != "" {
				require.ErrorAs(t, err, &refusal)
				assert.Equal(t, test.code, refusal.Code)
			}
			assert.True(t, herdr.NothingTyped(err))
			assert.Empty(t, calls.Params(t, "terminal.send"))
		})
	}
}

func TestCoreAgentAtFindsOnlyAWorkerRunningAListedProgram(t *testing.T) {
	core, _ := coreStandIn(t, map[string][]string{"terminal.get": {
		ok(term(worker("t1", "spA", "tbA", "working"))),
		ok(term(spec{address: "t2", program: "/Users/me/.local/bin/claude", status: "idle"})),
		fails("not_found", `no terminal "t3"`),
	}})

	agent, found, err := core.AgentAt(context.Background(), "t1")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, herdr.Agent{Kind: "claude", Status: "working", PaneID: "t1", TabID: "tbA", WorkspaceID: "spA", Session: "sess-t1"}, agent)

	_, found, err = core.AgentAt(context.Background(), "t2")
	require.NoError(t, err)
	assert.False(t, found)

	_, found, err = core.AgentAt(context.Background(), "t3")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestCoreStartWorkerRunsTheProgramLabelledForItsTree(t *testing.T) {
	shims, plugin := t.TempDir(), "/plugin"
	require.NoError(t, os.WriteFile(shims+"/claude", []byte("#!/bin/sh\n"), 0o755))
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.list": {list()},
		"terminal.run":  {ok(term(worker("t7", "spA", "tbA", "")))},
	})
	core = core.WithDispatchBin("/bin/dispatch")
	tree := t.TempDir()

	started, err := core.StartWorker(context.Background(), herdr.Worker{
		Workspace: "ignored", Dir: tree, Label: "ENG-1", Name: "eng-1", PluginRoot: plugin,
		Program: herdr.Program{
			AgentKind: "claude", Argv: []string{"--model", "sonnet"}, PathFirst: []string{shims},
			Session: "11111111-2222-4333-8444-555555555555", SessionFlag: "--session-id", Env: map[string]string{"X": "1"},
		},
	})

	require.NoError(t, err)
	assert.Equal(t, herdr.Started{Pane: "t7", Agent: started.Agent}, started)
	assert.Regexp(t, `^dw-[0-9a-f]{24}$`, started.Agent)
	runs := calls.Params(t, "terminal.run")
	require.Len(t, runs, 1)
	run := runs[0]
	assert.Equal(t, []any{shims + "/claude", "--model", "sonnet", "--session-id", "11111111-2222-4333-8444-555555555555"}, run["argv"])
	assert.Equal(t, tree, run["cwd"])
	assert.Equal(t, started.Agent, run["name"])
	assert.Equal(t, true, run["no_parent"])
	assert.Equal(t, true, run["no_notify"])
	assert.NotContains(t, run, "keep")
	assert.NotContains(t, run, "rm")
	env := run["env"].(map[string]any)
	assert.Equal(t, "1", env["AUTONOMOUS_WORKER"])
	assert.Equal(t, plugin, env["DISPATCH_PLUGIN_ROOT"])
	assert.Equal(t, "/bin/dispatch", env["DISPATCH_BIN"])
	assert.Equal(t, "1", env["X"])
	assert.True(t, strings.HasPrefix(env["PATH"].(string), shims+":"))
	labels := run["labels"].(map[string]any)
	assert.Equal(t, tree, labels["dispatch.tree"])
	assert.Equal(t, "11111111-2222-4333-8444-555555555555", labels["dispatch.session"])
	assert.Regexp(t, `^sp[0-9a-f]{32}$`, labels["herdr.space"])
	assert.Regexp(t, `^tb[0-9a-f]{32}$`, labels["herdr.tab"])
	space, err := core.OpenWorkspace(context.Background(), herdr.NewWorkspace{Path: tree})
	require.NoError(t, err)
	assert.Equal(t, space, labels["herdr.space"])
}

func TestCoreNamesOneTreeTheSameWhateverItsSpelling(t *testing.T) {
	real := t.TempDir()
	link := t.TempDir() + "/link"
	require.NoError(t, os.Symlink(real, link))
	core, _ := coreStandIn(t, map[string][]string{})

	first, err := core.OpenWorkspace(context.Background(), herdr.NewWorkspace{Path: real})
	require.NoError(t, err)
	second, err := core.OpenWorkspace(context.Background(), herdr.NewWorkspace{Path: link})
	require.NoError(t, err)
	other, err := core.OpenWorkspace(context.Background(), herdr.NewWorkspace{Path: t.TempDir()})
	require.NoError(t, err)

	assert.Equal(t, first, second)
	assert.NotEqual(t, first, other)
}

func TestCoreStartWorkerStartsNothingWhereAClaudeRuns(t *testing.T) {
	shims := t.TempDir()
	require.NoError(t, os.WriteFile(shims+"/claude", []byte("#!/bin/sh\n"), 0o755))
	users := spec{address: "t2", program: "/Users/me/.local/bin/claude", status: "idle", labels: map[string]string{"herdr.space": "spA"}}
	loading := worker("t1", "spA", "tbA", "")
	loading.program = "/bin/sh"
	for _, test := range []struct {
		name     string
		existing []spec
	}{
		{"AClaudeTheUserStartedInTheSpace", []spec{users}},
		{"AClaudeBesideATerminalThatRunsNone", []spec{loading, worker("t3", "spA", "tbB", "idle")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var terminals []string
			for _, s := range test.existing {
				s.labels["herdr.space"] = "spA"
				terminals = append(terminals, term(s))
			}
			core, calls := coreStandIn(t, map[string][]string{"terminal.list": {list(terminals...)}})

			_, err := core.StartWorker(context.Background(), herdr.Worker{Dir: t.TempDir(), Program: herdr.Program{AgentKind: "claude", PathFirst: []string{shims}}})

			require.ErrorIs(t, err, herdr.ErrSessionRunning)
			assert.Empty(t, calls.Params(t, "terminal.run"))
			assert.Empty(t, calls.Params(t, "terminal.kill"))
		})
	}
}

func TestCoreStartWorkerNamesTheTerminalThatHoldsTheTreeWithNoClaude(t *testing.T) {
	shims := t.TempDir()
	require.NoError(t, os.WriteFile(shims+"/claude", []byte("#!/bin/sh\n"), 0o755))
	stuck := worker("t1", "spA", "tbA", "")
	stuck.program = "/bin/sh"
	other := spec{address: "t9", program: "/bin/zsh", labels: map[string]string{"herdr.space": "spA"}}
	core, calls := coreStandIn(t, map[string][]string{"terminal.list": {list(term(other), term(stuck))}})

	_, err := core.StartWorker(context.Background(), herdr.Worker{Dir: t.TempDir(), Program: herdr.Program{AgentKind: "claude", PathFirst: []string{shims}}})

	var held *herdr.TerminalWithoutAgent
	require.ErrorAs(t, err, &held)
	assert.Equal(t, "t1", held.Address)
	assert.NotErrorIs(t, err, herdr.ErrSessionRunning)
	assert.Empty(t, calls.Params(t, "terminal.run"))
	assert.Empty(t, calls.Params(t, "terminal.kill"))
	assert.Empty(t, calls.Params(t, "terminal.rm"))
}

func TestCoreStartWorkerReportsALosingRaceAsASessionRunning(t *testing.T) {
	shims := t.TempDir()
	require.NoError(t, os.WriteFile(shims+"/claude", []byte("#!/bin/sh\n"), 0o755))
	core, _ := coreStandIn(t, map[string][]string{
		"terminal.list": {list()},
		"terminal.run":  {fails("name_taken", `a live terminal is already named "dw-1"`)},
	})

	_, err := core.StartWorker(context.Background(), herdr.Worker{Dir: t.TempDir(), Program: herdr.Program{AgentKind: "claude", PathFirst: []string{shims}}})

	require.ErrorIs(t, err, herdr.ErrSessionRunning)
}

func TestCoreStartWorkerRemovesOnlyTheFinishedWorkerTerminalsOfTheTree(t *testing.T) {
	shims := t.TempDir()
	require.NoError(t, os.WriteFile(shims+"/claude", []byte("#!/bin/sh\n"), 0o755))
	old := worker("t1", "spA", "tbA", "")
	old.state, old.program = "finished", ""
	mine := spec{address: "t2", state: "finished", labels: map[string]string{"herdr.space": "spA"}}
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.list": {list(term(old), term(mine))},
		"terminal.rm":   {ok(`{"removed":"t1"}`)},
		"terminal.run":  {ok(term(worker("t3", "spA", "tbA", "")))},
	})

	_, err := core.StartWorker(context.Background(), herdr.Worker{Dir: t.TempDir(), Program: herdr.Program{AgentKind: "claude", PathFirst: []string{shims}}})

	require.NoError(t, err)
	assert.Equal(t, []map[string]any{{"terminal": "t1"}}, calls.Params(t, "terminal.rm"))
	assert.Equal(t, []string{"terminal.list", "terminal.rm", "terminal.run"}, calls.Methods())
}

func TestCoreStartWorkerResumesTheSessionOfAWorkerTheCoreOrMachineLost(t *testing.T) {
	for _, reason := range []string{"server_stop", "stopped_by_restart", "holder_lost", "host_shutdown"} {
		t.Run(reason, func(t *testing.T) {
			shims := t.TempDir()
			require.NoError(t, os.WriteFile(shims+"/claude", []byte("#!/bin/sh\n"), 0o755))
			lost := worker("t1", "spA", "tbA", "")
			lost.state, lost.program, lost.reason = "finished", "", reason
			core, calls := coreStandIn(t, map[string][]string{
				"terminal.list": {list(term(lost))},
				"terminal.rm":   {ok(`{}`)},
				"terminal.run":  {ok(term(worker("t2", "spA", "tbA", "")))},
			})

			_, err := core.StartWorker(context.Background(), herdr.Worker{Dir: t.TempDir(), Program: herdr.Program{
				AgentKind: "claude", Argv: []string{"--model", "sonnet"}, PathFirst: []string{shims},
				Session: "new-session", SessionFlag: "--session-id", ResumeFlag: "--resume",
			}})

			require.NoError(t, err)
			run := calls.Params(t, "terminal.run")[0]
			// worker() labels terminal tN with session sess-tN.
			assert.Equal(t, []any{shims + "/claude", "--model", "sonnet", "--resume", "sess-t1"}, run["argv"])
			assert.Equal(t, "sess-t1", run["labels"].(map[string]any)["dispatch.session"])
		})
	}
}

func TestCoreStartWorkerStartsFreshWhenResumingIsNotPossible(t *testing.T) {
	for _, test := range []struct {
		name   string
		reason string
		resume string
	}{
		{name: "ProgramExited", reason: "exited", resume: "--resume"},
		{name: "ProgramKilled", reason: "killed", resume: "--resume"},
		{name: "ProgramSignaled", reason: "signaled", resume: "--resume"},
		{name: "NoResumeFlag", reason: "server_stop"},
	} {
		t.Run(test.name, func(t *testing.T) {
			shims := t.TempDir()
			require.NoError(t, os.WriteFile(shims+"/claude", []byte("#!/bin/sh\n"), 0o755))
			ended := worker("t1", "spA", "tbA", "")
			ended.state, ended.program, ended.reason = "finished", "", test.reason
			core, calls := coreStandIn(t, map[string][]string{
				"terminal.list": {list(term(ended))},
				"terminal.rm":   {ok(`{}`)},
				"terminal.run":  {ok(term(worker("t2", "spA", "tbA", "")))},
			})

			_, err := core.StartWorker(context.Background(), herdr.Worker{Dir: t.TempDir(), Program: herdr.Program{
				AgentKind: "claude", PathFirst: []string{shims},
				Session: "new-session", SessionFlag: "--session-id", ResumeFlag: test.resume,
			}})

			require.NoError(t, err)
			run := calls.Params(t, "terminal.run")[0]
			assert.Equal(t, []any{shims + "/claude", "--session-id", "new-session"}, run["argv"])
			assert.Equal(t, "new-session", run["labels"].(map[string]any)["dispatch.session"])
		})
	}
}

func TestCoreStartWorkerDecidesByTheTerminalThatFinishedLast(t *testing.T) {
	for _, test := range []struct {
		name   string
		listed func(stopped, exited spec) string
		want   []any
	}{
		{name: "NewerCrashListedFirst", listed: func(stopped, exited spec) string { return list(term(exited), term(stopped)) }, want: []any{"--session-id", "new-session"}},
		{name: "NewerCrashListedLast", listed: func(stopped, exited spec) string { return list(term(stopped), term(exited)) }, want: []any{"--session-id", "new-session"}},
		{name: "NewerStopListedFirst", listed: func(stopped, exited spec) string { return list(term(stopped), term(exited)) }, want: []any{"--resume", "sess-t1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			shims := t.TempDir()
			require.NoError(t, os.WriteFile(shims+"/claude", []byte("#!/bin/sh\n"), 0o755))
			stopped := worker("t1", "spA", "tbA", "")
			stopped.state, stopped.program, stopped.reason, stopped.finishedMS = "finished", "", "server_stop", 2000
			exited := worker("t2", "spA", "tbA", "")
			exited.state, exited.program, exited.reason, exited.finishedMS = "finished", "", "exited", 4000
			if test.name == "NewerStopListedFirst" {
				stopped.finishedMS, exited.finishedMS = 4000, 2000
			}
			core, calls := coreStandIn(t, map[string][]string{
				"terminal.list": {test.listed(stopped, exited)},
				"terminal.rm":   {ok(`{}`)},
				"terminal.run":  {ok(term(worker("t3", "spA", "tbA", "")))},
			})

			_, err := core.StartWorker(context.Background(), herdr.Worker{Dir: t.TempDir(), Program: herdr.Program{
				AgentKind: "claude", PathFirst: []string{shims}, Session: "new-session", SessionFlag: "--session-id", ResumeFlag: "--resume",
			}})

			require.NoError(t, err)
			assert.Equal(t, append([]any{shims + "/claude"}, test.want...), calls.Params(t, "terminal.run")[0]["argv"])
		})
	}
}

func TestCoreStartWorkerNeverResumesAnEarlierTicketsSessionOnAFreshLease(t *testing.T) {
	shims := t.TempDir()
	require.NoError(t, os.WriteFile(shims+"/claude", []byte("#!/bin/sh\n"), 0o755))
	stopped := worker("t1", "spA", "tbA", "")
	stopped.state, stopped.program, stopped.reason = "finished", "", "server_stop"
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.list": {list(term(stopped))},
		"terminal.rm":   {ok(`{}`)},
		"terminal.run":  {ok(term(worker("t2", "spA", "tbA", "")))},
	})

	_, err := core.StartWorker(context.Background(), herdr.Worker{Dir: t.TempDir(), FreshLease: true, Program: herdr.Program{
		AgentKind: "claude", PathFirst: []string{shims}, Session: "new-session", SessionFlag: "--session-id", ResumeFlag: "--resume",
	}})

	require.NoError(t, err)
	run := calls.Params(t, "terminal.run")[0]
	assert.Equal(t, []any{shims + "/claude", "--session-id", "new-session"}, run["argv"])
	assert.Equal(t, "new-session", run["labels"].(map[string]any)["dispatch.session"])
}

func TestCoreStartWorkerStartsFreshWhenTheStoppedTerminalHasNoSession(t *testing.T) {
	shims := t.TempDir()
	require.NoError(t, os.WriteFile(shims+"/claude", []byte("#!/bin/sh\n"), 0o755))
	stopped := spec{address: "t1", state: "finished", reason: "server_stop", labels: map[string]string{"dispatch.tree": "/trees/1", "herdr.space": "spA"}}
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.list": {list(term(stopped))},
		"terminal.rm":   {ok(`{}`)},
		"terminal.run":  {ok(term(worker("t2", "spA", "tbA", "")))},
	})

	_, err := core.StartWorker(context.Background(), herdr.Worker{Dir: t.TempDir(), Program: herdr.Program{
		AgentKind: "claude", PathFirst: []string{shims}, Session: "new-session", SessionFlag: "--session-id", ResumeFlag: "--resume",
	}})

	require.NoError(t, err)
	assert.Equal(t, []any{shims + "/claude", "--session-id", "new-session"}, calls.Params(t, "terminal.run")[0]["argv"])
}

func TestCoreCloseWorkspaceEndsEveryWorkerTerminalAndLeavesTheUsersAlone(t *testing.T) {
	live := worker("t1", "spA", "tbA", "idle")
	gone := worker("t2", "spA", "tbA", "")
	gone.state, gone.program = "finished", ""
	users := spec{address: "t3", program: "/bin/zsh", labels: map[string]string{"herdr.space": "spA"}}
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.list": {list(term(live), term(gone), term(users))},
		"terminal.kill": {ok(`{}`)},
		"terminal.rm":   {ok(`{}`)},
	})

	require.NoError(t, core.CloseWorkspace(context.Background(), "spA"))

	assert.Equal(t, []map[string]any{{"terminal": "t1"}}, calls.Params(t, "terminal.kill"))
	assert.Equal(t, []map[string]any{{"terminal": "t1"}, {"terminal": "t2"}}, calls.Params(t, "terminal.rm"))
}

func TestCoreCloseWorkspaceWithNoWorkerTerminalClosesNothing(t *testing.T) {
	users := spec{address: "t3", program: "/bin/zsh", labels: map[string]string{"herdr.space": "spA"}}
	core, calls := coreStandIn(t, map[string][]string{"terminal.list": {list(term(users))}})

	require.NoError(t, core.CloseWorkspace(context.Background(), "spA"))

	assert.Equal(t, []string{"terminal.list"}, calls.Methods())
}

func TestCoreRenamesNameTheSpaceAndTabInTheAppWindow(t *testing.T) {
	core, calls := coreStandIn(t, map[string][]string{})

	require.NoError(t, core.RenameWorkspace(context.Background(), "spA", "🟢 ENG-1"))
	require.NoError(t, core.RenameTab(context.Background(), "tbA", "ENG-1 the title"))

	assert.Equal(t, []string{
		"space.rename --id spA --name 🟢 ENG-1",
		"tab.rename --id tbA --name ENG-1 the title",
	}, calls.AppLines())
	assert.Empty(t, calls.Methods())
}

func TestCoreRenamesSucceedWhenNoWindowIsOpen(t *testing.T) {
	trace := t.TempDir() + "/trace"
	t.Setenv(envAppFails, "1")
	core, calls := coreStandIn(t, map[string][]string{})
	core.WithTrace(trace)

	require.NoError(t, core.RenameWorkspace(context.Background(), "spA", "🟢 ENG-1"))
	require.NoError(t, core.RenameTab(context.Background(), "tbA", "ENG-1 the title"))

	assert.Len(t, calls.AppLines(), 2)
	written, err := os.ReadFile(trace)
	require.NoError(t, err)
	assert.Contains(t, string(written), "no window is open")
}

func TestCoreRenamesSucceedWhenHerdrAppIsNotInstalled(t *testing.T) {
	core := herdr.NewCore("hc").WithApp("herdr-app-that-is-not-installed").WithEnv(environment.Environment{"PATH=" + t.TempDir()})

	require.NoError(t, core.RenameWorkspace(context.Background(), "spA", "🟢 ENG-1"))
	require.NoError(t, core.RenameTab(context.Background(), "tbA", "ENG-1 the title"))
}

// appDocument is the herdr-app document as hc returns it, on one line like every reply.
const appDocument = `{"value":{"spaces":[{"id":"spA","name":"🟢 ENG-1","folder":"/trees/1","tabs":[{"id":"tbA","name":"ENG-1 the title"}]},{"id":"spB","name":"","folder":"/trees/2","tabs":[{"id":"tbB","name":""}]},{"id":"spUser","name":"mine","tabs":[{"id":"tbUser","name":"shell"}]}]},"version":20}`

func TestCoreReadShowsTheNamesTheAppWindowHolds(t *testing.T) {
	other := worker("t2", "spB", "tbB", "idle")
	other.labels["dispatch.tree"] = "/trees/2"
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.list": {list(term(worker("t1", "spA", "tbA", "idle")), term(other))},
		"doc.get":       {ok(appDocument)},
	})

	snap, err := core.Read(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []herdr.Workspace{{ID: "spA", Label: "🟢 ENG-1", CheckoutPath: "/trees/1"}, {ID: "spB", CheckoutPath: "/trees/2"}}, snap.Workspaces)
	assert.Equal(t, []herdr.Tab{{ID: "tbA", Label: "ENG-1 the title", WorkspaceID: "spA"}, {ID: "tbB", WorkspaceID: "spB"}}, snap.Tabs)
	assert.Equal(t, []map[string]any{{"name": "herdr-app"}}, calls.Params(t, "doc.get"))
}

func TestCoreReadShowsNoNamesWhenTheAppDocumentIsEmptyOrUnreadable(t *testing.T) {
	for name, reply := range map[string]string{
		"neverWritten": ok(`{"value":null,"version":0}`),
		"refused":      fails("internal", "no document"),
		"otherShape":   ok(`{"value":{"spaces":"none"},"version":3}`),
	} {
		t.Run(name, func(t *testing.T) {
			core, _ := coreStandIn(t, map[string][]string{
				"terminal.list": {list(term(worker("t1", "spA", "tbA", "idle")))},
				"doc.get":       {reply},
			})

			snap, err := core.Read(context.Background())

			require.NoError(t, err)
			assert.Equal(t, []herdr.Workspace{{ID: "spA", CheckoutPath: "/trees/1"}}, snap.Workspaces)
			assert.Equal(t, []herdr.Tab{{ID: "tbA", WorkspaceID: "spA"}}, snap.Tabs)
		})
	}
}

func TestCoreCloseWorkspaceClosesTheSpaceWhenNoOtherTerminalIsLeft(t *testing.T) {
	live := worker("t1", "spA", "tbA", "idle")
	gone := worker("t2", "spA", "tbA", "")
	gone.state, gone.program = "finished", ""
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.list": {list(term(live), term(gone))},
		"terminal.kill": {ok(`{}`)},
		"terminal.rm":   {ok(`{}`)},
	})

	require.NoError(t, core.CloseWorkspace(context.Background(), "spA"))

	assert.Equal(t, []string{"space.close --id spA --confirmed true"}, calls.AppLines())
}

func TestCoreCloseWorkspaceKeepsTheSpaceWhileTheUsersTerminalIsInIt(t *testing.T) {
	users := spec{address: "t3", program: "/bin/zsh", labels: map[string]string{"herdr.space": "spA"}}
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.list": {list(term(worker("t1", "spA", "tbA", "idle")), term(users))},
		"terminal.kill": {ok(`{}`)},
		"terminal.rm":   {ok(`{}`)},
	})

	require.NoError(t, core.CloseWorkspace(context.Background(), "spA"))

	assert.Empty(t, calls.AppLines())
}

func TestCoreCloseWorkspaceLeavesTheSpaceWhenAWorkerCouldNotBeRemoved(t *testing.T) {
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.list": {list(term(worker("t1", "spA", "tbA", "idle")))},
		"terminal.kill": {fails("internal", "cannot kill")},
	})

	require.ErrorContains(t, core.CloseWorkspace(context.Background(), "spA"), "cannot kill")

	assert.Empty(t, calls.AppLines())
}

func TestCoreCloseWorkspaceWithNoWorkerClosesNoSpace(t *testing.T) {
	core, calls := coreStandIn(t, map[string][]string{"terminal.list": {list()}})

	require.NoError(t, core.CloseWorkspace(context.Background(), "spA"))

	assert.Empty(t, calls.AppLines())
}

func TestCoreCloseWorkspaceSucceedsWhenNoWindowIsOpenToCloseTheSpace(t *testing.T) {
	trace := t.TempDir() + "/trace"
	t.Setenv(envAppFails, "1")
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.list": {list(term(worker("t1", "spA", "tbA", "idle")))},
		"terminal.kill": {ok(`{}`)},
		"terminal.rm":   {ok(`{}`)},
	})
	core.WithTrace(trace)

	require.NoError(t, core.CloseWorkspace(context.Background(), "spA"))

	assert.Equal(t, []string{"space.close --id spA --confirmed true"}, calls.AppLines())
	written, err := os.ReadFile(trace)
	require.NoError(t, err)
	assert.Contains(t, string(written), "no window is open")
}

func TestCoreCloseTabNeverClosesASpace(t *testing.T) {
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.list": {list(term(worker("t1", "spA", "tbA", "idle")))},
		"terminal.kill": {ok(`{}`)},
		"terminal.rm":   {ok(`{}`)},
	})

	require.NoError(t, core.CloseTab(context.Background(), "tbA"))

	assert.Empty(t, calls.AppLines())
}

var startup = herdr.Startup{Answers: []herdr.Answer{
	{Shows: []*regexp.Regexp{regexp.MustCompile(`trust this folder`), regexp.MustCompile(`❯ *No`)}, Tries: []herdr.Try{{{"Down"}, {"Enter"}}}},
}}

func TestCoreKickoffAnswersTheTrustDialogBeforeItTypes(t *testing.T) {
	dialog := ok(`{"text":"Do you trust this folder?\n❯ No, exit\n  Yes, proceed","complete":true,"how":"screen"}`)
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.get": {
			ok(term(worker("t1", "spA", "tbA", ""))),
			ok(term(worker("t1", "spA", "tbA", "idle"))),
		},
		"terminal.read": {dialog},
		"terminal.keys": {ok(`{"turn":0}`)},
		"terminal.send": {ok(`{"turn":1,"started":true}`)},
	})

	require.NoError(t, core.Kickoff(context.Background(), "t1", "/dispatch:build ENG-1", startup))

	assert.Equal(t, []map[string]any{
		{"terminal": "t1", "keys": []any{"down"}},
		{"terminal": "t1", "keys": []any{"enter"}},
	}, calls.Params(t, "terminal.keys"))
	assert.Equal(t, []map[string]any{{"terminal": "t1", "text": "/dispatch:build ENG-1", "if": "idle"}}, calls.Params(t, "terminal.send"))
	methods := calls.Methods()
	assert.Less(t, indexOf(methods, "terminal.keys"), indexOf(methods, "terminal.send"))
}

func indexOf(list []string, item string) int {
	for i, v := range list {
		if v == item {
			return i
		}
	}
	return -1
}

func TestCoreKickoffTypesNothingWhileTheWorkerReportsNoStatusAndNoDialogShows(t *testing.T) {
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.get":  {ok(term(worker("t1", "spA", "tbA", "")))},
		"terminal.read": {ok(`{"text":"loading","complete":true,"how":"screen"}`)},
	})

	err := core.Kickoff(context.Background(), "t1", "/dispatch:build ENG-1", startup)

	require.ErrorIs(t, err, herdr.ErrKickoffUnconfirmed)
	assert.Empty(t, calls.Params(t, "terminal.send"))
	assert.Empty(t, calls.Params(t, "terminal.keys"))
}

func TestCoreKickoffGivesUpOnATerminalThatFinishedOrNeedsAPerson(t *testing.T) {
	finished := worker("t1", "spA", "tbA", "")
	finished.state, finished.program = "finished", ""
	for _, test := range []struct {
		name string
		get  string
	}{
		{"Finished", ok(term(finished))},
		{"Blocked", ok(term(worker("t1", "spA", "tbA", "blocked")))},
		{"Gone", fails("not_found", `no terminal "t1"`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			core, calls := coreStandIn(t, map[string][]string{"terminal.get": {test.get}})

			err := core.Kickoff(context.Background(), "t1", "/dispatch:build ENG-1", startup)

			require.ErrorIs(t, err, herdr.ErrKickoffUnconfirmed)
			assert.Empty(t, calls.Params(t, "terminal.send"))
		})
	}
}

func TestCoreKickoffConfirmsAWorkerThatIsAlreadyWorking(t *testing.T) {
	core, calls := coreStandIn(t, map[string][]string{"terminal.get": {ok(term(worker("t1", "spA", "tbA", "working")))}})

	require.NoError(t, core.Kickoff(context.Background(), "t1", "/dispatch:build ENG-1", startup))

	assert.Empty(t, calls.Params(t, "terminal.send"))
}

func TestCoreKickoffTypesIntoAResumedWorkerThatIsDoneWithoutAnIdleCheck(t *testing.T) {
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.get":  {ok(term(worker("t1", "spA", "tbA", "done")))},
		"terminal.send": {ok(`{"turn":1,"started":true}`)},
	})

	require.NoError(t, core.Kickoff(context.Background(), "t1", "/dispatch:build ENG-1", startup))

	assert.Equal(t, []map[string]any{{"terminal": "t1", "text": "/dispatch:build ENG-1"}}, calls.Params(t, "terminal.send"))
}

func TestCoreStartWorkerLooksOnlyAtTheTerminalsOfItsOwnSpace(t *testing.T) {
	shims := t.TempDir()
	require.NoError(t, os.WriteFile(shims+"/claude", []byte("#!/bin/sh\n"), 0o755))
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.list": {list()},
		"terminal.run":  {ok(term(worker("t1", "spA", "tbA", "")))},
	})
	tree := t.TempDir()

	_, err := core.StartWorker(context.Background(), herdr.Worker{Dir: tree, Program: herdr.Program{AgentKind: "claude", PathFirst: []string{shims}}})

	require.NoError(t, err)
	space, err := core.OpenWorkspace(context.Background(), herdr.NewWorkspace{Path: tree})
	require.NoError(t, err)
	assert.Equal(t, []map[string]any{{"all": true, "labels": map[string]any{"herdr.space": space}}}, calls.Params(t, "terminal.list"))
}

func TestCoreCloseWorkspaceAndCloseTabAskForOnlyTheTerminalsTheyName(t *testing.T) {
	live := worker("t1", "spA", "tbA", "idle")
	users := spec{address: "t3", program: "/bin/zsh", labels: map[string]string{"herdr.space": "spA", "herdr.tab": "tbA"}}
	replies := map[string][]string{
		"terminal.list": {list(term(live), term(users))},
		"terminal.kill": {ok(`{}`)},
		"terminal.rm":   {ok(`{}`)},
	}
	spaceCore, spaceCalls := coreStandIn(t, replies)
	require.NoError(t, spaceCore.CloseWorkspace(context.Background(), "spA"))
	tabCore, tabCalls := coreStandIn(t, replies)

	require.NoError(t, tabCore.CloseTab(context.Background(), "tbA"))

	assert.Equal(t, []map[string]any{{"all": true, "labels": map[string]any{"herdr.space": "spA"}}}, spaceCalls.Params(t, "terminal.list"))
	assert.Equal(t, []map[string]any{{"all": true, "labels": map[string]any{"herdr.tab": "tbA"}}}, tabCalls.Params(t, "terminal.list"))
	assert.Equal(t, []map[string]any{{"terminal": "t1"}}, tabCalls.Params(t, "terminal.kill"))
	assert.Equal(t, []map[string]any{{"terminal": "t1"}}, tabCalls.Params(t, "terminal.rm"))
}

func TestCoreCloseWorkspaceReportsATerminalItCouldNotKillAndKeepsClosingTheRest(t *testing.T) {
	first := worker("t1", "spA", "tbA", "idle")
	second := worker("t2", "spA", "tbA", "idle")
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.list": {list(term(first), term(second))},
		"terminal.kill": {fails("internal", "cannot kill"), ok(`{}`)},
		"terminal.rm":   {ok(`{}`)},
	})

	err := core.CloseWorkspace(context.Background(), "spA")

	require.ErrorContains(t, err, "cannot kill")
	assert.Equal(t, []map[string]any{{"terminal": "t2"}}, calls.Params(t, "terminal.rm"))
}

func TestCoreKickoffDoesNotCountAWorkerThatLeftIdleAsConfirmationWhenNothingWasTyped(t *testing.T) {
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.get": {
			ok(term(worker("t1", "spA", "tbA", "idle"))),
			ok(term(worker("t1", "spA", "tbA", "working"))),
		},
		"terminal.send": {fails("status_mismatch", "not idle"), ok(`{"turn":2,"started":true}`)},
	})

	require.NoError(t, core.Kickoff(context.Background(), "t1", "/dispatch:build ENG-1", startup))

	sends := calls.Params(t, "terminal.send")
	require.Len(t, sends, 2)
	assert.Equal(t, "idle", sends[0]["if"])
	assert.NotContains(t, sends[1], "if")
}

func TestCoreKickoffGivesUpWhenAMismatchLeavesTheWorkerBlocked(t *testing.T) {
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.get": {
			ok(term(worker("t1", "spA", "tbA", "idle"))),
			ok(term(worker("t1", "spA", "tbA", "blocked"))),
		},
		"terminal.send": {fails("status_mismatch", "not idle")},
	})

	err := core.Kickoff(context.Background(), "t1", "/dispatch:build ENG-1", startup)

	require.ErrorIs(t, err, herdr.ErrKickoffUnconfirmed)
	assert.Len(t, calls.Params(t, "terminal.send"), 1)
}

func TestCoreKickoffWaitsForTheStatusToMoveBeforeItRetypes(t *testing.T) {
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.get": {
			ok(term(worker("t1", "spA", "tbA", "idle"))),
			ok(term(worker("t1", "spA", "tbA", "idle"))),
			ok(term(worker("t1", "spA", "tbA", "idle"))),
			ok(term(worker("t1", "spA", "tbA", "working"))),
		},
		"terminal.send": {ok(`{"turn":1,"started":null}`)},
	})

	require.NoError(t, core.Kickoff(context.Background(), "t1", "/dispatch:build ENG-1", startup))

	assert.Len(t, calls.Params(t, "terminal.send"), 1)
}

func TestCoreKickoffCountsAKickoffThatRanToDoneAfterEnterAsConfirmed(t *testing.T) {
	idle := ok(term(worker("t1", "spA", "tbA", "idle")))
	gets := []string{}
	// One read finds the input box, fifteen wait for the status to move, and the one after Enter finds it done.
	for range 16 {
		gets = append(gets, idle)
	}
	gets = append(gets, ok(term(worker("t1", "spA", "tbA", "done"))))
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.get":  gets,
		"terminal.send": {ok(`{"turn":1,"started":false}`)},
		"terminal.read": {ok(`{"text":"❯ /dispatch:build ENG-1","complete":true,"how":"screen"}`)},
		"terminal.keys": {ok(`{"turn":1}`)},
	})

	require.NoError(t, core.Kickoff(context.Background(), "t1", "/dispatch:build ENG-1", startup))

	assert.Len(t, calls.Params(t, "terminal.send"), 1)
	assert.Len(t, calls.Params(t, "terminal.keys"), 1)
}

func TestCoreKickoffDoesNotConfirmAKickoffTypedIntoADoneWorkerThatNeverMoved(t *testing.T) {
	done := ok(term(worker("t1", "spA", "tbA", "done")))
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.get":  {done},
		"terminal.send": {ok(`{"turn":0,"started":null}`)},
		"terminal.read": {ok(`{"text":"❯ /dispatch:build ENG-1","complete":true,"how":"screen"}`)},
		"terminal.keys": {ok(`{"turn":0}`)},
	})

	err := core.Kickoff(context.Background(), "t1", "/dispatch:build ENG-1", startup)

	require.ErrorIs(t, err, herdr.ErrKickoffUnconfirmed)
	assert.Len(t, calls.Params(t, "terminal.keys"), 1)
}

func TestCoreKickoffConfirmsAKickoffTypedIntoADoneWorkerWhoseTurnWentUp(t *testing.T) {
	ran := worker("t1", "spA", "tbA", "done")
	ran.turn = 2
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.get":  {ok(term(worker("t1", "spA", "tbA", "done"))), ok(term(ran))},
		"terminal.send": {ok(`{"turn":1,"started":null}`)},
	})

	require.NoError(t, core.Kickoff(context.Background(), "t1", "/dispatch:build ENG-1", startup))

	assert.Empty(t, calls.Params(t, "terminal.keys"))
	assert.Len(t, calls.Params(t, "terminal.send"), 1)
}

func TestCoreKickoffReadsAStartupDialogFromTheVisibleScreenOnly(t *testing.T) {
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.get": {
			ok(term(worker("t1", "spA", "tbA", ""))),
			ok(term(worker("t1", "spA", "tbA", "idle"))),
		},
		"terminal.read": {ok(`{"text":"loading","complete":true,"how":"screen"}`)},
		"terminal.send": {ok(`{"turn":1,"started":true}`)},
	})

	require.NoError(t, core.Kickoff(context.Background(), "t1", "/dispatch:build ENG-1", startup))

	reads := calls.Params(t, "terminal.read")
	require.Len(t, reads, 1)
	assert.NotContains(t, reads[0], "lines")
}

func TestCoreKickoffConfirmsAKickoffWhoseTurnWentUpEvenIfTheWorkerReadsIdle(t *testing.T) {
	ran := worker("t1", "spA", "tbA", "idle")
	ran.turn = 2
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.get":  {ok(term(worker("t1", "spA", "tbA", "done"))), ok(term(ran))},
		"terminal.send": {ok(`{"turn":1,"started":null}`)},
		"terminal.read": {ok(`{"text":"❯ /dispatch:build ENG-1","complete":true,"how":"screen"}`)},
	})

	require.NoError(t, core.Kickoff(context.Background(), "t1", "/dispatch:build ENG-1", startup))

	assert.Empty(t, calls.Params(t, "terminal.keys"))
	assert.Len(t, calls.Params(t, "terminal.send"), 1)
}

func TestCoreKickoffCountsAKickoffThatRanToDoneWithoutPressingEnter(t *testing.T) {
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.get": {
			ok(term(worker("t1", "spA", "tbA", "idle"))),
			ok(term(worker("t1", "spA", "tbA", "done"))),
		},
		"terminal.send": {ok(`{"turn":0,"started":null}`)},
		"terminal.read": {ok(`{"text":"❯ /dispatch:build ENG-1","complete":true,"how":"screen"}`)},
	})

	require.NoError(t, core.Kickoff(context.Background(), "t1", "/dispatch:build ENG-1", startup))

	assert.Empty(t, calls.Params(t, "terminal.keys"))
	assert.Len(t, calls.Params(t, "terminal.send"), 1)
}

func TestCoreKickoffLooksForTheTypedTextInHistoryAboveTheScreen(t *testing.T) {
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.get":  {ok(term(worker("t1", "spA", "tbA", "idle")))},
		"terminal.send": {ok(`{"turn":0,"started":null}`)},
		"terminal.read": {ok(`{"text":"nothing","complete":true,"how":"screen"}`)},
	})

	err := core.Kickoff(context.Background(), "t1", "/dispatch:build ENG-1", startup)

	require.ErrorIs(t, err, herdr.ErrKickoffUnconfirmed)
	reads := calls.Params(t, "terminal.read")
	require.NotEmpty(t, reads)
	assert.EqualValues(t, 30, reads[0]["lines"])
}

func TestCoreKickoffConfirmsADoneWorkerWhoseTurnWentUpAfterEnter(t *testing.T) {
	stuck := ok(term(worker("t1", "spA", "tbA", "done")))
	ran := worker("t1", "spA", "tbA", "idle")
	ran.turn = 2
	gets := []string{}
	// One read finds the input box and fifteen wait for the status to move.
	for range 16 {
		gets = append(gets, stuck)
	}
	gets = append(gets, ok(term(ran)))
	core, calls := coreStandIn(t, map[string][]string{
		"terminal.get":  gets,
		"terminal.send": {ok(`{"turn":1,"started":null}`)},
		"terminal.read": {ok(`{"text":"❯ /dispatch:build ENG-1","complete":true,"how":"screen"}`)},
		"terminal.keys": {ok(`{"turn":1}`)},
	})

	require.NoError(t, core.Kickoff(context.Background(), "t1", "/dispatch:build ENG-1", startup))

	assert.Len(t, calls.Params(t, "terminal.send"), 1)
	assert.Len(t, calls.Params(t, "terminal.keys"), 1)
}
