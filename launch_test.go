package dispatch_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
	"github.com/kapetan-io/dispatch/internal/tooltest"
	"github.com/kapetan-io/dispatch/internal/treehouse"
)

// launchEnv is the plugin root and the herdr preflight wait launch tests run under, unless a test
// is about one of them.
var launchEnv = map[string]string{"DISPATCH_PLUGIN_ROOT": "/plugin", "DISPATCH_LAUNCH_PREFLIGHT_TIMEOUT": "1"}

func TestLaunchStartsAWorkerTypedTheBuildKickoff(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Config: "[plugins.herdr]\nplugin_root = \"/plugin\"\n"})
	tree := d.Trees.Pooled()

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.NoError(t, out.err)
	assert.Equal(t, "w2", out.result["workspace"])
	assert.Equal(t, []string{"w2:p2|/dispatch:build ENG-1"}, d.fakes().Multiplexer.Kicked())
	assert.Equal(t, "thrawn01/eng-1", branchIn(t, tree))
	require.Len(t, d.fakes().Multiplexer.Started(), 1)
	started := d.fakes().Multiplexer.Started()[0]
	assert.Equal(t, herdr.Worker{Workspace: "w2", Dir: tree, FreshLease: true, Label: "ENG-1", Name: "eng-1", PluginRoot: "/plugin", Program: started.Program}, started)
	assert.Equal(t, "claude", started.Program.AgentKind)
	assert.Equal(t, []string{
		"--model", "sonnet", "--plugin-dir", "/plugin", "--disallowedTools", "EnterPlanMode,ExitPlanMode",
		"--settings", `{"crossSessionInbound":"accept"}`, "--dangerously-skip-permissions",
	}, started.Program.Argv)
	assert.Equal(t, []string{"/plugin/scripts/shims"}, started.Program.PathFirst)
}

func TestLaunchLabelsTheTabWithTheTicketAndTitle(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()

	out := d.launch("ENG-1", "thrawn01/eng-1", "--title", "Fix the thing")

	assert.Equal(t, "ok", out.result["status"])
	require.Len(t, d.fakes().Multiplexer.Started(), 1)
	assert.Equal(t, "ENG-1 Fix the thing", d.fakes().Multiplexer.Started()[0].Label)
}

func TestLaunchReportsAWorkerThatIsAlreadyRunning(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	// The lease and the session were started by hand while dispatch ran, so no worker row names them.
	d.Tick()
	d.fakes().Treehouse.MoveLease(tree, "ENG-1")
	d.fakes().Multiplexer.AddWorkspace(herdr.Workspace{ID: "w3", CheckoutPath: tree})
	d.fakes().Multiplexer.AddAgent(herdr.Agent{Kind: "claude", Status: "working", PaneID: "w3:p2", TabID: "w3:t1", WorkspaceID: "w3"})

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "already_running", out.result["status"])
	assert.Empty(t, d.fakes().Multiplexer.Kicked())
	assert.Empty(t, d.fakes().Multiplexer.Started())
	assert.Empty(t, d.fakes().Calls("treehouse", "lease"))
}

func TestLaunchReusesTheLeaseOfAnEarlierRun(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	d.fakes().Treehouse.MoveLease(tree, "ENG-1")

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, tree, out.result["worktree"])
	assert.Empty(t, d.fakes().Calls("treehouse", "lease"))
	require.Len(t, d.fakes().Multiplexer.Started(), 1)
	assert.False(t, d.fakes().Multiplexer.Started()[0].FreshLease)
}

func TestLaunchWaitsOutAPoolReadThatFailsAndTheNextLaunchReusesTheLease(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	d.fakes().Treehouse.MoveLease(tree, "ENG-1")
	d.fakes().Treehouse.FailTrees(errors.New("exit status 1"))

	out := d.launch("ENG-1", "thrawn01/eng-1")

	// The worker is unknown while the pool is unread, so no launch runs.
	assert.Empty(t, out.result["status"])
	assert.Empty(t, d.fakes().Calls("treehouse", "lease"))

	out = d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, tree, out.result["worktree"])
	assert.Empty(t, d.fakes().Calls("treehouse", "lease"))
	require.Len(t, d.fakes().Multiplexer.Started(), 1)
	assert.False(t, d.fakes().Multiplexer.Started()[0].FreshLease)
}

func TestLaunchReportsWhyItCouldNotStart(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		setup  func(d *Dispatch)
		status string
		detail string
	}{
		{
			name:   "NotPooled",
			setup:  func(d *Dispatch) { require.NoError(t, os.Remove(filepath.Join(d.Repo, "treehouse.toml"))) },
			status: "not_pooled",
			detail: "has no treehouse.toml",
		},
		{
			name: "PoolExhausted",
			setup: func(d *Dispatch) {
				d.fakes().Treehouse.Clear(d.Repo)
				d.fakes().Treehouse.Seed(d.Repo,
					treehouse.Tree{Path: "/t/1", Status: "leased", LeaseHolder: "ENG-7"},
					treehouse.Tree{Path: "/t/2", Status: "leased", LeaseHolder: "ENG-8"},
					treehouse.Tree{Path: "/t/3", Status: "leased", LeaseHolder: "ENG-9"},
					treehouse.Tree{Path: "/t/4", Status: "leased"},
				)
			},
			status: "pool_exhausted",
			detail: "all 4/4 pool trees are taken: ENG-7 (leased), ENG-8 (leased), ENG-9 (leased), ? (leased)",
		},
		{
			name: "LeaseFailedWhenATreeIsFree",
			setup: func(d *Dispatch) {
				d.fakes().Treehouse.FailLease(errors.New("no tree for you"))
			},
			status: "lease_failed",
			detail: "treehouse lease failed but the pool is not exhausted (1 of 1 available, max_trees=4; holders: ). treehouse stderr: no tree for you",
		},
		{
			// A pool that can still grow is not exhausted, so this is a failure to print,
			// not a wait for a slot.
			name: "LeaseFailedWhenEveryTreeIsTakenBelowMaxTrees",
			setup: func(d *Dispatch) {
				d.fakes().Treehouse.Clear(d.Repo)
				d.fakes().Treehouse.Seed(d.Repo,
					treehouse.Tree{Path: "/t/1", Status: "leased", LeaseHolder: "ENG-7"},
					treehouse.Tree{Path: "/t/2", Status: "leased", LeaseHolder: "ENG-8"},
					treehouse.Tree{Path: "/t/3", Status: "leased", LeaseHolder: "ENG-9"},
				)
				d.fakes().Treehouse.FailLease(errors.New("cannot create a worktree"))
			},
			status: "lease_failed",
			detail: "the pool is not exhausted (0 of 3 available, max_trees=4",
		},
		{
			name: "LeaseWithoutAPath",
			setup: func(d *Dispatch) {
				d.fakes().Treehouse.FailLease(fmt.Errorf("%w: {}", treehouse.ErrNoPath))
			},
			status: "pool_error",
			detail: "lease succeeded but treehouse JSON had no path",
		},
		{
			name: "LeaseFailedWhenMaxTreesIsUnknown",
			setup: func(d *Dispatch) {
				require.NoError(t, os.WriteFile(filepath.Join(d.Repo, "treehouse.toml"), nil, 0o644))
				d.fakes().Treehouse.Clear(d.Repo)
				d.fakes().Treehouse.Seed(d.Repo, treehouse.Tree{Path: "/t/1", Status: "leased", LeaseHolder: "ENG-7"})
			},
			status: "lease_failed",
			detail: "the pool is not exhausted (0 of 1 available, max_trees=-1",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
			d.Trees.Pooled()
			if test.setup != nil {
				test.setup(d)
			}

			out := d.launch("ENG-1", "b")

			assert.Equal(t, test.status, out.result["status"])
			// A full pool's receipt holds the status alone, and the line dispatch run prints names the holders.
			assert.Contains(t, out.result["detail"]+d.loopOut(), test.detail)
			assert.Error(t, out.err)
		})
	}
}

