package dispatch_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.True(t, strings.HasPrefix(out.raw, `{"status":"ok"`))
	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, "ENG-1", out.result["ticket"])
	assert.Equal(t, "thrawn01/eng-1", out.result["branch"])
	assert.Equal(t, tree, out.result["worktree"])
	assert.Equal(t, "w2", out.result["workspace"])
	assert.Equal(t, "w2:p2", out.result["pane"])
	assert.Equal(t, "eng-1", out.result["agent"])
	assert.Equal(t, "/dispatch:build ENG-1", out.result["kickoff"])
	assert.Equal(t, []string{"w2:p2|/dispatch:build ENG-1"}, d.fakes().Herdr.Kicked())
	assert.Equal(t, "thrawn01/eng-1", branchIn(t, tree))
	require.Len(t, d.fakes().Herdr.Started(), 1)
	started := d.fakes().Herdr.Started()[0]
	assert.Equal(t, herdr.Worker{Workspace: "w2", Dir: tree, Label: "ENG-1", Name: "eng-1", PluginRoot: "/plugin", Program: started.Program}, started)
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
	require.Len(t, d.fakes().Herdr.Started(), 1)
	assert.Equal(t, "ENG-1 Fix the thing", d.fakes().Herdr.Started()[0].Label)
}

func TestLaunchTypesTheKickoffItIsGiven(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()

	out := d.launch("ENG-1", "thrawn01/eng-1", "--model", "opus", "--kickoff", "/dispatch:design ENG-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, "/dispatch:design ENG-1", out.result["kickoff"])
	assert.Equal(t, []string{"w2:p2|/dispatch:design ENG-1"}, d.fakes().Herdr.Kicked())
	require.Len(t, d.fakes().Herdr.Started(), 1)
	assert.Equal(t, "opus", modelOf(d.fakes().Herdr.Started()[0].Program))
}

func TestLaunchStartsNewBranchesFromTheBase(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	hintGit(t, d.Repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "second")
	hintGit(t, d.Repo, "branch", "release", "HEAD~1")

	out := d.launch("ENG-1", "thrawn01/eng-1", "--base", "release")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, revParse(t, d.Repo, "release"), revParse(t, tree, "HEAD"))
}

func TestLaunchReportsAWorkerThatIsAlreadyRunning(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	d.fakes().Treehouse.MoveLease(tree, "ENG-1")
	d.fakes().Herdr.AddWorkspace(herdr.Workspace{ID: "w3", CheckoutPath: tree})
	d.fakes().Herdr.AddAgent(herdr.Agent{Kind: "claude", Status: "working", PaneID: "w3:p2", TabID: "w3:t1", WorkspaceID: "w3"})

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "already_running", out.result["status"])
	assert.Equal(t, "w3", out.result["workspace"])
	assert.Equal(t, "w3:p2", out.result["pane"])
	assert.Empty(t, d.fakes().Herdr.Kicked())
	assert.Empty(t, d.fakes().Herdr.Started())
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
}

