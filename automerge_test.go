package dispatch_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	autoMergeConfig = "auto_merge = [\"kapetan-io/dispatch\"]\n"
	mergedText1     = "ENG-1 was approved and merged: " + prURL1
	mergeCall1      = prURL1 + "|a1"
	mergeRefused    = "Pull request is not mergeable: the base branch policy prohibits the merge"
)

func approvedPR(head string) prState {
	pr := cleanPR(head)
	pr.approved = []string{head}
	return pr
}

func TestTickMergesAnApprovedReadyPRInAListedRepo(t *testing.T) {
	h := newHarness(t)
	h.config(autoMergeConfig)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", approvedPR("a1"))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "merge ENG-1: PR approved\n", out)
	assert.Equal(t, []string{mergeCall1}, h.merges())
	assert.Equal(t, []string{mergedText1}, h.notifications())
	assert.Empty(t, h.prompts())

	// The merged PR is no longer open, so later ticks do nothing.
	out, err = h.run("tick")
	require.NoError(t, err)
	assert.Empty(t, out)
	assert.Len(t, h.merges(), 1)
}

func TestTickMatchesTheRepoCaseInsensitively(t *testing.T) {
	h := newHarness(t)
	h.config("auto_merge = [\"Kapetan-IO/Dispatch\"]\n")
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", approvedPR("a1"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.merges(), 1)
}

func TestTickDoesNotMergeInARepoThatIsNotListed(t *testing.T) {
	for _, test := range []struct {
		name   string
		config string
	}{
		{name: "NoAutoMerge", config: ""},
		{name: "EmptyList", config: "auto_merge = []\n"},
		{name: "OtherRepo", config: "auto_merge = [\"kapetan-io/tiger\"]\n"},
		{name: "SameNameOtherOwner", config: "auto_merge = [\"someone/dispatch\"]\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.config(test.config)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", "idle")
			h.pr("ENG-1", approvedPR("a1"))

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.Equal(t, "notify ENG-1: PR ready for review\n", out)
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
			h.config(autoMergeConfig)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", "idle")
			h.pr("ENG-1", test.pr)

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.Equal(t, "notify ENG-1: PR ready for review\n", out)
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
			h.config(autoMergeConfig)
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
	h.config(autoMergeConfig)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a1", checks: "SUCCESS", mergeable: "MERGEABLE"})

	_, err := h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, []string{readyText1}, h.notifications())

	h.pr("ENG-1", approvedPR("a1"))
	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "merge ENG-1: PR approved\n", out)
	assert.Equal(t, []string{mergeCall1}, h.merges())
	assert.Equal(t, []string{readyText1, mergedText1}, h.notifications())
}

func TestTickWaitsForANewApprovalAfterAPush(t *testing.T) {
	h := newHarness(t)
	h.config(autoMergeConfig)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a2", checks: "SUCCESS", decision: "APPROVED", mergeable: "MERGEABLE", approved: []string{"a1"}})

	_, err := h.run("tick")
	require.NoError(t, err)
	assert.Empty(t, h.merges())

	h.pr("ENG-1", approvedPR("a2"))
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{strings.Replace(mergeCall1, "a1", "a2", 1)}, h.merges())
}

func TestTickReportsAFailedMergeOnceAndRetriesOnlyAfterAPush(t *testing.T) {
	h := newHarness(t)
	h.config(autoMergeConfig)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", approvedPR("a1"))
	h.tools.PullRequests.FailMerge(errors.New("while running gh pr merge " + prURL1 + ": exit status 1: " + mergeRefused))

	out, stderr, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Contains(t, stderr, "while merging ENG-1")
	assert.Contains(t, stderr, "the base branch policy prohibits the merge")
	require.Len(t, h.notifications(), 1)
	assert.Contains(t, h.notifications()[0], "ENG-1: merging "+prURL1+" failed")
	assert.Contains(t, h.notifications()[0], "the base branch policy prohibits the merge")

	// The same head is not retried, and the user hears nothing more.
	h.tools.PullRequests.FailMerge(nil)
	for range 2 {
		out, stderr, err = h.runContext(t.Context(), "tick")
		require.NoError(t, err)
		assert.Empty(t, out)
		assert.Empty(t, stderr)
	}
	assert.Empty(t, h.merges())
	assert.Len(t, h.notifications(), 1)

	// A push that gets approved is a new head, so it merges.
	h.pr("ENG-1", approvedPR("a2"))
	out, err = h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, "merge ENG-1: PR approved\n", out)
	assert.Len(t, h.merges(), 1)
}

func TestTickMergesNothingWhenThePRHeadMovesAfterTheApprovalWasRead(t *testing.T) {
	h := newHarness(t)
	h.config(autoMergeConfig)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", approvedPR("a1"))
	// Someone pushes between dispatch reading the approval and merging.
	h.tools.PullRequests.PushAfterRead(prBranch("ENG-1"), "a2")

	out, stderr, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Contains(t, stderr, "while merging ENG-1")
	assert.Contains(t, stderr, "Head branch was modified")
	assert.Empty(t, h.merges())
	assert.Equal(t, "a2", h.tools.PullRequests.Head(prBranch("ENG-1")))
	require.Len(t, h.notifications(), 1)
	assert.Contains(t, h.notifications()[0], "ENG-1: merging "+prURL1+" failed")

	// The new head has no approval, so it is announced as ready for review and not merged.
	out, err = h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, "notify ENG-1: PR ready for review\n", out)
	assert.Empty(t, h.merges())
}

