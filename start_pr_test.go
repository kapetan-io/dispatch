package dispatch_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startPRRemote gives the repo an origin whose refs/pull/88/head holds a commit that no branch
// holds, as GitHub keeps a fork's PR. It returns that commit.
func startPRRemote(t *testing.T, d *Dispatch) string {
	t.Helper()
	remote := filepath.Join(filepath.Dir(d.Repo), "remote.git")
	hintGit(t, filepath.Dir(d.Repo), "init", "-q", "--bare", remote)
	hintGit(t, d.Repo, "remote", "add", "origin", remote)
	hintGit(t, d.Repo, "push", "-q", "origin", "main")
	hintGit(t, d.Repo, "switch", "-q", "-c", "fork-work")
	hintGit(t, d.Repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "fork work")
	head := revParse(t, d.Repo, "HEAD")
	hintGit(t, d.Repo, "switch", "-q", "main")
	hintGit(t, d.Repo, "branch", "-q", "-D", "fork-work")
	hintGit(t, d.Repo, "push", "-q", "origin", head+":refs/heads/tmp")
	hintGit(t, remote, "update-ref", "refs/pull/88/head", head)
	hintGit(t, remote, "update-ref", "-d", "refs/heads/tmp")
	return head
}

func startPRDispatch(t *testing.T) *Dispatch {
	t.Helper()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Config: localOn})
	d.StartBeforeTheEvents()
	return d
}

func TestTickStartsALocalTicketsBranchAtItsStartPRHead(t *testing.T) {
	t.Parallel()
	d := startPRDispatch(t)
	tree := d.Trees.Pooled()
	head := startPRRemote(t, d)
	d.fileLocal("In Progress", "--start-pr", "88")

	d.Tick()

	assert.Equal(t, "loc-1-work", branchIn(t, tree))
	assert.Equal(t, head, revParse(t, tree, "HEAD"))
	assert.Empty(t, gitConfig(t, d.Repo, "branch.loc-1-work.dispatchStackParent"))
	assert.Empty(t, gitConfig(t, d.Repo, "branch.loc-1-work.remote"))
}

func TestTickNeverTreatsAStartPRAsTheTicketsOwnPR(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn, RepoSettings: autoMergeSetting})
	d.StartBeforeTheEvents()
	d.fileLocal("In Review", "--start-pr", "88")
	d.Workers.Running("LOC-1", "idle")
	// An approved PR on another branch, as a start PR is. It would merge if dispatch took it for LOC-1's.
	d.GitHub.PullRequest("LOC-2", approvedPR("a1"))

	d.Tick()

	assert.Equal(t, []string{"loc-1-work"}, d.prReads())
	assert.Empty(t, d.GitHub.Merged())
	assert.Empty(t, d.receiptsOfKind("LOC-1", "nudge"))
	assert.Empty(t, d.receiptsOfKind("LOC-1", "merge"))
}

func TestTickStartsALocalTicketWithoutStartPRAtTheBase(t *testing.T) {
	t.Parallel()
	d := startPRDispatch(t)
	tree := d.Trees.Pooled()
	startPRRemote(t, d)
	d.fileLocal("In Progress")

	d.Tick()

	assert.Equal(t, revParse(t, d.Repo, "main"), revParse(t, tree, "HEAD"))
}

func TestTickReturnsTheLeaseWhenTheStartPRIsMissing(t *testing.T) {
	t.Parallel()
	d := startPRDispatch(t)
	d.Trees.Pooled()
	startPRRemote(t, d)
	d.fileLocal("In Progress", "--start-pr", "99")

	// A failed launch makes Tick return an error, which is what this test reads.
	res := d.Run("tick")

	require.ErrorContains(t, res.Err, "LOC-1 launch failed: switch_failed: git switch to loc-1-work from PR 99 failed")
	assert.NotEmpty(t, d.Trees.Returned())
}

func TestTickKeepsAnExistingBranchWhenTheTicketHasAStartPR(t *testing.T) {
	t.Parallel()
	d := startPRDispatch(t)
	tree := d.Trees.Pooled()
	startPRRemote(t, d)
	hintGit(t, d.Repo, "branch", "loc-1-work", "main")
	d.fileLocal("In Progress", "--start-pr", "88")

	d.Tick()

	assert.Equal(t, revParse(t, d.Repo, "main"), revParse(t, tree, "HEAD"))
}