func TestLaunchReportsWhyItCouldNotStart(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		dir    string
		setup  func(d *Dispatch)
		args   []string
		status string
		detail string
		// expire is the deadline the clock passes while the launch runs.
		expire string
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
			dir:    t.TempDir(),
			args:   []string{"ENG-1", "b"},
			status: "not_a_repo",
			detail: "run from inside the target repo",
		},
		{
			name:   "NotPooled",
			setup:  func(d *Dispatch) { require.NoError(t, os.Remove(filepath.Join(d.Repo, "treehouse.toml"))) },
			args:   []string{"ENG-1", "b"},
			status: "not_pooled",
			detail: "has no treehouse.toml",
		},
		{
			name:   "HerdrUnavailable",
			setup:  func(d *Dispatch) { d.fakes().Herdr.Fail(herdrtest.OpRead, errors.New("server not running")) },
			args:   []string{"ENG-1", "b"},
			status: "herdr_unavailable",
			detail: "herdr server not answering",
			expire: "launch preflight",
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
			args:   []string{"ENG-1", "b"},
			status: "pool_exhausted",
			detail: "all 4/4 pool trees are taken: ENG-7 (leased), ENG-8 (leased), ENG-9 (leased), ? (leased)",
		},
		{
			name: "PoolErrorWhenATreeIsFree",
			setup: func(d *Dispatch) {
				d.fakes().Treehouse.FailLease(errors.New("no tree for you"))
			},
			args:   []string{"ENG-1", "b"},
			status: "pool_error",
			detail: "treehouse lease failed but the pool is not exhausted (1 of 1 available, max_trees=4; holders: ). treehouse stderr: no tree for you",
		},
		{
			// A pool that can still grow is not exhausted, so the ticket must
			// escalate instead of waiting for a slot.
			name: "PoolErrorWhenEveryTreeIsTakenBelowMaxTrees",
			setup: func(d *Dispatch) {
				d.fakes().Treehouse.Clear(d.Repo)
				d.fakes().Treehouse.Seed(d.Repo,
					treehouse.Tree{Path: "/t/1", Status: "leased", LeaseHolder: "ENG-7"},
					treehouse.Tree{Path: "/t/2", Status: "leased", LeaseHolder: "ENG-8"},
					treehouse.Tree{Path: "/t/3", Status: "leased", LeaseHolder: "ENG-9"},
				)
				d.fakes().Treehouse.FailLease(errors.New("cannot create a worktree"))
			},
			args:   []string{"ENG-1", "b"},
			status: "pool_error",
			detail: "the pool is not exhausted (0 of 3 available, max_trees=4",
		},
		{
			name: "LeaseWithoutAPath",
			setup: func(d *Dispatch) {
				d.fakes().Treehouse.FailLease(fmt.Errorf("%w: {}", treehouse.ErrNoPath))
			},
			args:   []string{"ENG-1", "b"},
			status: "pool_error",
			detail: "lease succeeded but treehouse JSON had no path",
		},
		{
			name: "PoolErrorWhenMaxTreesIsUnknown",
			setup: func(d *Dispatch) {
				require.NoError(t, os.WriteFile(filepath.Join(d.Repo, "treehouse.toml"), nil, 0o644))
				d.fakes().Treehouse.Clear(d.Repo)
				d.fakes().Treehouse.Seed(d.Repo, treehouse.Tree{Path: "/t/1", Status: "leased", LeaseHolder: "ENG-7"})
			},
			args:   []string{"ENG-1", "b"},
			status: "pool_error",
			detail: "the pool is not exhausted (0 of 1 available, max_trees=-1",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
			d.Trees.Pooled()
			if test.setup != nil {
				test.setup(d)
			}
			dir := d.Repo
			if test.dir != "" {
				dir = test.dir
			}

			launched := later(func() launchOutput { return d.launchIn(dir, test.args...) })
			if test.expire != "" {
				d.Expire(test.expire)
			}
			out := launched()

			assert.Equal(t, test.status, out.result["status"])
			assert.Contains(t, out.result["detail"], test.detail)
			assert.Error(t, out.err)
		})
	}
}

func TestLaunchNamesTheBaseWhenANewBranchWillNotCheckOut(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()

	out := d.launch("ENG-1", "thrawn01/eng-1", "--base", "nosuch")

	assert.Equal(t, "checkout_failed", out.result["status"])
	assert.Contains(t, out.result["detail"], "from nosuch")
}

func TestLaunchReturnsALeaseWhenTheBranchWillNotCheckOut(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	// A branch checked out in the main clone cannot be switched to in the pool tree.
	hintGit(t, d.Repo, "checkout", "-q", "-b", "thrawn01/eng-1")

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "checkout_failed", out.result["status"])
	assert.Contains(t, out.result["detail"], "(lease returned)")
	assert.Equal(t, []string{d.Repo + "|" + tree + "|ENG-1"}, d.Trees.Returned())
}

