package dispatch_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kapetan-io/dispatch/internal/herdrtest"
	"github.com/kapetan-io/dispatch/internal/ledger"
	"github.com/kapetan-io/dispatch/internal/treehouse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/lineartest"
)

func TestTickReturnsTheTreeOfAFinishedTicket(t *testing.T) {
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
			h := newHarness(t)
			path := h.gitTree("ENG-1")
			h.worker("ENG-1", test.worker)
			h.linear.setElsewhere(ticket("ENG-1", test.status, "dispatch"))

			h.tick()

			assert.Equal(t, []string{"w-ENG-1"}, h.closes())
			assert.Equal(t, []string{h.repo + "|" + path + "|ENG-1"}, h.returns())
			assert.Empty(t, h.linear.mutations())
			assert.Empty(t, h.launches())
			returned := teardownReceipts(h, "return")
			require.Len(t, returned, 1)
			assert.Equal(t, ledger.Done, returned[0].Outcome)
			assert.Equal(t, "returned "+path, returned[0].Detail)

			// The tree is back in the pool, so the next tick has nothing left to do.
			h.tick()
			assert.Len(t, h.closes(), 1)
			assert.Len(t, h.returns(), 1)
		})
	}
}

func TestTickReturnsATreeWhoseWorkspaceIsAlreadyClosed(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.lease("ENG-1", path)
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.closes())
	assert.Equal(t, []string{h.repo + "|" + path + "|ENG-1"}, h.returns())
}

func TestTickReturnsATreeWhoseWorkspaceHoldsNoAgent(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	// A worker whose Claude exited leaves its workspace open, and the slot only
	// comes back if teardown closes it.
	h.worker("ENG-1", "")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"w-ENG-1"}, h.closes())
	assert.Equal(t, []string{h.repo + "|" + path + "|ENG-1"}, h.returns())
}

func TestTickKeepsTheTreeOfAFinishedTicketThatHoldsWork(t *testing.T) {
	for _, test := range []struct {
		name   string
		strand func(h *harness, path string)
		work   string
	}{
		{
			name:   "UncommittedFile",
			strand: func(h *harness, path string) { h.dirty(path) },
			work:   "uncommitted changes",
		},
		{
			name:   "UnpushedCommit",
			strand: func(h *harness, path string) { h.commit(path) },
			work:   "1 commit that no remote has",
		},
		{
			name: "BothKinds",
			strand: func(h *harness, path string) {
				h.commit(path)
				h.commit(path)
				h.dirty(path)
			},
			work: "uncommitted changes and 2 commits that no remote has",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			path := h.gitTree("ENG-1")
			test.strand(h, path)
			h.worker("ENG-1", "idle")
			h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))

			h.tick()

			assert.Equal(t, []string{"w-ENG-1"}, h.closes())
			assert.Empty(t, h.returns())
			mutations := h.linear.mutations()
			require.Len(t, mutations, 1)
			assert.Equal(t, lineartest.Comment, mutations[0].Kind)
			assert.Equal(t, "uuid-ENG-1", mutations[0].TicketID)
			assert.Contains(t, mutations[0].Body, "`"+path+"` has "+test.work+".")
			assert.Contains(t, mutations[0].Body, "kept this ticket's tree leased")

			// Later ticks neither comment again nor close anything else.
			h.tick()
			h.tick()
			assert.Len(t, h.linear.mutations(), 1)
			assert.Len(t, h.closes(), 1)
			assert.Empty(t, h.returns())
		})
	}
}

func TestTickRemovesUntrackedFilesBeforeReturningTheTree(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.untracked(path, "zz_tmp_test.go")
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))

	h.tick()

	assert.NoFileExists(t, filepath.Join(path, "zz_tmp_test.go"))
	assert.Equal(t, []string{"w-ENG-1"}, h.closes())
	assert.Equal(t, []string{h.repo + "|" + path + "|ENG-1"}, h.returns())
	assert.Empty(t, h.linear.mutations())
	returned := teardownReceipts(h, "return")
	require.Len(t, returned, 1)
	assert.Equal(t, "returned "+path+", removed untracked zz_tmp_test.go", returned[0].Detail)
}