func TestLaunchReturnsALeaseWhenTheBranchWillNotCheckOut(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	// A branch checked out in the main clone cannot be switched to in the pool tree.
	hintGit(t, d.Repo, "checkout", "-q", "-b", "thrawn01/eng-1")

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "switch_failed", out.result["status"])
	assert.Contains(t, out.result["detail"], "(lease returned)")
	assert.Equal(t, []string{d.Repo + "|" + tree + "|ENG-1"}, d.Trees.Returned())
}

func TestLaunchReturnsALeaseItTookWhenTheWorkspaceFails(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	d.fakes().Multiplexer.Fail(herdrtest.OpOpenWorkspace, errors.New("herdr worktree open failed"))

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "workspace_failed", out.result["status"])
	assert.Contains(t, out.result["detail"], "herdr worktree open failed")
	assert.Equal(t, []string{d.Repo + "|" + tree + "|ENG-1"}, d.Trees.Returned())
}

func TestLaunchKeepsALeaseFromAnEarlierRunWhenTheWorkspaceFails(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	// The tree is already leased to ENG-1, so it may hold a worker's
	// uncommitted work that a forced return would discard.
	d.fakes().Treehouse.MoveLease(tree, "ENG-1")
	d.fakes().Multiplexer.Fail(herdrtest.OpOpenWorkspace, errors.New("herdr worktree open failed"))

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "workspace_failed", out.result["status"])
	assert.Empty(t, d.Trees.Returned())
}

func TestLaunchClosesTheWorkspaceBeforeReturningALeaseItTook(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	d.fakes().Multiplexer.Fail(herdrtest.OpStartTab, errors.New("herdr tab create failed in w2"))

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "workspace_failed", out.result["status"])
	assert.Equal(t, []string{"w2"}, d.fakes().Multiplexer.ClosedWorkspaces())
	assert.Equal(t, []string{d.Repo + "|" + tree + "|ENG-1"}, d.Trees.Returned())
	assert.Less(t, d.callIndex("herdr", "close-workspace"), d.callIndex("treehouse", "return"))
}

func TestLaunchKeepsALeaseWhoseWorkspaceWouldNotClose(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.fakes().Multiplexer.Fail(herdrtest.OpStartTab, errors.New("herdr tab create failed in w2"))
	// A tree returned with its workspace still open would match that
	// workspace by path, so the next ticket to lease it would never start.
	d.fakes().Multiplexer.Fail(herdrtest.OpCloseWorkspace, errors.New("exit status 1"))

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "workspace_failed", out.result["status"])
	assert.Empty(t, d.Trees.Returned())
}

func TestLaunchClosesTheTabsTheWorkspaceOpenedWith(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.fakes().Multiplexer.AddWorkspace(herdr.Workspace{ID: "w2", Label: "other"})
	d.fakes().Multiplexer.AddTab(herdr.Tab{ID: "w2:t1", Label: "other", WorkspaceID: "w2"})
	d.fakes().Multiplexer.AddPane(herdr.Pane{ID: "w2:p1", TabID: "w2:t1", WorkspaceID: "w2"})

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	snap, err := d.fakes().Multiplexer.Read(context.Background())
	require.NoError(t, err)
	assert.Contains(t, snap.Tabs, herdr.Tab{ID: "w2:t1", Label: "other", WorkspaceID: "w2"})
	assert.Contains(t, snap.Tabs, herdr.Tab{ID: out.result["workspace"] + ":t2", Label: "ENG-1", WorkspaceID: out.result["workspace"]})
	assert.NotContains(t, snap.Tabs, herdr.Tab{ID: out.result["workspace"] + ":t1", Label: "ENG-1", WorkspaceID: out.result["workspace"]})
}

func TestLaunchGivesBackTheLeaseWhenTheAgentWillNotStart(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	d.fakes().Multiplexer.Fail(herdrtest.OpStartAgent, errors.New("herdr agent start failed in pane w2:p2"))

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "agent_start_failed", out.result["status"])
	assert.Equal(t, []string{"w2"}, d.fakes().Multiplexer.ClosedWorkspaces())
	assert.Equal(t, []string{d.Repo + "|" + tree + "|ENG-1"}, d.Trees.Returned())
	assert.Empty(t, d.fakes().Multiplexer.Kicked())
}

func TestLaunchNamesARelaunchedWorkerApartFromItsPredecessor(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.fakes().Multiplexer.AddWorkspace(herdr.Workspace{ID: "w1", CheckoutPath: "/elsewhere"})
	d.fakes().Multiplexer.AddAgent(herdr.Agent{Kind: "claude", Status: "done", PaneID: "w1:p1", TabID: "w1:t1", WorkspaceID: "w1"})
	d.fakes().Multiplexer.NameAgent("w1:p1", "eng-1")

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	require.Len(t, d.fakes().Multiplexer.Kicked(), 1)
	pane, _, _ := strings.Cut(d.fakes().Multiplexer.Kicked()[0], "|")
	assert.Regexp(t, `^eng-1-r\d+$`, d.fakes().Multiplexer.NameOf(pane))
}

func TestLaunchClosesTheWorkspaceWhenTheKickoffNeverRuns(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.fakes().Multiplexer.Fail(herdrtest.OpKickoff, herdr.ErrKickoffUnconfirmed)

	out := d.launch("ENG-1", "thrawn01/eng-1")

	// Left open, the idle Claude would look like a live worker and no tick
	// would retry the launch.
	assert.Equal(t, "injection_unconfirmed", out.result["status"])
	assert.Contains(t, out.result["detail"], "Claude started but herdr never showed the kickoff running")
	assert.Equal(t, []string{"w2"}, d.fakes().Multiplexer.ClosedWorkspaces())
	assert.Empty(t, d.Trees.Returned())
}

