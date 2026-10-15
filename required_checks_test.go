package dispatch_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const requiredOnly = "ignore_optional_checks = true\n"

const readyNotice = "ENG-1 is ready for review: https://github.com/kapetan-io/dispatch/pull/7"

func requiredOnlyHarness(t *testing.T, p prState) *harness {
	h := newHarness(t)
	h.repoSettings("dispatch", requiredOnly)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", p)
	return h
}

func TestTickIgnoresAnOptionalCheckThatFails(t *testing.T) {
	for _, test := range []struct {
		name     string
		checks   string
		contexts []checkContext
	}{
		{name: "OptionalFailed", checks: "FAILURE", contexts: []checkContext{{"test", "SUCCESS", true}, {"CodeRabbit", "FAILURE", false}}},
		{name: "OptionalNeverFinishes", checks: "PENDING", contexts: []checkContext{{"test", "SUCCESS", true}, {"lint", "IN_PROGRESS", false}}},
		{name: "OptionalErrored", checks: "ERROR", contexts: []checkContext{{"test", "SUCCESS", true}, {"deploy", "FAILURE", false}}},
		{name: "NoRequiredChecks", checks: "FAILURE", contexts: []checkContext{{"lint", "FAILURE", false}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := requiredOnlyHarness(t, prState{head: "a1", checks: test.checks, decision: "APPROVED", mergeable: "MERGEABLE", contexts: test.contexts})

			_, err := h.run("tick")
			require.NoError(t, err)

			assert.Equal(t, []string{readyNotice}, h.notifications())
			assert.Empty(t, h.prompts())
		})
	}
}

func TestTickCountsARequiredCheckThatFails(t *testing.T) {
	for _, test := range []struct {
		name     string
		checks   string
		contexts []checkContext
	}{
		{name: "RequiredFailed", checks: "FAILURE", contexts: []checkContext{{"test", "FAILURE", true}, {"CodeRabbit", "FAILURE", false}}},
		{name: "RequiredCancelled", checks: "FAILURE", contexts: []checkContext{{"test", "CANCELLED", true}}},
		{name: "RequiredTimedOut", checks: "FAILURE", contexts: []checkContext{{"test", "TIMED_OUT", true}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := requiredOnlyHarness(t, prState{head: "a1", checks: test.checks, mergeable: "MERGEABLE", contexts: test.contexts})

			_, err := h.run("tick")
			require.NoError(t, err)

			require.Len(t, h.prompts(), 1)
			assert.Contains(t, h.prompts()[0], "has a failed check")
		})
	}
}

func TestTickWaitsOnARequiredCheckThatHasNotFinished(t *testing.T) {
	for _, test := range []struct {
		name     string
		checks   string
		contexts []checkContext
	}{
		{name: "Running", checks: "PENDING", contexts: []checkContext{{"test", "IN_PROGRESS", true}}},
		{name: "RunningBesideAFailedOptional", checks: "FAILURE", contexts: []checkContext{{"test", "IN_PROGRESS", true}, {"CodeRabbit", "FAILURE", false}}},
		{name: "NotStarted", checks: "PENDING", contexts: []checkContext{{"test", "EXPECTED", true}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := requiredOnlyHarness(t, prState{head: "a1", checks: test.checks, decision: "APPROVED", mergeable: "MERGEABLE", contexts: test.contexts})

			_, err := h.run("tick")
			require.NoError(t, err)

			assert.Empty(t, h.notifications())
		})
	}
}

func TestTickLeavesAnExpectedRollupNotReadyWithoutAskingForChecks(t *testing.T) {
	h := requiredOnlyHarness(t, prState{head: "a1", checks: "EXPECTED", decision: "APPROVED", mergeable: "MERGEABLE"})

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.notifications())
	assert.Empty(t, h.tools.Calls("pullrequest", "checks"))
}

func TestTickNudgesOverChangesRequestedWhileOnlyAnOptionalCheckRuns(t *testing.T) {
	h := requiredOnlyHarness(t, prState{head: "b2", checks: "PENDING", decision: "CHANGES_REQUESTED", mergeable: "MERGEABLE",
		contexts: []checkContext{{"test", "SUCCESS", true}, {"CodeRabbit", "IN_PROGRESS", false}}})

	_, err := h.run("tick")
	require.NoError(t, err)

	require.Len(t, h.prompts(), 1)
	assert.Contains(t, h.prompts()[0], "has changes requested")
}

