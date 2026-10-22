package herdr_test

// Package exception: this package wraps the herdr tool, so every test here is an adapter or contract test of it.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdr"
)

const envState = "HERDR_TEST_STATE"

const (
	tabCreated  = `{"result":{"root_pane":{"pane_id":"w9:p1"}}}`
	worktreeOK  = `{"result":{"worktree":{"open_workspace_id":"w9"}}}`
	herdrNoWay  = `{"error":{"code":"refused","message":"no"}}`
	consentText = "Bypass Permissions mode  1. No, exit  2. Yes, I accept"
)

// runStatefulStandIn is the test binary acting as herdr for the worker
// operations. It appends each command line to calls.log in dir, answers list
// commands from the JSON files the test put there, and lets the steps of a
// launch succeed when the test has created their marker file.
func runStatefulStandIn(dir string, args []string) int {
	read := func(name, fallback string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return fallback
		}
		return string(b)
	}
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	count := func(name string) int {
		n, _ := strconv.Atoi(strings.TrimSpace(read(name, "0")))
		return n
	}
	f, err := os.OpenFile(filepath.Join(dir, "calls.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 2
	}
	_, _ = fmt.Fprintln(f, strings.Join(args, " "))
	_ = f.Close()

	if len(args) < 2 {
		return 2
	}
	switch args[0] + " " + args[1] {
	case "workspace list":
		fmt.Print(read("workspaces.json", `{"result":{"workspaces":[]}}`))
	case "pane list":
		fmt.Print(read("panes.json", `{"result":{"panes":[]}}`))
	case "agent list":
		fmt.Print(read("agents.json", `{"result":{"agents":[]}}`))
	case "worktree open":
		if !exists("open.json") {
			fmt.Println(herdrNoWay)
			return 1
		}
		fmt.Print(read("open.json", ""))
	case "tab create":
		if !exists("tab.json") {
			fmt.Println(herdrNoWay)
			return 1
		}
		fmt.Print(read("tab.json", ""))
	case "agent start":
		if exists("start-works") && !exists("start-fails") {
			return 0
		}
		return 1
	case "pane read":
		fmt.Println("$ ")
	case "agent read":
		n := count("reads")
		_ = os.WriteFile(filepath.Join(dir, "reads"), []byte(strconv.Itoa(n+1)), 0o644)
		screen := read(filepath.Join("screens", strconv.Itoa(n)), "")
		if screen == "" {
			screen = read(filepath.Join("screens", "last"), "❯\n")
		}
		fmt.Print(screen)
	case "agent wait":
		if strings.Contains(strings.Join(args, " "), "working") {
			if left := count("wait-fails"); left > 0 {
				_ = os.WriteFile(filepath.Join(dir, "wait-fails"), []byte(strconv.Itoa(left-1)), 0o644)
				return 1
			}
		}
	case "pane send-keys":
		if exists("send-keys-fails") {
			fmt.Println(herdrNoWay)
			return 1
		}
	case "tab close", "pane run":
	default:
		return 2
	}
	return 0
}

type statefulHerdr struct {
	t   *testing.T
	dir string
	cli *herdr.CLI
}

func newStatefulHerdr(t *testing.T) *statefulHerdr {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	dir := t.TempDir()
	t.Setenv(envState, dir)
	return &statefulHerdr{t: t, dir: dir, cli: herdr.NewCLI(exe).WithPace(time.Millisecond)}
}

func (s *statefulHerdr) write(name, content string) {
	s.t.Helper()
	require.NoError(s.t, os.MkdirAll(filepath.Dir(filepath.Join(s.dir, name)), 0o755))
	require.NoError(s.t, os.WriteFile(filepath.Join(s.dir, name), []byte(content), 0o644))
}

// workerStarts lets the tab and agent steps succeed.
func (s *statefulHerdr) workerStarts() {
	s.write("tab.json", tabCreated)
	s.write("start-works", "")
}

// screens sets what the agent's screen shows on each read, the last repeating.
func (s *statefulHerdr) screens(texts ...string) {
	for i, text := range texts {
		name := strconv.Itoa(i)
		if i == len(texts)-1 {
			name = "last"
		}
		s.write(filepath.Join("screens", name), text+"\n")
	}
}