func TestTickDryRunNamesTheMergeWithoutMerging(t *testing.T) {
	h := newHarness(t)
	h.config(autoMergeConfig)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", approvedPR("a1"))

	out, err := h.run("tick", "--dry-run")
	require.NoError(t, err)

	assert.Equal(t, "merge ENG-1: PR approved\n", out)
	assert.Empty(t, h.merges())
	assert.Empty(t, h.notifications())
}

func TestTickReadsAutoMergeOnEveryTick(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", approvedPR("a1"))

	// Not listed yet, so the PR is announced.
	_, err := h.run("tick")
	require.NoError(t, err)
	assert.Empty(t, h.merges())

	h.config(autoMergeConfig)
	_, err = h.run("tick")
	require.NoError(t, err)
	assert.Len(t, h.merges(), 1)
}

func TestConfigRejectsAMalformedAutoMergeEntry(t *testing.T) {
	for _, test := range []struct {
		name   string
		config string
		want   string
	}{
		{name: "NoOwner", config: "auto_merge = [\"dispatch\"]\n", want: `auto_merge entry "dispatch" is not owner/name`},
		{name: "ThreeParts", config: "auto_merge = [\"a/b/c\"]\n", want: `auto_merge entry "a/b/c" is not owner/name`},
		{name: "Empty", config: "auto_merge = [\"\"]\n", want: `auto_merge entry "" is not owner/name`},
		{name: "EmptyName", config: "auto_merge = [\"kapetan-io/\"]\n", want: `auto_merge entry "kapetan-io/" is not owner/name`},
		{name: "Path", config: "auto_merge = [\"~/Development/dispatch\"]\n", want: `is not owner/name`},
		{name: "OneBadAmongGood", config: "auto_merge = [\"kapetan-io/dispatch\", \"nope\"]\n", want: `auto_merge entry "nope" is not owner/name`},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.config(test.config)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))

			_, err := h.run("tick")

			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestRunWarnsOncePerRunAboutAnAutoMergeEntryThatMatchesNoRepo(t *testing.T) {
	h := newHarness(t)
	h.config("auto_merge = [\"kapetan-io/dispatch\", \"kapetan-io/tiger\"]\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	var stderr string

	go func() {
		var err error
		_, stderr, err = h.runContext(ctx, "run", "--interval", "20ms")
		done <- err
	}()
	require.Eventually(t, func() bool { return len(h.launches()) >= 4 }, 15*time.Second, 10*time.Millisecond)
	cancel()

	require.NoError(t, <-done)
	assert.Equal(t, 1, strings.Count(stderr, `auto_merge entry "kapetan-io/tiger" matches no repo`))
	assert.NotContains(t, stderr, `"kapetan-io/dispatch"`)
	// The repo name is looked up once, not on every tick.
	assert.Len(t, h.tools.Calls("pullrequest", "repo-name"), 1)
}

func TestTickWarnsAboutAnAutoMergeEntryThatMatchesNoRepoWithoutFailing(t *testing.T) {
	h := newHarness(t)
	h.config("auto_merge = [\"kapetan-io/tiger\"]\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	out, stderr, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)

	assert.Equal(t, "launch ENG-1: ok\n", out)
	assert.Contains(t, stderr, `warning: auto_merge entry "kapetan-io/tiger" matches no repo under [repos]`)
}

func TestTickSaysNothingOfAutoMergeWhenItIsEmpty(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	_, stderr, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)

	assert.Empty(t, stderr)
	assert.Empty(t, h.tools.Calls("pullrequest", "repo-name"))
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
			h.config(autoMergeConfig)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", "idle")
			h.prWithReview(prURL1, state, "a1")

			_, err := h.run("tick")
			require.NoError(t, err)

			assert.Empty(t, h.merges())
		})
	}
}

func TestTickMatchesThePRByTheRepoItsURLNames(t *testing.T) {
	const otherURL = "https://github.com/someone/fork/pull/7"

	// The clone resolves to a listed repo, but the PR lives in another one.
	h := newHarness(t)
	h.config(autoMergeConfig)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.prWithReview(otherURL, "APPROVED", "a1")

	out, err := h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, "notify ENG-1: PR ready for review\n", out)
	assert.Empty(t, h.merges())

	// The PR's repo is listed, though the clone resolves to a different one.
	h = newHarness(t)
	h.config("auto_merge = [\"someone/fork\"]\n")
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.prWithReview(otherURL, "APPROVED", "a1")

	out, err = h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, "merge ENG-1: PR approved\n", out)
	assert.Equal(t, []string{otherURL + "|a1"}, h.merges())
}

