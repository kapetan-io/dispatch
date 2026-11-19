package dispatch_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const needsHumanActivity = "ENG-1 is in Needs Human and its PR has new activity, "

// activityNotices are the notices about PR activity on a Needs Human ticket among what followers were told.
func activityNotices(told []string) []string {
	var found []string
	for _, line := range told {
		if strings.HasPrefix(line, needsHumanActivity) {
			found = append(found, line)
		}
	}
	return found
}

func TestTickTellsFollowersOfEachNewReviewOrCommentOnANeedsHumanPR(t *testing.T) {
	t.Parallel()
	socket, lines := sessionSocket(t)
	d := newDispatch(t, dispatchOptions{Env: map[string]string{"CLAUDE_CODE_SESSION_ID": socket}})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Workers.CheckedOut("ENG-1", "idle")
	pr := cleanPR("a1")
	d.GitHub.PullRequest("ENG-1", pr)
	require.NoError(t, d.Run("watch", "ENG-1").Err)
	d.Tick()
	assert.Empty(t, activityNotices(d.Followed.Told()))

	d.Advance(time.Second)
	pr.reviews = []activity{{"COMMENTED", time1, "alice", "", ""}}
	d.GitHub.PullRequest("ENG-1", pr)
	d.Tick()

	assert.Equal(t, []string{needsHumanActivity + "a review by alice: " + prURL1}, activityNotices(d.Followed.Told()))
	// The session may first hear of entering Needs Human; skip that line to reach the review.
	got := next(t, lines)
	if strings.Contains(got, "(entered Needs Human)") {
		got = next(t, lines)
	}
	assert.Contains(t, got, "dispatch event (PR activity while in Needs Human): "+needsHumanActivity+"a review by alice")

	// The next read with nothing new says nothing, and a later comment tells again.
	d.Advance(time.Second)
	d.Tick()
	assert.Len(t, activityNotices(d.Followed.Told()), 1)
	d.Advance(time.Second)
	pr.comments = []activity{{"", time2, "bob", "", ""}}
	d.GitHub.PullRequest("ENG-1", pr)
	d.Tick()

	assert.Equal(t, []string{
		needsHumanActivity + "a review by alice: " + prURL1,
		needsHumanActivity + "a comment by bob: " + prURL1,
	}, activityNotices(d.Followed.Told()))
	assert.Empty(t, d.Workers.Received("ENG-1"))
}

func TestTickTellsNoFollowerOfAListedAuthorOnANeedsHumanPR(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{RepoSettings: ignoreSetting})
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Workers.CheckedOut("ENG-1", "idle")
	pr := cleanPR("a1")
	d.GitHub.PullRequest("ENG-1", pr)
	d.Tick()
	d.Advance(time.Second)
	pr.comments = []activity{{"", time1, listedBot, "", ""}}
	pr.reviews = []activity{{"COMMENTED", time2, "codeowners-bot", "", ""}}
	d.GitHub.PullRequest("ENG-1", pr)

	d.Tick()

	assert.NotEmpty(t, d.GitHub.Reads())
	assert.Empty(t, activityNotices(d.Followed.Told()))
	assert.Empty(t, d.Workers.Received("ENG-1"))
}

func TestTickReadsNoNeedsHumanPRWhileItsWorkerIsWorking(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	pr := cleanPR("a1")
	pr.reviews = []activity{{"COMMENTED", time1, "alice", "", ""}}
	d.GitHub.PullRequest("ENG-1", pr)

	d.Tick()

	assert.Empty(t, d.GitHub.Reads())
	assert.Empty(t, activityNotices(d.Followed.Told()))
}

func TestTickTellsFollowersOfTheNewestOfItemsThatCameBetweenTwoReadsOfANeedsHumanPR(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Workers.CheckedOut("ENG-1", "idle")
	pr := cleanPR("a1")
	d.GitHub.PullRequest("ENG-1", pr)
	d.Tick()
	d.Advance(time.Second)
	pr.reviews = []activity{{"COMMENTED", time1, "alice", "", ""}}
	pr.comments = []activity{{"", time2, "bob", "", ""}}
	d.GitHub.PullRequest("ENG-1", pr)

	d.Tick()

	assert.Equal(t, []string{needsHumanActivity + "a comment by bob: " + prURL1}, activityNotices(d.Followed.Told()))
}

func TestTickTellsFollowersOfANewReviewOnADesignTicketInNeedsHuman(t *testing.T) {
	t.Parallel()
	for _, label := range []string{"design", "design-review"} {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(withLabel(ticket("ENG-1", "Needs Human", "dispatch"), label))
			d.Workers.CheckedOut("ENG-1", "idle")
			pr := cleanPR("a1")
			d.GitHub.PullRequest("ENG-1", pr)
			d.Tick()
			d.Advance(time.Second)
			pr.reviews = []activity{{"COMMENTED", time1, "alice", "", ""}}
			d.GitHub.PullRequest("ENG-1", pr)

			d.Tick()

			assert.Equal(t, []string{needsHumanActivity + "a review by alice: " + prURL1}, activityNotices(d.Followed.Told()))
			assert.Empty(t, d.Workers.Received("ENG-1"))
		})
	}
}

func TestTickReadsNoPRForANeedsHumanTicketWhoseBranchTheCloneLacks(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"), ticket("ENG-2", "Needs Human", "dispatch"))
	d.Workers.CheckedOut("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))

	d.Tick()

	assert.Equal(t, []string{prBranch("ENG-1")}, d.GitHub.Reads())
}

func TestTickReadsTheNeedsHumanPRWhenTheCloneCannotBeListed(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Workers.CheckedOut("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	// A corrupt packed-refs file fails git for-each-ref, so the clone's branches are unknown.
	require.NoError(t, os.WriteFile(filepath.Join(d.Repo, ".git", "packed-refs"), []byte("garbage line\n"), 0o644))

	d.Tick()

	assert.Equal(t, []string{prBranch("ENG-1")}, d.GitHub.Reads())
}
