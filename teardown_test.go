package dispatch_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kapetan-io/dispatch/internal/herdrtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTickReturnsTheTreeOfAFinishedTicket(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		status string
		worker string
	}{
		{name: "Done", status: "Done", worker: "idle"},
		{name: "Canceled", status: "Canceled", worker: "idle"},
		{name: "Duplicate", status: "Duplicate", worker: "idle"},
		{name: "WorkerStillWorking", status: "Done", worker: "working"},
		{name: "WorkerBlocked", status: "Canceled", worker: "blocked"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{})
			path := d.Trees.Clean("ENG-1")
			d.Workers.Running("ENG-1", test.worker)
			d.Linear.Finished(ticket("ENG-1", test.status, "dispatch"))

			d.Tick()

			assert.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())
			assert.Equal(t, []string{d.Repo + "|" + path + "|ENG-1"}, d.Trees.Returned())
			assert.Empty(t, d.Linear.Moves())
			assert.Empty(t, d.Linear.Comments())
			assert.Empty(t, d.Workers.Launched())
			returns := teardownReceipts(d, "ENG-1", "return")
			require.Len(t, returns, 1)
			assert.Equal(t, "done", returns[0].State)
			assert.Equal(t, "returned "+path, returns[0].Detail)

			// The tree is back in the pool, so the next tick has nothing left to do.
			d.Tick()
			assert.Len(t, d.Workers.Closed(), 1)
			assert.Len(t, d.Trees.Returned(), 1)
		})
	}
}

func TestTickReturnsATreeWhoseWorkspaceIsAlreadyClosed(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.Leased("ENG-1", path)
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	d.Tick()

	assert.Empty(t, d.Workers.Closed())
	assert.Equal(t, []string{d.Repo + "|" + path + "|ENG-1"}, d.Trees.Returned())
}

func TestTickReturnsATreeWhoseWorkspaceHoldsNoAgent(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	// A worker whose Claude exited leaves its workspace open, and the slot only
	// comes back if teardown closes it.
	d.Workers.Running("ENG-1", "")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	d.Tick()

	assert.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())
	assert.Equal(t, []string{d.Repo + "|" + path + "|ENG-1"}, d.Trees.Returned())
}

func TestTickCommentsOnTheDiscardedWorkThenReturnsTheTreeOfAFinishedTicket(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		strand  func(d *Dispatch, path string)
		discard string
		detail  string
	}{
		{
			name:    "UncommittedFile",
			strand:  func(d *Dispatch, path string) { d.Trees.Dirty(path) },
			discard: "uncommitted changes to tracked files.",
			detail:  ", discarded uncommitted changes",
		},
		{
			name:    "UntrackedFile",
			strand:  func(d *Dispatch, path string) { d.Trees.Leave(path, "zz_tmp_test.go") },
			discard: "untracked files (zz_tmp_test.go).",
			detail:  ", removed untracked zz_tmp_test.go",
		},
		{
			name: "BothKinds",
			strand: func(d *Dispatch, path string) {
				d.Trees.Dirty(path)
				d.Trees.Leave(path, "zz_tmp_test.go")
			},
			discard: "uncommitted changes to tracked files and untracked files (zz_tmp_test.go).",
			detail:  ", discarded uncommitted changes, removed untracked zz_tmp_test.go",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{})
			path := d.Trees.Clean("ENG-1")
			test.strand(d, path)
			d.Workers.Running("ENG-1", "idle")
			d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

			d.Tick()
			d.Tick()
			d.Tick()

			assert.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())
			assert.Equal(t, []string{d.Repo + "|" + path + "|ENG-1"}, d.Trees.Returned())
			assert.Empty(t, d.Linear.Moves())
			comments := d.Linear.Comments()
			require.Len(t, comments, 1)
			assert.True(t, strings.HasPrefix(comments[0], "ENG-1: "))
			assert.Contains(t, comments[0], "`"+path+"`")
			assert.Contains(t, comments[0], "It discards "+test.discard)
			assert.Contains(t, comments[0], "Branch `thrawn01/eng-1` and its commits stay.")
			assert.NoFileExists(t, filepath.Join(path, "zz_tmp_test.go"))
			returns := teardownReceipts(d, "ENG-1", "return")
			require.Len(t, returns, 1)
			assert.Equal(t, "done", returns[0].State)
			assert.Equal(t, "returned "+path+test.detail, returns[0].Detail)
		})
	}
}

func TestTickReturnsTheTreeOfAFinishedTicketWhoseCommitsNoRemoteHas(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.Commit(path)
	d.Trees.Commit(path)
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	d.Tick()

	// The ticket's branch keeps the commits, so they are no reason to keep the tree.
	assert.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())
	assert.Equal(t, []string{d.Repo + "|" + path + "|ENG-1"}, d.Trees.Returned())
	assert.Empty(t, d.Linear.Comments())
}

func TestTickRemovesUntrackedFilesBeforeReturningTheTree(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "idle")
	path := d.Trees.Untracked("ENG-1", "zz_tmp_test.go")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	d.Tick()
	d.Tick()

	assert.NoFileExists(t, filepath.Join(path, "zz_tmp_test.go"))
	assert.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())
	assert.Equal(t, []string{d.Repo + "|" + path + "|ENG-1"}, d.Trees.Returned())
	assert.Empty(t, d.Linear.Moves())
	require.Len(t, d.Linear.Comments(), 1)
	returns := slices.DeleteFunc(d.Receipts("ENG-1"), func(r Receipt) bool { return r.Kind != "return" })
	require.Len(t, returns, 1)
	assert.Equal(t, "done", returns[0].State)
	assert.Equal(t, "returned "+path+", removed untracked zz_tmp_test.go", returns[0].Detail)
}