func TestTickDryRunLeavesTheMergeForTheNextRealTick(t *testing.T) {
	h := newHarness(t)
	h.config(autoMergeConfig)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", approvedPR("a1"))

	_, err := h.run("tick", "--dry-run")
	require.NoError(t, err)
	out, err := h.run("tick", "--dry-run")
	require.NoError(t, err)
	assert.Equal(t, "merge ENG-1: PR approved\n", out)

	out, err = h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, "merge ENG-1: PR approved\n", out)
	assert.Len(t, h.merges(), 1)
}

func TestTickDoesNotRetryAFailedMergeOnAHeadAnnouncedAsReady(t *testing.T) {
	h := newHarness(t)
	h.config(autoMergeConfig)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", cleanPR("a1"))
	_, err := h.run("tick")
	require.NoError(t, err)
	h.pr("ENG-1", approvedPR("a1"))
	h.tools.PullRequests.FailMerge(errors.New("while running gh pr merge " + prURL1 + ": exit status 1: " + mergeRefused))
	_, _, err = h.runContext(t.Context(), "tick")
	require.NoError(t, err)
	h.tools.PullRequests.FailMerge(nil)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Empty(t, h.merges())
	assert.Len(t, h.notifications(), 2)
}

func TestTickWarnsWhenTheResolvedRepoIsNotTheListedOne(t *testing.T) {
	h := newHarness(t)
	h.config(autoMergeConfig)
	h.tools.PullRequests.SetRepoName("kapetan-io/other")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	_, stderr, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)

	assert.Contains(t, stderr, `auto_merge entry "kapetan-io/dispatch" matches no repo`)
}

func TestTickDoesNotWarnWhenTheEntryDiffersFromTheRepoOnlyInCase(t *testing.T) {
	h := newHarness(t)
	h.config("auto_merge = [\"KAPETAN-io/Dispatch\"]\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	_, stderr, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)

	assert.Empty(t, stderr)
}

func TestTickDoesNotWarnWhenTheRepoNameCannotBeResolved(t *testing.T) {
	h := newHarness(t)
	h.config(autoMergeConfig)
	h.tools.PullRequests.FailRepoName(errors.New("no remote"))
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	out, stderr, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)

	assert.Equal(t, "launch ENG-1: ok\n", out)
	assert.NotContains(t, stderr, "matches no repo")
	assert.Contains(t, stderr, "no remote")
}

func TestTickDoesNotRetryAFailedMergeAfterTheTicketLeavesTheReadAndReturns(t *testing.T) {
	h := newHarness(t)
	h.config(autoMergeConfig)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", approvedPR("a1"))
	h.tools.PullRequests.FailMerge(errors.New("while running gh pr merge " + prURL1 + ": exit status 1: " + mergeRefused))
	_, err := h.run("tick")
	require.NoError(t, err)
	require.Len(t, h.notifications(), 1)

	// One tick where the ticket is missing from Linear's read, with its tree still leased.
	h.tickets()
	_, err = h.run("tick")
	require.NoError(t, err)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.tools.PullRequests.FailMerge(nil)
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.notifications(), 1)
	assert.Empty(t, h.merges())
}

// Without branch protection GitHub leaves reviewDecision empty even when a
// reviewer's latest review requests changes.
func TestTickDoesNotMergeOverAnotherReviewersChangesRequested(t *testing.T) {
	h := newHarness(t)
	h.config(autoMergeConfig)
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

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.merges())
}

func TestTickDoesNotMergeAForkPRThatSharesTheWorkersBranchName(t *testing.T) {
	h := newHarness(t)
	h.config(autoMergeConfig)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	fork := approvedPR("evil")
	fork.fork = true
	h.pr("ENG-1", fork)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Empty(t, h.merges())
	assert.Empty(t, h.notifications())
}

func TestTickUsesTheWorkersPRWhenAForkPRSharesItsBranchName(t *testing.T) {
	h := newHarness(t)
	h.config(autoMergeConfig)
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

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "merge ENG-1: PR approved\n", out)
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
	h.config(autoMergeConfig)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", approvedPR("a1"))
	h.respond("ENG-1", "c1", json.RawMessage(h.responded("ENG-1", "")))
	h.forkPage("c1")

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "merge ENG-1: PR approved\n", out)
	assert.Equal(t, []string{mergeCall1}, h.merges())
	assert.Equal(t, []string{"thrawn01/eng-1", "thrawn01/eng-1.c1"}, h.prReads())
}

func TestTickReportsABranchWhoseForkPRsFillEveryPageItReads(t *testing.T) {
	h := newHarness(t)
	h.config(autoMergeConfig)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", approvedPR("a1"))
	h.forkPage("c1")
	// Every page points at the next one, so the worker's PR is never reached.
	h.respond("ENG-1", "c1", json.RawMessage(h.responded("ENG-1", "")))

	_, stderr, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)

	assert.Contains(t, stderr, "while reading the PR for ENG-1")
	assert.Contains(t, stderr, "none from this repo")
	assert.Empty(t, h.merges())
	assert.Len(t, h.prReads(), 5)
}