func TestLaunchReturnsALeaseItTookWhenTheWorkspaceFails(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	d.fakes().Herdr.Fail(herdrtest.OpOpenWorkspace, errors.New("herdr worktree open failed"))

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
	d.fakes().Herdr.Fail(herdrtest.OpOpenWorkspace, errors.New("herdr worktree open failed"))

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "workspace_failed", out.result["status"])
	assert.Empty(t, d.Trees.Returned())
}

func TestLaunchClosesTheWorkspaceBeforeReturningALeaseItTook(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	d.fakes().Herdr.Fail(herdrtest.OpStartTab, errors.New("herdr tab create failed in w2"))

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "workspace_failed", out.result["status"])
	assert.Equal(t, []string{"w2"}, d.fakes().Herdr.ClosedWorkspaces())
	assert.Equal(t, []string{d.Repo + "|" + tree + "|ENG-1"}, d.Trees.Returned())
	assert.Less(t, d.callIndex("herdr", "close-workspace"), d.callIndex("treehouse", "return"))
}

func TestLaunchKeepsALeaseWhoseWorkspaceWouldNotClose(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.fakes().Herdr.Fail(herdrtest.OpStartTab, errors.New("herdr tab create failed in w2"))
	// A tree returned with its workspace still open would match that
	// workspace by path, so the next ticket to lease it would never start.
	d.fakes().Herdr.Fail(herdrtest.OpCloseWorkspace, errors.New("exit status 1"))

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "workspace_failed", out.result["status"])
	assert.Empty(t, d.Trees.Returned())
}

func TestLaunchClosesTheTabsTheWorkspaceOpenedWith(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.fakes().Herdr.AddWorkspace(herdr.Workspace{ID: "w2", Label: "other"})
	d.fakes().Herdr.AddTab(herdr.Tab{ID: "w2:t1", Label: "other", WorkspaceID: "w2"})
	d.fakes().Herdr.AddPane(herdr.Pane{ID: "w2:p1", TabID: "w2:t1", WorkspaceID: "w2"})

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	snap, err := d.fakes().Herdr.Read(context.Background())
	require.NoError(t, err)
	assert.Contains(t, snap.Tabs, herdr.Tab{ID: "w2:t1", Label: "other", WorkspaceID: "w2"})
	assert.Contains(t, snap.Tabs, herdr.Tab{ID: out.result["workspace"] + ":t2", Label: "ENG-1", WorkspaceID: out.result["workspace"]})
	assert.NotContains(t, snap.Tabs, herdr.Tab{ID: out.result["workspace"] + ":t1", Label: "ENG-1", WorkspaceID: out.result["workspace"]})
}

func TestLaunchGivesBackTheLeaseWhenTheAgentWillNotStart(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	d.fakes().Herdr.Fail(herdrtest.OpStartAgent, errors.New("herdr agent start failed in pane w2:p2"))

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "agent_start_failed", out.result["status"])
	assert.Equal(t, "w2:p2", out.result["pane"])
	assert.Equal(t, []string{"w2"}, d.fakes().Herdr.ClosedWorkspaces())
	assert.Equal(t, []string{d.Repo + "|" + tree + "|ENG-1"}, d.Trees.Returned())
	assert.Empty(t, d.fakes().Herdr.Kicked())
}

func TestLaunchNamesARelaunchedWorkerApartFromItsPredecessor(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.fakes().Herdr.AddWorkspace(herdr.Workspace{ID: "w1", CheckoutPath: "/elsewhere"})
	d.fakes().Herdr.AddAgent(herdr.Agent{Kind: "claude", Status: "done", PaneID: "w1:p1", TabID: "w1:t1", WorkspaceID: "w1"})
	d.fakes().Herdr.NameAgent("w1:p1", "eng-1")

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Regexp(t, `^eng-1-r\d+$`, out.result["agent"])
}

