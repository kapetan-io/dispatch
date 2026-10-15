package dispatch_test

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/ledger"
)

const (
	autoMergeSetting = "auto_merge = true\n"
	mergedText1      = "ENG-1 was approved and merged: " + prURL1
	mergeCall1       = prURL1 + "|a1"
	mergeRefused     = "Pull request is not mergeable: the base branch policy prohibits the merge"
)

func approvedPR(head string) prState {
	pr := cleanPR(head)
	pr.approved = []string{head}
	return pr
}

// automergeReceipts lists the receipts of one kind the loop has written, oldest first.
func automergeReceipts(h *harness, kind string) []ledger.Receipt {
	var found []ledger.Receipt
	for _, r := range h.ledgerNow() {
		if r.Kind == kind {
			found = append(found, r)
		}
	}
	return found
}

func TestTickMergesAnApprovedReadyPRInAListedRepo(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", autoMergeSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", approvedPR("a1"))

	h.tick()

	assert.Equal(t, []string{mergeCall1}, h.merges())
	assert.Equal(t, []string{mergedText1}, h.notifications())
	assert.Empty(t, h.prompts())

	// The merged PR is no longer open, so later ticks do nothing.
	h.tick()
	assert.Len(t, h.merges(), 1)
	assert.Len(t, h.notifications(), 1)
}

func TestTickDoesNotMergeInARepoWithoutAutoMerge(t *testing.T) {
	for _, test := range []struct {
		name     string
		settings string
		other    string
	}{
		{name: "NoSetting"},
		{name: "SetFalse", settings: "auto_merge = false\n"},
		{name: "OnlyAnotherRepoSetsIt", other: "auto_merge = true\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.addRepo("tiger", filepath.Join(h.dir, "tiger"))
			h.repoSettings("dispatch", test.settings)
			h.repoSettings("tiger", test.other)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", "idle")
			h.pr("ENG-1", approvedPR("a1"))

			h.tick()

			assert.Equal(t, []string{readyText1}, h.notifications())
			assert.Empty(t, h.merges())
		})
	}
}

func TestTickDoesNotMergeAPRWithoutAnApprovalOnItsHead(t *testing.T) {
	for _, test := range []struct {
		name string
		pr   prState
	}{
		{name: "NoReview", pr: prState{head: "a1", checks: "SUCCESS", mergeable: "MERGEABLE"}},
		{name: "ApprovedAnEarlierCommit", pr: prState{head: "a2", checks: "SUCCESS", decision: "APPROVED", mergeable: "MERGEABLE", approved: []string{"a1"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.repoSettings("dispatch", autoMergeSetting)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", "idle")
			h.pr("ENG-1", test.pr)

			h.tick()

			assert.Equal(t, []string{readyText1}, h.notifications())
			assert.Empty(t, h.merges())
		})
	}
}

func TestTickDoesNotMergeAnApprovedPRThatIsNotReady(t *testing.T) {
	for _, test := range []struct {
		name string
		pr   prState
	}{
		{name: "CheckStillRunning", pr: prState{head: "a1", checks: "PENDING", mergeable: "MERGEABLE", approved: []string{"a1"}}},
		{name: "FailedCheck", pr: prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE", approved: []string{"a1"}}},
		{name: "UnresolvedThread", pr: prState{head: "a1", checks: "SUCCESS", mergeable: "MERGEABLE", unresolved: 1, approved: []string{"a1"}}},
		{name: "MergeConflict", pr: prState{head: "a1", checks: "SUCCESS", mergeable: "CONFLICTING", approved: []string{"a1"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.repoSettings("dispatch", autoMergeSetting)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", "idle")
			h.pr("ENG-1", test.pr)

			_, err := h.run("tick")
			require.NoError(t, err)

			assert.Empty(t, h.merges())
		})
	}
}

func TestTickMergesAfterAnApprovalArrivesOnAnAlreadyAnnouncedHead(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", autoMergeSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a1", checks: "SUCCESS", mergeable: "MERGEABLE"})

	h.tick()
	assert.Equal(t, []string{readyText1}, h.notifications())

	h.pr("ENG-1", approvedPR("a1"))
	h.tick()

	assert.Equal(t, []string{mergeCall1}, h.merges())
	assert.Equal(t, []string{readyText1, mergedText1}, h.notifications())
}

func TestTickTellsFollowersAboutAMergeNoticeWhenTheAlertFails(t *testing.T) {
	for _, test := range []struct {
		name    string
		refused bool
		want    string
	}{
		{name: "Merged", want: "ENG-1 was approved and merged"},
		{name: "MergeFailed", refused: true, want: "merging " + prURL1 + " failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.repoSettings("dispatch", autoMergeSetting)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", "idle")
			h.pr("ENG-1", approvedPR("a1"))
			socket, lines := sessionSocket(t)
			t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
			_, err := h.run("watch", "ENG-1")
			require.NoError(t, err)
			h.tools.Alerts.Fail(errors.New("exit status 1"))
			if test.refused {
				h.tools.PullRequests.FailMerge(errors.New("while running gh pr merge " + prURL1 + ": exit status 1: " + mergeRefused))
			}

			_, err = h.run("tick")
			if test.refused {
				require.ErrorContains(t, err, "merge refused")
			} else {
				require.NoError(t, err)
			}

			// The alert failed, yet the followers heard and the notice is Done, not retried.
			assert.Contains(t, next(t, lines), test.want)
			assert.Empty(t, h.notifications())
			notices := automergeReceipts(h, "notify")
			require.Len(t, notices, 1)
			assert.Equal(t, ledger.Done, notices[0].Outcome)
			assert.Contains(t, notices[0].Detail, "alert failed")

			h.tools.Alerts.Fail(nil)
			h.tools.PullRequests.FailMerge(nil)
			h.advance(5 * time.Minute)
			h.advance(time.Second)
			h.tick()
			assert.Empty(t, lines)
			assert.Empty(t, h.notifications())
			assert.Len(t, automergeReceipts(h, "notify"), 1)
		})
	}
}