func TestTickLeavesIgnoredFilesAloneWhenRemovingUntrackedOnes(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	require.NoError(t, os.WriteFile(filepath.Join(path, ".gitignore"), []byte("build/\n"), 0o644))
	d.Trees.Git(path, "add", ".gitignore")
	d.Trees.Git(path, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-m", "ignore")
	d.Trees.Push(path)
	require.NoError(t, os.MkdirAll(filepath.Join(path, "build"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(path, "build", "out.bin"), []byte("x"), 0o644))
	d.Trees.Leave(path, "scratch.txt")
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	d.Tick()

	returns := teardownReceipts(d, "ENG-1", "return")
	require.Len(t, returns, 1)
	assert.Equal(t, "returned "+path+", removed untracked scratch.txt", returns[0].Detail)
	assert.FileExists(t, filepath.Join(path, "build", "out.bin"))
	assert.Len(t, d.Trees.Returned(), 1)
}

func TestTickQuotesUntrackedFileNamesThatHoldSpaces(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.Leave(path, "sp ace.txt")
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	d.Tick()

	returns := teardownReceipts(d, "ENG-1", "return")
	require.Len(t, returns, 1)
	assert.Equal(t, "returned "+path+", removed untracked \"sp ace.txt\"", returns[0].Detail)
	assert.NoFileExists(t, filepath.Join(path, "sp ace.txt"))
}

func TestTickKeepsTheTreeWhenGitCleanLeavesANestedRepoBehind(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	require.NoError(t, os.MkdirAll(filepath.Join(path, "nested"), 0o755))
	d.Trees.Git(filepath.Join(path, "nested"), "init", "--initial-branch=main")
	d.Trees.Leave(path, "scratch.txt")
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	res := d.Run("tick")

	// A kept tree is a refusal the user has to see, not a full pool that frees itself.
	require.ErrorContains(t, res.Err, "untracked nested/")
	assert.NoFileExists(t, filepath.Join(path, "scratch.txt"))
	assert.DirExists(t, filepath.Join(path, "nested"))
	assert.Empty(t, d.Trees.Returned())
	returns := teardownReceipts(d, "ENG-1", "return")
	require.Len(t, returns, 1)
	assert.Equal(t, "refused", returns[0].State)
	assert.Equal(t, "untracked nested/ in "+path+" survived git clean", returns[0].Detail)
}

func TestTickIgnoresGitLocationVariablesWhenRemovingUntrackedFiles(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.Leave(path, "scratch.txt")
	other := d.Trees.Clean("ENG-2")
	d.Trees.Leave(other, "precious.txt")
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	// Set after the trees are built, because the setup's own git commands would obey them too.
	// A dispatcher started from a git hook inherits these, and they would point git at the wrong repository.
	d.env = d.env.With("GIT_DIR", filepath.Join(other, ".git")).With("GIT_WORK_TREE", other)

	d.Tick()

	returns := teardownReceipts(d, "ENG-1", "return")
	require.Len(t, returns, 1)
	assert.Equal(t, "returned "+path+", removed untracked scratch.txt", returns[0].Detail)
	assert.NoFileExists(t, filepath.Join(path, "scratch.txt"))
	assert.FileExists(t, filepath.Join(other, "precious.txt"))
}

func TestTickReturnsATreeWhoseOnlyLeftoversAreIgnored(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	require.NoError(t, os.WriteFile(filepath.Join(path, ".gitignore"), []byte("*.log\n"), 0o644))
	d.Trees.Git(path, "add", ".gitignore")
	d.Trees.Git(path, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-m", "ignore")
	d.Trees.Push(path)
	require.NoError(t, os.WriteFile(filepath.Join(path, "run.log"), []byte("x"), 0o644))
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	d.Tick()

	returns := teardownReceipts(d, "ENG-1", "return")
	require.Len(t, returns, 1)
	assert.Equal(t, "returned "+path, returns[0].Detail)
	assert.Len(t, d.Trees.Returned(), 1)
}

func TestTickDiscardsUntrackedFilesAndTrackedChangesInOneComment(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.Dirty(path)
	d.Trees.Leave(path, "scratch.txt")
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	d.Tick()
	d.Tick()

	assert.NoFileExists(t, filepath.Join(path, "scratch.txt"))
	assert.Len(t, d.Trees.Returned(), 1)
	comments := d.Linear.Comments()
	require.Len(t, comments, 1)
	assert.Contains(t, comments[0], "It discards uncommitted changes to tracked files and untracked files (scratch.txt).")
}

func TestTickDiscardsATrackedFileModifiedButNotStaged(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.Leave(path, "tracked.txt")
	d.Trees.Git(path, "add", "tracked.txt")
	d.Trees.Git(path, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-m", "track")
	d.Trees.Push(path)
	require.NoError(t, os.WriteFile(filepath.Join(path, "tracked.txt"), []byte("edited"), 0o644))
	d.Trees.Leave(path, "scratch.txt")
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	d.Tick()
	d.Tick()

	assert.NoFileExists(t, filepath.Join(path, "scratch.txt"))
	assert.Len(t, d.Trees.Returned(), 1)
	comments := d.Linear.Comments()
	require.Len(t, comments, 1)
	assert.Contains(t, comments[0], "It discards uncommitted changes to tracked files and untracked files (scratch.txt).")
}

func TestTickReturnsTheTreeOnlyAfterTheDiscardCommentLanded(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.Dirty(path)
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	d.Linear.FailComments(true)

	res := d.Run("tick")
	require.ErrorContains(t, res.Err, "while commenting on ENG-1:")
	assert.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())
	assert.Empty(t, d.Linear.Comments())
	assert.Empty(t, d.Trees.Returned())

	// The comment never landed, so the tree stays until a tick lands it.
	d.Linear.FailComments(false)
	d.Tick()
	d.Tick()

	assert.Len(t, d.Workers.Closed(), 1)
	assert.Len(t, d.Trees.Returned(), 1)
	comments := d.Linear.Comments()
	require.Len(t, comments, 1)
	assert.Contains(t, comments[0], "`"+path+"`")

	d.Tick()
	assert.Len(t, d.Linear.Comments(), 1)
}

func TestTickKeepsATreeItCannotRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	// The tree's directory is gone, so git can't say whether it held work.
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	d.Tick()

	assert.Empty(t, d.Workers.Closed())
	assert.Empty(t, d.Trees.Returned())
	assert.Empty(t, d.Linear.Comments())
	plan := d.Run("cycle")
	require.NoError(t, plan.Err)
	assert.Regexp(t, `(?m)^held .*teardown-clean-tree`, plan.Stdout)
}

func TestTickKeepsTheTreeWhenTheWorkspaceWillNotClose(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Trees.Clean("ENG-1")
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	d.tools.Multiplexer.Fail(herdrtest.OpCloseWorkspace, errors.New("exit status 1"))

	res := d.Run("tick")

	require.ErrorContains(t, res.Err, "while closing workspace w-ENG-1")
	assert.Empty(t, d.Trees.Returned())
	returns := teardownReceipts(d, "ENG-1", "return")
	require.Len(t, returns, 1)
	assert.Equal(t, "failed", returns[0].State)
}

func TestTickKeepsTheTreeWhenTreehouseRefusesTheReturn(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	// The lease moves to another ticket after dispatch looked at the pool.
	d.tools.Treehouse.MoveLeaseAfterRead(path, "ENG-2")

	res := d.Run("tick")

	require.ErrorContains(t, res.Err, "while returning")
	require.ErrorContains(t, res.Err, "lease holder does not match worktree")
	assert.Equal(t, "ENG-2", d.Trees.Holder(path))
	assert.Empty(t, d.Linear.Comments())
}

func TestTickCommentsOnlyOnTheTreeWithWorkToDiscardOfATicketWithTwoTrees(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	clean := d.Trees.Clean("ENG-1")
	stranded := d.Trees.Clean("library")
	d.Trees.Dirty(stranded)
	d.Workers.RunningAs("ENG-1", clean, "w-ENG-1", "idle")
	d.Workers.RunningAs("ENG-1", stranded, "w-library", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	d.Tick()
	d.Tick()

	assert.ElementsMatch(t, []string{"w-ENG-1", "w-library"}, d.Workers.Closed())
	assert.ElementsMatch(t, []string{d.Repo + "|" + clean + "|ENG-1", d.Repo + "|" + stranded + "|ENG-1"}, d.Trees.Returned())
	comments := d.Linear.Comments()
	require.Len(t, comments, 1)
	assert.Contains(t, comments[0], stranded)
	assert.NotContains(t, comments[0], clean)
}

func TestTickLeavesTheTreesOfTicketsItDoesNotManage(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		ticket fakeTicket
	}{
		{
			name:   "AssignedToSomeoneElse",
			ticket: assignedTo(ticket("ENG-1", "Done", "dispatch"), "someone-else"),
		},
		{
			name:   "NoAILabel",
			ticket: withoutLabels(ticket("ENG-1", "Done", "dispatch")),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Trees.Clean("ENG-1")
			d.Workers.Running("ENG-1", "idle")
			d.Linear.Finished(test.ticket)

			res := d.Run("tick")
			require.NoError(t, res.Err)

			assert.Empty(t, res.Stdout)
			assert.Empty(t, d.Workers.Closed())
			assert.Empty(t, d.Trees.Returned())
			assert.Empty(t, d.Linear.Moves())
			assert.Empty(t, d.Linear.Comments())
		})
	}
}

func TestTickIgnoresALeaseHeldByNoTicket(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Trees.Clean("scratch")
	d.Workers.Running("scratch", "idle")
	d.Linear.Finished()

	res := d.Run("tick")
	require.NoError(t, res.Err)

	assert.Empty(t, res.Stdout)
	assert.Empty(t, d.Workers.Closed())
	assert.Empty(t, d.Trees.Returned())
}

func TestTickReturnsTheTreeOfAFinishedTicketWhoseLabelsDidNotFitOnePage(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Workers.Running("ENG-1", "idle")
	cut := ticket("ENG-1", "Done", "dispatch")
	cut.LabelsTruncated = true
	d.Linear.Finished(cut)

	d.Tick()

	assert.Equal(t, []string{d.Repo + "|" + path + "|ENG-1"}, d.Trees.Returned())
}

func TestTickReturnsTheCleanTreeOfAFinishedReviewTicket(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"Done", "Canceled"} {
		t.Run(status, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			path := d.Trees.Clean("ENG-1")
			d.Workers.Running("ENG-1", "idle")
			d.Linear.Finished(withLabel(ticket("ENG-1", status, "dispatch"), "design-review"))

			d.Tick()

			assert.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())
			assert.Equal(t, []string{d.Repo + "|" + path + "|ENG-1"}, d.Trees.Returned())
		})
	}
}

