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
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
	"github.com/kapetan-io/dispatch/internal/journal"
	"github.com/kapetan-io/dispatch/internal/ledger"
	"github.com/kapetan-io/dispatch/internal/tooltest"
	"github.com/kapetan-io/dispatch/internal/treehouse"
)

func TestLaunchStartsAWorkerTypedTheBuildKickoff(t *testing.T) {
	l := newLaunchHarness(t)

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.True(t, strings.HasPrefix(out.raw, `{"status":"ok"`))
	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, "ENG-1", out.result["ticket"])
	assert.Equal(t, "thrawn01/eng-1", out.result["branch"])
	assert.Equal(t, l.tree, out.result["worktree"])
	assert.Equal(t, "w2", out.result["workspace"])
	assert.Equal(t, "w2:p2", out.result["pane"])
	assert.Equal(t, "eng-1", out.result["agent"])
	assert.Equal(t, "/dispatch:build ENG-1", out.result["kickoff"])
	assert.Equal(t, []string{"w2:p2|/dispatch:build ENG-1"}, l.tools.Herdr.Kicked())
	assert.Equal(t, l.branchIn(l.tree), "thrawn01/eng-1")
	require.Len(t, l.tools.Herdr.Started(), 1)
	started := l.tools.Herdr.Started()[0]
	assert.Equal(t, herdr.Worker{Workspace: "w2", Dir: l.tree, Label: "ENG-1", Name: "eng-1", Model: "sonnet", PluginRoot: "/plugin"}, started)
}

func TestLaunchLabelsTheTabWithTheTicketAndTitle(t *testing.T) {
	l := newLaunchHarness(t)

	out := l.launch("ENG-1", "thrawn01/eng-1", "--title", "Fix the thing")

	assert.Equal(t, "ok", out.result["status"])
	require.Len(t, l.tools.Herdr.Started(), 1)
	assert.Equal(t, "ENG-1 Fix the thing", l.tools.Herdr.Started()[0].Label)
}

func TestLaunchLabelsTheTabWithTheTicketWhenGivenNoTitle(t *testing.T) {
	l := newLaunchHarness(t)

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	require.Len(t, l.tools.Herdr.Started(), 1)
	assert.Equal(t, "ENG-1", l.tools.Herdr.Started()[0].Label)
}

func TestLaunchTypesTheKickoffItIsGiven(t *testing.T) {
	l := newLaunchHarness(t)

	out := l.launch("ENG-1", "thrawn01/eng-1", "--model", "opus", "--kickoff", "/dispatch:design ENG-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, "/dispatch:design ENG-1", out.result["kickoff"])
	assert.Equal(t, []string{"w2:p2|/dispatch:design ENG-1"}, l.tools.Herdr.Kicked())
	require.Len(t, l.tools.Herdr.Started(), 1)
	assert.Equal(t, "opus", l.tools.Herdr.Started()[0].Model)
}

func TestLaunchStartsNewBranchesFromTheBase(t *testing.T) {
	l := newLaunchHarness(t)
	l.git(l.repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "second")
	l.git(l.repo, "branch", "release", "HEAD~1")

	out := l.launch("ENG-1", "thrawn01/eng-1", "--base", "release")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, l.revParse(l.repo, "release"), l.revParse(l.tree, "HEAD"))
}

func TestLaunchReportsAWorkerThatIsAlreadyRunning(t *testing.T) {
	l := newLaunchHarness(t)
	l.tools.Treehouse.MoveLease(l.tree, "ENG-1")
	l.tools.Herdr.AddWorkspace(herdr.Workspace{ID: "w3", CheckoutPath: l.tree})
	l.tools.Herdr.AddAgent(herdr.Agent{Kind: "claude", Status: "working", PaneID: "w3:p2", TabID: "w3:t1", WorkspaceID: "w3"})

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "already_running", out.result["status"])
	assert.Equal(t, "w3", out.result["workspace"])
	assert.Equal(t, "w3:p2", out.result["pane"])
	assert.Empty(t, l.tools.Herdr.Kicked())
	assert.Empty(t, l.tools.Herdr.Started())
	assert.Empty(t, l.tools.Calls("treehouse", "lease"))
}

