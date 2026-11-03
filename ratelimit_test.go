package dispatch_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func toldAbout(d *Dispatch, text string) int {
	n := 0
	for _, told := range d.Followed.Told() {
		if strings.Contains(told, text) {
			n++
		}
	}
	return n
}

func TestTickTellsFollowersOnceWhenGitHubRateLimitsPRReads(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", approvedPR("a1"))
	reset := d.Clock.Now().Add(time.Hour).UTC().Truncate(time.Second)
	d.GitHub.RateLimitReads(reset)

	d.Tick()

	// The hold reason names the limit and its reset time in place of the generic unreadable reason.
	cycle := d.Run("cycle").Stdout
	assert.Contains(t, cycle, "GitHub rate-limited the read of the PR for ENG-1, its limit resets at "+reset.Format(time.RFC3339))
	assert.NotContains(t, cycle, "could not be read")
	assert.Empty(t, d.GitHub.Merged())
	explain := d.Run("explain", "ENG-1")
	require.NoError(t, explain.Err)
	assert.Contains(t, explain.Stdout, "GitHub rate-limited the read of the PR for ENG-1, its limit resets at "+reset.Format(time.RFC3339))
	assert.NotContains(t, explain.Stdout, "could not be read")

	// Followers hear it once however many resyncs find the same limit.
	d.Advance(time.Hour)
	d.Advance(time.Hour)
	assert.Equal(t, 1, toldAbout(d, "GitHub rate-limited the PR reads"))
	assert.Equal(t, 1, toldAbout(d, reset.Format(time.RFC3339)))
	assert.Equal(t, 1, strings.Count(d.LoopErr(), "GitHub rate-limited the PR reads"))

	// A read that completes ends the stretch, so the next limit is told again.
	d.GitHub.FailReads(nil)
	d.Advance(time.Hour)
	assert.Equal(t, 1, toldAbout(d, "GitHub rate-limited the PR reads"))
	d.GitHub.RateLimitReads(reset)
	d.Advance(time.Hour)
	assert.Equal(t, 2, toldAbout(d, "GitHub rate-limited the PR reads"))
}

func TestTickSaysRateLimitedWithoutATimeWhenGitHubGivesNone(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", approvedPR("a1"))
	d.GitHub.RateLimitReads(time.Time{})

	out := d.Tick()
	d.Advance(time.Hour)

	assert.Contains(t, out, "held: GitHub rate-limited the read of the PR for ENG-1\n")
	require.Len(t, d.Followed.Told(), 1)
	assert.Equal(t, "dispatch: GitHub rate-limited the PR reads, so rules that need a PR are held", d.Followed.Told()[0])
}

func TestTickTellsFollowersWhenGitHubRateLimitsMergedPRReads(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.StartBeforeTheEvents()
	d.fileLocal("In Review")
	reset := d.Clock.Now().Add(time.Hour).UTC().Truncate(time.Second)
	d.GitHub.RateLimitMergedReads(reset)

	d.Advance(5 * time.Minute)

	assert.Equal(t, 1, toldAbout(d, "GitHub rate-limited the PR reads"))
	assert.Equal(t, 1, toldAbout(d, reset.Format(time.RFC3339)))
	assert.Contains(t, d.plan(), "GitHub rate-limited the read of the PR for LOC-1, its limit resets at "+reset.Format(time.RFC3339))
}

func TestTickKeepsTheUnreadableReasonForAReadGitHubRefusedForAnotherReason(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", approvedPR("a1"))
	d.GitHub.FailReads(errors.New("HTTP 502"))

	d.Tick()

	assert.Regexp(t, `held\s+ENG-1\s+-\s+auto-merge\s+the PR for ENG-1 could not be read`, d.Run("cycle").Stdout)
	assert.Empty(t, d.Followed.Told())
}