func TestTickReturnsTheDirtyTreeOfADoneReviewTicketWithOneComment(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.Dirty(path)
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(withLabel(ticket("ENG-1", "Done", "dispatch"), "design-review"))

	d.Tick()
	d.Tick()

	assert.Len(t, d.Trees.Returned(), 1)
	comments := d.Linear.Comments()
	require.Len(t, comments, 1)
	assert.Contains(t, comments[0], "`"+path+"`")

	d.Tick()
	assert.Len(t, d.Linear.Comments(), 1)
}

func TestTickAsksLinearAboutLeasesOnlyWhenAManagedTicketDoesNotHoldThem(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "working")
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	d.Tick()

	assert.Zero(t, d.Linear.Lookups())
}

func TestTickStopsTheWorkerOfAPausedTicketAndReturnsItsTree(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"Todo", "Backlog"} {
		t.Run(status, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			path := d.Trees.Clean("ENG-1")
			d.Trees.Dirty(path)
			d.Workers.Running("ENG-1", "working")
			d.Linear.Finished(ticket("ENG-1", status, "dispatch"))

			d.Tick()
			d.Tick()
			d.Tick()

			assert.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())
			assert.Equal(t, []string{d.Repo + "|" + path + "|ENG-1"}, d.Trees.Returned())
			assert.Empty(t, d.Linear.Moves())
			assert.Empty(t, d.Workers.Launched())
			comments := d.Linear.Comments()
			require.Len(t, comments, 1)
			assert.Contains(t, comments[0], "`"+path+"`")
			assert.Contains(t, comments[0], "It discards uncommitted changes to tracked files.")

			// The tree is back in the pool, so nothing more happens while the ticket is paused.
			d.Tick()
			assert.Len(t, d.Workers.Closed(), 1)
			assert.Len(t, d.Trees.Returned(), 1)
			assert.Len(t, d.Linear.Comments(), 1)
		})
	}
}