func TestLaunchReusesTheLeaseOfAnEarlierRun(t *testing.T) {
	l := newLaunchHarness(t)
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
			setup:  func(l *launchHarness) { l.tools.Herdr.Fail(herdrtest.OpRead, errors.New("server not running")) },
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

func TestLaunchNamesTheBaseWhenANewBranchWillNotCheckOut(t *testing.T) {
	l := newLaunchHarness(t)

	out := l.launch("ENG-1", "thrawn01/eng-1", "--base", "nosuch")

	assert.Equal(t, "checkout_failed", out.result["status"])
	assert.Contains(t, out.result["detail"], "from nosuch")
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
	l.tools.Herdr.Fail(herdrtest.OpOpenWorkspace, errors.New("herdr worktree open failed"))

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "workspace_failed", out.result["status"])
	assert.Contains(t, out.result["detail"], "herdr worktree open failed")
	assert.Equal(t, []string{l.repo + "|" + l.tree + "|ENG-1"}, l.returns())
}

func TestLaunchKeepsALeaseFromAnEarlierRunWhenTheWorkspaceFails(t *testing.T) {
	l := newLaunchHarness(t)
	// The tree is already leased to ENG-1, so it may hold a worker's
	// uncommitted work that a forced return would discard.
	l.tools.Treehouse.MoveLease(l.tree, "ENG-1")
	l.tools.Herdr.Fail(herdrtest.OpOpenWorkspace, errors.New("herdr worktree open failed"))

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "workspace_failed", out.result["status"])
	assert.Empty(t, l.returns())
}

func TestLaunchClosesTheWorkspaceBeforeReturningALeaseItTook(t *testing.T) {
	l := newLaunchHarness(t)
	l.tools.Herdr.Fail(herdrtest.OpStartTab, errors.New("herdr tab create failed in w2"))

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "workspace_failed", out.result["status"])
	assert.Equal(t, []string{"w2"}, l.tools.Herdr.ClosedWorkspaces())
	assert.Equal(t, []string{l.repo + "|" + l.tree + "|ENG-1"}, l.returns())
	assert.Less(t, l.callIndex("herdr", "close-workspace"), l.callIndex("treehouse", "return"))
}

func TestLaunchKeepsALeaseWhoseWorkspaceWouldNotClose(t *testing.T) {
	l := newLaunchHarness(t)
	l.tools.Herdr.Fail(herdrtest.OpStartTab, errors.New("herdr tab create failed in w2"))
	// A tree returned with its workspace still open would match that
	// workspace by path, so the next ticket to lease it would never start.
	l.tools.Herdr.Fail(herdrtest.OpCloseWorkspace, errors.New("exit status 1"))

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "workspace_failed", out.result["status"])
	assert.Empty(t, l.returns())
}

func TestLaunchClosesTheTabsTheWorkspaceOpenedWith(t *testing.T) {
	l := newLaunchHarness(t)
	l.tools.Herdr.AddWorkspace(herdr.Workspace{ID: "w2", Label: "other"})
	l.tools.Herdr.AddTab(herdr.Tab{ID: "w2:t1", Label: "other", WorkspaceID: "w2"})
	l.tools.Herdr.AddPane(herdr.Pane{ID: "w2:p1", TabID: "w2:t1", WorkspaceID: "w2"})

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	snap, err := l.tools.Herdr.Read(context.Background())
	require.NoError(t, err)
	assert.Contains(t, snap.Tabs, herdr.Tab{ID: "w2:t1", Label: "other", WorkspaceID: "w2"})
	assert.Contains(t, snap.Tabs, herdr.Tab{ID: out.result["workspace"] + ":t2", Label: "ENG-1", WorkspaceID: out.result["workspace"]})
	assert.NotContains(t, snap.Tabs, herdr.Tab{ID: out.result["workspace"] + ":t1", Label: "ENG-1", WorkspaceID: out.result["workspace"]})
}

func TestLaunchGivesBackTheLeaseWhenTheAgentWillNotStart(t *testing.T) {
	l := newLaunchHarness(t)
	l.tools.Herdr.Fail(herdrtest.OpStartAgent, errors.New("herdr agent start failed in pane w2:p2"))

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "agent_start_failed", out.result["status"])
	assert.Equal(t, "w2:p2", out.result["pane"])
	assert.Equal(t, []string{"w2"}, l.tools.Herdr.ClosedWorkspaces())
	assert.Equal(t, []string{l.repo + "|" + l.tree + "|ENG-1"}, l.returns())
	assert.Empty(t, l.tools.Herdr.Kicked())
}

