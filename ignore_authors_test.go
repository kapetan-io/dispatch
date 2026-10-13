package dispatch_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	ignoreSetting = "ignore_comment_authors = [\"github-actions[bot]\", \"codeowners-bot\"]\n"
	listedBot     = "GitHub-Actions[bot]"
)

// watchedIgnoring is watched() in a repo that lists github-actions[bot] and codeowners-bot.
func watchedIgnoring(t *testing.T, before prState) *harness {
	h := newHarness(t)
	h.repoSettings("dispatch", ignoreSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", before)
	_, err := h.run("tick")
	require.NoError(t, err)
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
			h := watchedIgnoring(t, before)
			test.add(&before)
			h.pr("ENG-1", before)

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.Empty(t, out)
			assert.Empty(t, h.prompts())
			assert.Empty(t, h.launches())
		})
	}
}

func TestTickCountsPRActivityByAListedAuthorInARepoThatListsNobody(t *testing.T) {
	before := cleanPR("a1")
	h := watched(t, before)
	before.comments = []activity{{"", time2, listedBot, "", ""}}
	h.pr("ENG-1", before)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: new PR activity: a comment by "+listedBot+"\n", out)
}

func TestTickCountsPRActivityByAnAuthorWhoIsNotListed(t *testing.T) {
	before := cleanPR("a1")
	h := watchedIgnoring(t, before)
	before.comments = []activity{{"", time2, "alice", "", ""}, {"", time3, listedBot, "", ""}}
	h.pr("ENG-1", before)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: new PR activity: a comment by alice\n", out)
}

func TestTickReadyIgnoresThreadsThatOnlyListedAuthorsTouched(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", ignoreSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	p := cleanPR("a1")
	p.conversations = [][]activity{{{"", time1, listedBot, "", ""}, {"", time2, "codeowners-bot", "", ""}}}
	h.pr("ENG-1", p)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "notify ENG-1: PR ready for review\n", out)
}

func TestTickReadyWaitsOnAThreadWithAnyUnlistedComment(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", ignoreSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	p := cleanPR("a1")
	p.conversations = [][]activity{{{"", time1, "alice", "", ""}, {"", time2, listedBot, "", ""}}}
	h.pr("ENG-1", p)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.NotContains(t, out, "notify")
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
	out, err := h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, "notify ENG-1: PR ready for review\n", out)
	assert.Empty(t, h.merges())

	// Nor is its change request a veto on a person's approval.
	p = cleanPR("a2")
	p.opinions = []opinion{{"APPROVED", "a2", "alice"}, {"CHANGES_REQUESTED", "a2", "codeowners-bot"}}
	h.pr("ENG-1", p)
	out, err = h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, "merge ENG-1: PR approved\n", out)
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

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: PR needs work: changes requested\n", out)
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
			setup(&before, listed(time2))
			// Page 2 is full of listed authors too, and the person's item is on page 3.
			h := newHarness(t)
			h.repoSettings("dispatch", ignoreSetting)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", "idle")
			h.pr("ENG-1", before)
			h.respondOlder(kind, "page2", listed(time1), "page3")
			h.respondOlder(kind, "page3", []activity{human}, "")

			out, err := h.run("tick")
			require.NoError(t, err)

			// Dispatch has not seen the person's item before, so it is new activity.
			article := map[string]string{"comments": "a comment", "reviews": "a review"}[kind]
			assert.Equal(t, fmt.Sprintf("nudge ENG-1: new PR activity: %s by alice\n", article), out)
			assert.Len(t, h.tools.Calls("pullrequest", "older"), 2)
		})
	}
}

func TestTickStopsReadingOlderPagesOnceAnUnlistedItemIsFound(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", ignoreSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	p := cleanPR("a1")
	p.comments = []activity{{"", time2, listedBot, "", ""}, {"", time3, "alice", "", ""}}
	p.commentsBefore = "page2"
	h.pr("ENG-1", p)

	_, err := h.run("tick")
	require.NoError(t, err)

	// An unset page 2 would fail the read, and no older call proves it was not requested.
	assert.Empty(t, h.tools.Calls("pullrequest", "older"))
}

func TestTickJudgesAPRWhoseNewestCommentsAreListedByOlderUnlistedOnes(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", ignoreSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "working")
	p := cleanPR("a1")
	p.comments = []activity{{"", time3, listedBot, "", ""}}
	p.commentsBefore = "page2"
	h.pr("ENG-1", p)
	h.respondOlder("comments", "page2", []activity{{"", time1, "alice", "", ""}}, "")
	_, err := h.run("tick")
	require.NoError(t, err)
	h.setWorkerStatus("ENG-1", "idle")

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: new PR activity: a comment by alice\n", out)
}

func TestTickMatchesListedAuthorsWithoutRegardToCase(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", "ignore_comment_authors = [\"CodeOwners-BOT\"]\n")
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	before := cleanPR("a1")
	h.pr("ENG-1", before)
	_, err := h.run("tick")
	require.NoError(t, err)
	before.comments = []activity{{"", time2, "codeowners-bot", "", ""}}
	h.pr("ENG-1", before)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, out)
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

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "notify ENG-1: PR ready for review\n", out)
}

func TestTickIgnoredAuthorsApplyOnlyToTheRepoThatListsThem(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	before := cleanPR("a1")
	h.pr("ENG-1", before)
	_, err := h.run("tick")
	require.NoError(t, err)
	before.reviews = []activity{{"COMMENTED", time2, listedBot, "", ""}}
	h.pr("ENG-1", before)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: new PR activity: a review by "+listedBot+"\n", out)
}

func TestTickReadsPastUnlistedReviewsThatAskForNothing(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", ignoreSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "working")
	p := cleanPR("a1")
	p.reviews = []activity{{state: "DISMISSED", at: time3, author: "alice"}}
	p.reviewsBefore = "page2"
	h.pr("ENG-1", p)
	h.respondOlder("reviews", "page2", []activity{{state: "COMMENTED", at: time1, author: "alice"}}, "")
	_, err := h.run("tick")
	require.NoError(t, err)
	h.setWorkerStatus("ENG-1", "idle")

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: new PR activity: a review by alice\n", out)
}
