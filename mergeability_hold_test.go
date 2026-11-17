package dispatch_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A PR that needs work or has an open thread is not held, though GitHub has not computed mergeability.
func TestTickDropsTheReadyLabelWhenAnotherProblemShowsWhileMergeabilityIsNotComputed(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		pr   prState
	}{
		{"FailedCheck", prState{head: "a1", checks: "FAILURE", decision: "APPROVED", mergeable: "UNKNOWN"}},
		{"ChangesRequested", prState{head: "a1", checks: "SUCCESS", decision: "CHANGES_REQUESTED", mergeable: "UNKNOWN"}},
		{"UnresolvedThread", prState{head: "a1", checks: "SUCCESS", decision: "APPROVED", mergeable: "UNKNOWN", unresolved: 1}},
		{"Conflict", prState{head: "a1", checks: "SUCCESS", decision: "APPROVED", mergeable: "CONFLICTING"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			d.Workers.Stop("ENG-1", stopTasks{})
			d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
			d.Tick()

			d.GitHub.PullRequest("ENG-1", c.pr)
			d.Tick()

			renames := d.Workers.Renames()
			assert.Equal(t, "w-ENG-1|👀 ENG-1", renames[0])
			assert.Greater(t, len(renames), 1)
		})
	}
}

// Replay reads the recorded world, so a held read must give the same decisions as live.
func TestReplayOfAHeldMergeabilityReadReportsEveryCycleTheSameAsLive(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{})
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "SUCCESS", decision: "APPROVED", mergeable: "UNKNOWN"})
	d.Tick()
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	d.Tick()
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "SUCCESS", decision: "APPROVED", mergeable: "UNKNOWN"})
	d.Tick()
	d.Stop()

	cycles, matched := replayOf(t, d)

	assert.Greater(t, cycles, 2)
	assert.Equal(t, cycles, matched)
}

// A worker's question comes before a ready label, held or not.
func TestTickShowsAnAskWhileGitHubComputesMergeability(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{})
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	d.Tick()

	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "SUCCESS", decision: "APPROVED", mergeable: "UNKNOWN"})
	d.Workers.Ask("ENG-1")
	d.Tick()

	assert.Equal(t, []string{"w-ENG-1|👀 ENG-1", "w-ENG-1|🔴 ENG-1"}, d.Workers.Renames())
}
