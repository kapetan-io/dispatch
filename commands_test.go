package dispatch_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
	"github.com/kapetan-io/dispatch/internal/loop"
	"github.com/kapetan-io/dispatch/internal/sessionlog"
)

// commandsRule comments each time what a worker's commands read as changes, one comment for each reading.
const commandsRule = `
whenever({ name: "commands", each: "linear.ticket",
  if: (t) => t.status === "in_progress" || t.status === "in_review",
  then: (t, ctx) => {
    const w = ctx.herdr.worker(t);
    const body = known(() => w.commands)
      ? "commands " + w.commands.map((c) => c.name + "|" + c.args + "|" + c.cut + "|" + c.by).join(";") + " of " + w.commands.length
      : "commands unknown";
    return ctx.linear.queueComment(t, body, { once: ["commands", t.id, body, ctx.now] });
  } });`

// checkedOutWorker is worker on a tree that exists as a git worktree, as a worker's tree does when dispatch types into it.
func (h *harness) checkedOutWorker(ticket, status string) {
	h.t.Helper()
	h.worktreeOf(ticket)
	h.worker(ticket, status)
}

// lastComment is the latest reading a rule commented.
func (h *harness) lastComment() string {
	h.t.Helper()
	comments := h.turnComments()
	require.NotEmpty(h.t, comments)
	return comments[len(comments)-1]
}

// useCommandsRule has dispatch run only the rule that comments what a worker's commands read as.
func (h *harness) useCommandsRule() {
	h.t.Helper()
	h.config("[dispatch]\nrules = " + `"` + oneRule(h.t, commandsRule) + `"` + "\n")
}

// worktreeOf is the path of ticket's tree, made a real git worktree of the harness repo.
func (h *harness) worktreeOf(ticket string) string {
	h.t.Helper()
	tree := filepath.Join(h.dir, "trees", ticket)
	if _, err := os.Stat(filepath.Join(h.repo, ".git")); err != nil {
		hintGit(h.t, h.repo, "init", "-q")
		hintGit(h.t, h.repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
	}
	if _, err := os.Stat(tree); err != nil {
		hintGit(h.t, h.repo, "worktree", "add", "-q", "-b", "br-"+ticket, tree)
	}
	return tree
}

// recordCommand runs the prompt hook as the Claude session in tree, and returns what the hook printed.
// It starts the loop first, because the hook reports to a running dispatch.
func (h *harness) recordCommand(tree, session, prompt string) (stdout, stderr string) {
	h.t.Helper()
	if h.running == nil {
		require.NoError(h.t, h.startLoop(dispatch.Options{}))
	}
	input, err := json.Marshal(map[string]string{"hook_event_name": "UserPromptSubmit", "session_id": session, "cwd": tree, "prompt": prompt})
	require.NoError(h.t, err)
	stdout, stderr, err = h.runWith(context.Background(), dispatch.Options{Stdin: bytes.NewReader(input)}, "wake")
	require.NoError(h.t, err)
	return stdout, stderr
}

// commandsRead is what a rule reads as the worker's commands once dispatch has restarted.
func (h *harness) commandsRead() string {
	h.t.Helper()
	h.useCommandsRule()
	h.restart()
	h.tick()
	return h.lastComment()
}

// typeInto has the person of ticket's worker type prompt into the "worker" session.
func (h *harness) typeInto(ticket, prompt string) {
	h.t.Helper()
	h.recordCommand(h.worktreeOf(ticket), "worker", prompt)
}

func TestRulesReadTheCommandsASessionTookWithTheirArgumentsOldestFirst(t *testing.T) {
	for _, test := range []struct {
		name    string
		prompts []string
		want    string
	}{
		{"NameAndArguments", []string{"/merge-it 42"}, "commands merge-it|42|false|person of 1"},
		{"NameOnly", []string{"/merge-it"}, "commands merge-it||false|person of 1"},
		{"ArgumentsHoldASecondSlash", []string{"/merge-it and /explain this"}, "commands merge-it|and /explain this|false|person of 1"},
		{"ArgumentsTrimmed", []string{"/merge-it \t 42 \n\n"}, "commands merge-it|42|false|person of 1"},
		{"MultiLineArguments", []string{"/merge-it 42\nand then\nstop"}, "commands merge-it|42\nand then\nstop|false|person of 1"},
		{"PluginAndSkillNames", []string{"/dispatch:build ENG-457"}, "commands dispatch:build|ENG-457|false|person of 1"},
		{"NameWithDotAndUnderscore", []string{"/a_b.c-d"}, "commands a_b.c-d||false|person of 1"},
		{"NameThenNewline", []string{"/merge-it\n42"}, "commands merge-it|42|false|person of 1"},
		{"UppercaseAndDigitName", []string{"/Merge2"}, "commands Merge2||false|person of 1"},
		{"NameFollowedByNonBreakingSpace", []string{"/merge-it\u00a042"}, "commands merge-it|42|false|person of 1"},
		{"OldestFirst", []string{"/one", "/two", "/three"}, "commands one||false|person;two||false|person;three||false|person of 3"},
		{"SkipsMessagesThatAreNotCommands", []string{"/one", "when should I run /merge-it?", "/two"}, "commands one||false|person;two||false|person of 2"},
		{"MentionIsNotACommand", []string{"when should I run /merge-it?"}, "commands  of 0"},
		{"PathIsNotACommand", []string{"/Users/x"}, "commands  of 0"},
		{"LeadingSpaceIsNotACommand", []string{" /x"}, "commands  of 0"},
		{"LoneSlashIsNotACommand", []string{"/", "/ x", "//x", "/-x", "/_x"}, "commands  of 0"},
		{"NameRunningIntoPunctuationIsNotACommand", []string{"/merge-it?", "/x/y"}, "commands  of 0"},
		{"TaskNotificationIsNotACommand", []string{"<task-notification>done</task-notification>"}, "commands  of 0"},
		{"EmptyMessage", []string{""}, "commands  of 0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.useCommandsRule()
			h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
			h.checkedOutWorker("ENG-1", "working")
			for _, prompt := range test.prompts {
				h.typeInto("ENG-1", prompt)
			}

			h.tick()

			assert.Equal(t, test.want, h.lastComment())
		})
	}
}

