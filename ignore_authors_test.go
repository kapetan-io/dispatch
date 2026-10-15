package dispatch_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	ignoreSetting = "ignore_comment_authors = [\"github-actions[bot]\", \"codeowners-bot\"]\n"
	listedBot     = "GitHub-Actions[bot]"
)

// watchedIgnoring starts an idle In Review worker whose PR is in the state before, in a repo with
// the given settings, and ticks so dispatch knows the worker. The clock then moves on, so
// anything that arrives afterwards is newer than the worker.
func watchedIgnoring(t *testing.T, settings string, before prState) *harness {
	h := newHarness(t)
	if settings != "" {
		h.repoSettings("dispatch", settings)
	}
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", before)
	h.tick()
	h.advance(time.Second)
	return h
}

func TestTickIgnoresPRActivityByAListedAuthor(t *testing.T) {
	for _, test := range []struct {
		name string
		add  func(p *prState)
	}{
		{name: "ConversationComment", add: func(p *prState) { p.comments = append(p.comments, activity{"", time2, listedBot, "", ""}) }},
		{name: "BodyOnlyReview", add: func(p *prState) {
			p.reviews = append(p.reviews, activity{"COMMENTED", time2, "codeowners-bot", "", ""})
		}},
		{name: "ChangesRequestedReview", add: func(p *prState) {
			p.reviews = append(p.reviews, activity{"CHANGES_REQUESTED", time2, listedBot, "", ""})
			p.opinions = append(p.opinions, opinion{"CHANGES_REQUESTED", "a1", listedBot})
		}},
		{name: "ThreadComment", add: func(p *prState) { p.threads = append(p.threads, activity{"", time2, listedBot, "", ""}) }},
		{name: "ThreadOfOnlyListedAuthors", add: func(p *prState) {
			p.conversations = append(p.conversations, []activity{{"", time1, listedBot, "", ""}, {"", time2, "codeowners-bot", "", ""}})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := cleanPR("a1")
			h := watchedIgnoring(t, ignoreSetting, before)
			notified := h.notifications()
			test.add(&before)
			h.pr("ENG-1", before)

			h.tick()

			assert.Empty(t, h.prompts())
			assert.Empty(t, h.launches())
			assert.Equal(t, notified, h.notifications())
		})
	}
}

func TestTickCountsPRActivityByAListedAuthorInARepoThatListsNobody(t *testing.T) {
	before := cleanPR("a1")
	h := watchedIgnoring(t, "", before)
	before.comments = []activity{{"", time2, listedBot, "", ""}}
	h.pr("ENG-1", before)

	h.tick()

	require.Len(t, h.prompts(), 1)
	assert.Contains(t, h.prompts()[0], "a comment by "+listedBot)
}

func TestTickCountsPRActivityByAnAuthorWhoIsNotListed(t *testing.T) {
	before := cleanPR("a1")
	h := watchedIgnoring(t, ignoreSetting, before)
	before.comments = []activity{{"", time2, "alice", "", ""}, {"", time3, listedBot, "", ""}}
	h.pr("ENG-1", before)

	h.tick()

	require.Len(t, h.prompts(), 1)
	assert.Contains(t, h.prompts()[0], "a comment by alice")
	assert.NotContains(t, h.prompts()[0], listedBot)
}

func TestTickReadyIgnoresThreadsThatOnlyListedAuthorsTouched(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", ignoreSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	p := cleanPR("a1")
	p.conversations = [][]activity{{{"", time1, listedBot, "", ""}, {"", time2, "codeowners-bot", "", ""}}}
	h.pr("ENG-1", p)

	h.tick()

	assert.Equal(t, []string{"ENG-1 is ready for review: " + prURL1}, h.notifications())
}

func TestTickReadyWaitsOnAThreadWithAnyUnlistedComment(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", ignoreSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	p := cleanPR("a1")
	p.conversations = [][]activity{{{"", time1, "alice", "", ""}, {"", time2, listedBot, "", ""}}}
	h.pr("ENG-1", p)

	h.tick()

	assert.Empty(t, h.notifications())
}

func TestTickIgnoresAListedAuthorsApprovalAndChangeRequest(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", ignoreSetting+autoMergeSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	p := cleanPR("a1")
	p.opinions = []opinion{{"APPROVED", "a1", listedBot}}
	h.pr("ENG-1", p)

	// A listed author's approval is not an approval.
	h.tick()
	assert.Equal(t, []string{"ENG-1 is ready for review: " + prURL1}, h.notifications())
	assert.Empty(t, h.merges())

	// Nor is its change request a veto on a person's approval.
	p = cleanPR("a2")
	p.opinions = []opinion{{"APPROVED", "a2", "alice"}, {"CHANGES_REQUESTED", "a2", "codeowners-bot"}}
	h.pr("ENG-1", p)
	h.tick()
	assert.Equal(t, []string{prURL1 + "|a2"}, h.merges())
}

func TestTickCountsAChangeRequestFromAnUnlistedAuthor(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", ignoreSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	p := cleanPR("a1")
	p.opinions = []opinion{{"CHANGES_REQUESTED", "a1", "alice"}, {"CHANGES_REQUESTED", "a1", listedBot}}
	h.pr("ENG-1", p)

	h.tick()

	require.Len(t, h.prompts(), 1)
	assert.Contains(t, h.prompts()[0], "changes requested")
}