func TestTickMutesChangesRequestedWhileARequiredCheckRuns(t *testing.T) {
	h := requiredOnlyHarness(t, prState{head: "b2", checks: "PENDING", decision: "CHANGES_REQUESTED", mergeable: "MERGEABLE",
		contexts: []checkContext{{"test", "IN_PROGRESS", true}}})

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.prompts())
}

func TestTickErrorsWhenChecksNeedASecondPage(t *testing.T) {
	h := requiredOnlyHarness(t, prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	h.respondChecks("a1", []checkContext{{"test", "SUCCESS", true}}, true)

	_, err := h.run("tick")

	require.NoError(t, err)
	assert.Contains(t, h.loopErr(), "while reading the PR for ENG-1: PR 7 has more than 100 checks")
	assert.Empty(t, h.prompts())
}

func TestTickCountsEveryCheckInARepoWithoutTheSetting(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	_, err := h.run("tick")
	require.NoError(t, err)

	require.Len(t, h.prompts(), 1)
	assert.Contains(t, h.prompts()[0], "has a failed check")
	assert.Empty(t, h.tools.Calls("pullrequest", "checks"))
}

func TestTickDoesNotAskForChecksWhenTheRollupPassed(t *testing.T) {
	h := requiredOnlyHarness(t, cleanPR("a1"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{readyNotice}, h.notifications())
	assert.Empty(t, h.tools.Calls("pullrequest", "checks"))
}

func TestTickWaitsWhenAPushLandsBetweenReadingThePRAndItsChecks(t *testing.T) {
	h := requiredOnlyHarness(t, prState{head: "a1", checks: "FAILURE", decision: "APPROVED", mergeable: "MERGEABLE"})
	h.respondChecks("b2", []checkContext{{"test", "SUCCESS", true}}, false)

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.notifications())
	assert.Empty(t, h.prompts())
}

func TestTickIgnoresAFailedRollupThatListsNoChecks(t *testing.T) {
	h := requiredOnlyHarness(t, prState{head: "a1", checks: "FAILURE", decision: "APPROVED", mergeable: "MERGEABLE", contexts: []checkContext{}})

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{readyNotice}, h.notifications())
	assert.Empty(t, h.prompts())
}

func TestTickReadsStatusContextsByWhetherTheyAreRequired(t *testing.T) {
	for _, test := range []struct {
		name     string
		required bool
		nudged   bool
	}{
		{name: "RequiredErrored", required: true, nudged: true},
		{name: "OptionalErrored", required: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := requiredOnlyHarness(t, prState{head: "a1", checks: "ERROR", decision: "APPROVED", mergeable: "MERGEABLE"})
			status := map[string]any{"__typename": "StatusContext", "context": "ci/legacy", "state": "ERROR", "isRequired": test.required}
			h.tools.PullRequests.RespondChecks(7, map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{"commits": map[string]any{"nodes": []any{
				map[string]any{"commit": map[string]any{"oid": "a1", "statusCheckRollup": map[string]any{"contexts": map[string]any{"nodes": []any{status}, "pageInfo": map[string]bool{"hasNextPage": false}}}}},
			}}}}}})

			_, err := h.run("tick")
			require.NoError(t, err)

			if test.nudged {
				require.Len(t, h.prompts(), 1)
				assert.Contains(t, h.prompts()[0], "has a failed check")
				assert.Empty(t, h.notifications())
				return
			}
			assert.Empty(t, h.prompts())
			assert.Equal(t, []string{readyNotice}, h.notifications())
		})
	}
}

func TestTickCountsEveryCheckWhenOnlyAnotherRepoIgnoresOptionalOnes(t *testing.T) {
	h := newHarness(t)
	h.addRepo("tiger", filepath.Join(h.dir, "tiger"))
	h.repoSettings("tiger", requiredOnly)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	_, err := h.run("tick")
	require.NoError(t, err)

	require.Len(t, h.prompts(), 1)
	assert.Contains(t, h.prompts()[0], "has a failed check")
	assert.Empty(t, h.tools.Calls("pullrequest", "checks"))
}
