package dispatch_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/tooltest"
	"github.com/kapetan-io/dispatch/internal/treehouse"
)

func TestLaunchStartsAWorkerTypedTheBuildKickoff(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.True(t, strings.HasPrefix(out.raw, `{"status":"ok"`))
	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, "ENG-1", out.result["ticket"])
	assert.Equal(t, "thrawn01/eng-1", out.result["branch"])
	assert.Equal(t, l.tree, out.result["worktree"])
	assert.Equal(t, "w9", out.result["workspace"])
	assert.Equal(t, "w9:p1", out.result["pane"])
	assert.Equal(t, "eng-1", out.result["agent"])
	assert.Equal(t, "/dispatch:build ENG-1", out.result["kickoff"])
	assert.Equal(t, "w9:p1 /dispatch:build ENG-1\n", l.readFile("runs.log"))
	assert.Equal(t, l.branchIn(l.tree), "thrawn01/eng-1")
	starts := l.readFile("starts.log")
	assert.Contains(t, starts, "agent start eng-1 --kind claude --pane w9:p1 --timeout 90000 -- --model sonnet --plugin-dir /plugin")
	tabs := l.readFile("tabs.log")
	assert.Contains(t, tabs, "--env DISPATCH_PLUGIN_ROOT=/plugin")
	assert.Contains(t, tabs, "--env AUTONOMOUS_WORKER=1")
	assert.Contains(t, tabs, "--env PATH=/plugin/scripts/shims:")
}

func TestLaunchLabelsTheTabWithTheTicketAndTitle(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()

	out := l.launch("ENG-1", "thrawn01/eng-1", "--title", "Fix the thing")

	assert.Equal(t, "ok", out.result["status"])
	assert.Contains(t, l.readFile("tabs.log"), "--label ENG-1 Fix the thing --env")
}

func TestLaunchLabelsTheTabWithTheTicketWhenGivenNoTitle(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Contains(t, l.readFile("tabs.log"), "--label ENG-1 --env")
}

func TestLaunchTypesTheKickoffItIsGiven(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()

	out := l.launch("ENG-1", "thrawn01/eng-1", "--model", "opus", "--kickoff", "/dispatch:design ENG-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, "/dispatch:design ENG-1", out.result["kickoff"])
	assert.Equal(t, "w9:p1 /dispatch:design ENG-1\n", l.readFile("runs.log"))
	assert.Contains(t, l.readFile("starts.log"), "--model opus")
}

func TestLaunchStartsNewBranchesFromTheBase(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()
	l.git(l.repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "second")
	l.git(l.repo, "branch", "release", "HEAD~1")

	out := l.launch("ENG-1", "thrawn01/eng-1", "--base", "release")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, l.revParse(l.repo, "release"), l.revParse(l.tree, "HEAD"))
}

func TestLaunchReportsAWorkerThatIsAlreadyRunning(t *testing.T) {
	l := newLaunchHarness(t)
	l.tools.Treehouse.MoveLease(l.tree, "ENG-1")
	l.writeFile("workspaces.json", `{"result":{"workspaces":[{"workspace_id":"w3","worktree":{"checkout_path":"`+l.tree+`"}}]}}`)
	l.writeFile("panes.json", `{"result":{"panes":[{"pane_id":"w3:p2","workspace_id":"w3","tab_id":"w3:t1","agent":"claude"}]}}`)

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "already_running", out.result["status"])
	assert.Equal(t, "w3", out.result["workspace"])
	assert.Equal(t, "w3:p2", out.result["pane"])
	assert.NoFileExists(t, filepath.Join(l.dir, "runs.log"))
	assert.Empty(t, l.tools.Calls("treehouse", "lease"))
}

func TestLaunchReusesTheLeaseOfAnEarlierRun(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()
	l.tools.Treehouse.MoveLease(l.tree, "ENG-1")

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, l.tree, out.result["worktree"])
	assert.Empty(t, l.tools.Calls("treehouse", "lease"))
}