func TestCommandArgumentsAreCutAtOneKilobyteWithoutSplittingACharacter(t *testing.T) {
	for _, test := range []struct {
		name, args string
		wantCut    bool
		wantChars  int
	}{
		{"UnderTheLimit", strings.Repeat("a", 1024), false, 1024},
		{"OverTheLimit", strings.Repeat("a", 2000), true, 1024},
		{"TwoByteCharacters", strings.Repeat("é", 1000), true, 512},
		{"ThreeByteCharacterOnTheLimit", "ab" + strings.Repeat("€", 500), true, 2 + 340},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.config("[dispatch]\nrules = " + `"` + oneRule(t, `
whenever({ name: "cut", each: "linear.ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => {
    const c = ctx.herdr.worker(t).commands[0];
    const body = "cut " + c.cut + " characters " + c.args.length;
    return ctx.linear.queueComment(t, body, { once: ["cut", t.id] });
  } });`) + `"` + "\n")
			h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
			h.checkedOutWorker("ENG-1", "working")
			h.typeInto("ENG-1", "/long "+test.args)

			h.tick()

			assert.Equal(t, fmt.Sprintf("cut %t characters %d", test.wantCut, test.wantChars), h.lastComment())
		})
	}
}

func TestCommandsAreReadForAWorkerInAnyStateOnAnInProgressOrInReviewTicket(t *testing.T) {
	for _, test := range []struct {
		name, status, worker string
	}{
		{"WorkingInProgress", "In Progress", "working"},
		{"BlockedInProgress", "In Progress", "blocked"},
		{"IdleInProgress", "In Progress", "idle"},
		{"IdleInReview", "In Review", "done"},
		{"WorkingInReview", "In Review", "working"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.useCommandsRule()
			h.tickets(ticket("ENG-1", test.status, "dispatch"))
			h.checkedOutWorker("ENG-1", test.worker)
			h.typeInto("ENG-1", "/merge-it 42")

			h.tick()

			assert.Equal(t, "commands merge-it|42|false|person of 1", h.lastComment())
		})
	}
}