func TestLaunchNamesARelaunchedWorkerApartFromItsPredecessor(t *testing.T) {
	l := newLaunchHarness(t)
	l.tools.Herdr.AddWorkspace(herdr.Workspace{ID: "w1", CheckoutPath: "/elsewhere"})
	l.tools.Herdr.AddAgent(herdr.Agent{Kind: "claude", Status: "done", PaneID: "w1:p1", TabID: "w1:t1", WorkspaceID: "w1"})
	l.tools.Herdr.NameAgent("w1:p1", "eng-1")

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Regexp(t, `^eng-1-r\d+$`, out.result["agent"])
}

func TestLaunchClosesTheWorkspaceWhenTheKickoffNeverRuns(t *testing.T) {
	l := newLaunchHarness(t)
	l.tools.Herdr.Fail(herdrtest.OpKickoff, herdr.ErrKickoffUnconfirmed)

	out := l.launch("ENG-1", "thrawn01/eng-1")

	// Left open, the idle Claude would look like a live worker and no tick
	// would retry the launch.
	assert.Equal(t, "injection_unconfirmed", out.result["status"])
	assert.Contains(t, out.result["detail"], "Claude started but herdr never showed the kickoff running")
	assert.Equal(t, []string{"w2"}, l.tools.Herdr.ClosedWorkspaces())
	assert.Empty(t, l.returns())
}

func TestLaunchReportsClaudeNeverStartedWhenItsCommandStaysAtTheShellPrompt(t *testing.T) {
	l := newLaunchHarness(t)
	l.tools.Herdr.Fail(herdrtest.OpKickoff, herdr.ErrClaudeNeverStarted)

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "injection_unconfirmed", out.result["status"])
	assert.Contains(t, out.result["detail"], "never started")
	assert.NotContains(t, out.result["detail"], "Claude started")
	assert.Equal(t, []string{"w2"}, l.tools.Herdr.ClosedWorkspaces())
}

func TestLaunchReportsEnterNotSentWhenHerdrRefusesTheKey(t *testing.T) {
	l := newLaunchHarness(t)
	l.tools.Herdr.Fail(herdrtest.OpKickoff, herdr.ErrEnterNotSent)

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "injection_unconfirmed", out.result["status"])
	assert.Contains(t, out.result["detail"], "could not send Enter")
	assert.NotContains(t, out.result["detail"], "after Enter was pressed")
}

func TestLaunchExitsCleanlyOnlyWhenTheWorkerIsRunning(t *testing.T) {
	l := newLaunchHarness(t)
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
	l.tools.Herdr.Fail(herdrtest.OpOpenWorkspace, errors.New("herdr worktree open failed"))

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "workspace_failed", out.result["status"])
	assert.True(t, strings.HasPrefix(out.raw, `{"status":"workspace_failed"`))
	assert.Equal(t, l.tree, out.result["worktree"])
}

func TestLaunchAcceptsTheNoFocusFlag(t *testing.T) {
	l := newLaunchHarness(t)

	out := l.launch("ENG-1", "thrawn01/eng-1", "--no-focus")

	assert.Equal(t, "ok", out.result["status"])
}

func TestLaunchChecksOutAnExistingLocalBranch(t *testing.T) {
	l := newLaunchHarness(t)
	l.git(l.repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "second")
	l.git(l.repo, "branch", "thrawn01/eng-1", "HEAD~1")

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, "thrawn01/eng-1", l.branchIn(l.tree))
	assert.Equal(t, l.revParse(l.repo, "thrawn01/eng-1"), l.revParse(l.tree, "HEAD"))
}

func TestLaunchChecksOutABranchOnlyTheRemoteHas(t *testing.T) {
	l := newLaunchHarness(t)
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

func TestLaunchStartsNewBranchesFromTheCheckedOutBranchWhenThereIsNoRemote(t *testing.T) {
	l := newLaunchHarness(t)
	l.git(l.repo, "branch", "-m", "master")

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, l.revParse(l.repo, "master"), l.revParse(l.tree, "HEAD"))
}

func TestLaunchStartsNewBranchesFromTheRemoteDefaultBranch(t *testing.T) {
	l := newLaunchHarness(t)
	remote := filepath.Join(l.dir, "remote.git")
	l.git(l.dir, "init", "-q", "--bare", "-b", "master", remote)
	l.git(l.repo, "branch", "-m", "master")
	l.git(l.repo, "remote", "add", "origin", remote)
	l.git(l.repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "remote master")
	l.git(l.repo, "push", "-q", "origin", "master")
	l.git(l.repo, "remote", "set-head", "origin", "master")
	l.git(l.repo, "reset", "-q", "--hard", "HEAD~1")
	l.git(l.repo, "switch", "-q", "-c", "scratch")

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, l.revParse(remote, "refs/heads/master"), l.revParse(l.tree, "HEAD"))
}