func (s *statefulHerdr) calls() []string {
	s.t.Helper()
	b, err := os.ReadFile(filepath.Join(s.dir, "calls.log"))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(s.t, err)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// callsStarting lists the command lines that begin with prefix.
func (s *statefulHerdr) callsStarting(prefix string) []string {
	s.t.Helper()
	var found []string
	for _, call := range s.calls() {
		if strings.HasPrefix(call, prefix) {
			found = append(found, call)
		}
	}
	return found
}

func TestOpenWorkspaceReturnsTheWorkspaceAlreadyShowingTheCheckout(t *testing.T) {
	s := newStatefulHerdr(t)
	s.write("workspaces.json", `{"result":{"workspaces":[{"workspace_id":"w1","worktree":{"checkout_path":"/t/other"}},{"workspace_id":"w3","worktree":{"checkout_path":"/t/1"}}]}}`)

	id, err := s.cli.OpenWorkspace(context.Background(), herdr.NewWorkspace{Repo: "/repo", Path: "/t/1", Label: "ENG-1"})

	require.NoError(t, err)
	assert.Equal(t, "w3", id)
	assert.Empty(t, s.callsStarting("worktree open"))
}

func TestOpenWorkspaceOpensTheCheckoutAsAWorktreeUnderTheRepo(t *testing.T) {
	s := newStatefulHerdr(t)
	s.write("open.json", worktreeOK)

	id, err := s.cli.OpenWorkspace(context.Background(), herdr.NewWorkspace{Repo: "/repo", Path: "/t/1", Label: "ENG-1"})

	require.NoError(t, err)
	assert.Equal(t, "w9", id)
	assert.Equal(t, []string{"worktree open --cwd /repo --path /t/1 --label ENG-1 --no-focus --json"}, s.callsStarting("worktree open"))
}

func TestOpenWorkspaceReadsTheIdFromEitherShapeHerdrAnswersWith(t *testing.T) {
	for _, test := range []struct {
		name   string
		output string
		want   string
	}{
		{name: "WorktreeShape", output: `{"result":{"worktree":{"open_workspace_id":"w4"}}}`, want: "w4"},
		{name: "WorkspaceShape", output: `{"result":{"workspace":{"workspace_id":"w5"}}}`, want: "w5"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newStatefulHerdr(t)
			s.write("open.json", test.output)

			id, err := s.cli.OpenWorkspace(context.Background(), herdr.NewWorkspace{Repo: "/repo", Path: "/t/1", Label: "ENG-1"})

			require.NoError(t, err)
			assert.Equal(t, test.want, id)
		})
	}
}

func TestOpenWorkspaceFailsWhenHerdrOpensNothing(t *testing.T) {
	s := newStatefulHerdr(t)

	id, err := s.cli.OpenWorkspace(context.Background(), herdr.NewWorkspace{Repo: "/repo", Path: "/t/1", Label: "ENG-1"})

	require.ErrorContains(t, err, "herdr worktree open failed for /t/1: "+herdrNoWay)
	assert.Empty(t, id)
}

func TestStartWorkerStampsTheTabAndStartsClaudeInIt(t *testing.T) {
	s := newStatefulHerdr(t)
	s.workerStarts()

	started, err := s.cli.StartWorker(context.Background(), herdr.Worker{
		Workspace: "w9", Dir: "/t/1", Label: "ENG-1 Fix the thing", Name: "eng-1", Model: "sonnet", PluginRoot: "/plugin",
	})

	require.NoError(t, err)
	assert.Equal(t, herdr.Started{Pane: "w9:p1", Agent: "eng-1"}, started)
	tabs := s.callsStarting("tab create")
	require.Len(t, tabs, 1)
	assert.Contains(t, tabs[0], "--workspace w9 --cwd /t/1 --label ENG-1 Fix the thing --env")
	assert.Contains(t, tabs[0], "--env PATH=/plugin/scripts/shims:")
	assert.Contains(t, tabs[0], "--env AUTONOMOUS_WORKER=1")
	assert.Contains(t, tabs[0], "--env DISPATCH_PLUGIN_ROOT=/plugin")
	exe, err := os.Executable()
	require.NoError(t, err)
	assert.Contains(t, tabs[0], "--env DISPATCH_BIN="+exe)
	assert.Equal(t, []string{"agent start eng-1 --kind claude --pane w9:p1 --timeout 90000 -- --model sonnet --plugin-dir /plugin --disallowedTools EnterPlanMode,ExitPlanMode --settings {\"crossSessionInbound\":\"accept\"} --dangerously-skip-permissions"},
		s.callsStarting("agent start"))
}