func TestLaunchNamesTheMultiplexerInUseWhenTheKickoffNeverRuns(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.fakes().Multiplexer.NameMultiplexer("Rex")
	d.fakes().Multiplexer.Fail(herdrtest.OpKickoff, herdr.ErrKickoffUnconfirmed)

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "injection_unconfirmed", out.result["status"])
	assert.Contains(t, out.result["detail"], "Claude started but Rex never showed the kickoff running")
	assert.NotContains(t, out.result["detail"], "herdr")
}

func TestLaunchReportsTheExitAndScreenOfAProgramThatExitedBeforeTheKickoff(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.fakes().Multiplexer.NameMultiplexer("Rex")
	d.fakes().Multiplexer.Fail(herdrtest.OpKickoff, &herdr.ProgramExited{Exit: "exit status 1", Screen: "dispatch: no real claude found on PATH"})

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "program_exited", out.result["status"])
	assert.Equal(t, "Claude exited (exit status 1) before Rex showed the kickoff running. Its screen ended with: dispatch: no real claude found on PATH. The workspace was closed and the next resync retries.", out.result["detail"])
	assert.Equal(t, []string{"w2"}, d.fakes().Multiplexer.ClosedWorkspaces())
}

func TestLaunchReportsAnExitWithNoStatusOrScreenWithoutQuotingOne(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.fakes().Multiplexer.Fail(herdrtest.OpKickoff, &herdr.ProgramExited{})

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "program_exited", out.result["status"])
	assert.Equal(t, "Claude exited before herdr showed the kickoff running. The workspace was closed and the next resync retries.", out.result["detail"])
}

func TestLaunchReportsClaudeNeverStartedWhenItsCommandStaysAtTheShellPrompt(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.fakes().Multiplexer.Fail(herdrtest.OpKickoff, herdr.ErrNeverStarted)

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "injection_unconfirmed", out.result["status"])
	assert.Contains(t, out.result["detail"], "never started")
	assert.NotContains(t, out.result["detail"], "Claude started")
	assert.Equal(t, []string{"w2"}, d.fakes().Multiplexer.ClosedWorkspaces())
}

func TestLaunchReportsEnterNotSentWhenHerdrRefusesTheKey(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.fakes().Multiplexer.Fail(herdrtest.OpKickoff, herdr.ErrEnterNotSent)

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "injection_unconfirmed", out.result["status"])
	assert.Contains(t, out.result["detail"], "could not send Enter")
	assert.NotContains(t, out.result["detail"], "after Enter was pressed")
}

func TestLaunchChecksOutAnExistingLocalBranch(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	hintGit(t, d.Repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "second")
	hintGit(t, d.Repo, "branch", "thrawn01/eng-1", "HEAD~1")

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, "thrawn01/eng-1", branchIn(t, tree))
	assert.Equal(t, revParse(t, d.Repo, "thrawn01/eng-1"), revParse(t, tree, "HEAD"))
}

func TestLaunchChecksOutABranchOnlyTheRemoteHas(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	remote := filepath.Join(filepath.Dir(d.Repo), "remote.git")
	hintGit(t, filepath.Dir(d.Repo), "init", "-q", "--bare", remote)
	hintGit(t, d.Repo, "remote", "add", "origin", remote)
	hintGit(t, d.Repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "remote work")
	hintGit(t, d.Repo, "push", "-q", "origin", "HEAD:refs/heads/thrawn01/eng-1")
	hintGit(t, d.Repo, "reset", "-q", "--hard", "HEAD~1")

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, "thrawn01/eng-1", branchIn(t, tree))
	assert.Equal(t, revParse(t, remote, "refs/heads/thrawn01/eng-1"), revParse(t, tree, "HEAD"))
}

func TestLaunchStartsNewBranchesFromTheRemoteBase(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	remote := filepath.Join(filepath.Dir(d.Repo), "remote.git")
	hintGit(t, filepath.Dir(d.Repo), "init", "-q", "--bare", remote)
	hintGit(t, d.Repo, "remote", "add", "origin", remote)
	hintGit(t, d.Repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "remote main")
	hintGit(t, d.Repo, "push", "-q", "origin", "main")
	hintGit(t, d.Repo, "reset", "-q", "--hard", "HEAD~1")

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, revParse(t, remote, "refs/heads/main"), revParse(t, tree, "HEAD"))
}

func TestLaunchStartsNewBranchesFromTheCheckedOutBranchWhenThereIsNoRemote(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	hintGit(t, d.Repo, "branch", "-m", "master")

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, revParse(t, d.Repo, "master"), revParse(t, tree, "HEAD"))
}

func TestLaunchStartsNewBranchesFromTheRemoteDefaultBranch(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	remote := filepath.Join(filepath.Dir(d.Repo), "remote.git")
	hintGit(t, filepath.Dir(d.Repo), "init", "-q", "--bare", "-b", "master", remote)
	hintGit(t, d.Repo, "branch", "-m", "master")
	hintGit(t, d.Repo, "remote", "add", "origin", remote)
	hintGit(t, d.Repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "remote master")
	hintGit(t, d.Repo, "push", "-q", "origin", "master")
	hintGit(t, d.Repo, "remote", "set-head", "origin", "master")
	hintGit(t, d.Repo, "reset", "-q", "--hard", "HEAD~1")
	hintGit(t, d.Repo, "switch", "-q", "-c", "scratch")

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, revParse(t, remote, "refs/heads/master"), revParse(t, tree, "HEAD"))
}

func TestLaunchFollowsARemoteThatRenamedItsDefaultBranch(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	remote := filepath.Join(filepath.Dir(d.Repo), "remote.git")
	hintGit(t, filepath.Dir(d.Repo), "init", "-q", "--bare", "-b", "master", remote)
	hintGit(t, d.Repo, "branch", "-m", "master")
	hintGit(t, d.Repo, "remote", "add", "origin", remote)
	hintGit(t, d.Repo, "push", "-q", "origin", "master")
	hintGit(t, d.Repo, "remote", "set-head", "origin", "master")
	// origin renames master to main and moves ahead, leaving origin/HEAD stale.
	hintGit(t, remote, "branch", "-m", "master", "main")
	hintGit(t, d.Repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "ahead")
	hintGit(t, d.Repo, "push", "-q", "origin", "master:main")
	hintGit(t, d.Repo, "reset", "-q", "--hard", "HEAD~1")

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, revParse(t, remote, "refs/heads/main"), revParse(t, tree, "HEAD"))
}

func TestLaunchSkipsAnUnpushedCheckedOutBranchWhenChoosingTheBase(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	remote := filepath.Join(filepath.Dir(d.Repo), "remote.git")
	hintGit(t, filepath.Dir(d.Repo), "init", "-q", "--bare", remote)
	hintGit(t, d.Repo, "remote", "add", "origin", remote)
	hintGit(t, d.Repo, "push", "-q", "origin", "main")
	hintGit(t, d.Repo, "fetch", "-q", "origin")
	hintGit(t, d.Repo, "config", "init.defaultBranch", "main")
	hintGit(t, d.Repo, "switch", "-q", "-c", "thrawn01/eng-9")
	hintGit(t, d.Repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "unpushed work")

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, revParse(t, remote, "refs/heads/main"), revParse(t, tree, "HEAD"))
}