func TestLaunchFollowsARemoteThatRenamedItsDefaultBranch(t *testing.T) {
	l := newLaunchHarness(t)
	remote := filepath.Join(l.dir, "remote.git")
	l.git(l.dir, "init", "-q", "--bare", "-b", "master", remote)
	l.git(l.repo, "branch", "-m", "master")
	l.git(l.repo, "remote", "add", "origin", remote)
	l.git(l.repo, "push", "-q", "origin", "master")
	l.git(l.repo, "remote", "set-head", "origin", "master")
	// origin renames master to main and moves ahead, leaving origin/HEAD stale.
	l.git(remote, "branch", "-m", "master", "main")
	l.git(l.repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "ahead")
	l.git(l.repo, "push", "-q", "origin", "master:main")
	l.git(l.repo, "reset", "-q", "--hard", "HEAD~1")

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, l.revParse(remote, "refs/heads/main"), l.revParse(l.tree, "HEAD"))
}

func TestLaunchSkipsAnUnpushedCheckedOutBranchWhenChoosingTheBase(t *testing.T) {
	l := newLaunchHarness(t)
	remote := filepath.Join(l.dir, "remote.git")
	l.git(l.dir, "init", "-q", "--bare", remote)
	l.git(l.repo, "remote", "add", "origin", remote)
	l.git(l.repo, "push", "-q", "origin", "main")
	l.git(l.repo, "fetch", "-q", "origin")
	l.git(l.repo, "config", "init.defaultBranch", "main")
	l.git(l.repo, "switch", "-q", "-c", "thrawn01/eng-9")
	l.git(l.repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "unpushed work")

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, l.revParse(remote, "refs/heads/main"), l.revParse(l.tree, "HEAD"))
}

func TestLaunchOpensTheWorkspaceFromTheMainCheckout(t *testing.T) {
	l := newLaunchHarness(t)
	l.writeFile(filepath.Join("tree", "treehouse.toml"), "max_trees = 4\n")
	l.dirOverride = l.tree

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	resolved, err := filepath.EvalSymlinks(l.repo)
	require.NoError(t, err)
	assert.Equal(t, []herdr.NewWorkspace{{Repo: resolved, Path: l.tree, Label: "ENG-1"}}, l.tools.Herdr.Opened())
}

func TestLaunchReusesAWorkspaceThatHasNoClaudePane(t *testing.T) {
	l := newLaunchHarness(t)
	l.tools.Treehouse.MoveLease(l.tree, "ENG-1")
	l.tools.Herdr.AddWorkspace(herdr.Workspace{ID: "w3", CheckoutPath: l.tree})
	l.tools.Herdr.AddTab(herdr.Tab{ID: "w3:t1", WorkspaceID: "w3"})
	l.tools.Herdr.AddPane(herdr.Pane{ID: "w3:p2", TabID: "w3:t1", WorkspaceID: "w3"})

	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "ok", out.result["status"])
	assert.Equal(t, "w3", out.result["workspace"])
	assert.Empty(t, l.tools.Herdr.Opened())
	require.Len(t, l.tools.Herdr.Started(), 1)
	assert.Equal(t, "w3", l.tools.Herdr.Started()[0].Workspace)
}

func TestTickLaunchesWithTheBuiltInLauncher(t *testing.T) {
	h := newHarness(t)
	h.builtinLauncher = true
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	tree := h.pool()

	_, err := h.run("tick")
	require.NoError(t, err)

	launched := h.receipts("launch", "ENG-1")
	require.Len(t, launched, 1)
	assert.Equal(t, ledger.Done, launched[0].Outcome)
	assert.Equal(t, []string{"w2:p2|/dispatch:build ENG-1"}, h.tools.Herdr.Kicked())
	require.Len(t, h.tools.Herdr.Started(), 1)
	started := h.tools.Herdr.Started()[0]
	assert.Equal(t, "sonnet", started.Model)
	assert.Equal(t, "ENG-1 ticket ENG-1", started.Label)
	assert.Equal(t, tree, h.tools.Herdr.Opened()[0].Path)
}

func TestLaunchTakesFlagsBeforeAndBetweenTheTicketAndBranch(t *testing.T) {
	for _, args := range [][]string{
		{"--model", "opus", "ENG-1", "thrawn01/eng-1"},
		{"ENG-1", "--model", "opus", "thrawn01/eng-1"},
	} {
		l := newLaunchHarness(t)

		out := l.launchArgs(args...)

		assert.Equal(t, "ok", out.result["status"])
		assert.Equal(t, "thrawn01/eng-1", out.result["branch"])
		require.Len(t, l.tools.Herdr.Started(), 1)
		assert.Equal(t, "opus", l.tools.Herdr.Started()[0].Model)
	}
}

