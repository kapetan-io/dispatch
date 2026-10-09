package dispatch_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			assert.Equal(t, []string{h.repo + "|return " + path + " --if-lease-holder ENG-1 --force"}, h.returns())
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
	assert.Equal(t, []string{h.repo + "|return " + path + " --if-lease-holder ENG-1 --force"}, h.returns())
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

			assert.Equal(t, "keep ENG-1: Done, but its tree has "+test.work+"\n", out)
			assert.Equal(t, []string{"w-ENG-1"}, h.closes())
			assert.Empty(t, h.returns())
			mutations := h.linear.mutations()
			require.Len(t, mutations, 1)
			assert.Equal(t, "CommentTicket", mutations[0].operation)
			assert.Equal(t, "uuid-ENG-1", mutations[0].variables["id"])
			assert.Contains(t, mutations[0].variables["body"], "`"+path+"` has "+test.work+".")
			assert.Contains(t, mutations[0].variables["body"], "stopped this ticket's worker but kept its tree leased")

			// Later ticks neither comment again nor close anything else.
			for range 2 {
				out, err = h.run("tick")
				require.NoError(t, err)
				assert.Empty(t, out)
			}
			assert.Len(t, h.linear.mutations(), 1)
			assert.Len(t, h.closes(), 1)
			assert.Empty(t, h.returns())
		})
	}
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

	assert.Equal(t, "keep ENG-1: Done, but its tree has uncommitted changes\n", out)
	assert.Len(t, h.closes(), 1)
	assert.Len(t, h.linear.mutations(), 1)

	out, err = h.run("tick")
	require.NoError(t, err)
	assert.Empty(t, out)
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
	h.writeFile("close-fails", "")

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
	h.writeFile("return-fails", "")

	out, stderr, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Contains(t, stderr, "dispatch: while tearing down ENG-1: while returning")
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
	assert.Equal(t, []string{h.repo + "|return " + clean + " --if-lease-holder ENG-1 --force"}, h.returns())
	mutations := h.linear.mutations()
	require.Len(t, mutations, 1)
	assert.Contains(t, mutations[0].variables["body"], stranded)
	assert.NotContains(t, mutations[0].variables["body"], clean)
}

func TestTickLeavesTheTreesOfTicketsItDoesNotManage(t *testing.T) {
	for _, test := range []struct {
		name   string
		ticket fakeTicket
	}{
		{
			name:   "DesignTicket",
			ticket: withLabel(ticket("ENG-1", "Done", "dispatch"), "design"),
		},
		{
			name:   "ReviewTicket",
			ticket: withLabel(ticket("ENG-1", "Canceled", "dispatch"), "design-review"),
		},
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

func TestTickSkipsAFinishedTicketWhoseLabelsDidNotFitOnePage(t *testing.T) {
	h := newHarness(t)
	h.gitTree("ENG-1")
	h.worker("ENG-1", "idle")
	cut := ticket("ENG-1", "Done", "dispatch")
	cut["labels"].(map[string]any)["pageInfo"] = map[string]bool{"hasNextPage": true}
	h.linear.setElsewhere(cut)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "skip ENG-1: it has more labels than the dispatcher reads")
	assert.Empty(t, h.closes())
	assert.Empty(t, h.returns())
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

func TestTickStopsAPausedTicketsWorkerEvenWithoutAClaudeAgent(t *testing.T) {
	h := newHarness(t)
	h.gitTree("ENG-1")
	h.worker("ENG-1", "")
	h.linear.setElsewhere(ticket("ENG-1", "Todo", "dispatch"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"w-ENG-1"}, h.closes())
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
	h.config("[linear.statuses]\ndone = [\"Shipped\"]\npaused = [\"Parked\"]\n")
	h.gitTree("ENG-1")
	h.gitTree("ENG-2")
	h.gitTree("ENG-3")
	h.worker("ENG-1", "idle")
	h.worker("ENG-2", "idle")
	h.worker("ENG-3", "idle")
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
	h.script("treehouse", `case "$1" in
return)
  if grep -q '"w-ENG-1"' "`+h.dir+`/workspaces.json"; then echo open >> "`+h.dir+`/order.log"; else echo closed >> "`+h.dir+`/order.log"; fi
  echo "$PWD|$*" >> "`+h.dir+`/returns.log" ;;
*) cat "`+h.dir+`/trees.json" ;;
esac`)

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"closed"}, h.lines("order.log"))
	assert.Equal(t, []string{h.repo + "|return " + path + " --if-lease-holder ENG-1 --force"}, h.returns())
}