func TestTickWaitsForANewApprovalAfterAPush(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", autoMergeSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a2", checks: "SUCCESS", decision: "APPROVED", mergeable: "MERGEABLE", approved: []string{"a1"}})

	h.tick()
	assert.Empty(t, h.merges())

	h.pr("ENG-1", approvedPR("a2"))
	h.tick()

	assert.Equal(t, []string{strings.Replace(mergeCall1, "a1", "a2", 1)}, h.merges())
}

func TestTickReportsAFailedMergeOnceAndRetriesOnlyAfterAPush(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", autoMergeSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", approvedPR("a1"))
	h.tools.PullRequests.FailMerge(errors.New("while running gh pr merge " + prURL1 + ": exit status 1: " + mergeRefused))

	_, err := h.run("tick")
	require.ErrorContains(t, err, "the base branch policy prohibits the merge")

	require.Len(t, h.notifications(), 1)
	assert.Contains(t, h.notifications()[0], "ENG-1: merging "+prURL1+" failed")
	assert.Contains(t, h.notifications()[0], "the base branch policy prohibits the merge")
	attempts := automergeReceipts(h, "merge")
	require.Len(t, attempts, 1)
	assert.Equal(t, ledger.Refused, attempts[0].Outcome)
	assert.Equal(t, "a1", attempts[0].Once[1])

	// A refused merge never runs again for its head, not at a tick and not at a resync,
	// and the user hears nothing more.
	h.tools.PullRequests.FailMerge(nil)
	h.advance(time.Second)
	h.tick()
	h.advance(5 * time.Minute)
	h.advance(time.Second)
	h.tick()
	assert.Empty(t, h.merges())
	assert.Len(t, h.notifications(), 1)
	assert.Len(t, automergeReceipts(h, "merge"), 1)

	// A push that gets approved is a new head, so it merges.
	h.pr("ENG-1", approvedPR("a2"))
	h.tick()
	assert.Len(t, h.merges(), 1)
	attempts = automergeReceipts(h, "merge")
	require.Len(t, attempts, 2)
	assert.Equal(t, ledger.Done, attempts[1].Outcome)
	assert.Equal(t, "a2", attempts[1].Once[1])
}

func TestTickMergesNothingWhenThePRHeadMovesAfterTheApprovalWasRead(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", autoMergeSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", approvedPR("a1"))
	// Someone pushes between dispatch reading the approval and merging.
	h.tools.PullRequests.PushAfterRead(prBranch("ENG-1"), "a2")

	_, err := h.run("tick")
	require.ErrorContains(t, err, "Head branch was modified")

	assert.Empty(t, h.merges())
	assert.Equal(t, "a2", h.tools.PullRequests.Head(prBranch("ENG-1")))
	require.Len(t, h.notifications(), 1)
	assert.Contains(t, h.notifications()[0], "ENG-1: merging "+prURL1+" failed")

	// The refused merge is the only attempt for a1, and the new head has no approval, so it
	// is announced as ready for review and not merged.
	h.advance(time.Second)
	h.tick()
	assert.Empty(t, h.merges())
	assert.Contains(t, h.notifications()[len(h.notifications())-1], "ready for review")
	attempts := automergeReceipts(h, "merge")
	require.Len(t, attempts, 1)
	assert.Equal(t, ledger.Refused, attempts[0].Outcome)

	// An approval of the head that moved merges it: a push makes a new key.
	h.pr("ENG-1", approvedPR("a2"))
	h.tick()
	assert.Equal(t, []string{strings.Replace(mergeCall1, "a1", "a2", 1)}, h.merges())
}