func TestStartWorkerNeverGivesClaudeASessionName(t *testing.T) {
	s := newStatefulHerdr(t)
	s.workerStarts()

	_, err := s.cli.StartWorker(context.Background(), herdr.Worker{Workspace: "w9", Dir: "/t/1", Label: "ENG-1", Name: "eng-1", Model: "sonnet", PluginRoot: "/plugin"})

	// Claude draws a --name below an open question, which hides the question
	// from herdr's blocked detection.
	require.NoError(t, err)
	starts := s.callsStarting("agent start")
	require.Len(t, starts, 1)
	assert.NotContains(t, starts[0], "--name")
}

func TestStartWorkerClosesTheAgentlessTabsOfTheWorkspace(t *testing.T) {
	s := newStatefulHerdr(t)
	s.workerStarts()
	s.write("panes.json", `{"result":{"panes":[`+
		`{"pane_id":"w9:p0","workspace_id":"w9","tab_id":"w9:t0","agent":""},`+
		`{"pane_id":"w9:p1","workspace_id":"w9","tab_id":"w9:t1","agent":""},`+
		`{"pane_id":"w9:p2","workspace_id":"w9","tab_id":"w9:t2","agent":"claude"},`+
		`{"pane_id":"w2:p1","workspace_id":"w2","tab_id":"w2:t1","agent":""}]}}`)

	_, err := s.cli.StartWorker(context.Background(), herdr.Worker{Workspace: "w9", Dir: "/t/1", Label: "ENG-1", Name: "eng-1", Model: "sonnet", PluginRoot: "/plugin"})

	require.NoError(t, err)
	assert.Equal(t, []string{"tab close w9:t0"}, s.callsStarting("tab close"))
}

func TestStartWorkerRetriesAFailedStartOnceThenReportsThePane(t *testing.T) {
	s := newStatefulHerdr(t)
	s.workerStarts()
	s.write("start-fails", "")

	started, err := s.cli.StartWorker(context.Background(), herdr.Worker{Workspace: "w9", Dir: "/t/1", Label: "ENG-1", Name: "eng-1", Model: "sonnet", PluginRoot: "/plugin"})

	// The first start fails and the second runs after a pause, since a
	// timed-out start may still bring Claude up.
	require.ErrorContains(t, err, "herdr agent start failed in pane w9:p1")
	assert.Equal(t, "w9:p1", started.Pane)
	assert.Len(t, s.callsStarting("agent start"), 2)
}

func TestStartWorkerKeepsAnAgentWhoseStartTimedOutButCameUp(t *testing.T) {
	s := newStatefulHerdr(t)
	s.workerStarts()
	s.write("start-fails", "")
	s.write("agents.json", `{"result":{"agents":[{"name":"eng-1","pane_id":"w9:p1","agent":"claude","agent_status":"idle"}]}}`)

	started, err := s.cli.StartWorker(context.Background(), herdr.Worker{Workspace: "w9", Dir: "/t/1", Label: "ENG-1", Name: "eng-1", Model: "sonnet", PluginRoot: "/plugin"})

	require.NoError(t, err)
	assert.Equal(t, "w9:p1", started.Pane)
	assert.Len(t, s.callsStarting("agent start"), 1)
}

func TestStartWorkerNamesARelaunchedWorkerApartFromItsPredecessor(t *testing.T) {
	s := newStatefulHerdr(t)
	s.workerStarts()
	s.write("agents.json", `{"result":{"agents":[{"name":"eng-1","pane_id":"w1:p1","agent":"claude","agent_status":"done"}]}}`)

	started, err := s.cli.StartWorker(context.Background(), herdr.Worker{Workspace: "w9", Dir: "/t/1", Label: "ENG-1", Name: "eng-1", Model: "sonnet", PluginRoot: "/plugin"})

	require.NoError(t, err)
	assert.Regexp(t, `^eng-1-r\d+$`, started.Agent)
	starts := s.callsStarting("agent start")
	require.Len(t, starts, 1)
	assert.Contains(t, starts[0], "agent start "+started.Agent+" --kind claude")
}

func TestStartWorkerFailsWhenTheTabCannotBeCreated(t *testing.T) {
	s := newStatefulHerdr(t)
	s.write("start-works", "")

	started, err := s.cli.StartWorker(context.Background(), herdr.Worker{Workspace: "w9", Dir: "/t/1", Label: "ENG-1", Name: "eng-1", Model: "sonnet", PluginRoot: "/plugin"})

	require.ErrorContains(t, err, "herdr tab create failed in w9: "+herdrNoWay)
	assert.Equal(t, herdr.Started{}, started)
	assert.Empty(t, s.callsStarting("agent start"))
}

