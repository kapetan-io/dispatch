package dispatch_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	autoMergeConfig = "auto_merge = [\"kapetan-io/dispatch\"]\n"
	mergedText1     = "ENG-1 was approved and merged: " + prURL1
	mergeCall1      = "pr merge " + prURL1 + " --merge --match-head-commit a1"
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
	assert.Equal(t, []string{mergeCall1}, h.lines("merges.log"))
	assert.Equal(t, []string{mergedText1 + "|dispatch"}, h.notifications())
	assert.Empty(t, h.prompts())

	// The merged PR is no longer open, so later ticks do nothing.
	out, err = h.run("tick")
	require.NoError(t, err)
	assert.Empty(t, out)
	assert.Len(t, h.lines("merges.log"), 1)
}

func TestTickMatchesTheRepoCaseInsensitively(t *testing.T) {
	h := newHarness(t)
	h.config("auto_merge = [\"Kapetan-IO/Dispatch\"]\n")
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", approvedPR("a1"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.lines("merges.log"), 1)
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
			assert.Equal(t, []string{readyText1 + "|dispatch"}, h.notifications())
			assert.Empty(t, h.lines("merges.log"))
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
			assert.Empty(t, h.lines("merges.log"))
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

			assert.Empty(t, h.lines("merges.log"))
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
	assert.Equal(t, []string{readyText1 + "|dispatch"}, h.notifications())

	h.pr("ENG-1", approvedPR("a1"))
	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "merge ENG-1: PR approved\n", out)
	assert.Equal(t, []string{mergeCall1}, h.lines("merges.log"))
	assert.Equal(t, []string{readyText1 + "|dispatch", mergedText1 + "|dispatch"}, h.notifications())
}

func TestTickWaitsForANewApprovalAfterAPush(t *testing.T) {
	h := newHarness(t)
	h.config(autoMergeConfig)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a2", checks: "SUCCESS", decision: "APPROVED", mergeable: "MERGEABLE", approved: []string{"a1"}})

	_, err := h.run("tick")
	require.NoError(t, err)
	assert.Empty(t, h.lines("merges.log"))

	h.pr("ENG-1", approvedPR("a2"))
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{strings.Replace(mergeCall1, "a1", "a2", 1)}, h.lines("merges.log"))
}

func TestTickReportsAFailedMergeOnceAndRetriesOnlyAfterAPush(t *testing.T) {
	h := newHarness(t)
	h.config(autoMergeConfig)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", approvedPR("a1"))
	h.writeFile("merge-fails", "")

	out, stderr, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Contains(t, stderr, "while merging ENG-1")
	assert.Contains(t, stderr, "the base branch policy prohibits the merge")
	require.Len(t, h.notifications(), 1)
	assert.Contains(t, h.notifications()[0], "ENG-1: merging "+prURL1+" failed")
	assert.Contains(t, h.notifications()[0], "the base branch policy prohibits the merge")

	// The same head is not retried, and the user hears nothing more.
	h.removeFile("merge-fails")
	for range 2 {
		out, stderr, err = h.runContext(t.Context(), "tick")
		require.NoError(t, err)
		assert.Empty(t, out)
		assert.Empty(t, stderr)
	}
	assert.Empty(t, h.lines("merges.log"))
	assert.Len(t, h.notifications(), 1)

	// A push that gets approved is a new head, so it merges.
	h.pr("ENG-1", approvedPR("a2"))
	out, err = h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, "merge ENG-1: PR approved\n", out)
	assert.Len(t, h.lines("merges.log"), 1)
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
	assert.Empty(t, h.lines("merges.log"))
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
	assert.Empty(t, h.lines("merges.log"))

	h.config(autoMergeConfig)
	_, err = h.run("tick")
	require.NoError(t, err)
	assert.Len(t, h.lines("merges.log"), 1)
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
	assert.Len(t, h.lines("repo-views.log"), 1)
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
	assert.Empty(t, h.lines("repo-views.log"))
}

func (h *harness) prWithReview(url, state, commit string) {
	h.writeJSON("pr-eng-1.json", map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequests": map[string]any{"nodes": []any{map[string]any{
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

			assert.Empty(t, h.lines("merges.log"))
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
	assert.Empty(t, h.lines("merges.log"))

	// The PR's repo is listed, though the clone resolves to a different one.
	h = newHarness(t)
	h.config("auto_merge = [\"someone/fork\"]\n")
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.prWithReview(otherURL, "APPROVED", "a1")

	out, err = h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, "merge ENG-1: PR approved\n", out)
	assert.Equal(t, []string{"pr merge " + otherURL + " --merge --match-head-commit a1"}, h.lines("merges.log"))
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
	assert.Len(t, h.lines("merges.log"), 1)
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
	h.writeFile("merge-fails", "")
	_, _, err = h.runContext(t.Context(), "tick")
	require.NoError(t, err)
	h.removeFile("merge-fails")

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Empty(t, h.lines("merges.log"))
	assert.Len(t, h.notifications(), 2)
}

func TestTickWarnsWhenTheResolvedRepoIsNotTheListedOne(t *testing.T) {
	h := newHarness(t)
	h.config(autoMergeConfig)
	h.writeFile("repo-name", "kapetan-io/other\n")
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
	h.script("gh", `[ "$1 $2" = "repo view" ] && { echo "no remote" >&2; exit 1; }
echo '{"data":{"repository":{"pullRequests":{"nodes":[]}}}}'`)
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
	h.writeFile("merge-fails", "")
	_, err := h.run("tick")
	require.NoError(t, err)
	require.Len(t, h.notifications(), 1)

	// One tick where the ticket is missing from Linear's read, with its tree still leased.
	h.tickets()
	_, err = h.run("tick")
	require.NoError(t, err)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.removeFile("merge-fails")
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.notifications(), 1)
	assert.Empty(t, h.lines("merges.log"))
}

// Without branch protection GitHub leaves reviewDecision empty even when a
// reviewer's latest review requests changes.
func TestTickDoesNotMergeOverAnotherReviewersChangesRequested(t *testing.T) {
	h := newHarness(t)
	h.config(autoMergeConfig)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.writeJSON("pr-eng-1.json", map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequests": map[string]any{"nodes": []any{map[string]any{
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

	assert.Empty(t, h.lines("merges.log"))
}
