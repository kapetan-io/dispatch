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

func TestTickKeepsTheTreeOfAFinishedTicketThatHoldsWork(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		strand func(d *Dispatch, path string)
		work   string
	}{
		{
			name:   "UncommittedFile",
			strand: func(d *Dispatch, path string) { d.Trees.Dirty(path) },
			work:   "uncommitted changes",
		},
		{
			name:   "UnpushedCommit",
			strand: func(d *Dispatch, path string) { d.Trees.Commit(path) },
			work:   "1 commit that no remote has",
		},
		{
			name: "BothKinds",
			strand: func(d *Dispatch, path string) {
				d.Trees.Commit(path)
				d.Trees.Commit(path)
				d.Trees.Dirty(path)
			},
			work: "uncommitted changes and 2 commits that no remote has",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			path := d.Trees.Clean("ENG-1")
			test.strand(d, path)
			d.Workers.Running("ENG-1", "idle")
			d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

			d.Tick()

			assert.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())
			assert.Empty(t, d.Trees.Returned())
			assert.Empty(t, d.Linear.Moves())
			comments := d.Linear.Comments()
			require.Len(t, comments, 1)
			assert.True(t, strings.HasPrefix(comments[0], "ENG-1: "))
			assert.Contains(t, comments[0], "`"+path+"` has "+test.work+".")
			assert.Contains(t, comments[0], "kept this ticket's tree leased")

			// Later ticks neither comment again nor close anything else.
			d.Tick()
			d.Tick()
			assert.Len(t, d.Linear.Comments(), 1)
			assert.Len(t, d.Workers.Closed(), 1)
			assert.Empty(t, d.Trees.Returned())
		})
	}
}