func TestTickReturnsThePausedTicketsTreeAndClosesItsAgentlessWorkspace(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	// A workspace with no Claude agent is not a worker, so the pause leaves it open until the return closes it.
	d.Workers.Running("ENG-1", "")
	d.Linear.Finished(ticket("ENG-1", "Todo", "dispatch"))

	d.Tick()

	assert.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())
	assert.Equal(t, []string{d.Repo + "|" + path + "|ENG-1"}, d.Trees.Returned())
}

func TestTickClosesAnAgentlessWorkspaceWhenItReturnsADirtyTree(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.Dirty(path)
	d.Workers.Running("ENG-1", "")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	d.Tick()
	d.Tick()

	assert.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())
	assert.Equal(t, []string{d.Repo + "|" + path + "|ENG-1"}, d.Trees.Returned())
	assert.Len(t, d.Linear.Comments(), 1)
}

func TestTickDoesNotRepeatTheCommentAfterALeaseLookupFails(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.NestRepo(path)
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	require.ErrorContains(t, d.Run("tick").Err, "untracked nested/")
	d.Tick()
	require.Len(t, d.Linear.Comments(), 2)

	// A tick that can't tell who holds a lease holds the tree's rules, and the receipt still stands.
	d.Linear.FailLookups()
	d.Tick()
	d.Linear.AllowLookups()
	d.Tick()

	assert.Len(t, d.Linear.Comments(), 2)
}

func TestTickDoesNotRepeatTheCommentAfterAPoolCannotBeRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.NestRepo(path)
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	require.ErrorContains(t, d.Run("tick").Err, "untracked nested/")
	d.Advance(time.Second)
	d.Tick()
	require.Len(t, d.Linear.Comments(), 2)

	// A second repo whose pool fails leaves the first one's trees readable.
	d.addRepo("tiger", filepath.Join(d.dir, "moved"))
	d.Restart()
	d.Tick()
	// Dropping the extra repo makes the next start read every pool again.
	d.resetRepos()
	d.Restart()
	d.Tick()

	assert.Len(t, d.Linear.Comments(), 2)
}

func TestTickReturnsATreeWhileAnotherBranchOfTheRepoHoldsUnpushedWork(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	// Pool trees share one repository, so a branch another ticket is still
	// working on is no reason to keep this ticket's tree.
	d.Trees.Git(path, "branch", "other")
	d.Trees.Git(path, "checkout", "other")
	d.Trees.Commit(path)
	d.Trees.Git(path, "checkout", "--detach", "main")
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	d.Tick()

	assert.Equal(t, []string{d.Repo + "|" + path + "|ENG-1"}, d.Trees.Returned())
}

func TestTickKeepsTickingWhenLinearCannotListTheTicketsHoldingTrees(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Trees.Clean("ENG-1")
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Tickets(ticket("ENG-2", "In Progress", "dispatch"))
	d.Linear.FailLookups()

	d.Tick()

	assert.Len(t, d.Workers.Launched(), 1)
	assert.Contains(t, d.loopErr(), "dispatch: while reading the tickets that hold trees: linear LeasedTickets")
	assert.Empty(t, d.Trees.Returned())
}

func TestTickRelaunchesAPausedTicketOnceItsTreeIsReturned(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Trees.Clean("ENG-1")
	d.Workers.Running("ENG-1", "working")
	d.Linear.Finished(ticket("ENG-1", "Todo", "dispatch"))
	d.Tick()
	require.Empty(t, d.Workers.Launched())
	require.Len(t, d.Trees.Returned(), 1)

	d.Linear.Finished()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()

	assert.Len(t, d.Workers.Launched(), 1)
	assert.Len(t, d.Trees.Returned(), 1)
}

func TestTickDoesNotTouchTreesOfTicketsInReviewOrNeedsHuman(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Trees.Clean("ENG-1")
	d.Trees.Clean("ENG-2")
	d.Workers.Running("ENG-1", "working")
	d.Workers.Running("ENG-2", "working")
	d.Linear.Tickets(
		ticket("ENG-1", "In Review", "dispatch"),
		ticket("ENG-2", "Needs Human", "dispatch"),
	)

	d.Tick()

	assert.Empty(t, d.Workers.Closed())
	assert.Empty(t, d.Trees.Returned())
}

func TestTickTeardownFreesATreeForALaunchInTheSameTick(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	// The real launcher leaves a workspace behind, as production does, so one launch is one worker.
	parentsRealLauncher(d)
	d.Trees.Clean("ENG-1")
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	d.Linear.Tickets(ticket("ENG-2", "In Progress", "dispatch"))

	d.Tick()

	assert.Len(t, d.Trees.Returned(), 1)
	launches := teardownReceipts(d, "ENG-2", "launch")
	require.Len(t, launches, 1)
	assert.Equal(t, "done", launches[0].State)
}