func TestLaunchReportsWhyItCouldNotStart(t *testing.T) {
	for _, test := range []struct {
		name    string
		setup   func(l *launchHarness)
		args    []string
		status  string
		detail  string
		wantErr string
	}{
		{
			name:   "NoArguments",
			args:   []string{},
			status: "usage_error",
			detail: "TICKET and BRANCH are required",
		},
		{
			name:   "UnknownFlag",
			args:   []string{"ENG-1", "b", "--nope"},
			status: "usage_error",
			detail: "flag provided but not defined: -nope",
		},
		{
			name:   "UnknownArgument",
			args:   []string{"ENG-1", "b", "stray"},
			status: "usage_error",
			detail: "unknown argument: stray",
		},
		{
			name:   "NotARepo",
			setup:  func(l *launchHarness) { l.dirOverride = t.TempDir() },
			args:   []string{"ENG-1", "b"},
			status: "not_a_repo",
			detail: "run from inside the target repo",
		},
		{
			name:   "NotPooled",
			setup:  func(l *launchHarness) { require.NoError(t, os.Remove(filepath.Join(l.repo, "treehouse.toml"))) },
			args:   []string{"ENG-1", "b"},
			status: "not_pooled",
			detail: "has no treehouse.toml",
		},
		{
			name:   "HerdrUnavailable",
			setup:  func(l *launchHarness) { l.writeFile("herdr-down", "") },
			args:   []string{"ENG-1", "b"},
			status: "herdr_unavailable",
			detail: "herdr server not answering",
		},
		{
			name: "PoolExhausted",
			setup: func(l *launchHarness) {
				l.tools.Treehouse.Clear(l.repo)
				l.tools.Treehouse.Seed(l.repo,
					treehouse.Tree{Path: "/t/1", Status: "leased", LeaseHolder: "ENG-7"},
					treehouse.Tree{Path: "/t/2", Status: "leased", LeaseHolder: "ENG-8"},
					treehouse.Tree{Path: "/t/3", Status: "leased", LeaseHolder: "ENG-9"},
					treehouse.Tree{Path: "/t/4", Status: "leased"},
				)
			},
			args:   []string{"ENG-1", "b"},
			status: "pool_exhausted",
			detail: "all 4/4 pool trees are taken: ENG-7 (leased), ENG-8 (leased), ENG-9 (leased), ? (leased)",
		},
		{
			name: "PoolErrorWhenATreeIsFree",
			setup: func(l *launchHarness) {
				l.tools.Treehouse.FailLease(errors.New("no tree for you"))
			},
			args:   []string{"ENG-1", "b"},
			status: "pool_error",
			detail: "treehouse lease failed but the pool is not exhausted (1 of 1 available, max_trees=4; holders: ). treehouse stderr: no tree for you",
		},
		{
			// A pool that can still grow is not exhausted, so the ticket must
			// escalate instead of waiting for a slot.
			name: "PoolErrorWhenEveryTreeIsTakenBelowMaxTrees",
			setup: func(l *launchHarness) {
				l.tools.Treehouse.Clear(l.repo)
				l.tools.Treehouse.Seed(l.repo,
					treehouse.Tree{Path: "/t/1", Status: "leased", LeaseHolder: "ENG-7"},
					treehouse.Tree{Path: "/t/2", Status: "leased", LeaseHolder: "ENG-8"},
					treehouse.Tree{Path: "/t/3", Status: "leased", LeaseHolder: "ENG-9"},
				)
				l.tools.Treehouse.FailLease(errors.New("cannot create a worktree"))
			},
			args:   []string{"ENG-1", "b"},
			status: "pool_error",
			detail: "the pool is not exhausted (0 of 3 available, max_trees=4",
		},
		{
			name: "LeaseWithoutAPath",
			setup: func(l *launchHarness) {
				l.tools.Treehouse.FailLease(fmt.Errorf("%w: {}", treehouse.ErrNoPath))
			},
			args:   []string{"ENG-1", "b"},
			status: "pool_error",
			detail: "lease succeeded but treehouse JSON had no path",
		},
		{
			name: "PoolErrorWhenMaxTreesIsUnknown",
			setup: func(l *launchHarness) {
				require.NoError(t, os.WriteFile(filepath.Join(l.repo, "treehouse.toml"), nil, 0o644))
				l.tools.Treehouse.Clear(l.repo)
				l.tools.Treehouse.Seed(l.repo, treehouse.Tree{Path: "/t/1", Status: "leased", LeaseHolder: "ENG-7"})
			},
			args:   []string{"ENG-1", "b"},
			status: "pool_error",
			detail: "the pool is not exhausted (0 of 1 available, max_trees=-1",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			l := newLaunchHarness(t)
			if test.setup != nil {
				test.setup(l)
			}

			out := l.launchArgs(test.args...)

			assert.Equal(t, test.status, out.result["status"])
			assert.Contains(t, out.result["detail"], test.detail)
			assert.Error(t, out.err)
		})
	}
}

func TestLaunchReturnsALeaseWhenTheBranchWillNotCheckOut(t *testing.T) {
	l := newLaunchHarness(t)
	// A branch checked out in the main clone cannot be switched to in the pool tree.
	l.git(l.repo, "checkout", "-q", "-b", "thrawn01/eng-1")

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "checkout_failed", out.result["status"])
	assert.Contains(t, out.result["detail"], "(lease returned)")
	assert.Equal(t, []string{l.repo + "|" + l.tree + "|ENG-1"}, l.returns())
}

