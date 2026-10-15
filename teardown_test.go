package dispatch_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kapetan-io/dispatch/internal/herdrtest"
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

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.Equal(t, "teardown ENG-1: "+test.status+", clean and pushed\n", out)
			assert.Equal(t, []string{"w-ENG-1"}, h.closes())
			assert.Equal(t, []string{h.repo + "|" + path + "|ENG-1"}, h.returns())
			assert.Empty(t, h.linear.mutations())
			assert.Empty(t, h.launches())

			// The tree is back in the pool, so the next tick has nothing left to do.
			out, err = h.run("tick")
			require.NoError(t, err)
			assert.Empty(t, out)
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

func TestTickDryRunListsTeardownWithoutActing(t *testing.T) {
	h := newHarness(t)
	h.gitTree("ENG-1")
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))

	out, err := h.run("tick", "--dry-run")
	require.NoError(t, err)

	assert.Equal(t, "teardown ENG-1: Done, clean and pushed\n", out)
	assert.Empty(t, h.closes())
	assert.Empty(t, h.returns())
	assert.NoFileExists(t, filepath.Join(h.state, "cache.json"))
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

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.Equal(t, "keep ENG-1: Done, but its tree "+path+" has "+test.work+"\n", out)
			assert.Equal(t, []string{"w-ENG-1"}, h.closes())
			assert.Empty(t, h.returns())
			mutations := h.linear.mutations()
			require.Len(t, mutations, 1)
			assert.Equal(t, lineartest.Comment, mutations[0].Kind)
			assert.Equal(t, "uuid-ENG-1", mutations[0].TicketID)
			assert.Contains(t, mutations[0].Body, "`"+path+"` has "+test.work+".")
			assert.Contains(t, mutations[0].Body, "kept this ticket's tree leased")

			// Later ticks neither comment again nor close anything else, but still name the held tree.
			for range 2 {
				out, err = h.run("tick")
				require.NoError(t, err)
				assert.Equal(t, "keep ENG-1: Done, but its tree "+path+" has "+test.work+"\n", out)
			}
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

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "teardown ENG-1: Done, removed untracked zz_tmp_test.go\n", out)
	assert.NoFileExists(t, filepath.Join(path, "zz_tmp_test.go"))
	assert.Equal(t, []string{"w-ENG-1"}, h.closes())
	assert.Equal(t, []string{h.repo + "|" + path + "|ENG-1"}, h.returns())
	assert.Empty(t, h.linear.mutations())
}

func TestTickDryRunListsUntrackedFilesWithoutDeletingThem(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.untracked(path, "a.txt")
	h.untracked(path, "b.txt")
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))

	out, err := h.run("tick", "--dry-run")
	require.NoError(t, err)

	assert.Equal(t, "teardown ENG-1: Done, would remove untracked a.txt b.txt\n", out)
	assert.FileExists(t, filepath.Join(path, "a.txt"))
	assert.FileExists(t, filepath.Join(path, "b.txt"))
	assert.Empty(t, h.closes())
	assert.Empty(t, h.returns())
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

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "teardown ENG-1: Done, removed untracked scratch.txt\n", out)
	assert.FileExists(t, filepath.Join(path, "build", "out.bin"))
	assert.Len(t, h.returns(), 1)
}

func TestTickQuotesUntrackedFileNamesThatHoldSpaces(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.untracked(path, "sp ace.txt")
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "teardown ENG-1: Done, removed untracked \"sp ace.txt\"\n", out)
}

func TestTickKeepsTheTreeWhenGitCleanLeavesANestedRepoBehind(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	require.NoError(t, os.MkdirAll(filepath.Join(path, "nested"), 0o755))
	h.git(filepath.Join(path, "nested"), "init", "--initial-branch=main")
	h.untracked(path, "scratch.txt")
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "keep ENG-1: untracked nested/ in "+path+" survived git clean\n", out)
	assert.NoFileExists(t, filepath.Join(path, "scratch.txt"))
	assert.DirExists(t, filepath.Join(path, "nested"))
	assert.Empty(t, h.returns())
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

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "teardown ENG-1: Done, removed untracked scratch.txt\n", out)
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

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "teardown ENG-1: Done, clean and pushed\n", out)
	assert.Len(t, h.returns(), 1)
}