func TestTickRemovesUntrackedFilesBeforeReturningTheTree(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "idle")
	path := d.Trees.Untracked("ENG-1", "zz_tmp_test.go")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	d.Tick()

	assert.NoFileExists(t, filepath.Join(path, "zz_tmp_test.go"))
	assert.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())
	assert.Equal(t, []string{d.Repo + "|" + path + "|ENG-1"}, d.Trees.Returned())
	assert.Empty(t, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
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
	assert.Equal(t, "kept: untracked nested/ in "+path+" survived git clean", returns[0].Detail)
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

func TestTickKeepsTheTreeAndItsUntrackedFilesWhenTrackedWorkIsAlsoThere(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.Dirty(path)
	d.Trees.Leave(path, "scratch.txt")
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	d.Tick()

	assert.FileExists(t, filepath.Join(path, "scratch.txt"))
	assert.Empty(t, d.Trees.Returned())
	comments := d.Linear.Comments()
	require.Len(t, comments, 1)
	assert.Contains(t, comments[0], "`"+path+"` has uncommitted changes.")
}

func TestTickKeepsTheTreeWhenATrackedFileIsModifiedButNotStaged(t *testing.T) {
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

	assert.FileExists(t, filepath.Join(path, "scratch.txt"))
	assert.Empty(t, d.Trees.Returned())
	comments := d.Linear.Comments()
	require.Len(t, comments, 1)
	assert.Contains(t, comments[0], "`"+path+"` has uncommitted changes.")
}

func TestTickReturnsTheTreeOnceItsWorkIsPushed(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.Commit(path)
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	d.Tick()
	require.Empty(t, d.Trees.Returned())

	d.Trees.Push(path)
	d.Tick()

	assert.Len(t, d.Trees.Returned(), 1)
	assert.Len(t, d.Linear.Comments(), 1)
	returns := teardownReceipts(d, "ENG-1", "return")
	require.Len(t, returns, 1)
	assert.Equal(t, "done", returns[0].State)
}

func TestTickRetriesADirtyTreeCommentThatLinearRefused(t *testing.T) {
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

	// The comment never landed, so the next tick tries it again.
	d.Linear.FailComments(false)
	d.Tick()

	assert.Len(t, d.Workers.Closed(), 1)
	comments := d.Linear.Comments()
	require.Len(t, comments, 1)
	assert.Contains(t, comments[0], "`"+path+"` has uncommitted changes.")

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
	plan := d.Run("plan")
	require.NoError(t, plan.Err)
	assert.Regexp(t, `(?m)^held .*teardown-clean-tree`, plan.Stdout)
}

func TestTickKeepsTheTreeWhenTheWorkspaceWillNotClose(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Trees.Clean("ENG-1")
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	d.tools.Herdr.Fail(herdrtest.OpCloseWorkspace, errors.New("exit status 1"))

	res := d.Run("tick")

	require.ErrorContains(t, res.Err, "while closing herdr workspace w-ENG-1")
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

func TestTickTearsDownOneTreeAndKeepsAnotherOfTheSameTicket(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	clean := d.Trees.Clean("ENG-1")
	stranded := d.Trees.Clean("library")
	d.Trees.Dirty(stranded)
	d.Workers.RunningAs("ENG-1", clean, "w-ENG-1", "idle")
	d.Workers.RunningAs("ENG-1", stranded, "w-library", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	d.Tick()

	assert.ElementsMatch(t, []string{"w-ENG-1", "w-library"}, d.Workers.Closed())
	assert.Equal(t, []string{d.Repo + "|" + clean + "|ENG-1"}, d.Trees.Returned())
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

func TestTickKeepsTheDirtyTreeOfADoneReviewTicketWithOneComment(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.Dirty(path)
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(withLabel(ticket("ENG-1", "Done", "dispatch"), "design-review"))

	d.Tick()

	assert.Empty(t, d.Trees.Returned())
	comments := d.Linear.Comments()
	require.Len(t, comments, 1)
	assert.Contains(t, comments[0], "`"+path+"` has uncommitted changes.")

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

func TestTickStopsTheWorkerOfAPausedTicketAndKeepsItsTree(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"Todo", "Backlog"} {
		t.Run(status, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			path := d.Trees.Clean("ENG-1")
			d.Trees.Dirty(path)
			d.Workers.Running("ENG-1", "working")
			d.Linear.Finished(ticket("ENG-1", status, "dispatch"))

			d.Tick()

			assert.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())
			assert.Empty(t, d.Trees.Returned())
			assert.Empty(t, d.Linear.Moves())
			assert.Empty(t, d.Linear.Comments())
			assert.Empty(t, d.Workers.Launched())
			assert.FileExists(t, filepath.Join(path, "wip.txt"))

			// The lease stays, and nothing more happens while the ticket is paused.
			d.Tick()
			assert.Len(t, d.Workers.Closed(), 1)
			assert.Empty(t, d.Trees.Returned())
		})
	}
}

func TestTickLeavesAWorkspaceOpenedByHandOnAPausedTicket(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Trees.Clean("ENG-1")
	// A workspace with no Claude agent is not a worker, so it may be yours.
	d.Workers.Running("ENG-1", "")
	d.Linear.Finished(ticket("ENG-1", "Todo", "dispatch"))

	d.Tick()

	assert.Empty(t, d.Workers.Closed())
}

func TestTickLeavesAWorkspaceOpenedByHandOnAKeptTree(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.Dirty(path)
	d.Workers.Running("ENG-1", "")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	d.Tick()

	// The comment is posted once, and the workspace stays for the next tick too.
	d.Tick()

	assert.Empty(t, d.Workers.Closed())
	assert.Len(t, d.Linear.Comments(), 1)
}

func TestTickDoesNotRepeatTheCommentAfterALeaseLookupFails(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.Dirty(path)
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	d.Tick()
	require.Len(t, d.Linear.Comments(), 1)

	// A tick that can't tell who holds a lease holds the tree's rules, and the receipt still stands.
	d.Linear.FailLookups()
	d.Tick()
	d.Linear.AllowLookups()
	d.Tick()

	assert.Len(t, d.Linear.Comments(), 1)
}

func TestTickDoesNotRepeatTheCommentAfterAPoolCannotBeRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.Dirty(path)
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	d.Tick()
	require.Len(t, d.Linear.Comments(), 1)

	// A second repo whose pool fails leaves the first one's trees readable.
	d.addRepo("tiger", filepath.Join(d.dir, "moved"))
	d.Restart()
	d.Tick()
	// Dropping the extra repo makes the next start read every pool again.
	d.resetRepos()
	d.Restart()
	d.Tick()

	assert.Len(t, d.Linear.Comments(), 1)
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

func TestTickRelaunchesAPausedTicketOnItsKeptTree(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Workers.Running("ENG-1", "working")
	d.Linear.Finished(ticket("ENG-1", "Todo", "dispatch"))
	d.Tick()
	require.Empty(t, d.Workers.Launched())

	d.Linear.Finished()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()

	assert.Len(t, d.Workers.Launched(), 1)
	assert.Empty(t, d.Trees.Returned())
	assert.Equal(t, "ENG-1", d.Trees.Holder(path))
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
	d.Trees.Clean("ENG-2")
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

	assert.Equal(t, []string{d.Repo + "|" + first + "|ENG-1"}, d.Trees.Returned())
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
		if d.tools.Herdr.HasWorkspace("w-ENG-1") {
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

	snap, err := d.tools.Herdr.Read(t.Context())
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

func TestTickKeepsTheTreeOfWorkWrittenWhileTheWorkspaceClosed(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Workers.Running("ENG-1", "working")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	// The cycle saw a clean tree, and the worker writes a file as it is killed.
	d.tools.Herdr.BeforeCloseWorkspace(func(string) {
		require.NoError(t, os.WriteFile(filepath.Join(path, "late.txt"), []byte("late\n"), 0o644))
		require.NoError(t, exec.Command("git", "-C", path, "add", "late.txt").Run())
	})

	res := d.Run("tick")

	require.ErrorContains(t, res.Err, "the worker left uncommitted changes")
	assert.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())
	assert.Empty(t, d.Trees.Returned())
	assert.FileExists(t, filepath.Join(path, "late.txt"))
	returns := teardownReceipts(d, "ENG-1", "return")
	require.Len(t, returns, 1)
	assert.Equal(t, "refused", returns[0].State)
	assert.Equal(t, "kept: the worker left uncommitted changes in "+path, returns[0].Detail)

	// The next tick sees the work and posts the one comment. A tick reports the receipts written
	// since it asked, and the fake clock only moves when told.
	d.Advance(time.Second)
	d.Tick()
	comments := d.Linear.Comments()
	require.Len(t, comments, 1)
	assert.Contains(t, comments[0], "`"+path+"` has uncommitted changes.")
	assert.Empty(t, d.Trees.Returned())
	assert.Len(t, d.Workers.Closed(), 1)

	// Pushing the work frees the slot, though the earlier return was refused.
	d.Trees.Git(path, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-m", "late work")
	d.Trees.Git(path, "push", "origin", "main")
	d.Advance(time.Second)
	d.Tick()
	assert.Equal(t, []string{d.Repo + "|" + path + "|ENG-1"}, d.Trees.Returned())
}

func TestTickCommentsOnADirtyTreeThatHasNoWorkspace(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.Dirty(path)
	d.Trees.Leased("ENG-1", path)
	d.Linear.Finished(ticket("ENG-1", "Canceled", "dispatch"))

	d.Tick()

	assert.Empty(t, d.Workers.Closed())
	assert.Empty(t, d.Trees.Returned())
	assert.Len(t, d.Linear.Comments(), 1)

	d.Tick()
	assert.Len(t, d.Linear.Comments(), 1)
}

func TestTickKeepsATreeWhoseCommitsNoRemoteHas(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	// A tree with no remote at all has nowhere its commits could be pushed.
	path := filepath.Join(d.dir, "trees", "ENG-1")
	require.NoError(t, os.MkdirAll(path, 0o755))
	d.Trees.Git(path, "init", "--initial-branch=main")
	d.Trees.Commit(path)
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	d.Tick()

	assert.Empty(t, d.Trees.Returned())
	comments := d.Linear.Comments()
	require.Len(t, comments, 1)
	assert.Contains(t, comments[0], "`"+path+"` has 1 commit that no remote has.")
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

func TestTickStopsEveryWorkerOfAPausedTicket(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	first := d.Trees.Clean("ENG-1")
	second := d.Trees.Clean("library")
	d.Workers.RunningAs("ENG-1", first, "w-ENG-1", "working")
	d.Workers.RunningAs("ENG-1", second, "w-library", "idle")
	d.Linear.Finished(ticket("ENG-1", "Backlog", "dispatch"))

	d.Tick()

	assert.ElementsMatch(t, []string{"w-ENG-1", "w-library"}, d.Workers.Closed())
	assert.Empty(t, d.Trees.Returned())
}

func TestTickDoesNothingForAPausedTicketWithNoWorkspace(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Trees.Leased("ENG-1", path)
	d.Linear.Finished(ticket("ENG-1", "Todo", "dispatch"))

	out := d.Tick()

	assert.Empty(t, out)
	assert.Empty(t, d.Workers.Closed())
	assert.Empty(t, d.Trees.Returned())
}

func TestTickStopsTheWorkerOfAPausedReviewTicketAndKeepsItsTree(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Trees.Clean("ENG-1")
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(withLabel(ticket("ENG-1", "Backlog", "dispatch"), "design-review"))

	d.Tick()

	assert.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())
	assert.Empty(t, d.Trees.Returned())
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

func TestTickPostsACommentForEachKeptTree(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	first := d.Trees.Clean("ENG-1")
	second := d.Trees.Clean("library")
	d.Trees.Dirty(first)
	d.Trees.Commit(second)
	d.Workers.RunningAs("ENG-1", first, "w-ENG-1", "idle")
	d.Workers.RunningAs("ENG-1", second, "w-library", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	d.Tick()

	assert.ElementsMatch(t, []string{"w-ENG-1", "w-library"}, d.Workers.Closed())
	assert.Empty(t, d.Trees.Returned())
	comments := d.Linear.Comments()
	require.Len(t, comments, 2)
	assert.Contains(t, strings.Join(comments, "\n"), "`"+first+"` has uncommitted changes.")
	assert.Contains(t, strings.Join(comments, "\n"), "`"+second+"` has 1 commit that no remote has.")

	d.Tick()
	assert.Len(t, d.Linear.Comments(), 2)
}

func TestPlanNamesEachKeptTreeOfOneTicket(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	first := d.Trees.Clean("ENG-1")
	second := d.Trees.Clean("library")
	d.Trees.Dirty(first)
	d.Trees.Dirty(second)
	d.Workers.RunningAs("ENG-1", first, "w-ENG-1", "idle")
	d.Workers.RunningAs("ENG-1", second, "w-library", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	d.Tick()

	plan := d.Run("plan")
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
	d.Trees.Commit(path)
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	d.Tick()
	require.Len(t, d.Linear.Comments(), 1)

	d.Tick()
	plan := d.Run("plan")
	require.NoError(t, plan.Err)

	assert.Regexp(t, `(?m)^covered +ENG-1 +comment +comment-kept-tree +done .*`+regexp.QuoteMeta(path), plan.Stdout)
	assert.Len(t, d.Linear.Comments(), 1)
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
	plan := d.Run("plan")
	require.NoError(t, plan.Err)
	assert.Regexp(t, `(?m)^held .*teardown-clean-tree`, plan.Stdout)
	assert.DirExists(t, path)
}

func TestTickKeepsATreeThatGainedACommitAfterTheCycleDecidedToReturnIt(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Workers.Running("ENG-1", "working")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	// The cycle read a clean, pushed tree, and the worker commits as its workspace closes.
	d.tools.Herdr.BeforeCloseWorkspace(func(string) { d.Trees.Commit(path) })

	res := d.Run("tick")

	require.ErrorContains(t, res.Err, "the worker left 1 commit that no remote has")
	assert.Empty(t, d.Trees.Returned())
	returns := teardownReceipts(d, "ENG-1", "return")
	require.Len(t, returns, 1)
	assert.Equal(t, "refused", returns[0].State)
	assert.Equal(t, "kept: the worker left 1 commit that no remote has in "+path, returns[0].Detail)

	// The next tick sees the commit and explains why the tree was kept.
	d.Advance(time.Second)
	d.Tick()
	comments := d.Linear.Comments()
	require.Len(t, comments, 1)
	assert.Contains(t, comments[0], "`"+path+"` has 1 commit that no remote has.")
	assert.Empty(t, d.Trees.Returned())
}

func TestTickKeepsATreeThatGainedANestedRepoAfterTheCycleDecidedToReturnIt(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	path := d.Trees.Clean("ENG-1")
	d.Workers.Running("ENG-1", "working")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	// An untracked repository is work git clean will not remove and returning would discard.
	d.tools.Herdr.BeforeCloseWorkspace(func(string) {
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
	assert.Equal(t, "kept: untracked nested/ in "+path+" survived git clean", returns[0].Detail)

	// While the nested repository stays, the tree is not returned however many ticks follow,
	// and a comment tells the user why it is still leased.
	d.Advance(time.Second)
	d.Tick()
	assert.Empty(t, d.Trees.Returned())
	assert.DirExists(t, filepath.Join(path, "nested"))
	comments := d.Linear.Comments()
	require.Len(t, comments, 1)
	assert.Contains(t, comments[0], "nested/")

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
	d.tools.Herdr.BeforeCloseWorkspace(func(string) {
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
	d.Trees.Dirty(path)
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	d.Tick()
	require.Len(t, d.Linear.Comments(), 1)

	// FailTrees fails only the next read of the pool.
	d.tools.Treehouse.FailTrees(errors.New("treehouse is busy"))
	d.Advance(time.Minute)
	d.Tick()
	d.Advance(time.Minute)
	d.Tick()

	assert.Len(t, d.Linear.Comments(), 1)
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

// Leased holds the tree for the ticket with no herdr workspace on it.
func (t *Trees) Leased(ticket, path string) { t.d.lease(ticket, path) }

// Dirty stages a new file in the tree.
func (t *Trees) Dirty(path string) { t.d.dirty(path) }

// Leave puts a file git has never seen into the tree.
func (t *Trees) Leave(path, name string) { t.d.untracked(path, name) }

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