func TestLaunchReusesAWorkspaceThatHasNoClaudePane(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	d.fakes().Treehouse.MoveLease(tree, "ENG-1")
	d.fakes().Multiplexer.AddWorkspace(herdr.Workspace{ID: "w3", CheckoutPath: tree})
	d.fakes().Multiplexer.AddTab(herdr.Tab{ID: "w3:t1", WorkspaceID: "w3"})
	d.fakes().Multiplexer.AddPane(herdr.Pane{ID: "w3:p2", TabID: "w3:t1", WorkspaceID: "w3"})

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, "w3", out.result["workspace"])
	assert.Empty(t, d.fakes().Multiplexer.Opened())
	require.Len(t, d.fakes().Multiplexer.Started(), 1)
	assert.Equal(t, "w3", d.fakes().Multiplexer.Started()[0].Workspace)
}

func TestTickLaunchesWithTheBuiltInLauncher(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Launcher: builtinLauncher})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	tree := d.Trees.Pooled()

	d.Tick()

	launched := d.receiptsOfKind("ENG-1", "launch")
	require.Len(t, launched, 1)
	assert.Equal(t, "done", launched[0].State)
	assert.Equal(t, []string{"w2:p2|/dispatch:build ENG-1"}, d.fakes().Multiplexer.Kicked())
	require.Len(t, d.fakes().Multiplexer.Started(), 1)
	started := d.fakes().Multiplexer.Started()[0]
	assert.Equal(t, "sonnet", modelOf(started.Program))
	assert.Equal(t, "ENG-1 ticket ENG-1", started.Label)
	assert.Equal(t, tree, d.fakes().Multiplexer.Opened()[0].Path)
}

func TestTickRefusesAnEmptyPluginRoot(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Launcher: builtinLauncher, Config: "[plugins.herdr]\nplugin_root = \"\"\n"})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Trees.Pooled()

	res := d.Run("tick")
	require.ErrorContains(t, res.Err, "ENG-1 launch failed: usage_error")
	require.ErrorContains(t, res.Err, "no plugin root")

	launched := d.receiptsOfKind("ENG-1", "launch")
	require.Len(t, launched, 1)
	assert.Equal(t, "failed", launched[0].State)
	assert.Contains(t, launched[0].Detail, "no plugin root")
}

func TestClaudeShimRestoresWorkerFlagsWithoutAName(t *testing.T) {
	t.Parallel()
	env := isolateShimEnv(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "claude"),
		[]byte("#!/usr/bin/env bash\necho \"$*\" > \""+filepath.Join(dir, "argv")+"\"\n"), 0o755))
	shims, err := filepath.Abs("scripts/shims")
	require.NoError(t, err)
	bin := buildDispatch(t)

	// herdr restarts a worker with a bare `claude --resume` and replays its
	// env, which for workers launched before --name was dropped still holds
	// DISPATCH_WORKER_NAME.
	cmd := exec.Command(filepath.Join(shims, "claude"), "--resume")
	cmd.Env = append(env,
		"PATH="+shims+":"+bin+":"+dir+":"+os.Getenv("PATH"),
		"AUTONOMOUS_WORKER=1",
		"DISPATCH_PLUGIN_ROOT=/plugin",
		"DISPATCH_WORKER_NAME=eng-1",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	argv, err := os.ReadFile(filepath.Join(dir, "argv"))
	require.NoError(t, err)
	assert.Equal(t, "--resume --plugin-dir /plugin --disallowedTools EnterPlanMode,ExitPlanMode --settings {\"crossSessionInbound\":\"accept\"} --dangerously-skip-permissions\n", string(argv))
}

func TestClaudeShimSkipsEveryShimDirectoryOnPath(t *testing.T) {
	t.Parallel()
	env := isolateShimEnv(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "claude"),
		[]byte("#!/usr/bin/env bash\necho \"$*\" > \""+filepath.Join(dir, "argv")+"\"\n"), 0o755))
	shims, err := filepath.Abs("scripts/shims")
	require.NoError(t, err)
	bin := buildDispatch(t)

	// A launch from inside a worker or a plugin session already has one shim
	// directory on PATH when the launcher stamps its own in front.
	other := filepath.Join(t.TempDir(), "scripts", "shims")
	require.NoError(t, os.MkdirAll(other, 0o755))
	body, err := os.ReadFile(filepath.Join(shims, "claude"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(other, "claude"), body, 0o755))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(shims, "claude"), "--resume")
	cmd.Env = append(env,
		"PATH="+strings.Join([]string{shims, other, bin, dir, os.Getenv("PATH")}, ":"),
		"AUTONOMOUS_WORKER=1",
		"DISPATCH_PLUGIN_ROOT=/plugin",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	argv, err := os.ReadFile(filepath.Join(dir, "argv"))
	require.NoError(t, err)
	assert.Equal(t, "--resume --plugin-dir /plugin --disallowedTools EnterPlanMode,ExitPlanMode --settings {\"crossSessionInbound\":\"accept\"} --dangerously-skip-permissions\n", string(argv))
}

// isolateShimEnv keeps the maintainer's own session, which may itself be a
// worker, from reaching the shim through its dispatch, config and plugin root.
func isolateShimEnv(t *testing.T) environment.Environment {
	t.Helper()
	return environment.Process().With("HOME", t.TempDir()).With("DISPATCH_BIN", "").
		With("DISPATCH_PLUGIN_ROOT", "").With("AUTONOMOUS_WORKER", "")
}

