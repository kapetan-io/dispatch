package dispatch_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mergeLocal records a merged PR of the local ticket's branch and moves the clock past the merge.
func (d *Dispatch) mergeLocal(id string, pr mergedPR) {
	d.t.Helper()
	d.GitHub.PullRequestMerged(id, pr)
	d.Advance(2 * time.Minute)
}

func TestTickClosesALocalTicketWhosePRMerged(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"In Review", "In Progress", "Backlog", "Needs Human", "Todo"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			d := localDispatch(t)
			d.fileLocal(status)
			d.mergeLocal("LOC-1", mergedPR{})

			d.settles("LOC-1", "Done")
		})
	}
}

func TestTickReturnsTheTreeAndClosesTheWorkspaceOfAMergedLocalTicket(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.fileLocal("In Review")
	path := d.Trees.Clean("LOC-1")
	d.Workers.Running("LOC-1", "idle")
	d.mergeLocal("LOC-1", mergedPR{})

	d.settles("LOC-1", "Done")
	d.Tick()

	assert.Equal(t, []string{"w-LOC-1"}, d.Workers.Closed())
	assert.Equal(t, []string{d.Repo + "|" + path + "|LOC-1"}, d.Trees.Returned())
}

func TestTickLaunchesNoWorkerOnAnInProgressLocalTicketWhosePRMerged(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.fileLocal("In Progress")
	d.Tick()
	require.Len(t, d.Workers.Launched(), 1)

	// The launched worker left no workspace, so the resync that sees the merge sees no worker either.
	d.mergeLocal("LOC-1", mergedPR{})

	d.settles("LOC-1", "Done")
	assert.Len(t, d.Workers.Launched(), 1)
}

func TestTickLeavesALinearTicketWhosePRMerged(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequestMerged("ENG-1", mergedPR{})
	d.Advance(2 * time.Minute)

	d.Tick()

	noLinearChanges(t, d)
	assert.Empty(t, d.Workers.Closed())
}

func TestTickKeepsALocalTicketWhosePRDidNotMergeIntoTheDefaultBranch(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		pr   mergedPR
	}{
		{name: "FromAFork", pr: mergedPR{fork: true}},
		{name: "IntoAnotherBranch", pr: mergedPR{base: "loc-9-parent"}},
		{name: "BeforeTheTicketEnteredItsStatus", pr: mergedPR{after: -time.Hour}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := localDispatch(t)
			d.fileLocal("In Review")
			d.Workers.Running("LOC-1", "idle")
			d.mergeLocal("LOC-1", test.pr)

			d.Tick()
			d.Tick()

			assert.Equal(t, "In Review", d.statusOf("LOC-1"))
		})
	}
}

func TestTickKeepsALocalTicketMovedBackOutOfDone(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.fileLocal("In Review")
	d.mergeLocal("LOC-1", mergedPR{})
	d.settles("LOC-1", "Done")

	d.Advance(time.Minute)
	d.ticketOut("move", "LOC-1", "In Progress")
	d.Tick()
	d.Tick()

	assert.Equal(t, "In Progress", d.statusOf("LOC-1"))
}

func TestTickClosesNoLocalTicketWhileTheMergedReadFails(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.fileLocal("In Review")
	d.Workers.Running("LOC-1", "idle")
	reads := len(d.Tools().Calls("pullrequest", "merged"))
	d.GitHub.FailMergedReads(errors.New("gh: HTTP 502"))
	d.mergeLocal("LOC-1", mergedPR{})

	d.Tick()
	d.Tick()

	assert.Equal(t, "In Review", d.statusOf("LOC-1"))
	failed := len(d.Tools().Calls("pullrequest", "merged")) - reads
	assert.Positive(t, failed)
	assert.Equal(t, failed, strings.Count(d.LoopErr(), "dispatch: while reading the merged PRs of the dispatch repo: gh: HTTP 502\n"))
}

func TestTickClosesALocalTicketOnceTheMergedReadRecovers(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.fileLocal("In Review")
	d.GitHub.FailMergedReads(errors.New("gh: HTTP 502"))
	d.mergeLocal("LOC-1", mergedPR{})
	d.Tick()

	d.GitHub.FailMergedReads(nil)

	d.settles("LOC-1", "Done")
}

func TestReplayMatchesARunThatReadAMergedPR(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.fileLocal("In Review")
	d.mergeLocal("LOC-1", mergedPR{})
	d.settles("LOC-1", "Done")
	d.Stop()

	cycles, matched := replayOf(t, d)

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}

func TestTickPrintsAFailedMergedReadOnceNotOncePerTicket(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.fileLocal("In Review")
	d.fileLocal("In Review")
	d.fileLocal("In Review")
	d.GitHub.FailMergedReads(errors.New("gh: HTTP 502"))
	reads := len(d.Tools().Calls("pullrequest", "merged"))

	d.Advance(2 * time.Minute)
	d.Tick()

	failed := len(d.Tools().Calls("pullrequest", "merged")) - reads
	assert.Positive(t, failed)
	assert.Equal(t, failed, strings.Count(d.LoopErr(), "gh: HTTP 502\n"))
}