func TestLaunchReturnsALeaseItTookWhenTheWorkspaceFails(t *testing.T) {
	l := newLaunchHarness(t)

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "workspace_failed", out.result["status"])
	assert.Equal(t, []string{l.repo + "|" + l.tree + "|ENG-1"}, l.returns())
}

func TestLaunchKeepsALeaseFromAnEarlierRunWhenTheWorkspaceFails(t *testing.T) {
	l := newLaunchHarness(t)
	// The tree is already leased to ENG-1, so it may hold a worker's
	// uncommitted work that a forced return would discard.
	l.tools.Treehouse.MoveLease(l.tree, "ENG-1")

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "workspace_failed", out.result["status"])
	assert.Empty(t, l.returns())
}

func TestLaunchClosesTheWorkspaceBeforeReturningALeaseItTook(t *testing.T) {
	l := newLaunchHarness(t)
	l.writeFile("open-works", "")

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "workspace_failed", out.result["status"])
	assert.Equal(t, "workspace close w9\n", l.readFile("closes.log"))
	assert.Equal(t, []string{l.repo + "|" + l.tree + "|ENG-1"}, l.returns())
}

func TestLaunchKeepsALeaseWhoseWorkspaceWouldNotClose(t *testing.T) {
	l := newLaunchHarness(t)
	l.writeFile("open-works", "")
	// A tree returned with its workspace still open would match that
	// workspace by path, so the next ticket to lease it would never start.
	l.writeFile("close-fails", "")

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "workspace_failed", out.result["status"])
	assert.Empty(t, l.returns())
}

func TestLaunchClosesTheTabsTheWorkspaceOpenedWith(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()
	l.writeFile("panes.json", `{"result":{"panes":[`+
		`{"pane_id":"w9:p0","workspace_id":"w9","tab_id":"w9:t0","agent":""},`+
		`{"pane_id":"w9:p1","workspace_id":"w9","tab_id":"w9:t1","agent":""},`+
		`{"pane_id":"w2:p1","workspace_id":"w2","tab_id":"w2:t1","agent":""}]}}`)

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, "tab close w9:t0\n", l.readFile("tab-closes.log"))
}

func TestLaunchNeverGivesClaudeASessionName(t *testing.T) {
	l := newLaunchHarness(t)
	l.writeFile("open-works", "")
	l.writeFile("tab-works", "")

	out := l.launch("ENG-1", "thrawn01/eng-1")

	// Claude draws a --name below an open question, which hides the question
	// from herdr's blocked detection.
	assert.Equal(t, "agent_start_failed", out.result["status"])
	starts := l.readFile("starts.log")
	assert.Contains(t, starts, "agent start eng-1 --kind claude --pane w9:p1")
	assert.NotContains(t, starts, "--name")
}

func TestLaunchGivesBackTheLeaseWhenTheAgentWillNotStart(t *testing.T) {
	l := newLaunchHarness(t)
	l.writeFile("open-works", "")
	l.writeFile("tab-works", "")

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "agent_start_failed", out.result["status"])
	assert.Equal(t, "workspace close w9\n", l.readFile("closes.log"))
	assert.Equal(t, []string{l.repo + "|" + l.tree + "|ENG-1"}, l.returns())
	// The first start fails and the second runs after a pause, since a
	// timed-out start may still bring Claude up.
	assert.Len(t, strings.Split(strings.TrimSpace(l.readFile("starts.log")), "\n"), 2)
}

func TestLaunchKeepsAnAgentWhoseStartTimedOutButCameUp(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()
	l.writeFile("start-fails", "")
	l.writeFile("agents.json", `{"result":{"agents":[{"name":"eng-1","pane_id":"w9:p1","agent":"claude","agent_status":"idle"}]}}`)

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Len(t, strings.Split(strings.TrimSpace(l.readFile("starts.log")), "\n"), 1)
}

func TestLaunchNamesARelaunchedWorkerApartFromItsPredecessor(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()
	l.writeFile("agents.json", `{"result":{"agents":[{"name":"eng-1","pane_id":"w1:p1","agent":"claude","agent_status":"done"}]}}`)

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Regexp(t, `^eng-1-r\d+$`, out.result["agent"])
}

func TestLaunchAnswersTheConsentAndFolderTrustDialogs(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()
	l.screens("Bypass Permissions mode  1. No, exit  2. Yes, I accept",
		"Bypass Permissions mode  1. No, exit  2. Yes, I accept",
		"Do you trust this folder?  ❯ No, exit  Yes, proceed",
		"❯")

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	// The consent dialog is answered two ways in turn, because herdr's key
	// names differ across versions.
	assert.Equal(t, "pane send-keys w9:p1 Down Enter\n"+
		"pane send-keys w9:p1 2\n"+
		"pane send-keys w9:p1 Enter\n"+
		"pane send-keys w9:p1 Down\n"+
		"pane send-keys w9:p1 Enter\n"+
		"pane send-keys w9:p1 Enter\n", l.readFile("keys.log"))
}