func TestTickReadsDoneAndPausedStatusesFromConfig(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[plugins.linear.statuses]\nfinished = [\"Shipped\"]\npaused = [\"Parked\"]\n"})
	d.tools.Linear.AddState("Shipped", "completed")
	d.tools.Linear.AddState("Parked", "backlog")
	first := d.Trees.Clean("ENG-1")
	second := d.Trees.Clean("ENG-2")
	d.Trees.Clean("ENG-3")
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Running("ENG-2", "idle")
	d.Workers.Running("ENG-3", "idle")
	// ENG-3 is Done, a default status the config replaced, so it is left alone.
	d.Linear.Finished(
		ticket("ENG-1", "Shipped", "dispatch"),
		ticket("ENG-2", "Parked", "dispatch"),
		ticket("ENG-3", "Done", "dispatch"),
	)

	d.Tick()

	assert.ElementsMatch(t, []string{d.Repo + "|" + first + "|ENG-1", d.Repo + "|" + second + "|ENG-2"}, d.Trees.Returned())
	assert.ElementsMatch(t, []string{"w-ENG-1", "w-ENG-2"}, d.Workers.Closed())
}

func TestTickClosesTheWorkspaceBeforeItReturnsTheTree(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	// A worker still running in the tree could write to it after the return.
	var order []string
	d.tools.Treehouse.BeforeReturn(func(string) {
		if d.tools.Multiplexer.HasWorkspace("w-ENG-1") {
			order = append(order, "open")
		} else {
			order = append(order, "closed")
		}
	})

	d.Tick()

	assert.Equal(t, []string{"closed"}, order)
	assert.Equal(t, []string{d.Repo + "|" + path + "|ENG-1"}, d.Trees.Returned())
}

func TestTickLeavesNoTabPaneOrAgentOfAClosedWorkspace(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Trees.Clean("ENG-1")
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Running("ENG-2", "working")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	d.Tick()

	snap, err := d.tools.Multiplexer.Read(t.Context())
	require.NoError(t, err)
	require.Len(t, snap.Workspaces, 1)
	assert.Equal(t, "w-ENG-2", snap.Workspaces[0].ID)
	require.Len(t, snap.Tabs, 1)
	assert.Equal(t, "w-ENG-2:t1", snap.Tabs[0].ID)
	require.Len(t, snap.Panes, 1)
	assert.Equal(t, "w-ENG-2:p1", snap.Panes[0].ID)
	require.Len(t, snap.Agents, 1)
	assert.Equal(t, "w-ENG-2:p1", snap.Agents[0].PaneID)
}

func TestTickKeepsTheTreeOfChangesWrittenWhileTheWorkspaceClosedUntilTheyAreNoted(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Workers.Running("ENG-1", "working")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	// The cycle saw a clean tree, and the worker stages a file as it is killed.
	d.tools.Multiplexer.BeforeCloseWorkspace(func(string) {
		d.Clock.Advance(time.Second)
		require.NoError(t, os.WriteFile(filepath.Join(path, "late.txt"), []byte("late\n"), 0o644))
		require.NoError(t, exec.Command("git", "-C", path, "add", "late.txt").Run())
	})

	res := d.Run("tick")

	require.ErrorContains(t, res.Err, "the worker left uncommitted changes")
	assert.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())
	// The refusal is the first look at the late work, so the comment lands before the retry returns the tree.
	assert.Contains(t, strings.Join(d.Linear.Comments(), "\n"), "It discards uncommitted changes to tracked files.")
	assert.Equal(t, []string{d.Repo + "|" + path + "|ENG-1"}, d.Trees.Returned())
	returns := teardownReceipts(d, "ENG-1", "return")
	require.Len(t, returns, 2)
	assert.Equal(t, "refused", returns[0].State)
	assert.Equal(t, "done", returns[1].State)
}

func TestTickKeepsATreeWhoseUnpushedCommitsAreOnADetachedHead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.Git(path, "checkout", "--detach")
	d.Trees.Commit(path)
	d.Trees.Leased("ENG-1", path)
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	res := d.Run("tick")

	// No branch holds the commit, so a return would leave it reachable only from the reflog.
	require.ErrorContains(t, res.Err, "1 commit on a detached HEAD that no remote has")
	assert.Empty(t, d.Trees.Returned())
}

func TestTickCommentsThenReturnsADirtyTreeThatHasNoWorkspace(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.Dirty(path)
	d.Trees.Leased("ENG-1", path)
	d.Linear.Finished(ticket("ENG-1", "Canceled", "dispatch"))

	d.Tick()

	assert.Empty(t, d.Workers.Closed())
	assert.Equal(t, []string{d.Repo + "|" + path + "|ENG-1"}, d.Trees.Returned())
	assert.Len(t, d.Linear.Comments(), 1)

	d.Tick()
	assert.Len(t, d.Linear.Comments(), 1)
}

func TestTickReturnsATreeWithNoRemoteForItsCommits(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	// A tree with no remote at all has nowhere its commits could be pushed, and the ticket's branch keeps them.
	path := d.Trees.PathOf("ENG-1")
	require.NoError(t, os.MkdirAll(path, 0o755))
	d.Trees.Git(path, "init", "--initial-branch=main")
	d.Trees.Commit(path)
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	d.Tick()

	assert.Equal(t, []string{d.Repo + "|" + path + "|ENG-1"}, d.Trees.Returned())
	assert.Empty(t, d.Linear.Comments())
}

func TestTickLeavesPausedTicketsItDoesNotManage(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		ticket fakeTicket
	}{
		{
			name:   "AssignedToSomeoneElse",
			ticket: assignedTo(ticket("ENG-1", "Todo", "dispatch"), "someone-else"),
		},
		{
			name:   "NoAILabel",
			ticket: withoutLabels(ticket("ENG-1", "Backlog", "dispatch")),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Trees.Clean("ENG-1")
			d.Workers.Running("ENG-1", "idle")
			d.Linear.Finished(test.ticket)

			d.Tick()

			assert.Empty(t, d.Workers.Closed())
			assert.Empty(t, d.Trees.Returned())
			assert.Empty(t, d.Linear.Moves())
			assert.Empty(t, d.Linear.Comments())
		})
	}
}