func TestClaudeShimFlags(t *testing.T) {
	t.Parallel()
	env := isolateShimEnv(t)
	shims, err := filepath.Abs("scripts/shims")
	require.NoError(t, err)
	bin := buildDispatch(t)

	for _, test := range []struct {
		name string
		args []string
		env  []string
		want string
	}{
		{
			name: "PlainSessionGetsThePluginAndCrossSessionSetting",
			args: []string{"--resume"},
			env:  []string{"DISPATCH_PLUGIN_ROOT=/plugin", "DISPATCH_WORKER_NAME=eng-1"},
			want: "--resume --plugin-dir /plugin --settings {\"crossSessionInbound\":\"accept\"}\n",
		},
		{
			name: "NoEnvAddsOnlyTheCrossSessionSetting",
			args: []string{"--resume"},
			want: "--resume --settings {\"crossSessionInbound\":\"accept\"}\n",
		},
		{
			name: "CallersSettingsAreKept",
			args: []string{"--settings", "/mine.json"},
			want: "--settings /mine.json\n",
		},
		{
			name: "SubcommandPassesThroughUntouched",
			args: []string{"plugin", "update", "dispatch@dispatch"},
			env:  []string{"DISPATCH_PLUGIN_ROOT=/plugin"},
			want: "plugin update dispatch@dispatch\n",
		},
		{
			name: "WorkerSubcommandPassesThroughUntouched",
			args: []string{"mcp", "list"},
			env:  []string{"AUTONOMOUS_WORKER=1", "DISPATCH_PLUGIN_ROOT=/plugin"},
			want: "mcp list\n",
		},
		{
			name: "OptionBeforeASubcommandPassesThroughUntouched",
			args: []string{"--debug", "mcp", "list"},
			env:  []string{"DISPATCH_PLUGIN_ROOT=/plugin"},
			want: "--debug mcp list\n",
		},
		{
			name: "EqualsFormFlagsAreNotRepeated",
			args: []string{"--settings={\"model\":\"haiku\"}", "--plugin-dir=/mine", "--disallowedTools=Bash"},
			env:  []string{"AUTONOMOUS_WORKER=1", "DISPATCH_PLUGIN_ROOT=/plugin"},
			want: "--settings={\"model\":\"haiku\"} --plugin-dir=/mine --disallowedTools=Bash --dangerously-skip-permissions\n",
		},
		{
			name: "PromptThatIsNotASubcommandIsASession",
			args: []string{"fix the plugin"},
			want: "fix the plugin --settings {\"crossSessionInbound\":\"accept\"}\n",
		},
		{
			name: "WorkerFlagsNeedAutonomousWorkerSetTo1",
			args: []string{"--resume"},
			env:  []string{"AUTONOMOUS_WORKER=0", "DISPATCH_PLUGIN_ROOT=/plugin"},
			want: "--resume --plugin-dir /plugin --settings {\"crossSessionInbound\":\"accept\"}\n",
		},
		{
			name: "ShortPrintPassesThroughUntouched",
			args: []string{"-p", "hi"},
			env:  []string{"AUTONOMOUS_WORKER=1", "DISPATCH_PLUGIN_ROOT=/plugin"},
			want: "-p hi\n",
		},
		{
			name: "LongPrintPassesThroughUntouched",
			args: []string{"--print", "hi"},
			env:  []string{"AUTONOMOUS_WORKER=1", "DISPATCH_PLUGIN_ROOT=/plugin"},
			want: "--print hi\n",
		},
		{
			name: "PresentFlagsAreNotRepeated",
			args: []string{"--plugin-dir", "/mine", "--disallowedTools", "Bash", "--settings", "/mine.json", "--dangerously-skip-permissions"},
			env:  []string{"AUTONOMOUS_WORKER=1", "DISPATCH_PLUGIN_ROOT=/plugin"},
			want: "--plugin-dir /mine --disallowedTools Bash --settings /mine.json --dangerously-skip-permissions\n",
		},
		{
			name: "KebabDisallowedToolsIsNotRepeated",
			args: []string{"--disallowed-tools", "Bash"},
			env:  []string{"AUTONOMOUS_WORKER=1"},
			want: "--disallowed-tools Bash --settings {\"crossSessionInbound\":\"accept\"} --dangerously-skip-permissions\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "claude"),
				[]byte("#!/usr/bin/env bash\necho \"$*\" > \""+filepath.Join(dir, "argv")+"\"\n"), 0o755))

			cmd := exec.Command(filepath.Join(shims, "claude"), test.args...)
			cmd.Env = append(append(slices.Clone(env), "AUTONOMOUS_WORKER=", "DISPATCH_PLUGIN_ROOT=", "DISPATCH_WORKER_NAME="), test.env...)
			cmd.Env = append(cmd.Env, "PATH="+shims+":"+bin+":"+dir+":"+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, string(out))

			argv, err := os.ReadFile(filepath.Join(dir, "argv"))
			require.NoError(t, err)
			assert.Equal(t, test.want, string(argv))
		})
	}
}

func TestClaudeShimWithNoRealClaudeFailsClearly(t *testing.T) {
	t.Parallel()
	shims, err := filepath.Abs("scripts/shims")
	require.NoError(t, err)
	bin := buildDispatch(t)

	// A claude without the execute bit is not a claude.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\n"), 0o644))

	cmd := exec.Command(filepath.Join(bin, "dispatch"), "claude-shim", "--resume")
	cmd.Env = append(os.Environ(), "PATH="+shims+":"+dir)
	out, err := cmd.CombinedOutput()

	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit)
	assert.NotZero(t, exit.ExitCode())
	assert.Contains(t, string(out), "no real claude found on PATH")
}

func TestClaudeShimNamesTheMissingDispatchWhenItIsNotOnPath(t *testing.T) {
	t.Parallel()
	shims, err := filepath.Abs("scripts/shims")
	require.NoError(t, err)

	cmd := exec.Command(filepath.Join(shims, "claude"), "--resume")
	cmd.Env = []string{"PATH=" + shims + ":/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()

	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit)
	assert.Equal(t, 127, exit.ExitCode())
	assert.Contains(t, string(out), "dispatch is not on PATH")
}

func TestClaudeShimSkipsAShimDirectoryReachedThroughASymlink(t *testing.T) {
	t.Parallel()
	env := isolateShimEnv(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "claude"),
		[]byte("#!/usr/bin/env bash\necho \"$*\" > \""+filepath.Join(dir, "argv")+"\"\n"), 0o755))
	shims, err := filepath.Abs("scripts/shims")
	require.NoError(t, err)
	bin := buildDispatch(t)
	link := filepath.Join(t.TempDir(), "plugin-shims")
	require.NoError(t, os.Symlink(shims, link))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(shims, "claude"), "--resume")
	cmd.Env = append(env, "PATH="+strings.Join([]string{link, bin, dir, os.Getenv("PATH")}, ":"), "AUTONOMOUS_WORKER=", "DISPATCH_PLUGIN_ROOT=")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	argv, err := os.ReadFile(filepath.Join(dir, "argv"))
	require.NoError(t, err)
	assert.Equal(t, "--resume --settings {\"crossSessionInbound\":\"accept\"}\n", string(argv))
}

func TestClaudeShimNamesAStaleDispatchBinary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/usr/bin/env bash\necho started > \""+filepath.Join(dir, "ran")+"\"\n"), 0o755))
	stale := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(stale, "dispatch"), []byte("#!/usr/bin/env bash\necho 'dispatch: unknown command \"claude-shim\"' >&2\nexit 1\n"), 0o755))
	shims, err := filepath.Abs("scripts/shims")
	require.NoError(t, err)

	cmd := exec.Command(filepath.Join(shims, "claude"), "--resume")
	cmd.Env = append(os.Environ(), "PATH="+strings.Join([]string{shims, stale, dir, os.Getenv("PATH")}, ":"), "DISPATCH_BIN=")
	out, err := cmd.CombinedOutput()

	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit)
	assert.Equal(t, 127, exit.ExitCode())
	assert.Contains(t, string(out), filepath.Join(stale, "dispatch"))
	assert.Contains(t, string(out), "go install ./cmd/dispatch")
	assert.NoFileExists(t, filepath.Join(dir, "ran"))
}