func TestTickLeavesIgnoredFilesAloneWhenRemovingUntrackedOnes(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	require.NoError(t, os.WriteFile(filepath.Join(path, ".gitignore"), []byte("build/\n"), 0o644))
	h.git(path, "add", ".gitignore")
	h.git(path, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-m", "ignore")
	h.push(path)
	require.NoError(t, os.MkdirAll(filepath.Join(path, "build"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(path, "build", "out.bin"), []byte("x"), 0o644))
	h.untracked(path, "scratch.txt")
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))

	h.tick()

	returned := teardownReceipts(h, "return")
	require.Len(t, returned, 1)
	assert.Equal(t, "returned "+path+", removed untracked scratch.txt", returned[0].Detail)
	assert.FileExists(t, filepath.Join(path, "build", "out.bin"))
	assert.Len(t, h.returns(), 1)
}

func TestTickQuotesUntrackedFileNamesThatHoldSpaces(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.untracked(path, "sp ace.txt")
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))

	h.tick()

	returned := teardownReceipts(h, "return")
	require.Len(t, returned, 1)
	assert.Equal(t, "returned "+path+", removed untracked \"sp ace.txt\"", returned[0].Detail)
	assert.NoFileExists(t, filepath.Join(path, "sp ace.txt"))
}

func TestTickKeepsTheTreeWhenGitCleanLeavesANestedRepoBehind(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	require.NoError(t, os.MkdirAll(filepath.Join(path, "nested"), 0o755))
	h.git(filepath.Join(path, "nested"), "init", "--initial-branch=main")
	h.untracked(path, "scratch.txt")
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))

	_, err := h.run("tick")

	// A kept tree is a refusal the user has to see, not a full pool that frees itself.
	require.ErrorContains(t, err, "untracked nested/")
	assert.NoFileExists(t, filepath.Join(path, "scratch.txt"))
	assert.DirExists(t, filepath.Join(path, "nested"))
	assert.Empty(t, h.returns())
	returned := teardownReceipts(h, "return")
	require.Len(t, returned, 1)
	assert.Equal(t, ledger.Refused, returned[0].Outcome)
	assert.Equal(t, "kept: untracked nested/ in "+path+" survived git clean", returned[0].Detail)
}

func TestTickIgnoresGitLocationVariablesWhenRemovingUntrackedFiles(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.untracked(path, "scratch.txt")
	other := h.gitTree("ENG-2")
	h.untracked(other, "precious.txt")
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	// A dispatcher started from a git hook inherits these, and they would point git at the wrong repository.
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)

	h.tick()

	returned := teardownReceipts(h, "return")
	require.Len(t, returned, 1)
	assert.Equal(t, "returned "+path+", removed untracked scratch.txt", returned[0].Detail)
	assert.NoFileExists(t, filepath.Join(path, "scratch.txt"))
	assert.FileExists(t, filepath.Join(other, "precious.txt"))
}

func TestTickReturnsATreeWhoseOnlyLeftoversAreIgnored(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	require.NoError(t, os.WriteFile(filepath.Join(path, ".gitignore"), []byte("*.log\n"), 0o644))
	h.git(path, "add", ".gitignore")
	h.git(path, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-m", "ignore")
	h.push(path)
	require.NoError(t, os.WriteFile(filepath.Join(path, "run.log"), []byte("x"), 0o644))
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))

	h.tick()

	returned := teardownReceipts(h, "return")
	require.Len(t, returned, 1)
	assert.Equal(t, "returned "+path, returned[0].Detail)
	assert.Len(t, h.returns(), 1)
}

func TestTickKeepsTheTreeAndItsUntrackedFilesWhenTrackedWorkIsAlsoThere(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.dirty(path)
	h.untracked(path, "scratch.txt")
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))

	h.tick()

	assert.FileExists(t, filepath.Join(path, "scratch.txt"))
	assert.Empty(t, h.returns())
	mutations := h.linear.mutations()
	require.Len(t, mutations, 1)
	assert.Contains(t, mutations[0].Body, "`"+path+"` has uncommitted changes.")
}

func TestTickKeepsTheTreeWhenATrackedFileIsModifiedButNotStaged(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.untracked(path, "tracked.txt")
	h.git(path, "add", "tracked.txt")
	h.git(path, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-m", "track")
	h.push(path)
	require.NoError(t, os.WriteFile(filepath.Join(path, "tracked.txt"), []byte("edited"), 0o644))
	h.untracked(path, "scratch.txt")
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))

	h.tick()

	assert.FileExists(t, filepath.Join(path, "scratch.txt"))
	assert.Empty(t, h.returns())
	mutations := h.linear.mutations()
	require.Len(t, mutations, 1)
	assert.Contains(t, mutations[0].Body, "`"+path+"` has uncommitted changes.")
}