func TestTickStopsEveryWorkerOfAPausedTicketAndReturnsItsTrees(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	first := d.Trees.Clean("ENG-1")
	second := d.Trees.Clean("library")
	d.Workers.RunningAs("ENG-1", first, "w-ENG-1", "working")
	d.Workers.RunningAs("ENG-1", second, "w-library", "idle")
	d.Linear.Finished(ticket("ENG-1", "Backlog", "dispatch"))

	d.Tick()
	d.Tick()

	assert.ElementsMatch(t, []string{"w-ENG-1", "w-library"}, d.Workers.Closed())
	assert.ElementsMatch(t, []string{d.Repo + "|" + first + "|ENG-1", d.Repo + "|" + second + "|ENG-1"}, d.Trees.Returned())
}

func TestTickReturnsTheTreeOfAPausedTicketWithNoWorkspace(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.Leased("ENG-1", path)
	d.Linear.Finished(ticket("ENG-1", "Todo", "dispatch"))

	d.Tick()

	assert.Empty(t, d.Workers.Closed())
	assert.Equal(t, []string{d.Repo + "|" + path + "|ENG-1"}, d.Trees.Returned())
	assert.Empty(t, d.Linear.Comments())
}

func TestTickStopsTheWorkerOfAPausedReviewTicketAndReturnsItsTree(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(withLabel(ticket("ENG-1", "Backlog", "dispatch"), "design-review"))

	d.Tick()

	assert.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())
	assert.Equal(t, []string{d.Repo + "|" + path + "|ENG-1"}, d.Trees.Returned())
}

func TestTickCommentsAgainWhenAReopenedTicketFinishesWithWorkInItsTree(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.Dirty(path)
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	d.Tick()
	require.Len(t, d.Linear.Comments(), 1)

	// Reopened, the ticket is managed again and its old warning no longer applies.
	d.Advance(time.Minute)
	d.Linear.Finished()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Trees.Leased("ENG-1", path)
	d.Tick()
	d.Advance(time.Minute)
	d.Linear.Tickets()
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	d.Tick()

	assert.Len(t, d.Linear.Comments(), 2)
}