func TestCommandsAreUnknownWhenHerdrReportsNoSessionOrTheLogHasAHole(t *testing.T) {
	t.Run("NoSession", func(t *testing.T) {
		h := newHarness(t)
		h.useCommandsRule()
		h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
		h.checkedOutWorker("ENG-1", "working")
		h.typeInto("ENG-1", "/merge-it 42")
		h.setWorkerSession("ENG-1", "")

		h.tick()

		assert.Equal(t, "commands unknown", h.lastComment())
	})
	t.Run("BadLine", func(t *testing.T) {
		h := newHarness(t)
		h.useCommandsRule()
		h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
		h.checkedOutWorker("ENG-1", "working")
		h.typeInto("ENG-1", "/one")
		appendToSessionLog(t, h.state, "not json\n")
		h.typeInto("ENG-1", "/two")

		h.tick()

		assert.Equal(t, "commands unknown", h.lastComment())
	})
}

func TestCommandsOfASessionThatTookNoneReadAsEmpty(t *testing.T) {
	h := newHarness(t)
	h.useCommandsRule()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.checkedOutWorker("ENG-1", "working")

	h.tick()

	assert.Equal(t, "commands  of 0", h.lastComment())
}

func TestAWorkerRelaunchedInANewSessionReadsNoCommandsEvenOnTheSameTree(t *testing.T) {
	h := newHarness(t)
	h.useCommandsRule()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.checkedOutWorker("ENG-1", "working")
	h.typeInto("ENG-1", "/merge-it 42")

	h.setWorkerSession("ENG-1", "relaunched")
	h.advance(time.Minute)
	h.tick()

	assert.Equal(t, "commands  of 0", h.lastComment())
}

func TestCommandsOfAnotherSessionInTheTreeAreNotRead(t *testing.T) {
	h := newHarness(t)
	h.useCommandsRule()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.checkedOutWorker("ENG-1", "working")
	h.recordCommand(h.worktreeOf("ENG-1"), "stranger", "/eval-run")
	h.typeInto("ENG-1", "/merge-it 42")

	h.tick()

	assert.Equal(t, "commands merge-it|42|false|person of 1", h.lastComment())
}

func TestCommandsSurviveARestartAndAStateResetButNotOneTakenWhileDispatchWasDown(t *testing.T) {
	h := newHarness(t)
	h.useCommandsRule()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.checkedOutWorker("ENG-1", "working")
	h.typeInto("ENG-1", "/one")
	h.tick()
	require.NoError(t, h.stopLoop())
	_, err := h.run("state", "reset")
	require.NoError(t, err)
	h.typeInto("ENG-1", "/two")
	require.NoError(t, h.stopLoop())

	// A hook with no dispatch to tell drops its report.
	input, err := json.Marshal(map[string]string{"session_id": "worker", "cwd": h.worktreeOf("ENG-1"), "prompt": "/lost"})
	require.NoError(t, err)
	_, _, err = h.runWith(t.Context(), dispatch.Options{Stdin: bytes.NewReader(input)}, "wake")
	require.NoError(t, err)
	h.restart()
	h.advance(5 * time.Minute)
	h.tick()

	assert.Equal(t, "commands one||false|person;two||false|person of 2", h.turnComments()[len(h.turnComments())-1])
}

func TestACommandTakenWhileTheLoopRunsReachesRulesAtTheNextResync(t *testing.T) {
	h := newHarness(t)
	h.useCommandsRule()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.checkedOutWorker("ENG-1", "working")
	require.NoError(t, h.startLoop(dispatch.Options{}))
	t.Cleanup(func() { _ = h.stopLoop() })
	h.typeInto("ENG-1", "/merge-it 42")

	h.advance(time.Hour)

	assert.Equal(t, "commands merge-it|42|false|person of 1", h.turnComments()[len(h.turnComments())-1])
}

func TestReplayReproducesADecisionThatReadCommandsAndTraceShowsThem(t *testing.T) {
	h := newHarness(t)
	h.useCommandsRule()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.checkedOutWorker("ENG-1", "working")
	h.typeInto("ENG-1", "/merge-it 42")
	h.tick()
	require.NoError(t, h.stopLoop())
	// Replay opens no log, so the tree's logs can go.
	require.NoError(t, os.RemoveAll(sessionlog.Dir(h.state)))

	cycles, matched := replayCounts(t, h)
	out, err := h.run("trace", "eng-1")

	require.NoError(t, err)
	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
	assert.Regexp(t, `commands.*\{"name":"merge-it","args":"42","cut":false,"at":\d+,"by":"person"\}`, out)
}