func TestLaunchNeverRetypesAKickoffThatIsRunning(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()
	// While a skill runs, the screen shows only a spinner, so the kickoff is
	// confirmed by herdr's status and not by its text.
	l.writeFile("wait-fails", "1")
	l.writeFile("agents.json", `{"result":{"agents":[{"name":"x","pane_id":"w9:p1","agent":"claude","agent_status":"working"}]}}`)

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, "w9:p1 /dispatch:build ENG-1\n", l.readFile("runs.log"))
}

func TestLaunchRetypesAKickoffWhileTheAgentStaysIdle(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()
	l.writeFile("wait-fails", "1")
	l.writeFile("agents.json", `{"result":{"agents":[{"name":"x","pane_id":"w9:p1","agent":"claude","agent_status":"idle"}]}}`)

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, "w9:p1 /dispatch:build ENG-1\nw9:p1 /dispatch:build ENG-1\n", l.readFile("runs.log"))
}

func TestLaunchPressesEnterOnAKickoffThatWasTypedButNotSubmitted(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()
	l.writeFile("wait-fails", "1")
	l.writeFile("agents.json", `{"result":{"agents":[{"name":"x","pane_id":"w9:p1","agent":"claude","agent_status":"idle"}]}}`)
	l.screens("❯", "❯ /dispatch:build ENG-1")

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, "w9:p1 /dispatch:build ENG-1\n", l.readFile("runs.log"))
}

func TestLaunchPressesEnterOnAGivenKickoffTypedButNotSubmitted(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()
	l.writeFile("wait-fails", "1")
	l.writeFile("agents.json", `{"result":{"agents":[{"name":"x","pane_id":"w9:p1","agent":"claude","agent_status":"idle"}]}}`)
	l.screens("❯", "❯ /dispatch:design ENG-1")

	out := l.launch("ENG-1", "thrawn01/eng-1", "--kickoff", "/dispatch:design ENG-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, "w9:p1 /dispatch:design ENG-1\n", l.readFile("runs.log"))
}

func TestLaunchClosesTheWorkspaceWhenTheKickoffNeverRuns(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()
	l.writeFile("wait-fails", "100")
	l.writeFile("agents.json", `{"result":{"agents":[{"name":"x","pane_id":"w9:p1","agent":"claude","agent_status":"idle"}]}}`)

	out := l.launch("ENG-1", "thrawn01/eng-1")

	// Left open, the idle Claude would look like a live worker and no tick
	// would retry the launch.
	assert.Equal(t, "injection_unconfirmed", out.result["status"])
	assert.Equal(t, "workspace close w9\n", l.readFile("closes.log"))
	assert.Len(t, strings.Split(strings.TrimSpace(l.readFile("runs.log")), "\n"), 3)
	assert.Empty(t, l.returns())
}

func TestLaunchExitsCleanlyOnlyWhenTheWorkerIsRunning(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()
	ok := l.launch("ENG-1", "thrawn01/eng-1")
	l.tools.Treehouse.MoveLease(l.tree, "ENG-1")
	l.writeFile("workspaces.json", `{"result":{"workspaces":[{"workspace_id":"w3","worktree":{"checkout_path":"`+l.tree+`"}}]}}`)
	l.writeFile("panes.json", `{"result":{"panes":[{"pane_id":"w3:p2","workspace_id":"w3","tab_id":"w3:t1","agent":"claude"}]}}`)

	running := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", ok.result["status"])
	assert.NoError(t, ok.err)
	assert.Equal(t, "already_running", running.result["status"])
	assert.NoError(t, running.err)
}

func TestLaunchPutsStatusFirstInEveryResult(t *testing.T) {
	l := newLaunchHarness(t)

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "workspace_failed", out.result["status"])
	assert.True(t, strings.HasPrefix(out.raw, `{"status":"workspace_failed"`))
	assert.Equal(t, l.tree, out.result["worktree"])
}

func TestLaunchAcceptsTheNoFocusFlag(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()

	out := l.launch("ENG-1", "thrawn01/eng-1", "--no-focus")

	assert.Equal(t, "ok", out.result["status"])
}