func TestClaudeShimRunsTheDispatchThatLaunchedIt(t *testing.T) {
	t.Parallel()
	env := isolateShimEnv(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "claude"),
		[]byte("#!/usr/bin/env bash\necho \"$*\" > \""+filepath.Join(dir, "argv")+"\"\n"), 0o755))
	stale := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(stale, "dispatch"), []byte("#!/usr/bin/env bash\nexit 1\n"), 0o755))
	shims, err := filepath.Abs("scripts/shims")
	require.NoError(t, err)
	bin := buildDispatch(t)

	// The stale dispatch comes first on PATH, as after a git pull before go install.
	cmd := exec.Command(filepath.Join(shims, "claude"), "--resume")
	cmd.Env = append(env,
		"PATH="+strings.Join([]string{shims, stale, dir, os.Getenv("PATH")}, ":"),
		"DISPATCH_BIN="+filepath.Join(bin, "dispatch"),
		"AUTONOMOUS_WORKER=", "DISPATCH_PLUGIN_ROOT=",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	argv, err := os.ReadFile(filepath.Join(dir, "argv"))
	require.NoError(t, err)
	assert.Equal(t, "--resume --settings {\"crossSessionInbound\":\"accept\"}\n", string(argv))
}

func TestClaudeShimLoadsThePluginRootFromTheConfig(t *testing.T) {
	t.Parallel()
	env := isolateShimEnv(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "claude"),
		[]byte("#!/usr/bin/env bash\necho \"$*\" > \""+filepath.Join(dir, "argv")+"\"\n"), 0o755))
	shims, err := filepath.Abs("scripts/shims")
	require.NoError(t, err)
	bin := buildDispatch(t)
	home := env.Get("HOME")
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".config", "dispatch"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".config", "dispatch", "config.toml"),
		[]byte("[plugins.herdr]\nplugin_root = \"/from-config\"\n"), 0o644))

	for _, test := range []struct {
		name string
		env  string
		want string
	}{
		{name: "ConfigFillsAnUnsetEnv", env: "DISPATCH_PLUGIN_ROOT=", want: "--resume --plugin-dir /from-config --settings {\"crossSessionInbound\":\"accept\"}\n"},
		{name: "EnvBeatsTheConfig", env: "DISPATCH_PLUGIN_ROOT=/from-env", want: "--resume --plugin-dir /from-env --settings {\"crossSessionInbound\":\"accept\"}\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd := exec.Command(filepath.Join(shims, "claude"), "--resume")
			cmd.Env = append(env, "HOME="+home, "AUTONOMOUS_WORKER=", test.env,
				"PATH="+shims+":"+bin+":"+dir+":"+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, string(out))

			argv, err := os.ReadFile(filepath.Join(dir, "argv"))
			require.NoError(t, err)
			assert.Equal(t, test.want, string(argv))
		})
	}
}
func TestLaunchesIntoOneRepoTakeTurnsLeasing(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	second := filepath.Join(filepath.Dir(d.Repo), "tree2")
	hintGit(t, d.Repo, "worktree", "add", "-q", "--detach", second)
	d.fakes().Treehouse.AddTree(d.Repo, second)
	d.fakes().Treehouse.HoldLeases(100 * time.Millisecond)

	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))

	d.Tick()

	for _, id := range []string{"ENG-1", "ENG-2"} {
		launched := d.receiptsOfKind(id, "launch")
		require.Len(t, launched, 1)
		assert.Equal(t, "done", launched[0].State)
	}
	assert.Equal(t, 1, d.fakes().Treehouse.PeakLeasing(d.Repo))
}