func TestTickKeepsTheTreeAndItsUntrackedFilesWhenTrackedWorkIsAlsoThere(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.dirty(path)
	h.untracked(path, "scratch.txt")
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "keep ENG-1: Done, but its tree "+path+" has uncommitted changes\n", out)
	assert.FileExists(t, filepath.Join(path, "scratch.txt"))
	assert.Empty(t, h.returns())
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

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "keep ENG-1: Done, but its tree "+path+" has uncommitted changes\n", out)
	assert.FileExists(t, filepath.Join(path, "scratch.txt"))
	assert.Empty(t, h.returns())
}

func TestTickReturnsTheTreeOnceItsWorkIsPushed(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.commit(path)
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	require.Empty(t, h.returns())
	assert.Contains(t, string(h.readFile("state/cache.json")), `"teardown_warned": true`)

	h.push(path)
	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "teardown ENG-1: Done, clean and pushed\n", out)
	assert.Len(t, h.returns(), 1)
	assert.Len(t, h.linear.mutations(), 1)

	// The warning goes with the lease.
	_, err = h.run("tick")
	require.NoError(t, err)
	assert.NotContains(t, string(h.readFile("state/cache.json")), "teardown_warned")
}

func TestTickRetriesADirtyTreeCommentThatLinearRefused(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.dirty(path)
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	h.linear.failComments(true)

	_, stderr, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)
	assert.Contains(t, stderr, "dispatch: while keeping the tree of ENG-1: while commenting on ENG-1:")
	assert.Equal(t, []string{"w-ENG-1"}, h.closes())

	// The comment never landed, so it is tried again, now that the workspace is gone.
	h.linear.failComments(false)
	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "keep ENG-1: Done, but its tree "+path+" has uncommitted changes\n", out)
	assert.Len(t, h.closes(), 1)
	assert.Len(t, h.linear.mutations(), 1)

	out, err = h.run("tick")
	require.NoError(t, err)
	// The warning is posted once, but the held tree stays in the log.
	assert.Equal(t, "keep ENG-1: Done, but its tree "+path+" has uncommitted changes\n", out)
	assert.Len(t, h.linear.mutations(), 1)
}

func TestTickKeepsATreeItCannotRead(t *testing.T) {
	h := newHarness(t)
	// The tree's directory is gone, so git can't say whether it held work.
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "skip ENG-1: can't tell whether "+filepath.Join(h.dir, "trees", "ENG-1")+" holds unpushed work, so it stays leased")
	assert.Empty(t, h.closes())
	assert.Empty(t, h.returns())
	assert.Empty(t, h.linear.mutations())
}

func TestTickKeepsTheTreeWhenTheWorkspaceWillNotClose(t *testing.T) {
	h := newHarness(t)
	h.gitTree("ENG-1")
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	h.tools.Herdr.Fail(herdrtest.OpCloseWorkspace, errors.New("exit status 1"))

	out, stderr, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Contains(t, stderr, "dispatch: while tearing down ENG-1: while closing herdr workspace w-ENG-1")
	assert.Empty(t, h.returns())
}

func TestTickKeepsTheTreeWhenTreehouseRefusesTheReturn(t *testing.T) {
	h := newHarness(t)
	h.gitTree("ENG-1")
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	// The lease moves to another ticket after dispatch looked at the pool.
	h.tools.Treehouse.MoveLeaseAfterRead(filepath.Join(h.dir, "trees", "ENG-1"), "ENG-2")

	out, stderr, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Contains(t, stderr, "dispatch: while tearing down ENG-1: while returning")
	assert.Contains(t, stderr, "lease holder does not match worktree")
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

func TestTickDryRunNamesEachHeldTreeOfOneTicket(t *testing.T) {
	h := newHarness(t)
	first := h.gitTree("ENG-1")
	second := h.gitTree("library")
	h.dirty(first)
	h.dirty(second)
	h.workerOn("ENG-1", first, "w-ENG-1", "idle")
	h.workerOn("ENG-1", second, "w-library", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))

	out, err := h.run("tick", "--dry-run")
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{
		"keep ENG-1: Done, but its tree " + first + " has uncommitted changes",
		"keep ENG-1: Done, but its tree " + second + " has uncommitted changes",
	}, strings.Split(strings.TrimSpace(out), "\n"))
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

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "teardown ENG-1: Done, clean and pushed\n", out)
	assert.Equal(t, []string{h.repo + "|" + path + "|ENG-1"}, h.returns())
}