func TestTickReturnsTheTreeOnceItsWorkIsPushed(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.commit(path)
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	h.tick()
	require.Empty(t, h.returns())

	h.push(path)
	h.tick()

	assert.Len(t, h.returns(), 1)
	assert.Len(t, h.linear.mutations(), 1)
	returned := teardownReceipts(h, "return")
	require.Len(t, returned, 1)
	assert.Equal(t, ledger.Done, returned[0].Outcome)
}

func TestTickRetriesADirtyTreeCommentThatLinearRefused(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.dirty(path)
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	h.linear.failComments(true)

	_, err := h.run("tick")
	require.ErrorContains(t, err, "while commenting on ENG-1:")
	assert.Equal(t, []string{"w-ENG-1"}, h.closes())
	assert.Empty(t, h.linear.mutations())

	// The comment never landed, so the next tick tries it again.
	h.linear.failComments(false)
	h.tick()

	assert.Len(t, h.closes(), 1)
	mutations := h.linear.mutations()
	require.Len(t, mutations, 1)
	assert.Contains(t, mutations[0].Body, "`"+path+"` has uncommitted changes.")

	h.tick()
	assert.Len(t, h.linear.mutations(), 1)
}

func TestTickKeepsATreeItCannotRead(t *testing.T) {
	h := newHarness(t)
	// The tree's directory is gone, so git can't say whether it held work.
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))

	h.tick()

	assert.Empty(t, h.closes())
	assert.Empty(t, h.returns())
	assert.Empty(t, h.linear.mutations())
	out, err := h.run("plan")
	require.NoError(t, err)
	assert.Regexp(t, `(?m)^held .*teardown-clean-tree`, out)
}

func TestTickKeepsTheTreeWhenTheWorkspaceWillNotClose(t *testing.T) {
	h := newHarness(t)
	h.gitTree("ENG-1")
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	h.tools.Herdr.Fail(herdrtest.OpCloseWorkspace, errors.New("exit status 1"))

	_, err := h.run("tick")

	require.ErrorContains(t, err, "while closing herdr workspace w-ENG-1")
	assert.Empty(t, h.returns())
	returned := teardownReceipts(h, "return")
	require.Len(t, returned, 1)
	assert.Equal(t, ledger.Failed, returned[0].Outcome)
}

func TestTickKeepsTheTreeWhenTreehouseRefusesTheReturn(t *testing.T) {
	h := newHarness(t)
	h.gitTree("ENG-1")
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	// The lease moves to another ticket after dispatch looked at the pool.
	h.tools.Treehouse.MoveLeaseAfterRead(filepath.Join(h.dir, "trees", "ENG-1"), "ENG-2")

	_, err := h.run("tick")

	require.ErrorContains(t, err, "while returning")
	require.ErrorContains(t, err, "lease holder does not match worktree")
	assert.Equal(t, "ENG-2", h.tools.Treehouse.Holder(filepath.Join(h.dir, "trees", "ENG-1")))
	assert.Empty(t, h.linear.mutations())
}

func TestTickTearsDownOneTreeAndKeepsAnotherOfTheSameTicket(t *testing.T) {
	h := newHarness(t)
	clean := h.gitTree("ENG-1")
	stranded := h.gitTree("library")
	h.dirty(stranded)
	h.workerOn("ENG-1", clean, "w-ENG-1", "idle")
	h.workerOn("ENG-1", stranded, "w-library", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"w-ENG-1", "w-library"}, h.closes())
	assert.Equal(t, []string{h.repo + "|" + clean + "|ENG-1"}, h.returns())
	mutations := h.linear.mutations()
	require.Len(t, mutations, 1)
	assert.Contains(t, mutations[0].Body, stranded)
	assert.NotContains(t, mutations[0].Body, clean)
}

func TestTickLeavesTheTreesOfTicketsItDoesNotManage(t *testing.T) {
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
			h := newHarness(t)
			h.gitTree("ENG-1")
			h.worker("ENG-1", "idle")
			h.linear.setElsewhere(test.ticket)

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.Empty(t, out)
			assert.Empty(t, h.closes())
			assert.Empty(t, h.returns())
			assert.Empty(t, h.linear.mutations())
		})
	}
}