func TestLaunchChecksOutAnExistingLocalBranch(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()
	l.git(l.repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "second")
	l.git(l.repo, "branch", "thrawn01/eng-1", "HEAD~1")

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, "thrawn01/eng-1", l.branchIn(l.tree))
	assert.Equal(t, l.revParse(l.repo, "thrawn01/eng-1"), l.revParse(l.tree, "HEAD"))
}

func TestLaunchChecksOutABranchOnlyTheRemoteHas(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()
	remote := filepath.Join(l.dir, "remote.git")
	l.git(l.dir, "init", "-q", "--bare", remote)
	l.git(l.repo, "remote", "add", "origin", remote)
	l.git(l.repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "remote work")
	l.git(l.repo, "push", "-q", "origin", "HEAD:refs/heads/thrawn01/eng-1")
	l.git(l.repo, "reset", "-q", "--hard", "HEAD~1")

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, "thrawn01/eng-1", l.branchIn(l.tree))
	assert.Equal(t, l.revParse(remote, "refs/heads/thrawn01/eng-1"), l.revParse(l.tree, "HEAD"))
}

func TestLaunchStartsNewBranchesFromTheRemoteBase(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()
	remote := filepath.Join(l.dir, "remote.git")
	l.git(l.dir, "init", "-q", "--bare", remote)
	l.git(l.repo, "remote", "add", "origin", remote)
	l.git(l.repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "remote main")
	l.git(l.repo, "push", "-q", "origin", "main")
	l.git(l.repo, "reset", "-q", "--hard", "HEAD~1")

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, l.revParse(remote, "refs/heads/main"), l.revParse(l.tree, "HEAD"))
}

func TestLaunchOpensTheWorkspaceFromTheMainCheckout(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()
	l.writeFile(filepath.Join("tree", "treehouse.toml"), "max_trees = 4\n")
	l.dirOverride = l.tree

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	resolved, err := filepath.EvalSymlinks(l.repo)
	require.NoError(t, err)
	assert.Contains(t, l.readFile("opens.log"), "--cwd "+resolved+" ")
	assert.Contains(t, l.readFile("opens.log"), "--path "+l.tree)
}

func TestLaunchReusesAWorkspaceThatHasNoClaudePane(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()
	l.tools.Treehouse.MoveLease(l.tree, "ENG-1")
	l.writeFile("workspaces.json", `{"result":{"workspaces":[{"workspace_id":"w3","worktree":{"checkout_path":"`+l.tree+`"}}]}}`)
	l.writeFile("panes.json", `{"result":{"panes":[{"pane_id":"w3:p2","workspace_id":"w3","tab_id":"w3:t1","agent":""}]}}`)

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, "w3", out.result["workspace"])
	assert.NoFileExists(t, filepath.Join(l.dir, "opens.log"))
	assert.Contains(t, l.readFile("tabs.log"), "--workspace w3")
}

func TestLaunchDoesNotRetypeAKickoffThatAlreadyFinishedOrIsBlocked(t *testing.T) {
	for _, status := range []string{"done", "blocked"} {
		t.Run(status, func(t *testing.T) {
			l := newLaunchHarness(t)
			l.workerStarts()
			l.writeFile("wait-fails", "1")
			l.writeFile("agents.json", `{"result":{"agents":[{"name":"x","pane_id":"w9:p1","agent":"claude","agent_status":"`+status+`"}]}}`)

			out := l.launch("ENG-1", "thrawn01/eng-1")

			assert.Equal(t, "ok", out.result["status"])
			assert.Equal(t, "w9:p1 /dispatch:build ENG-1\n", l.readFile("runs.log"))
		})
	}
}

func TestLaunchClosesTheWorkspaceWhenTheAgentVanishesAfterTheKickoff(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()
	l.writeFile("wait-fails", "100")

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "injection_unconfirmed", out.result["status"])
	assert.Equal(t, "workspace close w9\n", l.readFile("closes.log"))
	assert.Equal(t, "w9:p1 /dispatch:build ENG-1\n", l.readFile("runs.log"))
}

func TestLaunchClosesTheWorkspaceWhenClaudeNeverShowsAnInputBox(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()
	l.screens("loading")

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "injection_unconfirmed", out.result["status"])
	assert.Equal(t, "workspace close w9\n", l.readFile("closes.log"))
	assert.NoFileExists(t, filepath.Join(l.dir, "runs.log"))
}

func TestLaunchPressesEnterOnlyOnTheFolderTrustDialogWhenYesIsSelected(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()
	l.screens("Do you trust this folder?  No, exit  ❯ Yes, proceed", "❯")

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, "pane send-keys w9:p1 Enter\npane send-keys w9:p1 Enter\n", l.readFile("keys.log"))
}