func TestLaunchesQueuedBehindOthersKeepTheirWholeDeadline(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	for _, name := range []string{"tree2", "tree3"} {
		extra := filepath.Join(filepath.Dir(d.Repo), name)
		hintGit(t, d.Repo, "worktree", "add", "-q", "--detach", extra)
		d.fakes().Treehouse.AddTree(d.Repo, extra)
	}
	release := d.fakes().Treehouse.GateLeases()
	d.Linear.Tickets(
		ticket("ENG-1", "In Progress", "dispatch"),
		ticket("ENG-2", "In Progress", "dispatch"),
		ticket("ENG-3", "In Progress", "dispatch"),
	)

	// GateLeases holds each launch's lease open until released and the fake clock moves only when
	// advanced, so each launch holds the repo for 60ms of its 100ms deadline. The third waits
	// 120ms in the queue, more than its whole deadline.
	ticked := later(func() Result { return d.RunWith(dispatch.Options{LaunchTimeout: 100 * time.Millisecond}, "tick") })
	// None of these waits fails the test when it runs out. A build that queues launches inside
	// their deadlines never reaches them, and the assertions below should show what it did.
	until := func(done func() bool) {
		for range 3000 {
			if done() {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}
	// The clock must not move before ENG-2 and ENG-3 are in the queue, or their time runs.
	until(func() bool { return d.DeadlineRunning("queue ENG-2") && d.DeadlineRunning("queue ENG-3") })
	for held := 1; held <= 3; held++ {
		until(func() bool { return len(d.fakes().Calls("treehouse", "lease")) == held })
		d.advance(60 * time.Millisecond)
		release()
		// A launch is over once it has typed its kickoff. The next advance must not land before.
		until(func() bool { return len(d.fakes().Multiplexer.Kicked()) == held })
	}
	res := ticked()

	require.NoError(t, res.Err)
	assert.Len(t, d.fakes().Multiplexer.Started(), 3)
}

func TestALaunchHoldingTheRepoThatHangsFailsTheOnesQueuedBehindIt(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	extra := filepath.Join(filepath.Dir(d.Repo), "tree2")
	hintGit(t, d.Repo, "worktree", "add", "-q", "--detach", extra)
	d.fakes().Treehouse.AddTree(d.Repo, extra)
	release := d.fakes().Treehouse.GateLeases()
	d.Linear.Tickets(
		ticket("ENG-1", "In Progress", "dispatch"),
		ticket("ENG-2", "In Progress", "dispatch"),
	)

	// Whichever launch queues first holds the repo and never gets its lease. The other waits in
	// the queue for four launch deadlines, then fails without using any of its own.
	ticked := later(func() Result { return d.RunWith(dispatch.Options{LaunchTimeout: 100 * time.Millisecond}, "tick") })
	require.Eventually(t, func() bool { return len(d.fakes().Calls("treehouse", "lease")) == 1 }, 20*time.Second, time.Millisecond)
	var queued string
	require.Eventually(t, func() bool {
		for _, id := range []string{"ENG-1", "ENG-2"} {
			if d.DeadlineRunning("queue " + id) {
				queued = id
			}
		}
		return queued != ""
	}, 20*time.Second, time.Millisecond)
	assert.Equal(t, 400*time.Millisecond, d.Deadline("queue "+queued))
	d.Expire("queue " + queued)
	release()
	res := ticked()

	require.ErrorContains(t, res.Err, queued+" launch failed: pool_error: gave up waiting for another launch into")
	assert.Len(t, d.fakes().Multiplexer.Started(), 1)
}

func TestALaunchThatHangsAfterQueueingStillTimesOut(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.fakes().Treehouse.GateLeases()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	// The lease never returns, so only the launch deadline can end it.
	ticked := later(func() Result { return d.RunWith(dispatch.Options{LaunchTimeout: 100 * time.Millisecond}, "tick") })
	require.Eventually(t, func() bool { return len(d.fakes().Calls("treehouse", "lease")) == 1 }, 20*time.Second, time.Millisecond)
	assert.Equal(t, 100*time.Millisecond, d.Deadline("launch ENG-1"))
	d.Expire("launch ENG-1")
	res := ticked()

	require.ErrorContains(t, res.Err, "ENG-1 launch failed")
	assert.Empty(t, d.fakes().Multiplexer.Started())
}

func TestLaunchesIntoDifferentReposLeaseTogether(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	other := filepath.Join(filepath.Dir(d.Repo), "other")
	require.NoError(t, os.MkdirAll(other, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(other, "treehouse.toml"), []byte("max_trees = 4\n"), 0o644))
	hintGit(t, other, "init", "-q", "-b", "main")
	hintGit(t, other, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	otherTree := filepath.Join(filepath.Dir(d.Repo), "other-tree")
	hintGit(t, other, "worktree", "add", "-q", "--detach", otherTree)
	d.fakes().Treehouse.AddTree(other, otherTree)
	d.fakes().Treehouse.AwaitLeases(2)
	d.addRepo("other", other)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "other"))

	d.Tick()

	for _, id := range []string{"ENG-1", "ENG-2"} {
		launched := d.receiptsOfKind(id, "launch")
		require.Len(t, launched, 1)
		assert.Equal(t, "done", launched[0].State)
	}

	assert.Equal(t, 2, d.fakes().Treehouse.PeakLeasingAnywhere())
}

// launchRule launches every In Progress ticket that has no worker, for a test whose own rules
// stand in for the shipped ones and which still needs a worker launched.
const launchRule = `
whenever({ name: "launch", each: "ticket",
  if: (t, ctx) => t.status === "in_progress" && ctx.worker.of(t).state === "none",
  then: (t, ctx) => ctx.worker.queueLaunch(t, { once: ["launch", t.id] }) });`

// launchOutput is how one launch action ended, read from its receipt.
type launchOutput struct {
	// result holds "status" (the receipt detail's first word, such as ok or lease_failed),
	// "detail" (the rest), for a started worker "workspace", and for a running one "worktree",
	// the tree its worker row names.
	result map[string]string
	// err is set unless a worker is running when the launch ends.
	err error
}

// launch has the running dispatch launch the ticket in the dispatch repo.
func (d *Dispatch) launch(ticket, branch string, flags ...string) launchOutput {
	d.t.Helper()
	return d.launchIn(d.Repo, append([]string{ticket, branch}, flags...)...)
}

// launchIn puts the ticket In Progress on the branch, in the repo whose clone is dir, and ticks, so
// the shipped launch rule queues a launch action. It returns how that launch ended. The one flag
// is --title, the ticket's title, which is empty without it.
func (d *Dispatch) launchIn(dir string, args ...string) launchOutput {
	d.t.Helper()
	require.GreaterOrEqual(d.t, len(args), 2)
	issue := ticket(args[0], "In Progress", d.labelOf(dir))
	issue.Branch, issue.Title = args[1], ""
	switch flags := args[2:]; {
	case len(flags) == 2 && flags[0] == "--title":
		issue.Title = flags[1]
	default:
		require.Empty(d.t, flags)
	}
	before := len(d.launchesEnded(issue.Identifier))
	others := slices.DeleteFunc(d.tools.Linear.Issues(), func(held fakeTicket) bool { return held.Identifier == issue.Identifier })
	d.Linear.Tickets(append(others, issue)...)

	d.Run("tick")

	ended := d.launchesEnded(issue.Identifier)
	out := launchOutput{result: map[string]string{}}
	if len(ended) == before {
		out.err = errors.New("no launch ran")
		return out
	}
	last := ended[len(ended)-1]
	status, detail, _ := strings.Cut(last.Detail, ": ")
	out.result["status"], out.result["detail"] = status, detail
	if workspace, ok := strings.CutPrefix(detail, "workspace "); ok && status == "ok" {
		out.result["workspace"], _, _ = strings.Cut(workspace, ",")
	}
	if last.State != "done" {
		out.err = errors.New("launch " + status)
		return out
	}
	out.result["worktree"] = d.recordedTree(issue.Identifier)
	return out
}

// launchesEnded is every launch of the ticket that has ended, oldest first. Receipts folds the
// tries of one once key into one, and a launch that failed is tried again under the same key.
func (d *Dispatch) launchesEnded(ticket string) []Receipt {
	d.t.Helper()
	res := d.Run("explain", ticket, "--json")
	if res.Err != nil {
		return nil
	}
	var ended []Receipt
	for _, text := range strings.Split(res.Stdout, "\n") {
		var line struct{ Type, Kind, State, Detail string }
		if text == "" || json.Unmarshal([]byte(text), &line) != nil {
			continue
		}
		if line.Type == "receipt" && line.Kind == "launch" && line.State != "attempted" {
			ended = append(ended, Receipt{Kind: line.Kind, State: line.State, Detail: line.Detail})
		}
	}
	return ended
}

// fakes is the herdr and treehouse fakes a launch scenario seeds and reads back.
func (d *Dispatch) fakes() *tooltest.Tools { return d.tools }

// Pooled makes the dispatch repo a git checkout that treehouse pools, holding one free tree, and
// returns the tree's path.
func (t *Trees) Pooled() string { return t.d.pool() }

// callIndex is the position of the first call to the tool's operation in the order the fakes saw
// them, or -1 when there was none. Order across herdr and treehouse shows only in the fakes' shared
// journal, which sits outside dispatch.
func (d *Dispatch) callIndex(tool, name string) int {
	for i, call := range d.fakes().Journal.Calls() {
		if call.Tool == tool && call.Name == name {
			return i
		}
	}
	return -1
}

// receiptsOfKind is the ticket's receipts of one kind.
func (d *Dispatch) receiptsOfKind(ticket, kind string) []Receipt {
	var found []Receipt
	for _, receipt := range d.Receipts(ticket) {
		if receipt.Kind == kind {
			found = append(found, receipt)
		}
	}
	return found
}

func revParse(t *testing.T, dir, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", ref).Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

func branchIn(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "branch", "--show-current").Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

func (d *Dispatch) calls(tool, name string) int {
	var n int
	for _, call := range d.fakes().Journal.Calls() {
		if call.Tool == tool && call.Name == name {
			n++
		}
	}
	return n
}

func TestLaunchLeasesOnceAndTheNextLaunchSucceedsWhenTreehouseFailsOnce(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	d.fakes().Treehouse.FailLeasesOnce(errors.New("exit status 1"))

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "lease_failed", out.result["status"])
	assert.Contains(t, out.result["detail"], "treehouse lease failed")
	assert.Equal(t, 1, d.calls("treehouse", "lease"))

	out = d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, tree, out.result["worktree"])
	assert.Equal(t, 2, d.calls("treehouse", "lease"))
}