func TestTickIgnoresALeaseHeldByNoTicket(t *testing.T) {
	h := newHarness(t)
	h.gitTree("scratch")
	h.worker("scratch", "idle")
	h.linear.setElsewhere()

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Empty(t, h.closes())
	assert.Empty(t, h.returns())
}

func TestTickReturnsTheTreeOfAFinishedTicketWhoseLabelsDidNotFitOnePage(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.worker("ENG-1", "idle")
	cut := ticket("ENG-1", "Done", "dispatch")
	cut.LabelsTruncated = true
	h.linear.setElsewhere(cut)

	h.tick()

	assert.Equal(t, []string{h.repo + "|" + path + "|ENG-1"}, h.returns())
}

func TestTickReturnsTheCleanTreeOfAFinishedReviewTicket(t *testing.T) {
	for _, status := range []string{"Done", "Canceled"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			path := h.gitTree("ENG-1")
			h.worker("ENG-1", "idle")
			h.linear.setElsewhere(withLabel(ticket("ENG-1", status, "dispatch"), "design-review"))

			h.tick()

			assert.Equal(t, []string{"w-ENG-1"}, h.closes())
			assert.Equal(t, []string{h.repo + "|" + path + "|ENG-1"}, h.returns())
		})
	}
}

func TestTickKeepsTheDirtyTreeOfADoneReviewTicketWithOneComment(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.dirty(path)
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(withLabel(ticket("ENG-1", "Done", "dispatch"), "design-review"))

	h.tick()

	assert.Empty(t, h.returns())
	mutations := h.linear.mutations()
	require.Len(t, mutations, 1)
	assert.Contains(t, mutations[0].Body, "`"+path+"` has uncommitted changes.")

	h.tick()
	assert.Len(t, h.linear.mutations(), 1)
}

func TestTickAsksLinearAboutLeasesOnlyWhenAManagedTicketDoesNotHoldThem(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-1", "working")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Zero(t, h.linear.lookups())
}

func TestTickStopsTheWorkerOfAPausedTicketAndKeepsItsTree(t *testing.T) {
	for _, status := range []string{"Todo", "Backlog"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			path := h.gitTree("ENG-1")
			h.dirty(path)
			h.worker("ENG-1", "working")
			h.linear.setElsewhere(ticket("ENG-1", status, "dispatch"))

			h.tick()

			assert.Equal(t, []string{"w-ENG-1"}, h.closes())
			assert.Empty(t, h.returns())
			assert.Empty(t, h.linear.mutations())
			assert.Empty(t, h.launches())
			assert.FileExists(t, filepath.Join(path, "wip.txt"))

			// The lease stays, and nothing more happens while the ticket is paused.
			h.tick()
			assert.Len(t, h.closes(), 1)
			assert.Empty(t, h.returns())
		})
	}
}

func TestTickLeavesAWorkspaceOpenedByHandOnAPausedTicket(t *testing.T) {
	h := newHarness(t)
	h.gitTree("ENG-1")
	// A workspace with no Claude agent is not a worker, so it may be yours.
	h.worker("ENG-1", "")
	h.linear.setElsewhere(ticket("ENG-1", "Todo", "dispatch"))

	h.tick()

	assert.Empty(t, h.closes())
}

func TestTickLeavesAWorkspaceOpenedByHandOnAKeptTree(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.dirty(path)
	h.worker("ENG-1", "")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	h.tick()

	// The comment is posted once, and the workspace stays for the next tick too.
	h.tick()

	assert.Empty(t, h.closes())
	assert.Len(t, h.linear.mutations(), 1)
}

func TestTickDoesNotRepeatTheCommentAfterALeaseLookupFails(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.dirty(path)
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	h.tick()
	require.Len(t, h.linear.mutations(), 1)

	// A tick that can't tell who holds a lease holds the tree's rules, and the receipt still stands.
	h.linear.failLookups()
	h.tick()
	h.linear.allowLookups()
	h.tick()

	assert.Len(t, h.linear.mutations(), 1)
}

func TestTickDoesNotRepeatTheCommentAfterAPoolCannotBeRead(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.dirty(path)
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	h.tick()
	require.Len(t, h.linear.mutations(), 1)

	// A second repo whose pool fails leaves the first one's trees readable.
	h.addRepo("tiger", filepath.Join(h.dir, "moved"))
	h.restart()
	h.tick()
	// Dropping the extra repo makes the next start read every pool again.
	h.resetRepos()
	h.restart()
	h.tick()

	assert.Len(t, h.linear.mutations(), 1)
}