func TestLaunchRecordsTheScreensItReadsWhenTraced(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()
	trace := filepath.Join(l.dir, "trace.log")
	t.Setenv("DISPATCH_LAUNCH_TRACE", trace)

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	b, err := os.ReadFile(trace)
	require.NoError(t, err)
	assert.Contains(t, string(b), "dialogs")
	assert.Contains(t, string(b), "typing")
}

func TestTickLaunchesWithTheBuiltInLauncher(t *testing.T) {
	h := newHarness(t)
	h.builtinLauncher = true
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	tree := filepath.Join(h.dir, "tree")
	require.NoError(t, os.WriteFile(filepath.Join(h.repo, "treehouse.toml"), []byte("max_trees = 4\n"), 0o644))
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "init"},
		{"worktree", "add", "-q", "--detach", tree},
	} {
		out, err := exec.Command("git", append([]string{"-C", h.repo}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	h.script("herdr", fakeHerdr(h.dir))
	h.tools.Treehouse.AddTree(h.repo, tree)
	for _, name := range []string{"open-works", "tab-works", "start-works"} {
		h.writeFile(name, "")
	}

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "launch ENG-1: ok\n", out)
	assert.Equal(t, "w9:p1 /dispatch:build ENG-1\n", string(h.readFile("runs.log")))
	assert.Contains(t, string(h.readFile("starts.log")), "--model sonnet")
	assert.Contains(t, string(h.readFile("tabs.log")), "--label ENG-1 ticket ENG-1 --env")
}

func TestLaunchTakesFlagsBeforeAndBetweenTheTicketAndBranch(t *testing.T) {
	for _, args := range [][]string{
		{"--model", "opus", "ENG-1", "thrawn01/eng-1"},
		{"ENG-1", "--model", "opus", "thrawn01/eng-1"},
	} {
		l := newLaunchHarness(t)
		l.workerStarts()

		out := l.launchArgs(args...)

		assert.Equal(t, "ok", out.result["status"])
		assert.Equal(t, "thrawn01/eng-1", out.result["branch"])
		assert.Contains(t, l.readFile("starts.log"), "--model opus")
	}
}

func TestLaunchGivesUpAtOnceWhenThePreflightTimeoutIsZero(t *testing.T) {
	l := newLaunchHarness(t)
	l.writeFile("herdr-down", "")
	t.Setenv("DISPATCH_LAUNCH_PREFLIGHT_TIMEOUT", "0")

	start := time.Now()
	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "herdr_unavailable", out.result["status"])
	assert.Less(t, time.Since(start), 4*time.Second)
}

