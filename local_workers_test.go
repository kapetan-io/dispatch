package dispatch_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fileLocal files a local ticket titled "work" in the dispatch repo, so its branch is loc-<n>-work.
func (d *Dispatch) fileLocal(status string, args ...string) string {
	d.t.Helper()
	out := d.ticketOut(append([]string{"create", "--repo", "dispatch", "--title", "work", "--status", status}, args...)...)
	return strings.TrimSpace(out)
}

// moveToInProgress starts a parent once its children exist. Filing the children under a parent that
// is already In Progress lets a cycle between two creates see only the finished ones and close it.
func (d *Dispatch) moveToInProgress(id string) { d.ticketOut("move", id, "In Progress") }

// settles ticks until the ticket reaches want. A write through the CLI starts a read that may still be
// in flight when a tick runs, so one tick can decide on a read from before the write.
func (d *Dispatch) settles(id, want string) {
	d.t.Helper()
	require.Eventually(d.t, func() bool {
		d.Tick()
		return d.statusOf(id) == want
	}, 10*time.Second, 50*time.Millisecond)
}

// statusOf is the status `dispatch ticket show` prints for a local ticket.
func (d *Dispatch) statusOf(id string) string {
	d.t.Helper()
	for _, line := range strings.Split(d.ticketOut("show", id), "\n") {
		if rest, ok := strings.CutPrefix(line, "status      "); ok {
			return rest
		}
	}
	d.t.Fatalf("show %s printed no status", id)
	return ""
}

func TestTickLaunchesAnInProgressLocalTicket(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.fileLocal("In Progress")

	d.Tick()

	assert.Equal(t, []string{d.Repo + " LOC-1 loc-1-work --model sonnet"}, d.Workers.Launched())
}

func TestTickHoldsALocalLaunchBehindALinearWorkerAtTheCap(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 1\n" + localOn})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.StartBeforeTheEvents()
	d.fileLocal("In Progress")

	d.Tick()

	assert.Empty(t, d.Workers.Launched())
	assert.Contains(t, d.plan(), "LOC-1")
}

func TestTickLabelsALocalWorkspaceByTheJobOfItsStatus(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		status string
		agent  string
		want   string
	}{
		{status: "In Progress", agent: "working", want: "🟢"},
		{status: "Needs Human", agent: "working", want: "🔴"},
	} {
		t.Run(test.status, func(t *testing.T) {
			t.Parallel()
			// Renaming Linear's statuses must leave the local ones alone.
			d := newDispatch(t, dispatchOptions{Config: localOn + "[plugins.linear.statuses]\nin_progress = \"Doing\"\nneeds_human = \"Stuck\"\n"})
			d.StartBeforeTheEvents()
			d.fileLocal(test.status)
			d.Workers.Running("LOC-1", test.agent)

			d.Tick()

			assert.Equal(t, "w-LOC-1|"+test.want+" LOC-1", d.Workers.lastRename(t))
		})
	}
}

func TestTickKeepsABacklogLocalTicketsTreeAndShowsItPaused(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.fileLocal("Backlog")
	d.Trees.Clean("LOC-1")
	d.Workers.Running("LOC-1", "")

	d.Tick()

	assert.Equal(t, []string{"w-LOC-1|⏸️ LOC-1"}, d.Workers.Renames())
	assert.Empty(t, d.Workers.Launched())
}

func TestTickPromotesAnUnblockedLocalChildAndHoldsTheBlockedOne(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.fileLocal("In Progress")
	d.Workers.Running("LOC-1", "working")
	d.fileLocal("Todo", "--parent", "LOC-1")
	d.fileLocal("Todo", "--parent", "LOC-1", "--blocked-by", "LOC-2")

	d.settles("LOC-2", "In Progress")

	assert.Equal(t, "Todo", d.statusOf("LOC-3"))
}

func TestTickPromotesALocalChildOnceItsBlockerIsDone(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.fileLocal("Todo")
	d.Workers.Running("LOC-1", "working")
	d.fileLocal("Done", "--parent", "LOC-1")
	d.fileLocal("Todo", "--parent", "LOC-1", "--blocked-by", "LOC-2")
	d.moveToInProgress("LOC-1")

	d.settles("LOC-3", "In Progress")
}