func TestTickReadsAutoMergeOnRestart(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", approvedPR("a1"))

	// Not listed yet, so the PR is announced.
	h.tick()
	assert.Empty(t, h.merges())

	// The loop reads config.toml when it starts, so a running loop still does not merge.
	h.repoSettings("dispatch", autoMergeSetting)
	h.tick()
	assert.Empty(t, h.merges())

	h.restart()
	h.tick()
	assert.Equal(t, []string{mergeCall1}, h.merges())
}

func (h *harness) prWithReview(url, state, commit string) {
	h.respond("ENG-1", "", map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequests": map[string]any{"nodes": []any{map[string]any{
		"number":                   7,
		"url":                      url,
		"headRefOid":               "a1",
		"mergeable":                "MERGEABLE",
		"reviewDecision":           "APPROVED",
		"commits":                  map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{"statusCheckRollup": map[string]string{"state": "SUCCESS"}}}}},
		"latestOpinionatedReviews": map[string]any{"nodes": []any{map[string]any{"state": state, "commit": map[string]string{"oid": commit}}}},
		"reviewThreads":            map[string]any{"nodes": []any{}, "pageInfo": map[string]bool{"hasNextPage": false}},
	}}}}}})
}

func TestTickDoesNotMergeWhenTheReviewOnTheHeadIsNotAnApproval(t *testing.T) {
	for _, state := range []string{"CHANGES_REQUESTED", "COMMENTED", "DISMISSED"} {
		t.Run(state, func(t *testing.T) {
			h := newHarness(t)
			h.repoSettings("dispatch", autoMergeSetting)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", "idle")
			h.prWithReview(prURL1, state, "a1")

			h.tick()

			assert.Empty(t, h.merges())
		})
	}
}

func TestTickDoesNotRetryAFailedMergeOnAHeadAnnouncedAsReady(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", autoMergeSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", cleanPR("a1"))
	h.tick()
	h.pr("ENG-1", approvedPR("a1"))
	h.tools.PullRequests.FailMerge(errors.New("while running gh pr merge " + prURL1 + ": exit status 1: " + mergeRefused))
	_, err := h.run("tick")
	require.ErrorContains(t, err, "merge refused")
	h.tools.PullRequests.FailMerge(nil)

	h.advance(time.Second)
	h.tick()

	assert.Empty(t, h.merges())
	assert.Len(t, h.notifications(), 2)
}

func TestTickDoesNotRetryAFailedMergeAfterTheTicketLeavesTheReadAndReturns(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", autoMergeSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", approvedPR("a1"))
	h.tools.PullRequests.FailMerge(errors.New("while running gh pr merge " + prURL1 + ": exit status 1: " + mergeRefused))
	_, err := h.run("tick")
	require.ErrorContains(t, err, "merge refused")
	require.Len(t, h.notifications(), 1)

	// One tick where the ticket is missing from Linear's read, with its tree still leased.
	h.tickets()
	h.advance(time.Second)
	h.tick()
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.tools.PullRequests.FailMerge(nil)
	h.advance(time.Second)
	h.tick()

	assert.Len(t, h.notifications(), 1)
	assert.Empty(t, h.merges())
}

// Without branch protection GitHub leaves reviewDecision empty even when a
// reviewer's latest review requests changes.
func TestTickDoesNotMergeOverAnotherReviewersChangesRequested(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", autoMergeSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.respond("ENG-1", "", map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequests": map[string]any{"nodes": []any{map[string]any{
		"number":         7,
		"url":            prURL1,
		"headRefOid":     "a1",
		"mergeable":      "MERGEABLE",
		"reviewDecision": "",
		"commits":        map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{"statusCheckRollup": map[string]string{"state": "SUCCESS"}}}}},
		"latestOpinionatedReviews": map[string]any{"nodes": []any{
			map[string]any{"state": "APPROVED", "commit": map[string]string{"oid": "a1"}},
			map[string]any{"state": "CHANGES_REQUESTED", "commit": map[string]string{"oid": "a1"}},
		}},
		"reviewThreads": map[string]any{"nodes": []any{}, "pageInfo": map[string]bool{"hasNextPage": false}},
	}}}}}})

	h.tick()

	assert.Empty(t, h.merges())
}