func TestTickReadsPastAWindowOfListedAuthorsToTheNewestUnlistedItem(t *testing.T) {
	for _, kind := range []string{"comments", "reviews"} {
		t.Run(kind, func(t *testing.T) {
			listed := func(at string) []activity {
				var out []activity
				for range 100 {
					a := activity{at: at, author: listedBot}
					if kind == "reviews" {
						a.state = "COMMENTED"
					}
					out = append(out, a)
				}
				return out
			}
			human := activity{at: time1, author: "alice"}
			if kind == "reviews" {
				human.state = "COMMENTED"
			}
			setup := func(p *prState, newest []activity) {
				if kind == "reviews" {
					p.reviews, p.reviewsBefore = newest, "page2"
				} else {
					p.comments, p.commentsBefore = newest, "page2"
				}
			}
			before := cleanPR("a1")
			h := watchedIgnoring(t, ignoreSetting, before)
			setup(&before, listed(time2))
			h.pr("ENG-1", before)
			// Page 2 is full of listed authors too, and the person's item is on page 3.
			h.respondOlder(kind, "page2", listed(time1), "page3")
			h.respondOlder(kind, "page3", []activity{human}, "")

			h.tick()

			// Dispatch has not seen the person's item before, so it is new activity.
			article := map[string]string{"comments": "a comment", "reviews": "a review"}[kind]
			require.Len(t, h.prompts(), 1)
			assert.Contains(t, h.prompts()[0], fmt.Sprintf("new activity, %s by alice", article))
			assert.Len(t, h.tools.Calls("pullrequest", "older"), 2)
		})
	}
}

func TestTickStopsReadingOlderPagesOnceAnUnlistedItemIsFound(t *testing.T) {
	p := cleanPR("a1")
	h := watchedIgnoring(t, ignoreSetting, p)
	p.comments = []activity{{"", time2, listedBot, "", ""}, {"", time3, "alice", "", ""}}
	p.commentsBefore = "page2"
	h.pr("ENG-1", p)

	h.tick()

	// An unset page 2 would fail the read, and no older call proves it was not requested.
	assert.Empty(t, h.tools.Calls("pullrequest", "older"))
}

func TestTickJudgesAPRWhoseNewestCommentsAreListedByOlderUnlistedOnes(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", ignoreSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "working")
	p := cleanPR("a1")
	h.pr("ENG-1", p)
	h.tick()
	h.advance(time.Second)
	p.comments = []activity{{"", time3, listedBot, "", ""}}
	p.commentsBefore = "page2"
	h.pr("ENG-1", p)
	h.respondOlder("comments", "page2", []activity{{"", time1, "alice", "", ""}}, "")
	h.tick()
	h.setWorkerStatus("ENG-1", "idle")

	h.tick()

	require.Len(t, h.prompts(), 1)
	assert.Contains(t, h.prompts()[0], "a comment by alice")
}

func TestTickMatchesListedAuthorsWithoutRegardToCase(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", "ignore_comment_authors = [\"CodeOwners-BOT\"]\n")
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	before := cleanPR("a1")
	h.pr("ENG-1", before)
	h.tick()
	h.advance(time.Second)
	before.comments = []activity{{"", time2, "codeowners-bot", "", ""}}
	h.pr("ENG-1", before)

	h.tick()

	assert.Empty(t, h.prompts())
}

func TestTickReadyIgnoresListedCommentsReviewsAndThreads(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", ignoreSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	p := cleanPR("a1")
	p.comments = []activity{{"", time2, listedBot, "", ""}}
	p.reviews = []activity{{"COMMENTED", time3, "codeowners-bot", "", ""}}
	p.threads = []activity{{"", time3, listedBot, "", ""}}
	h.pr("ENG-1", p)

	h.tick()

	assert.Equal(t, []string{"ENG-1 is ready for review: " + prURL1}, h.notifications())
}

func TestTickIgnoredAuthorsApplyOnlyToTheRepoThatListsThem(t *testing.T) {
	before := cleanPR("a1")
	h := watchedIgnoring(t, "", before)
	before.reviews = []activity{{"COMMENTED", time2, listedBot, "", ""}}
	h.pr("ENG-1", before)

	h.tick()

	require.Len(t, h.prompts(), 1)
	assert.Contains(t, h.prompts()[0], "a review by "+listedBot)
}

func TestTickReadsPastUnlistedReviewsThatAskForNothing(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", ignoreSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "working")
	p := cleanPR("a1")
	h.pr("ENG-1", p)
	h.tick()
	h.advance(time.Second)
	p.reviews = []activity{{state: "DISMISSED", at: time3, author: "alice"}}
	p.reviewsBefore = "page2"
	h.pr("ENG-1", p)
	h.respondOlder("reviews", "page2", []activity{{state: "COMMENTED", at: time1, author: "alice"}}, "")
	h.tick()
	h.setWorkerStatus("ENG-1", "idle")

	h.tick()

	require.Len(t, h.prompts(), 1)
	assert.Contains(t, h.prompts()[0], "a review by alice")
}