func TestLaunchGivesUpAtOnceWhenThePreflightTimeoutIsZero(t *testing.T) {
	l := newLaunchHarness(t)
	l.tools.Herdr.Fail(herdrtest.OpRead, errors.New("server not running"))
	t.Setenv("DISPATCH_LAUNCH_PREFLIGHT_TIMEOUT", "0")

	start := time.Now()
	out := l.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "herdr_unavailable", out.result["status"])
	assert.Less(t, time.Since(start), 4*time.Second)
	assert.Empty(t, l.tools.Calls("treehouse", "lease"))
}

func TestLaunchRefusesAnEmptyPluginRoot(t *testing.T) {
	l := newLaunchHarness(t)
	t.Setenv("DISPATCH_PLUGIN_ROOT", "")
	l.writeFile("config.toml", "[plugins.herdr]\nplugin_root = \"\"\n")

	out := l.launchWith(dispatch.Options{ConfigPath: filepath.Join(l.dir, "config.toml")}, "ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "usage_error", out.result["status"])
	assert.Contains(t, out.result["detail"], "no plugin root")
	assert.Empty(t, l.tools.Herdr.Started())
}

func TestTickRefusesAnEmptyPluginRoot(t *testing.T) {
	h := newHarness(t)
	h.builtinLauncher = true
	h.extra = "[plugins.herdr]\nplugin_root = \"\"\n"
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	require.NoError(t, os.WriteFile(filepath.Join(h.repo, "treehouse.toml"), []byte("max_trees = 4\n"), 0o644))
	out, err := exec.Command("git", "-C", h.repo, "init", "-q", "-b", "main").CombinedOutput()
	require.NoError(t, err, string(out))

	_, err = h.run("tick")

	require.ErrorContains(t, err, "ENG-1 launch failed: usage_error")
	require.ErrorContains(t, err, "no plugin root")
	launched := h.receipts("launch", "ENG-1")
	require.Len(t, launched, 1)
	assert.Equal(t, ledger.Failed, launched[0].Outcome)
	assert.Contains(t, launched[0].Detail, "no plugin root")
}

// launchHarness runs `dispatch launch` in a throwaway git repo against the
// in-process herdr and treehouse fakes in tools.
type launchHarness struct {
	t     *testing.T
	dir   string
	repo  string
	tree  string
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
		tools: tooltest.New(t),
	}
	require.NoError(t, os.MkdirAll(l.repo, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(l.repo, "treehouse.toml"), []byte("max_trees = 4\n"), 0o644))
	l.git(l.repo, "init", "-q", "-b", "main")
	l.git(l.repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	l.git(l.repo, "worktree", "add", "-q", "--detach", l.tree)
	l.tools.Treehouse.AddTree(l.repo, l.tree)
	t.Setenv("DISPATCH_PLUGIN_ROOT", "/plugin")
	t.Setenv("DISPATCH_LAUNCH_PREFLIGHT_TIMEOUT", "1")
	return l
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
	opts.Herdr = l.tools.Herdr
	opts.Treehouse = l.tools.Treehouse
	opts.LaunchPace = time.Millisecond
	opts.Stdout = &stdout
	opts.Stderr = io.Discard
	err := dispatch.Run(context.Background(), append([]string{"launch"}, args...), opts)
	out := launchOutput{raw: stdout.String(), err: err}
	require.NoError(l.t, json.Unmarshal(stdout.Bytes(), &out.result))
	return out
}

// callIndex is the position of the first call to the tool's operation in the
// order the fakes saw them, or -1 when there was none.
func (l *launchHarness) callIndex(tool, name string) int {
	return slices.IndexFunc(l.tools.Journal.Calls(), func(c journal.Call) bool { return c.Tool == tool && c.Name == name })
}

// returns lists the treehouse return calls, as "<repo>|<tree>|<holder>".
func (l *launchHarness) returns() []string {
	return l.tools.Calls("treehouse", "return")
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

func (l *launchHarness) writeFile(name, content string) {
	require.NoError(l.t, os.WriteFile(filepath.Join(l.dir, name), []byte(content), 0o644))
}

func TestClaudeShimRestoresWorkerFlagsWithoutAName(t *testing.T) {
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
	cmd.Env = append(os.Environ(),
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
	cmd.Env = append(os.Environ(),
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

func TestClaudeShimFlags(t *testing.T) {
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
			name: "PlainSessionGetsOnlyThePlugin",
			args: []string{"--resume"},
			env:  []string{"DISPATCH_PLUGIN_ROOT=/plugin", "DISPATCH_WORKER_NAME=eng-1"},
			want: "--resume --plugin-dir /plugin\n",
		},
		{
			name: "NoEnvAddsNothing",
			args: []string{"--resume"},
			want: "--resume\n",
		},
		{
			name: "WorkerFlagsOnlyWhenAutonomous",
			args: []string{"--resume"},
			env:  []string{"AUTONOMOUS_WORKER=0", "DISPATCH_PLUGIN_ROOT=/plugin"},
			want: "--resume --plugin-dir /plugin\n",
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
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "claude"),
				[]byte("#!/usr/bin/env bash\necho \"$*\" > \""+filepath.Join(dir, "argv")+"\"\n"), 0o755))

			cmd := exec.Command(filepath.Join(shims, "claude"), test.args...)
			cmd.Env = append(append(os.Environ(), "AUTONOMOUS_WORKER=", "DISPATCH_PLUGIN_ROOT=", "DISPATCH_WORKER_NAME="), test.env...)
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
	cmd.Env = append(os.Environ(), "PATH="+strings.Join([]string{link, bin, dir, os.Getenv("PATH")}, ":"), "AUTONOMOUS_WORKER=", "DISPATCH_PLUGIN_ROOT=")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	argv, err := os.ReadFile(filepath.Join(dir, "argv"))
	require.NoError(t, err)
	assert.Equal(t, "--resume\n", string(argv))
}

func TestClaudeShimNamesAStaleDispatchBinary(t *testing.T) {
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
	cmd.Env = append(os.Environ(),
		"PATH="+strings.Join([]string{shims, stale, dir, os.Getenv("PATH")}, ":"),
		"DISPATCH_BIN="+filepath.Join(bin, "dispatch"),
		"AUTONOMOUS_WORKER=", "DISPATCH_PLUGIN_ROOT=",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	argv, err := os.ReadFile(filepath.Join(dir, "argv"))
	require.NoError(t, err)
	assert.Equal(t, "--resume\n", string(argv))
}

func TestLaunchesIntoOneRepoTakeTurnsLeasing(t *testing.T) {
	l := newLaunchHarness(t)
	second := filepath.Join(l.dir, "tree2")
	l.git(l.repo, "worktree", "add", "-q", "--detach", second)
	l.tools.Treehouse.AddTree(l.repo, second)
	l.tools.Treehouse.HoldLeases(100 * time.Millisecond)

	var wg sync.WaitGroup
	results := make([]launchOutput, 2)
	for i, ticket := range []string{"ENG-1", "ENG-2"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = l.launch(ticket, "thrawn01/"+strings.ToLower(ticket))
		}()
	}
	wg.Wait()

	assert.Equal(t, "ok", results[0].result["status"])
	assert.Equal(t, "ok", results[1].result["status"])
	assert.Equal(t, 1, l.tools.Treehouse.PeakLeasing(l.repo))
}

func TestLaunchesIntoDifferentReposLeaseTogether(t *testing.T) {
	l := newLaunchHarness(t)
	other := filepath.Join(l.dir, "other")
	require.NoError(t, os.MkdirAll(other, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(other, "treehouse.toml"), []byte("max_trees = 4\n"), 0o644))
	l.git(other, "init", "-q", "-b", "main")
	l.git(other, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	otherTree := filepath.Join(l.dir, "other-tree")
	l.git(other, "worktree", "add", "-q", "--detach", otherTree)
	l.tools.Treehouse.AddTree(other, otherTree)
	l.tools.Treehouse.AwaitLeases(2)

	var wg sync.WaitGroup
	for i, dir := range []string{l.repo, other} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := dispatch.Run(context.Background(), []string{"launch", fmt.Sprintf("ENG-%d", i+1), fmt.Sprintf("thrawn01/eng-%d", i+1)}, dispatch.Options{
				Dir: dir, Herdr: l.tools.Herdr, Treehouse: l.tools.Treehouse, LaunchPace: time.Millisecond, Stdout: io.Discard, Stderr: io.Discard,
			})
			assert.NoError(t, err)
		}()
	}
	wg.Wait()

	assert.Equal(t, 2, l.tools.Treehouse.PeakLeasingAnywhere())
}