func TestTickPostsACommentForEachTreeWithWorkToDiscard(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	first := d.Trees.Clean("ENG-1")
	second := d.Trees.Clean("library")
	d.Trees.Dirty(first)
	d.Trees.Leave(second, "scratch.txt")
	d.Workers.RunningAs("ENG-1", first, "w-ENG-1", "idle")
	d.Workers.RunningAs("ENG-1", second, "w-library", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	d.Tick()
	d.Tick()

	assert.ElementsMatch(t, []string{"w-ENG-1", "w-library"}, d.Workers.Closed())
	assert.ElementsMatch(t, []string{d.Repo + "|" + first + "|ENG-1", d.Repo + "|" + second + "|ENG-1"}, d.Trees.Returned())
	comments := d.Linear.Comments()
	require.Len(t, comments, 2)
	assert.Contains(t, strings.Join(comments, "\n"), "`"+first+"` to its pool")
	assert.Contains(t, strings.Join(comments, "\n"), "`"+second+"` to its pool")

	d.Tick()
	assert.Len(t, d.Linear.Comments(), 2)
}

func TestPlanNamesEachKeptTreeOfOneTicket(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	first := d.Trees.Clean("ENG-1")
	second := d.Trees.Clean("library")
	d.Trees.NestRepo(first)
	d.Trees.NestRepo(second)
	d.Workers.RunningAs("ENG-1", first, "w-ENG-1", "idle")
	d.Workers.RunningAs("ENG-1", second, "w-library", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	require.ErrorContains(t, d.Run("tick").Err, "untracked nested/")
	d.Advance(time.Second)
	d.Tick()

	plan := d.Run("cycle")
	require.NoError(t, plan.Err)

	// The comment of each tree carries its path in the once key, so the two do not cover each other.
	var covered int
	for _, line := range strings.Split(plan.Stdout, "\n") {
		if strings.HasPrefix(line, "covered") && strings.Contains(line, "comment-kept-tree") {
			covered++
			assert.True(t, strings.Contains(line, first) != strings.Contains(line, second))
		}
	}
	assert.Equal(t, 2, covered)
}

func TestPlanListsTheCommentOfAKeptTreeAsCoveredOnEveryTick(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.NestRepo(path)
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	require.ErrorContains(t, d.Run("tick").Err, "untracked nested/")
	d.Advance(time.Second)
	d.Tick()
	require.Len(t, d.Linear.Comments(), 2)

	d.Tick()
	plan := d.Run("cycle")
	require.NoError(t, plan.Err)

	assert.Regexp(t, `(?m)^covered +ENG-1 +comment +comment-kept-tree +done .*`+regexp.QuoteMeta(path), plan.Stdout)
	assert.Len(t, d.Linear.Comments(), 2)
}

func TestTickKeepsATreeWhoseGitStateIsBroken(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	// Git fails in the tree, which is unknown, not clean.
	require.NoError(t, os.WriteFile(filepath.Join(path, ".git", "HEAD"), []byte("garbage\n"), 0o644))

	d.Tick()

	assert.Empty(t, d.Workers.Closed())
	assert.Empty(t, d.Trees.Returned())
	assert.Empty(t, d.Linear.Comments())
	assert.Empty(t, teardownReceipts(d, "ENG-1", "return"))
	plan := d.Run("cycle")
	require.NoError(t, plan.Err)
	assert.Regexp(t, `(?m)^held .*teardown-clean-tree`, plan.Stdout)
	assert.DirExists(t, path)
}

func TestTickReturnsATreeThatGainedACommitAfterTheCycleDecidedToReturnIt(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Workers.Running("ENG-1", "working")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	// The worker commits as its workspace closes, and the ticket's branch keeps the commit.
	d.tools.Multiplexer.BeforeCloseWorkspace(func(string) { d.Trees.Commit(path) })

	d.Tick()

	assert.Equal(t, []string{d.Repo + "|" + path + "|ENG-1"}, d.Trees.Returned())
	assert.Empty(t, d.Linear.Comments())
	returns := teardownReceipts(d, "ENG-1", "return")
	require.Len(t, returns, 1)
	assert.Equal(t, "done", returns[0].State)
}

func TestTickKeepsATreeThatGainedANestedRepoAfterTheCycleDecidedToReturnIt(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Workers.Running("ENG-1", "working")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	// An untracked repository is work git clean will not remove and returning would discard.
	d.tools.Multiplexer.BeforeCloseWorkspace(func(string) {
		require.NoError(t, os.MkdirAll(filepath.Join(path, "nested"), 0o755))
		d.Trees.Git(filepath.Join(path, "nested"), "init", "--initial-branch=main")
	})

	res := d.Run("tick")

	require.ErrorContains(t, res.Err, "untracked nested/")
	assert.Empty(t, d.Trees.Returned())
	assert.DirExists(t, filepath.Join(path, "nested"))
	returns := teardownReceipts(d, "ENG-1", "return")
	require.Len(t, returns, 1)
	assert.Equal(t, "refused", returns[0].State)
	assert.Equal(t, "untracked nested/ in "+path+" survived git clean", returns[0].Detail)

	// While the nested repository stays, the tree is not returned however many ticks follow.
	// One comment says what the return would discard, and one says why the tree is still leased.
	d.Advance(time.Second)
	d.Tick()
	assert.Empty(t, d.Trees.Returned())
	assert.DirExists(t, filepath.Join(path, "nested"))
	comments := d.Linear.Comments()
	require.Len(t, comments, 2)
	assert.Contains(t, comments[0], "It discards untracked files (nested/).")
	assert.Contains(t, comments[1], "kept this ticket's tree leased, because untracked nested/")

	// Removing the nested repository frees the slot, though nothing tracked changed.
	require.NoError(t, os.RemoveAll(filepath.Join(path, "nested")))
	d.Advance(time.Second)
	d.Tick()
	assert.Equal(t, []string{d.Repo + "|" + path + "|ENG-1"}, d.Trees.Returned())
}

func TestTickDoesNotReturnATreeWhoseGitFailsAfterTheCycleDecidedToReturnIt(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Workers.Running("ENG-1", "working")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	// The cycle read the tree fine, and git cannot read it when the executor looks again.
	d.tools.Multiplexer.BeforeCloseWorkspace(func(string) {
		require.NoError(t, os.WriteFile(filepath.Join(path, ".git", "HEAD"), []byte("garbage\n"), 0o644))
	})

	res := d.Run("tick")

	require.Error(t, res.Err)
	assert.Empty(t, d.Trees.Returned())
	returns := teardownReceipts(d, "ENG-1", "return")
	require.Len(t, returns, 1)
	assert.Equal(t, "failed", returns[0].State)
	assert.DirExists(t, path)
}

func TestTickDoesNotRepeatTheKeptTreeCommentAfterOnePoolReadFails(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.NestRepo(path)
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	require.ErrorContains(t, d.Run("tick").Err, "untracked nested/")
	d.Advance(time.Second)
	d.Tick()
	require.Len(t, d.Linear.Comments(), 2)

	// FailTrees fails only the next read of the pool.
	d.tools.Treehouse.FailTrees(errors.New("treehouse is busy"))
	d.Advance(time.Minute)
	d.Tick()
	d.Advance(time.Minute)
	d.Tick()

	assert.Len(t, d.Linear.Comments(), 2)
}

func assignedTo(t fakeTicket, user string) fakeTicket {
	t.Assignee = user
	return t
}

func withoutLabels(t fakeTicket) fakeTicket {
	t.Labels = nil
	return t
}

// teardownReceipts lists the receipts of one action kind dispatch wrote for the ticket.
func teardownReceipts(d *Dispatch, ticket, kind string) []Receipt {
	d.t.Helper()
	return slices.DeleteFunc(d.Receipts(ticket), func(r Receipt) bool { return r.Kind != kind })
}

// Clean makes the ticket's tree a clean, pushed clone and returns its path.
func (t *Trees) Clean(ticket string) string { return t.d.gitTree(ticket) }

// Leased holds the tree for the ticket with no workspace on it.
func (t *Trees) Leased(ticket, path string) { t.d.lease(ticket, path) }

// Dirty stages a new file in the tree.
func (t *Trees) Dirty(path string) { t.d.dirty(path) }

// Leave puts a file git has never seen into the tree.
func (t *Trees) Leave(path, name string) { t.d.untracked(path, name) }

// NestRepo leaves a git repository the tree does not track, which git clean will not remove.
func (t *Trees) NestRepo(path string) {
	require.NoError(t.d.t, os.MkdirAll(filepath.Join(path, "nested"), 0o755))
	t.d.git(filepath.Join(path, "nested"), "init", "--initial-branch=main")
}

// Commit adds a commit that no remote has.
func (t *Trees) Commit(path string) { t.d.commit(path) }

// Push gives the remote the tree's commits.
func (t *Trees) Push(path string) { t.d.push(path) }

// Git runs git in dir and requires it to succeed.
func (t *Trees) Git(dir string, args ...string) { t.d.git(dir, args...) }

// Holder is the ticket the pool says holds the tree.
func (t *Trees) Holder(path string) string { return t.d.tools.Treehouse.Holder(path) }

// RunningAs is Running for a tree at a chosen path and a workspace with a chosen id.
func (w *Workers) RunningAs(ticket, tree, id, status string) {
	w.d.workerOn(ticket, tree, id, status)
}

// FailComments makes Linear refuse comments while on is true.
func (l *Linear) FailComments(on bool) { l.d.linear.failComments(on) }

// FailLookups makes Linear refuse to list the tickets that hold trees.
func (l *Linear) FailLookups() { l.d.linear.failLookups() }

// AllowLookups undoes FailLookups.
func (l *Linear) AllowLookups() { l.d.linear.allowLookups() }

// Lookups counts the times dispatch asked Linear which tickets hold trees.
func (l *Linear) Lookups() int { return l.d.linear.lookups() }

func TestTickKeepsATreeUnreadWhenTheLeaseLookupFailsAfterTheTicketReopened(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.NestRepo(path)
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	require.ErrorContains(t, d.Run("tick").Err, "untracked nested/")
	require.Empty(t, d.Trees.Returned())
	require.NoError(t, os.RemoveAll(filepath.Join(path, "nested")))

	// The ticket reopened, but the lookup that would say so failed. The Done holder from the last
	// good lookup must not decide for a ticket that may have moved.
	d.Linear.Finished(ticket("ENG-1", "Todo", "dispatch"))
	d.Linear.FailLookups()
	d.Tick()

	assert.Empty(t, d.Trees.Returned())
}

func TestExplainShowsTheCommentAndTheReturnOfABacklogTicketsDirtyTree(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.Dirty(path)
	d.Trees.Leased("ENG-1", path)
	d.Linear.Finished(ticket("ENG-1", "Backlog", "dispatch"))

	d.Tick()
	d.Tick()

	assert.Equal(t, []string{d.Repo + "|" + path + "|ENG-1"}, d.Trees.Returned())
	comments := d.Linear.Comments()
	require.Len(t, comments, 1)
	assert.Contains(t, comments[0], "Branch `thrawn01/eng-1` and its commits stay.")
	assert.Contains(t, comments[0], "It discards uncommitted changes to tracked files.")
	res := d.Run("explain", "ENG-1")
	require.NoError(t, res.Err)
	assert.Regexp(t, `comment-discarded-work  wants comment`, res.Stdout)
	assert.Regexp(t, `teardown-clean-tree  wants return`, res.Stdout)
	assert.Regexp(t, `receipt  return ENG-1 done .*: returned .*, discarded uncommitted changes`, res.Stdout)
}

func TestTickReturnsTheDirtyTreeOfATicketNoWorkerRunsIn(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"Backlog", "Todo", "Needs Human"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{})
			path := d.Trees.Clean("ENG-1")
			d.Trees.Dirty(path)
			d.Trees.Leave(path, "scratch.txt")
			d.Trees.Leased("ENG-1", path)
			d.Linear.Finished(ticket("ENG-1", status, "dispatch"))

			d.Tick()
			d.Tick()

			assert.Equal(t, []string{d.Repo + "|" + path + "|ENG-1"}, d.Trees.Returned())
			assert.NoFileExists(t, filepath.Join(path, "scratch.txt"))
			comments := d.Linear.Comments()
			require.Len(t, comments, 1)
			assert.Contains(t, comments[0], "Branch `thrawn01/eng-1` and its commits stay.")
			assert.Contains(t, comments[0], "It discards uncommitted changes to tracked files and untracked files (scratch.txt).")
		})
	}
}