func TestTickEscalatesALocalChildWhoseBlockerWasCanceled(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.fileLocal("Todo")
	d.Workers.Running("LOC-1", "working")
	d.fileLocal("Canceled", "--parent", "LOC-1")
	d.fileLocal("Todo", "--parent", "LOC-1", "--blocked-by", "LOC-2")
	d.moveToInProgress("LOC-1")

	d.settles("LOC-3", "Needs Human")

	show := d.ticketOut("show", "LOC-3")
	assert.Equal(t, 1, strings.Count(show, "dispatch  "))
	assert.Contains(t, show, "LOC-2 is Canceled")
}

func TestTickClosesAParentWhoseLocalChildrenAreAllFinished(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.fileLocal("Todo")
	d.Workers.Running("LOC-1", "working")
	d.fileLocal("Done", "--parent", "LOC-1")
	d.fileLocal("Canceled", "--parent", "LOC-1")
	d.moveToInProgress("LOC-1")

	d.settles("LOC-1", "Done")
}

func TestTickMergesALocalTicketsApprovedPR(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn, RepoSettings: autoMergeSetting})
	d.StartBeforeTheEvents()
	d.fileLocal("In Review")
	d.Workers.Running("LOC-1", "idle")
	d.GitHub.PullRequest("LOC-1", approvedPR("a1"))

	d.Tick()

	assert.Equal(t, []string{mergeCall1}, d.GitHub.Merged())
}

func TestTickReturnsTheTreeOfAFinishedLocalTicket(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.fileLocal("Done")
	path := d.Trees.Clean("LOC-1")
	d.Workers.Running("LOC-1", "idle")

	d.Tick()

	assert.Equal(t, []string{"w-LOC-1"}, d.Workers.Closed())
	assert.Equal(t, []string{d.Repo + "|" + path + "|LOC-1"}, d.Trees.Returned())
}

func TestTickKeepsWorkingOnLocalTicketsWhileLinearCannotBeRead(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.fileLocal("Done")
	path := d.Trees.Clean("LOC-1")
	d.Workers.Running("LOC-1", "idle")
	d.fileLocal("In Progress")
	d.Linear.FailManaged(true)

	d.Tick()

	// The returned tree opens the pool again, which lets a launch the fake launcher left workerless run once more.
	require.NotEmpty(t, d.Workers.Launched())
	for _, launched := range d.Workers.Launched() {
		assert.Equal(t, d.Repo+" LOC-2 loc-2-work --model sonnet", launched)
	}
	assert.Equal(t, []string{d.Repo + "|" + path + "|LOC-1"}, d.Trees.Returned())
}

func TestTickKeepsWorkingOnLinearTicketsWhileLocalTicketsCannotBeRead(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.fileLocal("In Progress")
	d.Tick()
	require.Len(t, d.Workers.Launched(), 1)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.corruptLocalDatabase()

	d.Tick()

	assert.Equal(t, []string{d.Repo + " LOC-1 loc-1-work --model sonnet", d.Repo + " ENG-1 thrawn01/eng-1 --model sonnet"}, d.Workers.Launched())
}

// corruptLocalDatabase overwrites the start of every stored ticket, as a file written outside
// dispatch could, so the next read of local tickets fails.
func (d *Dispatch) corruptLocalDatabase() {
	d.t.Helper()
	path := filepath.Join(d.state, "tickets.db")
	raw, err := os.ReadFile(path)
	require.NoError(d.t, err)
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	require.NoError(d.t, err)
	defer func() { _ = file.Close() }()
	marker := []byte(`{"title":`)
	for from := 0; ; {
		at := bytes.Index(raw[from:], marker)
		if at < 0 {
			break
		}
		_, err = file.WriteAt([]byte("#"), int64(from+at))
		require.NoError(d.t, err)
		from += at + len(marker)
	}
}

func TestTickRotationKeepsALocalTicketsReceiptsAndMemory(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.fileLocal("In Progress")
	d.Tick()
	require.NotEmpty(t, d.ReceiptsOf("launch", "LOC-1"))

	d.Advance(7 * time.Hour)
	d.Tick()
	d.Restart()

	assert.NotEmpty(t, d.ReceiptsOf("launch", "LOC-1"))
	d.Tick()
	assert.Len(t, d.Workers.Launched(), 1)
}