func TestTickKeepsTheTreeOfWorkWrittenWhileTheWorkspaceClosed(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.worker("ENG-1", "working")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	// The plan saw a clean tree, and the worker writes a file as it is killed.
	h.script("herdr", `case "$1 $2" in
"workspace list") cat "`+h.dir+`/workspaces.json" ;;
"workspace close")
  echo "$3" >> "`+h.dir+`/closes.log"
  echo late > "`+path+`/late.txt"
  jq --arg id "$3" '.result.workspaces |= map(select(.workspace_id != $id))' "`+h.dir+`/workspaces.json" > "`+h.dir+`/workspaces.tmp"
  mv "`+h.dir+`/workspaces.tmp" "`+h.dir+`/workspaces.json" ;;
"agent list") cat "`+h.dir+`/agents.json" ;;
*) exit 2 ;;
esac`)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "keep ENG-1: the worker left uncommitted changes in "+path+"\n", out)
	assert.Equal(t, []string{"w-ENG-1"}, h.closes())
	assert.Empty(t, h.returns())
	assert.FileExists(t, filepath.Join(path, "late.txt"))

	// The next tick's plan sees the work and posts the one comment.
	out, err = h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, "keep ENG-1: Done, but its tree has uncommitted changes\n", out)
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

	assert.Equal(t, "keep ENG-1: Done, but its tree has uncommitted changes\nstop ENG-2: moved back to Todo\n", out)
	assert.Empty(t, h.closes())
	assert.Empty(t, h.returns())
	assert.Empty(t, h.linear.mutations())
	assert.NoFileExists(t, filepath.Join(h.state, "cache.json"))
}

func TestTickCommentsOnADirtyTreeThatHasNoWorkspace(t *testing.T) {
	h := newHarness(t)
	path := h.gitTree("ENG-1")
	h.dirty(path)
	h.lease("ENG-1", path)
	h.linear.setElsewhere(ticket("ENG-1", "Canceled", "dispatch"))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "keep ENG-1: Canceled, but its tree has uncommitted changes\n", out)
	assert.Empty(t, h.closes())
	assert.Empty(t, h.returns())
	assert.Len(t, h.linear.mutations(), 1)

	out, err = h.run("tick")
	require.NoError(t, err)
	assert.Empty(t, out)
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

	assert.Equal(t, "keep ENG-1: Done, but its tree has 1 commit that no remote has\n", out)
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
	assert.Contains(t, mutations[0].variables["body"], "`"+first+"` has uncommitted changes")
	assert.Contains(t, mutations[0].variables["body"], "`"+second+"` has 1 commit that no remote has")

	_, err = h.run("tick")
	require.NoError(t, err)
	assert.Len(t, h.linear.mutations(), 1)
}

func TestTickLeavesTheWorkersOfPausedTicketsItDoesNotManage(t *testing.T) {
	for _, test := range []struct {
		name   string
		ticket fakeTicket
	}{
		{
			name:   "DesignTicket",
			ticket: withLabel(ticket("ENG-1", "Todo", "dispatch"), "design"),
		},
		{
			name:   "ReviewTicketInBacklog",
			ticket: withLabel(ticket("ENG-1", "Backlog", "dispatch"), "design-review"),
		},
		{
			name:   "AssignedToSomeoneElse",
			ticket: assignedTo(ticket("ENG-1", "Todo", "dispatch"), "someone-else"),
		},
		{
			name:   "NoAILabel",
			ticket: withoutLabels(ticket("ENG-1", "Backlog", "dispatch")),
		},
		{
			name: "LabelsDidNotFitOnePage",
			ticket: func() fakeTicket {
				cut := ticket("ENG-1", "Todo", "dispatch")
				cut["labels"].(map[string]any)["pageInfo"] = map[string]bool{"hasNextPage": true}
				return cut
			}(),
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

func TestTickSkipsAPausedTicketWhoseLabelsDidNotFitOnePage(t *testing.T) {
	h := newHarness(t)
	h.gitTree("ENG-1")
	h.worker("ENG-1", "idle")
	cut := ticket("ENG-1", "Todo", "dispatch")
	cut["labels"].(map[string]any)["pageInfo"] = map[string]bool{"hasNextPage": true}
	h.linear.setElsewhere(cut)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "skip ENG-1: it has more labels than the dispatcher reads")
	assert.Empty(t, h.closes())
}

// gitTree makes a pool tree that is clean and pushed: a clone of a bare
// remote on main, at the path h.worker gives the ticket.
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
	h.writeJSON("trees.json", []map[string]string{{"path": path, "status": "leased", "lease_holder": ticket}})
}

// dirty leaves an uncommitted file in the tree.
func (h *harness) dirty(path string) {
	require.NoError(h.t, os.WriteFile(filepath.Join(path, "wip.txt"), []byte("wip"), 0o644))
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
	t["assignee"] = map[string]string{"id": user}
	return t
}

func withoutLabels(t fakeTicket) fakeTicket {
	t["labels"] = map[string]any{"nodes": []map[string]any{}}
	return t
}

func TestTickCommentsAgainWhenATicketFinishesASecondTimeWithWorkInItsTree(t *testing.T) {
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
	h.worker("ENG-1", "working")
	_, err = h.run("tick")
	require.NoError(t, err)
	h.tickets()
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.linear.mutations(), 2)
}