func TestLaunchClosesTheWorkspaceWhenTheKickoffNeverRuns(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.fakes().Herdr.Fail(herdrtest.OpKickoff, herdr.ErrKickoffUnconfirmed)

	out := d.launch("ENG-1", "thrawn01/eng-1")

	// Left open, the idle Claude would look like a live worker and no tick
	// would retry the launch.
	assert.Equal(t, "injection_unconfirmed", out.result["status"])
	assert.Contains(t, out.result["detail"], "Claude started but herdr never showed the kickoff running")
	assert.Equal(t, []string{"w2"}, d.fakes().Herdr.ClosedWorkspaces())
	assert.Empty(t, d.Trees.Returned())
}

func TestLaunchReportsClaudeNeverStartedWhenItsCommandStaysAtTheShellPrompt(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.fakes().Herdr.Fail(herdrtest.OpKickoff, herdr.ErrNeverStarted)

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "injection_unconfirmed", out.result["status"])
	assert.Contains(t, out.result["detail"], "never started")
	assert.NotContains(t, out.result["detail"], "Claude started")
	assert.Equal(t, []string{"w2"}, d.fakes().Herdr.ClosedWorkspaces())
}

func TestLaunchReportsEnterNotSentWhenHerdrRefusesTheKey(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.fakes().Herdr.Fail(herdrtest.OpKickoff, herdr.ErrEnterNotSent)

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "injection_unconfirmed", out.result["status"])
	assert.Contains(t, out.result["detail"], "could not send Enter")
	assert.NotContains(t, out.result["detail"], "after Enter was pressed")
}

func TestLaunchExitsCleanlyOnlyWhenTheWorkerIsRunning(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	ok := d.launch("ENG-1", "thrawn01/eng-1")
	d.fakes().Treehouse.MoveLease(tree, "ENG-1")

	running := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", ok.result["status"])
	assert.NoError(t, ok.err)
	assert.Equal(t, "already_running", running.result["status"])
	assert.NoError(t, running.err)
}

func TestLaunchPutsStatusFirstInEveryResult(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	d.fakes().Herdr.Fail(herdrtest.OpOpenWorkspace, errors.New("herdr worktree open failed"))

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "workspace_failed", out.result["status"])
	assert.True(t, strings.HasPrefix(out.raw, `{"status":"workspace_failed"`))
	assert.Equal(t, tree, out.result["worktree"])
}

func TestLaunchAcceptsTheNoFocusFlag(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()

	out := d.launch("ENG-1", "thrawn01/eng-1", "--no-focus")

	assert.Equal(t, "ok", out.result["status"])
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

func TestLaunchOpensTheWorkspaceFromTheMainCheckout(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	require.NoError(t, os.WriteFile(filepath.Join(tree, "treehouse.toml"), []byte("max_trees = 4\n"), 0o644))

	out := d.launchIn(tree, "ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	resolved, err := filepath.EvalSymlinks(d.Repo)
	require.NoError(t, err)
	assert.Equal(t, []herdr.NewWorkspace{{Repo: resolved, Path: tree, Label: "ENG-1"}}, d.fakes().Herdr.Opened())
}

func TestLaunchReusesAWorkspaceThatHasNoClaudePane(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	d.fakes().Treehouse.MoveLease(tree, "ENG-1")
	d.fakes().Herdr.AddWorkspace(herdr.Workspace{ID: "w3", CheckoutPath: tree})
	d.fakes().Herdr.AddTab(herdr.Tab{ID: "w3:t1", WorkspaceID: "w3"})
	d.fakes().Herdr.AddPane(herdr.Pane{ID: "w3:p2", TabID: "w3:t1", WorkspaceID: "w3"})

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, "w3", out.result["workspace"])
	assert.Empty(t, d.fakes().Herdr.Opened())
	require.Len(t, d.fakes().Herdr.Started(), 1)
	assert.Equal(t, "w3", d.fakes().Herdr.Started()[0].Workspace)
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
	assert.Equal(t, []string{"w2:p2|/dispatch:build ENG-1"}, d.fakes().Herdr.Kicked())
	require.Len(t, d.fakes().Herdr.Started(), 1)
	started := d.fakes().Herdr.Started()[0]
	assert.Equal(t, "sonnet", modelOf(started.Program))
	assert.Equal(t, "ENG-1 ticket ENG-1", started.Label)
	assert.Equal(t, tree, d.fakes().Herdr.Opened()[0].Path)
}

func TestLaunchTakesFlagsBeforeAndBetweenTheTicketAndBranch(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--model", "opus", "ENG-1", "thrawn01/eng-1"},
		{"ENG-1", "--model", "opus", "thrawn01/eng-1"},
	} {
		d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
		d.Trees.Pooled()

		out := d.launchIn(d.Repo, args...)

		assert.Equal(t, "ok", out.result["status"])
		assert.Equal(t, "thrawn01/eng-1", out.result["branch"])
		require.Len(t, d.fakes().Herdr.Started(), 1)
		assert.Equal(t, "opus", modelOf(d.fakes().Herdr.Started()[0].Program))
	}
}