func TestLaunchSwitchesOnceAndTheNextLaunchSucceedsWhenGitRefusesTheBranchOnce(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	// Stands in for another process holding the ref lock: git aborts the first update of the branch.
	marker := filepath.Join(d.dir, "refuse-once")
	require.NoError(t, os.WriteFile(marker, nil, 0o644))
	hook := "#!/bin/sh\n[ \"$1\" = prepared ] && grep -q 'refs/heads/thrawn01/eng-1$' && [ -f " + marker + " ] && rm " + marker + " && exit 1\nexit 0\n"
	require.NoError(t, os.WriteFile(filepath.Join(d.Repo, ".git", "hooks", "reference-transaction"), []byte(hook), 0o755))

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "switch_failed", out.result["status"])
	assert.Equal(t, []string{d.Repo + "|" + tree + "|ENG-1"}, d.Trees.Returned())

	out = d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, tree, out.result["worktree"])
	assert.Equal(t, "thrawn01/eng-1", strings.TrimSpace(revParseName(t, tree)))
}

func TestLaunchReturnsATreeWithoutTheIndexLockACutOffSwitchLeft(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	// Stands in for the launch deadline killing git: the switch dies with its index.lock still in the tree's git directory.
	hook := "#!/bin/sh\n[ \"$1\" = prepared ] && grep -q 'refs/heads/thrawn01/eng-1$' && touch \"$(git rev-parse --absolute-git-dir)/index.lock\" && exit 1\nexit 0\n"
	require.NoError(t, os.WriteFile(filepath.Join(d.Repo, ".git", "hooks", "reference-transaction"), []byte(hook), 0o755))

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "switch_failed", out.result["status"])
	assert.Equal(t, []string{d.Repo + "|" + tree + "|ENG-1"}, d.Trees.Returned())
	gitDir, err := exec.Command("git", "-C", tree, "rev-parse", "--absolute-git-dir").Output()
	require.NoError(t, err)
	assert.NoFileExists(t, filepath.Join(strings.TrimSpace(string(gitDir)), "index.lock"))
}

func revParseName(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	require.NoError(t, err)
	return string(out)
}

func TestLaunchReturnsATreeTreehouseLeasedBeforeItFailedAndTheNextLaunchChecksOutTheBranch(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	d.fakes().Treehouse.FailLeasesAfterTaking(errors.New("exit status 1"))

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "lease_failed", out.result["status"])
	assert.Equal(t, []string{d.Repo + "|" + tree + "|ENG-1"}, d.Trees.Returned())

	out = d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, tree, out.result["worktree"])
	assert.Equal(t, "thrawn01/eng-1", strings.TrimSpace(revParseName(t, tree)))
}

func TestLaunchStartsClaudeOnASessionIdDispatchChose(t *testing.T) {
	t.Parallel()
	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	var ids []string
	for range 2 {
		d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
		d.Trees.Pooled()

		assert.Equal(t, "ok", d.launch("ENG-1", "thrawn01/eng-1").result["status"])

		started := d.fakes().Multiplexer.Started()
		require.Len(t, started, 1)
		assert.Equal(t, "--session-id", started[0].Program.SessionFlag)
		assert.Regexp(t, uuid, started[0].Program.Session)
		assert.NotContains(t, started[0].Program.Argv, "--session-id")
		snap, err := d.fakes().Multiplexer.Read(context.Background())
		require.NoError(t, err)
		require.Len(t, snap.Agents, 1)
		assert.Equal(t, started[0].Program.Session, snap.Agents[0].Session)
		ids = append(ids, started[0].Program.Session)
	}
	assert.NotEqual(t, ids[0], ids[1])
}

func TestRecordStopFromAnotherClaudeSessionInAWorkersTreeDoesNotCountAsTheWorkers(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		other bool
		want  string
		turns []string
	}{
		{"WorkersOwnSession", false, "💤", []string{"ENG-1: turns unknown", "ENG-1: turns  of 0", "ENG-1: turns idle of 1"}},
		{"StrangerSession", true, "🟢", []string{"ENG-1: turns unknown", "ENG-1: turns  of 0"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: rule(turnsRule + launchRule)})
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
			tree := d.Trees.Pooled()
			out := d.launch("ENG-1", "thrawn01/eng-1")
			require.Equal(t, "ok", out.result["status"])
			session := d.fakes().Multiplexer.Started()[0].Program.Session
			pane, _, _ := strings.Cut(d.fakes().Multiplexer.Kicked()[0], "|")
			d.fakes().Multiplexer.SetStatus(pane, "idle")
			d.StartBeforeTheEvents()
			defer d.FromPane(pane)()
			if test.other {
				session = "a-stranger"
			}

			input := fmt.Sprintf(`{"session_id":%q,"cwd":%q,"background_tasks":[]}`, session, tree)
			require.NoError(t, d.Hook("record-stop", input).Err)

			d.Tick()
			renames := d.Workers.Renames()
			assert.Equal(t, "w2|"+test.want+" ENG-1", renames[len(renames)-1])
			assert.Equal(t, test.turns, d.Linear.Comments())
		})
	}
}

func TestTickPrintsEveryFailedLeaseAndNeverEscalatesTheTicket(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Trees.Pooled()
	d.fakes().Treehouse.FailLease(errors.New("exit status 1: no worktree"))

	for range 3 {
		res := d.Run("tick")

		require.ErrorContains(t, res.Err, "ENG-1 launch failed: lease_failed: treehouse lease failed")
		assert.Contains(t, res.Stdout, "no worktree")
		// Two failures at the same millisecond are one receipt, so the clock moves between ticks.
		d.Advance(time.Second)
	}

	assert.Empty(t, d.Linear.Changes())
	assert.Equal(t, 3, d.calls("treehouse", "lease"))
}

func TestLaunchReturnsATreeTreehouseLeasedBeforeReportingNoPathAndTheNextLaunchChecksOutTheBranch(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	d.fakes().Treehouse.FailLeasesAfterTaking(fmt.Errorf("%w: {}", treehouse.ErrNoPath))

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "pool_error", out.result["status"])
	assert.Equal(t, []string{d.Repo + "|" + tree + "|ENG-1"}, d.Trees.Returned())

	out = d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, "thrawn01/eng-1", strings.TrimSpace(revParseName(t, tree)))
}