func TestTickReturnsTheCleanTreeOfAFinishedReviewTicket(t *testing.T) {
	for _, status := range []string{"Done", "Canceled"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			path := h.gitTree("ENG-1")
			h.worker("ENG-1", "idle")
			h.linear.setElsewhere(withLabel(ticket("ENG-1", status, "dispatch"), "design-review"))

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.Equal(t, "teardown ENG-1: "+status+", clean and pushed\n", out)
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

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "keep ENG-1: Done, but its tree "+path+" has uncommitted changes")
	assert.Empty(t, h.returns())
	assert.Len(t, h.linear.mutations(), 1)

	_, err = h.run("tick")
	require.NoError(t, err)
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

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.Equal(t, "stop ENG-1: moved back to "+status+"\n", out)
			assert.Equal(t, []string{"w-ENG-1"}, h.closes())
			assert.Empty(t, h.returns())
			assert.Empty(t, h.linear.mutations())
			assert.Empty(t, h.launches())
			assert.FileExists(t, filepath.Join(path, "wip.txt"))

			// The lease stays, and nothing more happens while the ticket is paused.
			out, err = h.run("tick")
			require.NoError(t, err)
			assert.Empty(t, out)
			assert.Len(t, h.closes(), 1)
		})
	}
}

func TestTickLeavesAWorkspaceOpenedByHandOnAPausedTicket(t *testing.T) {
	h := newHarness(t)
	h.gitTree("ENG-1")
	// A workspace with no Claude agent is not a worker, so it may be yours.
	h.worker("ENG-1", "")
	h.linear.setElsewhere(ticket("ENG-1", "Todo", "dispatch"))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Empty(t, h.closes())
}

func TestTickLeavesAWorkspaceOpenedByHandOnAKeptTree(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.dirty(path)
	h.worker("ENG-1", "")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)

	// The comment is posted once, and the workspace stays for the next tick too.
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.closes())
	assert.Len(t, h.linear.mutations(), 1)
}

func TestTickDoesNotRepeatTheCommentAfterALeaseLookupFails(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.dirty(path)
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	require.Len(t, h.linear.mutations(), 1)

	// The failed tick can't tell who holds a lease, but the warning still stands.
	h.linear.failLookups()
	_, _, err = h.runContext(t.Context(), "tick")
	require.NoError(t, err)
	h.linear.allowLookups()
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.linear.mutations(), 1)
}

func TestTickDoesNotRepeatTheCommentAfterAPoolCannotBeRead(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.dirty(path)
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	require.Len(t, h.linear.mutations(), 1)

	// A second repo whose pool fails hides no lease from the first, but a tick
	// that can't read every pool can't tell which finished tickets are released.
	h.addRepo("tiger", filepath.Join(h.dir, "moved"))
	_, _, err = h.runContext(t.Context(), "tick")
	require.NoError(t, err)
	// Clearing the extra repo makes the next tick read every pool again.
	h.resetRepos()
	_, err = h.run("tick")
	require.NoError(t, err)

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

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "teardown ENG-1: Done, clean and pushed\n", out)
}

func TestTickKeepsTickingWhenLinearCannotListTheTicketsHoldingTrees(t *testing.T) {
	h := newHarness(t)
	h.gitTree("ENG-1")
	h.worker("ENG-1", "idle")
	h.tickets(ticket("ENG-2", "In Progress", "dispatch"))
	h.linear.failLookups()

	out, stderr, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)

	assert.Equal(t, "launch ENG-2: ok\n", out)
	assert.Contains(t, stderr, "dispatch: while reading the tickets that hold trees: linear LeasedTickets")
	assert.Empty(t, h.returns())
}

func TestTickRelaunchesAPausedTicketOnItsKeptTree(t *testing.T) {
	h := newHarness(t)
	h.gitTree("ENG-1")
	h.worker("ENG-1", "working")
	h.linear.setElsewhere(ticket("ENG-1", "Todo", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	require.Empty(t, h.launches())

	h.linear.setElsewhere()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "launch ENG-1: ok\n", out)
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

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "teardown ENG-1: Done, clean and pushed\nlaunch ENG-2: ok\n", out)
}