func TestTickDoesNotMergeAForkPRThatSharesTheWorkersBranchName(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", autoMergeSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	fork := approvedPR("evil")
	fork.fork = true
	h.pr("ENG-1", fork)

	h.tick()
	h.advance(5 * time.Minute)
	h.tick()

	assert.Empty(t, h.merges())
	assert.Empty(t, h.notifications())
	assert.Empty(t, automergeReceipts(h, "merge"))
}

func TestTickUsesTheWorkersPRWhenAForkPRSharesItsBranchName(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", autoMergeSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", approvedPR("a1"))
	var file struct {
		Data struct {
			Repository struct {
				PullRequests struct {
					Nodes []map[string]any `json:"nodes"`
				} `json:"pullRequests"`
			} `json:"repository"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(h.responded("ENG-1", ""), &file))
	own := file.Data.Repository.PullRequests.Nodes[0]
	evil := map[string]any{}
	for k, v := range own {
		evil[k] = v
	}
	evil["isCrossRepository"], evil["headRefOid"], evil["url"] = true, "evil", "https://github.com/kapetan-io/dispatch/pull/99"
	file.Data.Repository.PullRequests.Nodes = []map[string]any{evil, own}
	h.respond("ENG-1", "", file)

	h.tick()

	assert.Equal(t, []string{mergeCall1}, h.merges())
}

// forkPage writes the first page of the ticket's PR query: 20 fork PRs and a
// cursor to the next page, which holds the worker's own PR.
func (h *harness) forkPage(next string) {
	var file struct {
		Data struct {
			Repository struct {
				PullRequests struct {
					PageInfo map[string]any   `json:"pageInfo"`
					Nodes    []map[string]any `json:"nodes"`
				} `json:"pullRequests"`
			} `json:"repository"`
		} `json:"data"`
	}
	require.NoError(h.t, json.Unmarshal(h.responded("ENG-1", ""), &file))
	forks := []map[string]any{}
	for range 20 {
		fork := map[string]any{}
		for k, v := range file.Data.Repository.PullRequests.Nodes[0] {
			fork[k] = v
		}
		fork["isCrossRepository"], fork["headRefOid"], fork["url"] = true, "evil", "https://github.com/kapetan-io/dispatch/pull/99"
		forks = append(forks, fork)
	}
	file.Data.Repository.PullRequests.Nodes = forks
	file.Data.Repository.PullRequests.PageInfo = map[string]any{"hasNextPage": true, "endCursor": next}
	h.respond("ENG-1", "", file)
}

func TestTickFindsTheWorkersPRBehindAFullPageOfForkPRs(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", autoMergeSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", approvedPR("a1"))
	h.respond("ENG-1", "c1", json.RawMessage(h.responded("ENG-1", "")))
	h.forkPage("c1")

	h.tick()

	assert.Equal(t, []string{mergeCall1}, h.merges())
	// Each read of the branch follows the cursor to the second page; the loop reads again
	// at the tick and after the merge.
	require.GreaterOrEqual(t, len(h.prReads()), 2)
	assert.Equal(t, []string{"thrawn01/eng-1", "thrawn01/eng-1.c1"}, h.prReads()[:2])
}

func TestTickReportsABranchWhoseForkPRsFillEveryPageItReads(t *testing.T) {
	h := newHarness(t)
	h.repoSettings("dispatch", autoMergeSetting)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", approvedPR("a1"))
	h.forkPage("c1")
	// Every page points at the next one, so the worker's PR is never reached.
	h.respond("ENG-1", "c1", json.RawMessage(h.responded("ENG-1", "")))

	// The ticket is held and the tick still succeeds.
	h.tick()

	assert.Contains(t, h.loopErr(), "while reading the PR for ENG-1")
	assert.Contains(t, h.loopErr(), "none from this repo")
	plan, err := h.run("plan")
	require.NoError(t, err)
	assert.Regexp(t, `held\s+ENG-1\s+-\s+auto-merge\s+the PR for ENG-1 could not be read`, plan)
	assert.Empty(t, h.merges())
	// Each read of the branch stops after five pages.
	assert.NotEmpty(t, h.prReads())
	assert.Zero(t, len(h.prReads())%5)
}