func TestKickoffAnswersTheConsentAndFolderTrustDialogs(t *testing.T) {
	s := newStatefulHerdr(t)
	s.screens(consentText,
		consentText,
		"Do you trust this folder?  ❯ No, exit  Yes, proceed",
		"❯")

	err := s.cli.Kickoff(context.Background(), "w9:p1", "/dispatch:build ENG-1")

	require.NoError(t, err)
	// The consent dialog is answered two ways in turn, because herdr's key
	// names differ across versions.
	assert.Equal(t, []string{
		"pane send-keys w9:p1 Down Enter",
		"pane send-keys w9:p1 2",
		"pane send-keys w9:p1 Enter",
		"pane send-keys w9:p1 Down",
		"pane send-keys w9:p1 Enter",
		"pane send-keys w9:p1 Enter",
	}, s.callsStarting("pane send-keys"))
	assert.Equal(t, []string{"pane run w9:p1 /dispatch:build ENG-1"}, s.callsStarting("pane run"))
}

func TestKickoffPressesEnterOnlyOnTheFolderTrustDialogWhenYesIsSelected(t *testing.T) {
	s := newStatefulHerdr(t)
	s.screens("Do you trust this folder?  No, exit  ❯ Yes, proceed", "❯")

	err := s.cli.Kickoff(context.Background(), "w9:p1", "/dispatch:build ENG-1")

	require.NoError(t, err)
	assert.Equal(t, []string{"pane send-keys w9:p1 Enter", "pane send-keys w9:p1 Enter"}, s.callsStarting("pane send-keys"))
}

func TestKickoffPressesEnterOnAClaudeCommandLeftUnrunAtTheShellPrompt(t *testing.T) {
	s := newStatefulHerdr(t)
	s.screens("~/gubernator  thrawn01/eng-332 *35\n❯ claude --model opus --plugin-dir /plugin", "❯")

	err := s.cli.Kickoff(context.Background(), "w9:p1", "/dispatch:build ENG-1")

	require.NoError(t, err)
	// The first Enter runs the stranded command, the second submits the kickoff.
	assert.Equal(t, []string{"pane send-keys w9:p1 Enter", "pane send-keys w9:p1 Enter"}, s.callsStarting("pane send-keys"))
	assert.Equal(t, []string{"pane run w9:p1 /dispatch:build ENG-1"}, s.callsStarting("pane run"))
}

func TestKickoffReportsClaudeNeverStartedWhenItsCommandStaysAtTheShellPrompt(t *testing.T) {
	s := newStatefulHerdr(t)
	s.screens("❯ claude --model opus --plugin-dir /plugin")

	err := s.cli.Kickoff(context.Background(), "w9:p1", "/dispatch:build ENG-1")

	require.ErrorIs(t, err, herdr.ErrClaudeNeverStarted)
	assert.NotErrorIs(t, err, herdr.ErrKickoffUnconfirmed)
	assert.Empty(t, s.callsStarting("pane run"))
}

func TestKickoffGivesUpWhenClaudeNeverShowsAnInputBox(t *testing.T) {
	s := newStatefulHerdr(t)
	s.screens("loading")

	err := s.cli.Kickoff(context.Background(), "w9:p1", "/dispatch:build ENG-1")

	require.ErrorIs(t, err, herdr.ErrKickoffUnconfirmed)
	assert.Empty(t, s.callsStarting("pane run"))
}

func TestKickoffGivesUpWhenTheAgentNeverLeavesIdle(t *testing.T) {
	s := newStatefulHerdr(t)
	s.write("wait-fails", "100")
	s.write("agents.json", `{"result":{"agents":[{"name":"x","pane_id":"w9:p1","agent":"claude","agent_status":"idle"}]}}`)

	err := s.cli.Kickoff(context.Background(), "w9:p1", "/dispatch:build ENG-1")

	require.ErrorIs(t, err, herdr.ErrKickoffUnconfirmed)
	assert.Len(t, s.callsStarting("pane run"), 3)
}

func TestKickoffGivesUpWhenTheAgentVanishesAfterTheKickoff(t *testing.T) {
	s := newStatefulHerdr(t)
	s.write("wait-fails", "100")

	err := s.cli.Kickoff(context.Background(), "w9:p1", "/dispatch:build ENG-1")

	require.ErrorIs(t, err, herdr.ErrKickoffUnconfirmed)
	assert.Equal(t, []string{"pane run w9:p1 /dispatch:build ENG-1"}, s.callsStarting("pane run"))
}