func TestTickReturnsATreeWhileAnotherBranchOfTheRepoHoldsUnpushedWork(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	// Pool trees share one repository, so a branch another ticket is still
	// working on is no reason to keep this ticket's tree.
	h.git(path, "branch", "other")
	h.git(path, "checkout", "other")
	h.commit(path)
	h.git(path, "checkout", "--detach", "main")
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))

	h.tick()

	assert.Equal(t, []string{h.repo + "|" + path + "|ENG-1"}, h.returns())
}

func TestTickKeepsTickingWhenLinearCannotListTheTicketsHoldingTrees(t *testing.T) {
	h := newHarness(t)
	h.gitTree("ENG-1")
	h.worker("ENG-1", "idle")
	h.tickets(ticket("ENG-2", "In Progress", "dispatch"))
	h.linear.failLookups()

	h.tick()

	assert.Len(t, h.launches(), 1)
	assert.Contains(t, h.loopErr(), "dispatch: while reading the tickets that hold trees: linear LeasedTickets")
	assert.Empty(t, h.returns())
}

func TestTickRelaunchesAPausedTicketOnItsKeptTree(t *testing.T) {
	h := newHarness(t)
	h.gitTree("ENG-1")
	h.worker("ENG-1", "working")
	h.linear.setElsewhere(ticket("ENG-1", "Todo", "dispatch"))
	h.tick()
	require.Empty(t, h.launches())

	h.linear.setElsewhere()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.tick()

	assert.Len(t, h.launches(), 1)
	assert.Empty(t, h.returns())
	assert.Equal(t, "ENG-1", h.tools.Treehouse.Holder(filepath.Join(h.dir, "trees", "ENG-1")))
}

func TestTickDoesNotTouchTreesOfTicketsInReviewOrNeedsHuman(t *testing.T) {
	h := newHarness(t)
	h.gitTree("ENG-1")
	h.gitTree("ENG-2")
	h.worker("ENG-1", "working")
	h.worker("ENG-2", "working")
	h.tickets(
		ticket("ENG-1", "In Review", "dispatch"),
		ticket("ENG-2", "Needs Human", "dispatch"),
	)

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.closes())
	assert.Empty(t, h.returns())
}

func TestTickTeardownFreesATreeForALaunchInTheSameTick(t *testing.T) {
	h := newHarness(t)
	h.gitTree("ENG-1")
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	h.tickets(ticket("ENG-2", "In Progress", "dispatch"))

	h.tick()

	assert.Len(t, h.returns(), 1)
	// A return changes the launch rule's once key, and the fake launcher opens no workspace, so
	// the launch runs again after it. A real launch would leave a worker behind.
	assert.NotEmpty(t, h.launches())
}

func TestTickReadsDoneAndPausedStatusesFromConfig(t *testing.T) {
	h := newHarness(t)
	h.config("[plugins.linear.statuses]\nfinished = [\"Shipped\"]\npaused = [\"Parked\"]\n")
	h.tools.Linear.AddState("Shipped", "completed")
	h.tools.Linear.AddState("Parked", "backlog")
	first := h.gitTree("ENG-1")
	h.gitTree("ENG-2")
	h.gitTree("ENG-3")
	h.worker("ENG-1", "idle")
	h.worker("ENG-2", "idle")
	h.worker("ENG-3", "idle")
	// ENG-3 is Done, a default status the config replaced, so it is left alone.
	h.linear.setElsewhere(
		ticket("ENG-1", "Shipped", "dispatch"),
		ticket("ENG-2", "Parked", "dispatch"),
		ticket("ENG-3", "Done", "dispatch"),
	)

	h.tick()

	assert.Equal(t, []string{h.repo + "|" + first + "|ENG-1"}, h.returns())
	assert.ElementsMatch(t, []string{"w-ENG-1", "w-ENG-2"}, h.closes())
}

func TestTickClosesTheWorkspaceBeforeItReturnsTheTree(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	// A worker still running in the tree could write to it after the return.
	var order []string
	h.tools.Treehouse.BeforeReturn(func(string) {
		if h.tools.Herdr.HasWorkspace("w-ENG-1") {
			order = append(order, "open")
		} else {
			order = append(order, "closed")
		}
	})

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"closed"}, order)
	assert.Equal(t, []string{h.repo + "|" + path + "|ENG-1"}, h.returns())
}