func TestACommandDispatchKickedOffWithReadsAsSentByDispatchOnce(t *testing.T) {
	h := newHarness(t)
	h.builtinLauncher = true
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	tree := h.pool()
	h.tick()
	require.Len(t, h.tools.Herdr.Kicked(), 1)
	kickoff := strings.SplitN(h.tools.Herdr.Kicked()[0], "|", 2)
	h.tools.Herdr.SetSession(kickoff[0], "launched")
	h.recordCommand(tree, "launched", kickoff[1])
	h.recordCommand(tree, "launched", kickoff[1])

	h.useCommandsRule()
	h.restart()
	h.tick()

	assert.Equal(t, "commands dispatch:build|ENG-1|false|dispatch;dispatch:build|ENG-1|false|person of 2", h.lastComment())
}

func TestANudgeThatStartsWithASlashReadsAsSentByDispatchOnceWhetherOneLineOrMany(t *testing.T) {
	for _, test := range []struct {
		name, text, want string
	}{
		{"OneLine", "/merge-it 42", "commands merge-it|42|false|dispatch;merge-it|42|false|person of 2"},
		{"ManyLines", "/merge-it 42\nthen stop", "commands merge-it|42\nthen stop|false|dispatch;merge-it|42\nthen stop|false|person of 2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.useCommandsRule()
			h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
			h.checkedOutWorker("ENG-1", "idle")
			_, err := h.run("tell", "ENG-1", test.text)
			require.NoError(t, err)

			h.typeInto("ENG-1", test.text)
			h.typeInto("ENG-1", test.text)
			h.advance(5 * time.Minute)
			h.tick()

			assert.Equal(t, test.want, h.lastComment())
		})
	}
}

func TestANudgeThatIsNotACommandLeavesAPersonsLaterCommandAPersons(t *testing.T) {
	h := newHarness(t)
	h.useCommandsRule()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.checkedOutWorker("ENG-1", "idle")
	_, err := h.run("tell", "ENG-1", "keep going")
	require.NoError(t, err)

	h.typeInto("ENG-1", "keep going")
	h.typeInto("ENG-1", "/keep going")
	h.tick()

	assert.Equal(t, "commands keep|going|false|person of 1", h.lastComment())
}

func TestANudgeHerdrRefusedLeavesNoSendForAPersonsCommandToMatch(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(h *harness)
	}{
		{"BlockedAfterSnapshot", func(h *harness) { h.blockAfterSnapshot("ENG-1") }},
		{"BlockedBeforeSnapshot", func(h *harness) { h.setWorkerStatus("ENG-1", "blocked") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.useCommandsRule()
			h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
			h.checkedOutWorker("ENG-1", "idle")
			test.setup(h)
			_, err := h.run("tell", "ENG-1", "/merge-it 42")
			require.Error(t, err)
			require.Empty(t, h.prompts())

			h.typeInto("ENG-1", "/merge-it 42")
			h.tick()

			assert.Equal(t, "commands merge-it|42|false|person of 1", h.lastComment())
		})
	}
}

func TestANudgeThatTimedOutKeepsItsSendBecauseTheTextMayHaveBeenTyped(t *testing.T) {
	h := newHarness(t)
	h.useCommandsRule()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.checkedOutWorker("ENG-1", "idle")
	h.tools.Herdr.StallPrompts()
	_, _, err := h.runWith(t.Context(), dispatch.Options{ActionTimeout: 100 * time.Millisecond}, "tell", "ENG-1", "/merge-it 42")
	require.NoError(t, err)

	h.typeInto("ENG-1", "/merge-it 42")
	h.tick()

	assert.Equal(t, "commands merge-it|42|false|dispatch of 1", h.lastComment())
}

func TestEachSendMatchesOneCommandWhenManyHooksRace(t *testing.T) {
	h := newHarness(t)
	h.checkedOutWorker("ENG-1", "idle")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	_, err := h.run("tell", "ENG-1", "/merge-it 42")
	require.NoError(t, err)
	input, err := json.Marshal(map[string]string{"session_id": "worker", "cwd": h.worktreeOf("ENG-1"), "prompt": "/merge-it 42"})
	require.NoError(t, err)

	// The harness rewrites the config file for every command, so the options are made before any hook runs.
	var all []dispatch.Options
	for range 8 {
		all = append(all, h.options(dispatch.Options{Stdin: bytes.NewReader(input)}))
	}
	var wg sync.WaitGroup
	for _, opts := range all {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.NoError(t, dispatch.Run(t.Context(), []string{"wake"}, opts))
		}()
	}
	wg.Wait()

	read := h.commandsRead()
	assert.Equal(t, 1, strings.Count(read, "|dispatch"))
	assert.Equal(t, 7, strings.Count(read, "|person"))
}