func TestLaunchGivesUpAtOnceWhenThePreflightTimeoutIsZero(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Launcher: builtinLauncher, Env: map[string]string{"DISPATCH_PLUGIN_ROOT": "/plugin", "DISPATCH_LAUNCH_PREFLIGHT_TIMEOUT": "0"}})
	d.Trees.Pooled()
	d.fakes().Herdr.Fail(herdrtest.OpRead, errors.New("server not running"))

	launched := later(func() launchOutput { return d.launch("ENG-1", "thrawn01/eng-1") })
	assert.Equal(t, time.Nanosecond, d.Deadline("launch preflight"))
	d.Expire("launch preflight")
	out := launched()

	assert.Equal(t, "herdr_unavailable", out.result["status"])
	assert.Empty(t, d.fakes().Calls("treehouse", "lease"))
}

func TestLaunchRefusesAnEmptyPluginRoot(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{
		Launcher: builtinLauncher,
		Env:      map[string]string{"DISPATCH_PLUGIN_ROOT": "", "DISPATCH_LAUNCH_PREFLIGHT_TIMEOUT": "1"},
		Config:   "[plugins.herdr]\nplugin_root = \"\"\n",
	})
	d.Trees.Pooled()

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "usage_error", out.result["status"])
	assert.Contains(t, out.result["detail"], "no plugin root")
	assert.Empty(t, d.fakes().Herdr.Started())
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

func TestLaunchWrapperNamesThePluginRootForTheGoLauncher(t *testing.T) {
	t.Parallel()
	bin := buildDispatch(t)
	wrapper, err := filepath.Abs("bin/dispatch-launch")
	require.NoError(t, err)
	home := t.TempDir()

	// The root check runs before any git, herdr or treehouse call, so a plain
	// directory shows whether the wrapper set the root without touching either tool.
	cmd := exec.Command(wrapper, "ENG-1", "thrawn01/eng-1")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "HOME="+home, "XDG_CONFIG_HOME="+home, "DISPATCH_PLUGIN_ROOT=")
	stdout, _ := cmd.Output()

	var result map[string]string
	require.NoError(t, json.Unmarshal(stdout, &result))
	assert.Equal(t, "not_a_repo", result["status"])
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

	var wg sync.WaitGroup
	results := make([]launchOutput, 2)
	for i, ticket := range []string{"ENG-1", "ENG-2"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = d.launch(ticket, "thrawn01/"+strings.ToLower(ticket))
		}()
	}
	wg.Wait()

	assert.Equal(t, "ok", results[0].result["status"])
	assert.Equal(t, "ok", results[1].result["status"])
	assert.Equal(t, 1, d.fakes().Treehouse.PeakLeasing(d.Repo))
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

	var wg sync.WaitGroup
	for i, dir := range []string{d.Repo, other} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out := d.launchIn(dir, fmt.Sprintf("ENG-%d", i+1), fmt.Sprintf("thrawn01/eng-%d", i+1))
			assert.NoError(t, out.err)
		}()
	}
	wg.Wait()

	assert.Equal(t, 2, d.fakes().Treehouse.PeakLeasingAnywhere())
}