func TestTickLeavesNoTabPaneOrAgentOfAClosedWorkspace(t *testing.T) {
	h := newHarness(t)
	h.gitTree("ENG-1")
	h.worker("ENG-1", "idle")
	h.worker("ENG-2", "working")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))

	_, err := h.run("tick")
	require.NoError(t, err)

	snap, err := h.tools.Herdr.Read(t.Context())
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
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.worker("ENG-1", "working")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	// The cycle saw a clean tree, and the worker writes a file as it is killed.
	h.tools.Herdr.BeforeCloseWorkspace(func(string) {
		require.NoError(t, os.WriteFile(filepath.Join(path, "late.txt"), []byte("late\n"), 0o644))
		require.NoError(t, exec.Command("git", "-C", path, "add", "late.txt").Run())
	})

	_, err := h.run("tick")

	require.ErrorContains(t, err, "the worker left uncommitted changes")
	assert.Equal(t, []string{"w-ENG-1"}, h.closes())
	assert.Empty(t, h.returns())
	assert.FileExists(t, filepath.Join(path, "late.txt"))
	returned := teardownReceipts(h, "return")
	require.Len(t, returned, 1)
	assert.Equal(t, ledger.Refused, returned[0].Outcome)
	assert.Equal(t, "kept: the worker left uncommitted changes in "+path, returned[0].Detail)

	// The next tick sees the work and posts the one comment. A tick reports the receipts written
	// since it asked, and the fake clock only moves when told.
	h.advance(time.Second)
	_, err = h.run("tick")
	require.NoError(t, err)
	mutations := h.linear.mutations()
	require.Len(t, mutations, 1)
	assert.Contains(t, mutations[0].Body, "`"+path+"` has uncommitted changes.")
	assert.Empty(t, h.returns())
	assert.Len(t, h.closes(), 1)
}

func TestTickCommentsOnADirtyTreeThatHasNoWorkspace(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.dirty(path)
	h.lease("ENG-1", path)
	h.linear.setElsewhere(ticket("ENG-1", "Canceled", "dispatch"))

	h.tick()

	assert.Empty(t, h.closes())
	assert.Empty(t, h.returns())
	assert.Len(t, h.linear.mutations(), 1)

	h.tick()
	assert.Len(t, h.linear.mutations(), 1)
}

func TestTickKeepsATreeWhoseCommitsNoRemoteHas(t *testing.T) {
	h := newHarness(t)
	// A tree with no remote at all has nowhere its commits could be pushed.
	path := filepath.Join(h.dir, "trees", "ENG-1")
	require.NoError(t, os.MkdirAll(path, 0o755))
	h.git(path, "init", "--initial-branch=main")
	h.commit(path)
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))

	h.tick()

	assert.Empty(t, h.returns())
	mutations := h.linear.mutations()
	require.Len(t, mutations, 1)
	assert.Contains(t, mutations[0].Body, "`"+path+"` has 1 commit that no remote has.")
}

func TestTickLeavesPausedTicketsItDoesNotManage(t *testing.T) {
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
			h := newHarness(t)
			h.gitTree("ENG-1")
			h.worker("ENG-1", "idle")
			h.linear.setElsewhere(test.ticket)

			_, err := h.run("tick")
			require.NoError(t, err)

			assert.Empty(t, h.closes())
			assert.Empty(t, h.returns())
			assert.Empty(t, h.linear.mutations())
		})
	}
}