func TestClosingAWorkspaceDropsTheNotesOfWhatWasTypedIntoItsTree(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.checkedOutWorker("ENG-1", "idle")
	h.tools.Herdr.StallPrompts()
	_, _, err := h.runWith(t.Context(), dispatch.Options{ActionTimeout: 100 * time.Millisecond}, "tell", "ENG-1", "/merge-it 42")
	require.NoError(t, err)
	markLaunch(t, h.state, h.worktreeOf("ENG-1"), h.clock.Now())
	h.advance(6 * time.Minute)
	h.tick()
	require.Equal(t, []string{"w-ENG-1"}, h.tools.Herdr.ClosedWorkspaces())

	// A new Claude on the same tree must not inherit the note of the one that was closed.
	h.worker("ENG-1", "idle")
	h.typeInto("ENG-1", "/merge-it 42")

	assert.Equal(t, "commands merge-it|42|false|person of 1", h.commandsRead())
}

func TestReturningATreeDropsItsCommands(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-2", "In Progress", "dispatch"))
	h.checkedOutWorker("ENG-1", "idle")
	h.checkedOutWorker("ENG-2", "idle")
	h.typeInto("ENG-1", "/one")
	h.typeInto("ENG-2", "/two")
	require.Len(t, sessionLogs(t, h.state), 2)
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	remote := filepath.Join(h.dir, "remotes", "ENG-1.git")
	require.NoError(t, os.MkdirAll(filepath.Dir(remote), 0o755))
	hintGit(t, h.dir, "init", "-q", "--bare", remote)
	tree := filepath.Join(h.dir, "trees", "ENG-1")
	hintGit(t, tree, "remote", "add", "origin", remote)
	hintGit(t, tree, "push", "-q", "--set-upstream", "origin", "br-ENG-1")

	h.tick()

	assert.Len(t, sessionLogs(t, h.state), 1)
}

func TestTheHookNeverBlocksOrAddsTextWhateverGoesWrong(t *testing.T) {
	for _, test := range []struct {
		name    string
		breakIt func(h *harness)
	}{
		{"BadConfig", func(h *harness) { h.config("[dispatch]\nnot_a_setting = 1\n") }},
		{"NoDispatchRunning", func(h *harness) {}},
		{"StuckDispatch", func(h *harness) {
			ln, err := net.Listen("unix", loop.SocketPath(h.state))
			require.NoError(t, err)
			t.Cleanup(func() { _ = ln.Close() })
			go func() {
				for {
					conn, err := ln.Accept()
					if err != nil {
						return
					}
					t.Cleanup(func() { _ = conn.Close() })
				}
			}()
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.checkedOutWorker("ENG-1", "idle")
			tree := h.worktreeOf("ENG-1")
			test.breakIt(h)
			input, err := json.Marshal(map[string]string{"session_id": "worker", "cwd": tree, "prompt": "/merge-it 42"})
			require.NoError(t, err)
			started := time.Now()

			stdout, _, err := h.runWith(t.Context(), dispatch.Options{Stdin: bytes.NewReader(input)}, "wake")

			require.NoError(t, err)
			assert.Empty(t, stdout)
			assert.Less(t, time.Since(started), 3*time.Second)
		})
	}
}

func TestRecordCommandDoesNothingOutsideAWorkerTree(t *testing.T) {
	h := newHarness(t)
	h.checkedOutWorker("ENG-1", "idle")
	h.typeInto("ENG-1", "/merge-it 42")
	logsBefore := sessionLogs(t, h.state)
	require.Len(t, logsBefore, 1)
	other := newHintRepo(t)

	for _, test := range []struct {
		name string
		cwd  func() string
	}{
		{"MainCheckout", func() string { return h.repo }},
		{"UnmanagedRepo", func() string { return other }},
		{"NotARepo", func() string { return t.TempDir() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			h.recordCommand(test.cwd(), "worker", "/merge-it 42")

			assert.Equal(t, logsBefore, sessionLogs(t, h.state))
		})
	}
	_, _, err := h.runWith(t.Context(), dispatch.Options{Stdin: strings.NewReader("not json")}, "wake")
	require.NoError(t, err)
	assert.Equal(t, logsBefore, sessionLogs(t, h.state))
}