func TestTickReadsDoneAndPausedStatusesFromConfig(t *testing.T) {
	h := newHarness(t)
	h.config("[plugins.linear.statuses]\nfinished = [\"Shipped\"]\npaused = [\"Parked\"]\n")
	h.tools.Linear.AddState("Shipped", "completed")
	h.tools.Linear.AddState("Parked", "backlog")
	h.gitTree("ENG-1")
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

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "teardown ENG-1: Shipped, clean and pushed\nstop ENG-2: moved back to Parked\n", out)
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
	// The plan saw a clean tree, and the worker writes a file as it is killed.
	h.tools.Herdr.BeforeCloseWorkspace(func(string) {
		require.NoError(t, os.WriteFile(filepath.Join(path, "late.txt"), []byte("late\n"), 0o644))
		require.NoError(t, exec.Command("git", "-C", path, "add", "late.txt").Run())
	})

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "keep ENG-1: the worker left uncommitted changes in "+path+"\n", out)
	assert.Equal(t, []string{"w-ENG-1"}, h.closes())
	assert.Empty(t, h.returns())
	assert.FileExists(t, filepath.Join(path, "late.txt"))

	// The next tick's plan sees the work and posts the one comment.
	out, err = h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, "keep ENG-1: Done, but its tree "+path+" has uncommitted changes\n", out)
	assert.Len(t, h.linear.mutations(), 1)
	assert.Empty(t, h.returns())
	assert.Len(t, h.closes(), 1)
}

func TestTickDryRunListsKeepAndStopWithoutActing(t *testing.T) {
	h := newHarness(t)
	dirty := h.gitTree("ENG-1")
	h.dirty(dirty)
	h.gitTree("ENG-2")
	h.worker("ENG-1", "idle")
	h.worker("ENG-2", "idle")
	h.linear.setElsewhere(
		ticket("ENG-1", "Done", "dispatch"),
		ticket("ENG-2", "Todo", "dispatch"),
	)

	out, err := h.run("tick", "--dry-run")
	require.NoError(t, err)

	assert.Equal(t, "keep ENG-1: Done, but its tree "+dirty+" has uncommitted changes\nstop ENG-2: moved back to Todo\n", out)
	assert.Empty(t, h.closes())
	assert.Empty(t, h.returns())
	assert.Empty(t, h.linear.mutations())
	assert.NoFileExists(t, filepath.Join(h.state, "cache.json"))
}

func TestTickDryRunListsAWarnedTreeOnEveryTick(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.commit(path)
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	_, err := h.run("tick")
	require.NoError(t, err)
	require.Len(t, h.linear.mutations(), 1)

	out, err := h.run("tick", "--dry-run")
	require.NoError(t, err)

	assert.Equal(t, "keep ENG-1: Done, but its tree "+path+" has 1 commit that no remote has\n", out)
	assert.Len(t, h.linear.mutations(), 1)
}

func TestTickCommentsOnADirtyTreeThatHasNoWorkspace(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.dirty(path)
	h.lease("ENG-1", path)
	h.linear.setElsewhere(ticket("ENG-1", "Canceled", "dispatch"))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "keep ENG-1: Canceled, but its tree "+path+" has uncommitted changes\n", out)
	assert.Empty(t, h.closes())
	assert.Empty(t, h.returns())
	assert.Len(t, h.linear.mutations(), 1)

	out, err = h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, "keep ENG-1: Canceled, but its tree "+path+" has uncommitted changes\n", out)
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

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "keep ENG-1: Done, but its tree "+path+" has 1 commit that no remote has\n", out)
	assert.Empty(t, h.returns())
}

func TestTickPostsOneCommentForSeveralKeptTrees(t *testing.T) {
	h := newHarness(t)
	first := h.gitTree("ENG-1")
	second := h.gitTree("library")
	h.dirty(first)
	h.commit(second)
	h.workerOn("ENG-1", first, "w-ENG-1", "idle")
	h.workerOn("ENG-1", second, "w-library", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"w-ENG-1", "w-library"}, h.closes())
	assert.Empty(t, h.returns())
	mutations := h.linear.mutations()
	require.Len(t, mutations, 1)
	assert.Contains(t, mutations[0].Body, "`"+first+"` has uncommitted changes")
	assert.Contains(t, mutations[0].Body, "`"+second+"` has 1 commit that no remote has")

	_, err = h.run("tick")
	require.NoError(t, err)
	assert.Len(t, h.linear.mutations(), 1)
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
	_, err := h.run("tick")
	require.NoError(t, err)
	require.Len(t, h.linear.mutations(), 1)

	// Reopened, the ticket is managed again and its old warning no longer applies.
	h.linear.setElsewhere()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.lease("ENG-1", path)
	_, err = h.run("tick")
	require.NoError(t, err)
	h.tickets()
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.linear.mutations(), 2)
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