func TestTickReturnsTheTreeOfAnUnfinishedTicketWhoseCommitsNoRemoteHasWithoutAComment(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.Commit(path)
	d.Trees.Leased("ENG-1", path)
	d.Linear.Finished(ticket("ENG-1", "Backlog", "dispatch"))

	d.Tick()

	assert.Equal(t, []string{d.Repo + "|" + path + "|ENG-1"}, d.Trees.Returned())
	assert.Empty(t, d.Linear.Comments())
}

func TestTickKeepsTheDirtyTreeOfANeedsHumanTicketWhileItsWorkerRuns(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"blocked", "working", "idle"} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{})
			path := d.Trees.Clean("ENG-1")
			d.Trees.Dirty(path)
			d.Trees.Leave(path, "scratch.txt")
			d.Workers.Running("ENG-1", state)
			d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))

			d.Tick()
			d.Tick()

			assert.FileExists(t, filepath.Join(path, "scratch.txt"))
			assert.Empty(t, d.Workers.Closed())
			assert.Empty(t, d.Trees.Returned())
			assert.Empty(t, teardownComments(d))
		})
	}
}

func TestTickKeepsTheDirtyTreeOfATodoChildOfAnInProgressParent(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(parent("ENG-1", "In Progress"))
	d.setChildren(child("ENG-2", "ENG-1", "Todo"))
	path := d.Trees.Clean("ENG-2")
	d.Trees.Dirty(path)
	d.Trees.Leased("ENG-2", path)
	d.Linear.Finished(child("ENG-2", "ENG-1", "Todo"))

	d.Tick()
	d.Tick()

	assert.Empty(t, d.Trees.Returned())
	assert.Empty(t, teardownComments(d))
	assert.Equal(t, "ENG-2", d.Trees.Holder(path))
}

func TestTickKeepsTheDirtyTreeOfAnInProgressOrInReviewTicketWithNoWorker(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"In Progress", "In Review"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{})
			path := d.Trees.Clean("ENG-1")
			d.Trees.Dirty(path)
			d.Trees.Leased("ENG-1", path)
			d.Linear.Tickets(ticket("ENG-1", status, "dispatch"))

			d.Tick()
			d.Tick()

			assert.Empty(t, d.Trees.Returned())
			assert.Empty(t, teardownComments(d))
		})
	}
}

// teardownComments lists the comments that name a tree to be returned.
func teardownComments(d *Dispatch) []string {
	return slices.DeleteFunc(d.Linear.Comments(), func(c string) bool { return !strings.Contains(c, "is returning") })
}