type launchOutput struct {
	raw    string
	result map[string]string
	err    error
}

// launch runs `dispatch launch` from the dispatch repo.
func (d *Dispatch) launch(ticket, branch string, flags ...string) launchOutput {
	d.t.Helper()
	return d.launchIn(d.Repo, append([]string{ticket, branch}, flags...)...)
}

func (d *Dispatch) launchIn(dir string, args ...string) launchOutput {
	d.t.Helper()
	res := d.RunIn(dir, append([]string{"launch"}, args...)...)
	out := launchOutput{raw: res.Stdout, err: res.Err}
	require.NoError(d.t, json.Unmarshal([]byte(res.Stdout), &out.result))
	return out
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

func TestLaunchLeasesAgainWhenTreehouseFailsOnce(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	d.fakes().Treehouse.FailLeasesOnce(errors.New("exit status 1"))

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, tree, out.result["worktree"])
	assert.Equal(t, 2, d.calls("treehouse", "lease"))
}

func TestLaunchReadsThePoolAgainWhenTreehouseStatusFailsOnce(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.fakes().Treehouse.FailTrees(errors.New("exit status 1"))

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, 1, d.calls("treehouse", "lease"))
}

func TestLaunchReportsAPoolItCannotRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.fakes().Treehouse.FailTrees(errors.New("exit status 1 (db locked)"), errors.New("exit status 1 (db locked)"), errors.New("exit status 1 (db locked)"))

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "pool_error", out.result["status"])
	assert.Contains(t, out.result["detail"], "while running treehouse status in")
	assert.Contains(t, out.result["detail"], "db locked")
	assert.Equal(t, 0, d.calls("treehouse", "lease"))
}

func TestLaunchSwitchesAgainWhenGitRefusesTheBranchOnce(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	// Stands in for another process holding the ref lock: git aborts the first update of the branch.
	marker := filepath.Join(d.dir, "refuse-once")
	require.NoError(t, os.WriteFile(marker, nil, 0o644))
	hook := "#!/bin/sh\n[ \"$1\" = prepared ] && grep -q 'refs/heads/thrawn01/eng-1$' && [ -f " + marker + " ] && rm " + marker + " && exit 1\nexit 0\n"
	require.NoError(t, os.WriteFile(filepath.Join(d.Repo, ".git", "hooks", "reference-transaction"), []byte(hook), 0o755))

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, tree, out.result["worktree"])
	assert.Empty(t, d.Trees.Returned())
	assert.Equal(t, "thrawn01/eng-1", strings.TrimSpace(revParseName(t, tree)))
}

func TestLaunchCarriesGitsMessageWhenTheCheckoutFails(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()

	out := d.launch("ENG-1", "thrawn01/eng-1", "--base", "nosuch")

	assert.Equal(t, "checkout_failed", out.result["status"])
	assert.Contains(t, out.result["detail"], "(lease returned): fatal: invalid reference: origin/nosuch")
	assert.Contains(t, out.result["detail"], "invalid reference: nosuch")
}

func revParseName(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	require.NoError(t, err)
	return string(out)
}

func TestLaunchChecksOutTheBranchInATreeTreehouseLeasedBeforeItFailed(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	d.fakes().Treehouse.FailLeasesAfterTaking(errors.New("exit status 1"))

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, tree, out.result["worktree"])
	assert.Equal(t, "thrawn01/eng-1", strings.TrimSpace(revParseName(t, tree)))
}

func TestLaunchReturnsATreeTreehouseLeasedBeforeItFailedWhenTheWorkspaceFails(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	tree := d.Trees.Pooled()
	d.fakes().Treehouse.FailLeasesAfterTaking(errors.New("exit status 1"))
	d.fakes().Herdr.Fail(herdrtest.OpOpenWorkspace, errors.New("herdr worktree open failed"))

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "workspace_failed", out.result["status"])
	assert.Equal(t, []string{d.Repo + "|" + tree + "|ENG-1"}, d.Trees.Returned())
}