func TestTickRecordsKickoffScreensWhenTraced(t *testing.T) {
	h := newHarness(t)
	h.builtinLauncher = true
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	tree := filepath.Join(h.dir, "tree")
	require.NoError(t, os.WriteFile(filepath.Join(h.repo, "treehouse.toml"), []byte("max_trees = 4\n"), 0o644))
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "init"},
		{"worktree", "add", "-q", "--detach", tree},
	} {
		out, err := exec.Command("git", append([]string{"-C", h.repo}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	h.script("herdr", fakeHerdr(h.dir))
	h.tools.Treehouse.AddTree(h.repo, tree)
	for _, name := range []string{"open-works", "tab-works", "start-works"} {
		h.writeFile(name, "")
	}
	trace := filepath.Join(h.dir, "trace.log")
	t.Setenv("DISPATCH_LAUNCH_TRACE", trace)

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, string(h.readFile("trace.log")), "typing")
}

func TestLaunchRefusesAnEmptyPluginRoot(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()
	t.Setenv("DISPATCH_PLUGIN_ROOT", "")
	l.writeFile("config.toml", "plugin_root = \"\"\n")

	out := l.launchWith(dispatch.Options{ConfigPath: filepath.Join(l.dir, "config.toml")}, "ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "usage_error", out.result["status"])
	assert.Contains(t, out.result["detail"], "no plugin root")
	assert.NoFileExists(t, filepath.Join(l.dir, "starts.log"))
}

func TestTickRefusesAnEmptyPluginRoot(t *testing.T) {
	h := newHarness(t)
	h.builtinLauncher = true
	h.extra = "plugin_root = \"\"\n"
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	require.NoError(t, os.WriteFile(filepath.Join(h.repo, "treehouse.toml"), []byte("max_trees = 4\n"), 0o644))
	out, err := exec.Command("git", "-C", h.repo, "init", "-q", "-b", "main").CombinedOutput()
	require.NoError(t, err, string(out))

	out2, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out2, "launch ENG-1: usage_error (failure 1 of 2) no plugin root")
}

func TestLaunchWrapperRunsTheGoLauncher(t *testing.T) {
	l := newLaunchHarness(t)
	l.workerStarts()
	build := exec.Command("go", "build", "-o", filepath.Join(l.bin, "dispatch"), "./cmd/dispatch")
	out, err := build.CombinedOutput()
	require.NoError(t, err, string(out))
	wrapper, err := filepath.Abs("bin/dispatch-launch")
	require.NoError(t, err)

	cmd := exec.Command(wrapper, "ENG-1", "thrawn01/eng-1", "--kickoff", "/dispatch:design ENG-1")
	cmd.Dir = l.repo
	// The harness exports a plugin root, so clear it to see the wrapper set its own.
	cmd.Env = append(os.Environ(), "PATH="+l.bin+":"+os.Getenv("PATH"), "DISPATCH_PLUGIN_ROOT=")
	stdout, err := cmd.Output()
	require.NoError(t, err)

	root, err := filepath.Abs(".")
	require.NoError(t, err)
	assert.Contains(t, string(stdout), `"kickoff":"/dispatch:design ENG-1"`)
	assert.Contains(t, l.readFile("tabs.log"), "DISPATCH_PLUGIN_ROOT="+root)
}

// launchHarness runs `dispatch launch` in a throwaway git repo against fake
// herdr and treehouse commands. With no other setup the launch fails at the
// workspace step. workerStarts lets it run through to the kickoff.
type launchHarness struct {
	t     *testing.T
	dir   string
	repo  string
	tree  string
	bin   string
	tools *tooltest.Tools
	// dirOverride replaces the repo as the directory the launch runs in.
	dirOverride string
}

type launchOutput struct {
	raw    string
	result map[string]string
	err    error
}

func newLaunchHarness(t *testing.T) *launchHarness {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	l := &launchHarness{
		t:     t,
		dir:   dir,
		repo:  filepath.Join(dir, "repo"),
		tree:  filepath.Join(dir, "tree"),
		bin:   filepath.Join(dir, "bin"),
		tools: tooltest.New(t),
	}
	require.NoError(t, os.MkdirAll(l.repo, 0o755))
	require.NoError(t, os.MkdirAll(l.bin, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(l.repo, "treehouse.toml"), []byte("max_trees = 4\n"), 0o644))
	l.git(l.repo, "init", "-q", "-b", "main")
	l.git(l.repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	l.git(l.repo, "worktree", "add", "-q", "--detach", l.tree)
	l.tools.Treehouse.AddTree(l.repo, l.tree)
	l.script("herdr", fakeHerdr(dir))
	t.Setenv("DISPATCH_PLUGIN_ROOT", "/plugin")
	t.Setenv("DISPATCH_LAUNCH_PREFLIGHT_TIMEOUT", "1")
	return l
}

// fakeHerdr logs what it is asked and answers from files in dir. A file named
// like a step (open-works, tab-works, start-works) lets that step succeed.
// wait-fails holds how many waits for a running agent time out.
func fakeHerdr(dir string) string {
	return `d="` + dir + `"
[ -f "$d/herdr-down" ] && exit 1
case "$1 $2" in
"workspace list") if [ -f "$d/workspaces.json" ]; then cat "$d/workspaces.json"; else echo '{"result":{"workspaces":[]}}'; fi ;;
"worktree open")
  echo "$*" >> "$d/opens.log"
  [ -f "$d/open-works" ] || { echo '{"error":{"code":"refused","message":"no"}}'; exit 1; }
  echo '{"result":{"worktree":{"open_workspace_id":"w9"}}}' ;;
"tab create")
  [ -f "$d/tab-works" ] || { echo '{"error":{"code":"refused","message":"no"}}'; exit 1; }
  echo "$*" >> "$d/tabs.log"
  echo '{"result":{"root_pane":{"pane_id":"w9:p1"}}}' ;;
"tab close") echo "$*" >> "$d/tab-closes.log" ;;
"pane list") if [ -f "$d/panes.json" ]; then cat "$d/panes.json"; else echo '{"result":{"panes":[]}}'; fi ;;
"pane read") echo '$ ' ;;
"pane run") echo "$3 $4" >> "$d/runs.log" ;;
"pane send-keys") echo "$*" >> "$d/keys.log" ;;
"agent list") if [ -f "$d/agents.json" ]; then cat "$d/agents.json"; else echo '{"result":{"agents":[]}}'; fi ;;
"agent start")
  echo "$*" >> "$d/starts.log"
  [ -f "$d/start-works" ] && [ ! -f "$d/start-fails" ] && exit 0
  exit 1 ;;
"agent read")
  n=$(cat "$d/reads" 2>/dev/null || echo 0); echo $((n+1)) > "$d/reads"
  f="$d/screens/$n"; [ -f "$f" ] || f="$d/screens/last"
  if [ -f "$f" ]; then cat "$f"; else echo '❯'; fi ;;
"agent wait")
  case "$*" in *working*)
    left=$(cat "$d/wait-fails" 2>/dev/null || echo 0)
    if [ "$left" -gt 0 ]; then echo $((left-1)) > "$d/wait-fails"; exit 1; fi ;;
  esac ;;
"workspace close") [ -f "$d/close-fails" ] && exit 1; echo "$*" >> "$d/closes.log" ;;
*) exit 2 ;;
esac`
}

// workerStarts lets the workspace, tab and agent steps succeed.
func (l *launchHarness) workerStarts() {
	l.writeFile("open-works", "")
	l.writeFile("tab-works", "")
	l.writeFile("start-works", "")
}

// screens sets what the worker's screen shows on each read, the last one
// repeating. Without it the screen always shows an empty input box.
func (l *launchHarness) screens(texts ...string) {
	require.NoError(l.t, os.MkdirAll(filepath.Join(l.dir, "screens"), 0o755))
	for i, text := range texts {
		name := strconv.Itoa(i)
		if i == len(texts)-1 {
			name = "last"
		}
		l.writeFile(filepath.Join("screens", name), text+"\n")
	}
}

func (l *launchHarness) launch(ticket, branch string, flags ...string) launchOutput {
	return l.launchArgs(append([]string{ticket, branch}, flags...)...)
}

func (l *launchHarness) launchArgs(args ...string) launchOutput {
	return l.launchWith(dispatch.Options{}, args...)
}

func (l *launchHarness) launchWith(opts dispatch.Options, args ...string) launchOutput {
	var stdout bytes.Buffer
	dir := l.repo
	if l.dirOverride != "" {
		dir = l.dirOverride
	}
	opts.Dir = dir
	opts.Herdr = filepath.Join(l.bin, "herdr")
	opts.Treehouse = l.tools.Treehouse
	opts.LaunchPace = time.Millisecond
	opts.Stdout = &stdout
	opts.Stderr = io.Discard
	err := dispatch.Run(context.Background(), append([]string{"launch"}, args...), opts)
	out := launchOutput{raw: stdout.String(), err: err}
	require.NoError(l.t, json.Unmarshal(stdout.Bytes(), &out.result))
	return out
}

// returns lists the treehouse return calls, as "<repo>|<tree>|<holder>".
func (l *launchHarness) returns() []string {
	return l.tools.Calls("treehouse", "return")
}

func (l *launchHarness) readFile(name string) string {
	b, err := os.ReadFile(filepath.Join(l.dir, name))
	require.NoError(l.t, err)
	return string(b)
}

func (l *launchHarness) git(dir string, args ...string) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	require.NoError(l.t, err, string(out))
}

func (l *launchHarness) revParse(dir, ref string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", ref).Output()
	require.NoError(l.t, err)
	return strings.TrimSpace(string(out))
}

func (l *launchHarness) branchIn(dir string) string {
	out, err := exec.Command("git", "-C", dir, "branch", "--show-current").Output()
	require.NoError(l.t, err)
	return strings.TrimSpace(string(out))
}

func (l *launchHarness) script(name, body string) {
	writeFake(l.t, filepath.Join(l.bin, name), l.dir, body)
}

func (l *launchHarness) writeFile(name, content string) {
	require.NoError(l.t, os.WriteFile(filepath.Join(l.dir, name), []byte(content), 0o644))
}

func (l *launchHarness) writeJSON(name string, v any) {
	b, err := json.Marshal(v)
	require.NoError(l.t, err)
	l.writeFile(name, string(b))
}

func TestClaudeShimRestoresWorkerFlagsWithoutAName(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "claude"),
		[]byte("#!/usr/bin/env bash\necho \"$*\" > \""+filepath.Join(dir, "argv")+"\"\n"), 0o755))
	shims, err := filepath.Abs("scripts/shims")
	require.NoError(t, err)

	// herdr restarts a worker with a bare `claude --resume` and replays its
	// env, which for workers launched before --name was dropped still holds
	// DISPATCH_WORKER_NAME.
	cmd := exec.Command(filepath.Join(shims, "claude"), "--resume")
	cmd.Env = append(os.Environ(),
		"PATH="+shims+":"+dir+":"+os.Getenv("PATH"),
		"AUTONOMOUS_WORKER=1",
		"DISPATCH_PLUGIN_ROOT=/plugin",
		"DISPATCH_WORKER_NAME=eng-1",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	argv, err := os.ReadFile(filepath.Join(dir, "argv"))
	require.NoError(t, err)
	assert.Equal(t, "--resume --plugin-dir /plugin --disallowedTools EnterPlanMode,ExitPlanMode --dangerously-skip-permissions\n", string(argv))
}
