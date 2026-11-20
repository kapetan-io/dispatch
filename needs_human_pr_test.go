package dispatch_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// activityTold are the lines followers were told about PR activity.
func activityTold(told []string) []string {
	var found []string
	for _, line := range told {
		if strings.Contains(line, "new activity") {
			found = append(found, line)
		}
	}
	return found
}

func TestTickNudgesANeedsHumanWorkerAboutEachNewReviewOrCommentOnItsPR(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Workers.CheckedOut("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{})
	pr := cleanPR("a1")
	d.GitHub.PullRequest("ENG-1", pr)
	d.Tick()
	assert.Empty(t, d.Workers.Received("ENG-1"))

	d.Advance(time.Second)
	pr.reviews = []activity{{"COMMENTED", time1, "alice", "", ""}}
	d.GitHub.PullRequest("ENG-1", pr)
	d.Tick()

	assert.Equal(t, []string{activityPrompt("a review by alice")}, d.Workers.Received("ENG-1"))

	// The worker handles it and stops, and a later comment nudges it again.
	d.Advance(time.Second)
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Advance(time.Second)
	pr.comments = []activity{{"", time2, "bob", "", ""}}
	d.GitHub.PullRequest("ENG-1", pr)
	d.Tick()

	assert.Equal(t, []string{activityPrompt("a review by alice"), activityPrompt("a comment by bob")}, d.Workers.Received("ENG-1"))
	assert.Empty(t, activityTold(d.Followed.Told()))
}

func TestTickNudgesAnAskingNeedsHumanWorkerAboutAReviewOnItsPR(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Workers.CheckedOut("ENG-1", "working")
	require.NoError(t, d.Workers.Ask("ENG-1").Err)
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Workers.SetStatus("ENG-1", "idle")
	pr := cleanPR("a1")
	d.GitHub.PullRequest("ENG-1", pr)
	d.Tick()
	d.Advance(time.Second)
	pr.reviews = []activity{{"COMMENTED", time1, "alice", "", ""}}
	d.GitHub.PullRequest("ENG-1", pr)

	d.Tick()

	assert.Equal(t, []string{activityPrompt("a review by alice")}, d.Workers.Received("ENG-1"))
}

func TestTickNudgesNoNeedsHumanWorkerAboutAListedAuthor(t *testing.T) {
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
}

func TestTickNudgesANeedsHumanWorkerAboutTheNewestOfItemsThatCameBetweenTwoReads(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Workers.CheckedOut("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{})
	pr := cleanPR("a1")
	d.GitHub.PullRequest("ENG-1", pr)
	d.Tick()
	d.Advance(time.Second)
	pr.reviews = []activity{{"COMMENTED", time1, "alice", "", ""}}
	pr.comments = []activity{{"", time2, "bob", "", ""}}
	d.GitHub.PullRequest("ENG-1", pr)

	d.Tick()

	assert.Equal(t, []string{activityPrompt("a comment by bob")}, d.Workers.Received("ENG-1"))
}

func TestTickReadsNoPRForADesignTicketInNeedsHuman(t *testing.T) {
	t.Parallel()
	for _, label := range []string{"design", "design-review"} {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(withLabel(ticket("ENG-1", "Needs Human", "dispatch"), label))
			d.Workers.CheckedOut("ENG-1", "idle")
			pr := cleanPR("a1")
			pr.reviews = []activity{{"COMMENTED", time1, "alice", "", ""}}
			d.GitHub.PullRequest("ENG-1", pr)

			d.Tick()

			assert.Empty(t, d.GitHub.Reads())
			assert.Empty(t, d.Workers.Received("ENG-1"))
		})
	}
}

func TestTickReadsNoNeedsHumanPRWhileItsWorkerIsBlockedOrGone(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		start func(d *Dispatch)
	}{
		{name: "blocked", start: func(d *Dispatch) { d.Workers.CheckedOut("ENG-1", "blocked") }},
		{name: "gone", start: func(d *Dispatch) {}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
			test.start(d)
			pr := cleanPR("a1")
			pr.reviews = []activity{{"COMMENTED", time1, "alice", "", ""}}
			d.GitHub.PullRequest("ENG-1", pr)

			d.Tick()

			assert.Empty(t, d.GitHub.Reads())
		})
	}
}

func TestARuleOfYourOwnMergesAndEscalatesNothingForAnAskingNeedsHumanWorker(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: shippedRulesAnd(t, `
whenever({
  name: "merge-needs-human",
  each: "pr",
  if:   (pr) => pr.ticket.status === "needs_human",
  then: (pr, ctx) => ctx.pr.queueMerge(pr, { once: ["merge-nh", pr.head] }),
});
whenever({
  name: "escalate-needs-human",
  each: "ticket",
  if:   (t) => t.status === "needs_human",
  then: (t, ctx) => ctx.ticket.queueEscalate(t, "still waiting", { once: ["pr-ignored", t.id] }),
});
`)})
	d.StartBeforeTheEvents()
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Workers.CheckedOut("ENG-1", "working")
	require.NoError(t, d.Workers.Ask("ENG-1").Err)
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Workers.SetStatus("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))

	d.Tick()
	res := d.Run("rules", "check")

	assert.Empty(t, d.GitHub.Merged())
	assert.Empty(t, d.Linear.Moves())
	assert.Regexp(t, `merge-needs-human .*exempt for ENG-1 by no-merge-or-escalation-while-asking-in-needs-human`, res.Stdout)
	assert.Regexp(t, `escalate-needs-human .*exempt for ENG-1 by no-merge-or-escalation-while-asking-in-needs-human`, res.Stdout)
}