func TestTickStopsEveryWorkerOfAPausedTicket(t *testing.T) {
	h := newHarness(t)
	first := h.gitTree("ENG-1")
	second := h.gitTree("library")
	h.workerOn("ENG-1", first, "w-ENG-1", "working")
	h.workerOn("ENG-1", second, "w-library", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Backlog", "dispatch"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"w-ENG-1", "w-library"}, h.closes())
	assert.Empty(t, h.returns())
}

func TestTickDoesNothingForAPausedTicketWithNoWorkspace(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.lease("ENG-1", path)
	h.linear.setElsewhere(ticket("ENG-1", "Todo", "dispatch"))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Empty(t, h.closes())
	assert.Empty(t, h.returns())
}

func TestTickStopsTheWorkerOfAPausedReviewTicketAndKeepsItsTree(t *testing.T) {
	h := newHarness(t)
	h.gitTree("ENG-1")
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(withLabel(ticket("ENG-1", "Backlog", "dispatch"), "design-review"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"w-ENG-1"}, h.closes())
	assert.Empty(t, h.returns())
}

func TestTickCommentsAgainWhenAReopenedTicketFinishesWithWorkInItsTree(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.dirty(path)
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	h.tick()
	require.Len(t, h.linear.mutations(), 1)

	// Reopened, the ticket is managed again and its old warning no longer applies.
	h.advance(time.Minute)
	h.linear.setElsewhere()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.lease("ENG-1", path)
	h.tick()
	h.advance(time.Minute)
	h.tickets()
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	h.tick()

	assert.Len(t, h.linear.mutations(), 2)
}

func TestTickPostsACommentForEachKeptTree(t *testing.T) {
	h := newHarness(t)
	first := h.gitTree("ENG-1")
	second := h.gitTree("library")
	h.dirty(first)
	h.commit(second)
	h.workerOn("ENG-1", first, "w-ENG-1", "idle")
	h.workerOn("ENG-1", second, "w-library", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))

	h.tick()

	assert.ElementsMatch(t, []string{"w-ENG-1", "w-library"}, h.closes())
	assert.Empty(t, h.returns())
	var bodies []string
	for _, m := range h.linear.mutations() {
		bodies = append(bodies, m.Body)
	}
	require.Len(t, bodies, 2)
	assert.Contains(t, strings.Join(bodies, "\n"), "`"+first+"` has uncommitted changes.")
	assert.Contains(t, strings.Join(bodies, "\n"), "`"+second+"` has 1 commit that no remote has.")

	h.tick()
	assert.Len(t, h.linear.mutations(), 2)
}

func TestPlanNamesEachKeptTreeOfOneTicket(t *testing.T) {
	h := newHarness(t)
	first := h.gitTree("ENG-1")
	second := h.gitTree("library")
	h.dirty(first)
	h.dirty(second)
	h.workerOn("ENG-1", first, "w-ENG-1", "idle")
	h.workerOn("ENG-1", second, "w-library", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	h.tick()

	out, err := h.run("plan")
	require.NoError(t, err)

	// The comment of each tree carries its path in the once key, so the two do not cover each other.
	var covered int
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "covered") && strings.Contains(line, "comment-kept-tree") {
			covered++
			assert.True(t, strings.Contains(line, first) != strings.Contains(line, second))
		}
	}
	assert.Equal(t, 2, covered)
}

func TestPlanListsTheCommentOfAKeptTreeAsCoveredOnEveryTick(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.commit(path)
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	h.tick()
	require.Len(t, h.linear.mutations(), 1)

	h.tick()
	out, err := h.run("plan")
	require.NoError(t, err)

	assert.Regexp(t, `(?m)^covered +ENG-1 +comment +comment-kept-tree +done .*`+regexp.QuoteMeta(path), out)
	assert.Len(t, h.linear.mutations(), 1)
}

func TestTickKeepsATreeWhoseGitStateIsBroken(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	// Git fails in the tree, which is unknown, not clean.
	require.NoError(t, os.WriteFile(filepath.Join(path, ".git", "HEAD"), []byte("garbage\n"), 0o644))

	h.tick()

	assert.Empty(t, h.closes())
	assert.Empty(t, h.returns())
	assert.Empty(t, h.linear.mutations())
	assert.Empty(t, teardownReceipts(h, "return"))
	out, err := h.run("plan")
	require.NoError(t, err)
	assert.Regexp(t, `(?m)^held .*teardown-clean-tree`, out)
	assert.DirExists(t, path)
}

func TestTickKeepsATreeThatGainedACommitAfterTheCycleDecidedToReturnIt(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.worker("ENG-1", "working")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	// The cycle read a clean, pushed tree, and the worker commits as its workspace closes.
	h.tools.Herdr.BeforeCloseWorkspace(func(string) { h.commit(path) })

	_, err := h.run("tick")

	require.ErrorContains(t, err, "the worker left 1 commit that no remote has")
	assert.Empty(t, h.returns())
	returned := teardownReceipts(h, "return")
	require.Len(t, returned, 1)
	assert.Equal(t, ledger.Refused, returned[0].Outcome)
	assert.Equal(t, "kept: the worker left 1 commit that no remote has in "+path, returned[0].Detail)

	// The next tick sees the commit and explains why the tree was kept.
	h.advance(time.Second)
	_, err = h.run("tick")
	require.NoError(t, err)
	mutations := h.linear.mutations()
	require.Len(t, mutations, 1)
	assert.Contains(t, mutations[0].Body, "`"+path+"` has 1 commit that no remote has.")
	assert.Empty(t, h.returns())
}