func TestKickoffNeverRetypesAKickoffThatIsRunningOrFinished(t *testing.T) {
	// While a skill runs, the screen shows only a spinner, so the kickoff is
	// confirmed by herdr's status and not by its text.
	for _, status := range []string{"working", "done", "blocked"} {
		t.Run(status, func(t *testing.T) {
			s := newStatefulHerdr(t)
			s.write("wait-fails", "1")
			s.write("agents.json", `{"result":{"agents":[{"name":"x","pane_id":"w9:p1","agent":"claude","agent_status":"`+status+`"}]}}`)

			err := s.cli.Kickoff(context.Background(), "w9:p1", "/dispatch:build ENG-1")

			require.NoError(t, err)
			assert.Equal(t, []string{"pane run w9:p1 /dispatch:build ENG-1"}, s.callsStarting("pane run"))
		})
	}
}

func TestKickoffRetypesWhileTheAgentStaysIdle(t *testing.T) {
	s := newStatefulHerdr(t)
	s.write("wait-fails", "1")
	s.write("agents.json", `{"result":{"agents":[{"name":"x","pane_id":"w9:p1","agent":"claude","agent_status":"idle"}]}}`)

	err := s.cli.Kickoff(context.Background(), "w9:p1", "/dispatch:build ENG-1")

	require.NoError(t, err)
	assert.Equal(t, []string{"pane run w9:p1 /dispatch:build ENG-1", "pane run w9:p1 /dispatch:build ENG-1"}, s.callsStarting("pane run"))
}

func TestKickoffPressesEnterInsteadOfRetypingAKickoffThatWasTypedButNotSubmitted(t *testing.T) {
	s := newStatefulHerdr(t)
	s.write("wait-fails", "1")
	s.write("agents.json", `{"result":{"agents":[{"name":"x","pane_id":"w9:p1","agent":"claude","agent_status":"idle"}]}}`)
	s.screens("❯", "❯ /dispatch:design ENG-1")

	err := s.cli.Kickoff(context.Background(), "w9:p1", "/dispatch:design ENG-1")

	require.NoError(t, err)
	assert.Equal(t, []string{"pane run w9:p1 /dispatch:design ENG-1"}, s.callsStarting("pane run"))
	assert.Equal(t, []string{"pane send-keys w9:p1 Enter", "pane send-keys w9:p1 Enter"}, s.callsStarting("pane send-keys"))
}

func TestKickoffRecordsTheScreensItReadsWhenTraced(t *testing.T) {
	s := newStatefulHerdr(t)
	trace := filepath.Join(s.dir, "trace.log")
	s.cli.WithTrace(trace)

	err := s.cli.Kickoff(context.Background(), "w9:p1", "/dispatch:build ENG-1")

	require.NoError(t, err)
	b, err := os.ReadFile(trace)
	require.NoError(t, err)
	assert.Contains(t, string(b), "dialogs")
	assert.Contains(t, string(b), "typing")
}

func TestKickoffTraceShowsEachEnterPressedAtAStrandedCommand(t *testing.T) {
	s := newStatefulHerdr(t)
	trace := filepath.Join(s.dir, "trace.log")
	s.cli.WithTrace(trace)
	s.screens("❯ claude --model opus --plugin-dir /plugin")

	err := s.cli.Kickoff(context.Background(), "w9:p1", "/dispatch:build ENG-1")

	require.ErrorIs(t, err, herdr.ErrClaudeNeverStarted)
	b, err := os.ReadFile(trace)
	require.NoError(t, err)
	assert.Equal(t, 30, strings.Count(string(b), " enter\n"))
}

func TestKickoffTraceShowsEnterHerdrRefusedToSend(t *testing.T) {
	s := newStatefulHerdr(t)
	trace := filepath.Join(s.dir, "trace.log")
	s.cli.WithTrace(trace)
	s.screens("❯ claude --model opus --plugin-dir /plugin")
	s.write("send-keys-fails", "")

	err := s.cli.Kickoff(context.Background(), "w9:p1", "/dispatch:build ENG-1")

	require.ErrorIs(t, err, herdr.ErrEnterNotSent)
	assert.NotErrorIs(t, err, herdr.ErrClaudeNeverStarted)
	b, err := os.ReadFile(trace)
	require.NoError(t, err)
	assert.Equal(t, 0, strings.Count(string(b), " enter\n"))
	assert.Equal(t, 30, strings.Count(string(b), " enter-failed\n"))
}