func TestARecordCommandForAMessageThatIsNotACommandWritesNothing(t *testing.T) {
	h := newHarness(t)
	h.checkedOutWorker("ENG-1", "idle")

	h.typeInto("ENG-1", "please fix the build")

	assert.Empty(t, sessionLogs(t, h.state))
}

func TestWorkerFixtureTakesCommands(t *testing.T) {
	h := newHarness(t)
	dir := rulesDir(t, map[string]string{
		"dispatch.ts": `import { whenever, known } from "dispatch";
whenever({ name: "person-merged", each: "linear.ticket", if: (t, ctx) => {
    const w = ctx.herdr.worker(t);
    return t.status === "in_progress" && known(() => w.commands) && w.commands.some((c) => c.name === "merge-it" && c.by === "person" && c.args === "42" && !c.cut);
  }, then: (t, ctx) => ctx.herdr.queueNudge(t, "hi", { once: ["merged", t.id] }) });
whenever({ name: "commands-unread", each: "linear.ticket", if: (t, ctx) => t.status === "in_progress" && !known(() => ctx.herdr.worker(t).commands),
  then: (t, ctx) => ctx.herdr.queueNudge(t, "hi", { once: ["unread", t.id] }) });`,
		"commands.test.ts": `import { scenario, ticket, worker, rulesFrom } from "dispatch/testing";
const rules = rulesFrom("./dispatch");
const mine = { status: "in_progress", optedIn: true, repo: "dispatch" };

scenario(rules, "a rule reads the commands the fixture gives", s => {
  s.given(ticket("ENG-1", mine), worker("ENG-1", "idle", { commands: [{ name: "merge-it", args: "42", at: 1000, by: "person" }] }));
  s.expect([["person-merged", "herdr.nudge", "ENG-1"]]);
});

scenario(rules, "a command sent by dispatch is not a person's", s => {
  s.given(ticket("ENG-1", mine), worker("ENG-1", "idle", { commands: [{ name: "merge-it", args: "42", at: 1000, by: "dispatch" }] }));
  s.expect([]);
});

scenario(rules, "a worker given no commands has taken none", s => {
  s.given(ticket("ENG-1", mine), worker("ENG-1", "idle"));
  s.expect([]);
});

scenario(rules, "sessionUnread makes the commands unknown", s => {
  s.given(ticket("ENG-1", mine), worker("ENG-1", "idle", { sessionUnread: "no session" }));
  s.expect([["commands-unread", "herdr.nudge", "ENG-1"]]);
});
`,
	})

	out, err := h.rules(dir, "test")

	require.NoError(t, err, out)
	assert.Contains(t, out, "0 failed")
}

func TestACommandIsStampedWithTheTimeClaudeTookIt(t *testing.T) {
	h := newHarness(t)
	h.config("[dispatch]\nrules = " + `"` + oneRule(t, `
whenever({ name: "when", each: "linear.ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => {
    const cmds = ctx.herdr.worker(t).commands;
    const body = "at " + cmds.map((c) => c.at).join(",");
    return ctx.linear.queueComment(t, body, { once: ["when", t.id, body] });
  } });`) + `"` + "\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.checkedOutWorker("ENG-1", "working")
	h.advance(time.Minute)
	first := h.clock.Now().UnixMilli()
	h.typeInto("ENG-1", "/one")
	h.advance(time.Minute)
	second := h.clock.Now().UnixMilli()
	h.typeInto("ENG-1", "/two")

	h.tick()
	require.Eventually(t, func() bool {
		comments := h.turnComments()
		return len(comments) > 0 && comments[len(comments)-1] == fmt.Sprintf("at %d,%d", first, second)
	}, 5*time.Second, 5*time.Millisecond)
}

func TestAKickoffHerdrNeverConfirmedLeavesNoNoteForAPersonsCommandToMatch(t *testing.T) {
	h := newHarness(t)
	h.builtinLauncher = true
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	tree := h.pool()
	h.tools.Herdr.Fail(herdrtest.OpKickoff, herdr.ErrKickoffUnconfirmed)
	_, _ = h.run("tick")

	h.recordCommand(tree, "worker", "/dispatch:build ENG-1")

	h.workerOn("ENG-1", tree, "w-ENG-1", "working")
	assert.Equal(t, "commands dispatch:build|ENG-1|false|person of 1", h.commandsRead())
}