func TestTickKeepsATreeThatGainedANestedRepoAfterTheCycleDecidedToReturnIt(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.worker("ENG-1", "working")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	// An untracked repository is work git clean will not remove and returning would discard.
	h.tools.Herdr.BeforeCloseWorkspace(func(string) {
		require.NoError(t, os.MkdirAll(filepath.Join(path, "nested"), 0o755))
		h.git(filepath.Join(path, "nested"), "init", "--initial-branch=main")
	})

	_, err := h.run("tick")

	require.ErrorContains(t, err, "untracked nested/")
	assert.Empty(t, h.returns())
	assert.DirExists(t, filepath.Join(path, "nested"))
	returned := teardownReceipts(h, "return")
	require.Len(t, returned, 1)
	assert.Equal(t, ledger.Refused, returned[0].Outcome)
	assert.Equal(t, "kept: untracked nested/ in "+path+" survived git clean", returned[0].Detail)

	// A kept tree is never returned later by this lease, however many ticks follow, and a
	// comment tells the user why it is still leased.
	h.advance(time.Second)
	h.tick()
	assert.Empty(t, h.returns())
	assert.DirExists(t, filepath.Join(path, "nested"))
	mutations := h.linear.mutations()
	require.Len(t, mutations, 1)
	assert.Contains(t, mutations[0].Body, "nested/")
}

func TestTickDoesNotReturnATreeWhoseGitFailsAfterTheCycleDecidedToReturnIt(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.worker("ENG-1", "working")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	// The cycle read the tree fine, and git cannot read it when the executor looks again.
	h.tools.Herdr.BeforeCloseWorkspace(func(string) {
		require.NoError(t, os.WriteFile(filepath.Join(path, ".git", "HEAD"), []byte("garbage\n"), 0o644))
	})

	_, err := h.run("tick")

	require.Error(t, err)
	assert.Empty(t, h.returns())
	returned := teardownReceipts(h, "return")
	require.Len(t, returned, 1)
	assert.Equal(t, ledger.Failed, returned[0].Outcome)
	assert.DirExists(t, path)
}

// teardownReceipts lists the receipts of one action kind the loop has written.

func teardownReceipts(h *harness, kind string) []ledger.Receipt {
	var found []ledger.Receipt
	for _, r := range h.ledgerNow() {
		if r.Kind == kind {
			found = append(found, r)
		}
	}
	return found
}

// gitTree makes a pool tree that is clean and pushed: a clone of a bare
// remote on main, at <dir>/trees/<name>, the path h.worker uses for a ticket
// named <name>.
func (h *harness) gitTree(ticket string) string {
	path := filepath.Join(h.dir, "trees", ticket)
	remote := filepath.Join(h.dir, "remotes", ticket+".git")
	require.NoError(h.t, os.MkdirAll(filepath.Dir(remote), 0o755))
	h.git(h.dir, "init", "--bare", "--initial-branch=main", remote)
	require.NoError(h.t, os.MkdirAll(path, 0o755))
	h.git(path, "init", "--initial-branch=main")
	h.git(path, "remote", "add", "origin", remote)
	h.commit(path)
	h.push(path)
	return path
}

// lease holds a tree for a ticket with no herdr workspace on it.
func (h *harness) lease(ticket, path string) {
	h.tools.Treehouse.Clear(h.repo)
	h.tools.Treehouse.Seed(h.repo, treehouse.Tree{Path: path, Status: "leased", LeaseHolder: ticket})
}

// dirty stages a new file in the tree.
func (h *harness) dirty(path string) {
	h.untracked(path, "wip.txt")
	h.git(path, "add", "wip.txt")
}

// untracked leaves a file in the tree that git has never seen.
func (h *harness) untracked(path, name string) {
	require.NoError(h.t, os.WriteFile(filepath.Join(path, name), []byte("wip"), 0o644))
}

// commit adds a commit that no remote has.
func (h *harness) commit(path string) {
	h.git(path, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "work")
}

func (h *harness) push(path string) {
	h.git(path, "push", "origin", "HEAD:main")
	h.git(path, "fetch", "origin")
}

func (h *harness) git(dir string, args ...string) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	require.NoError(h.t, err, string(out))
}

func assignedTo(t fakeTicket, user string) fakeTicket {
	t.Assignee = user
	return t
}

func withoutLabels(t fakeTicket) fakeTicket {
	t.Labels = nil
	return t
}
