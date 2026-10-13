package dispatch_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const requiredOnly = "ignore_optional_checks = true\n"

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

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.Equal(t, "notify ENG-1: PR ready for review\n", out)
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

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.Equal(t, "nudge ENG-1: PR needs work: a failed check\n", out)
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

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.NotContains(t, out, "nudge")
			assert.NotContains(t, out, "notify")
			assert.Empty(t, h.notifications())
		})
	}
}

func TestTickLeavesAnExpectedRollupNotReadyWithoutAskingForChecks(t *testing.T) {
	h := requiredOnlyHarness(t, prState{head: "a1", checks: "EXPECTED", decision: "APPROVED", mergeable: "MERGEABLE"})

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Empty(t, h.notifications())
}

func TestTickNudgesOverChangesRequestedWhileOnlyAnOptionalCheckRuns(t *testing.T) {
	h := requiredOnlyHarness(t, prState{head: "b2", checks: "PENDING", decision: "CHANGES_REQUESTED", mergeable: "MERGEABLE",
		contexts: []checkContext{{"test", "SUCCESS", true}, {"CodeRabbit", "IN_PROGRESS", false}}})

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: PR needs work: changes requested\n", out)
}

func TestTickMutesChangesRequestedWhileARequiredCheckRuns(t *testing.T) {
	h := requiredOnlyHarness(t, prState{head: "b2", checks: "PENDING", decision: "CHANGES_REQUESTED", mergeable: "MERGEABLE",
		contexts: []checkContext{{"test", "IN_PROGRESS", true}}})

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.NotContains(t, out, "nudge")
	assert.Empty(t, h.prompts())
}

func TestTickErrorsWhenChecksNeedASecondPage(t *testing.T) {
	h := requiredOnlyHarness(t, prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	h.respondChecks([]checkContext{{"test", "SUCCESS", true}}, true)

	out, stderr, err := h.runContext(context.Background(), "tick")

	require.NoError(t, err)
	assert.Empty(t, out)
	assert.Contains(t, stderr, "while reading the PR for ENG-1: PR 7 has more than 100 checks")
	assert.Empty(t, h.prompts())
}

func TestTickCountsEveryCheckInARepoWithoutTheSetting(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: PR needs work: a failed check\n", out)
	assert.Empty(t, h.tools.Calls("pullrequest", "checks"))
}

func TestTickDoesNotAskForChecksWhenTheRollupPassed(t *testing.T) {
	h := requiredOnlyHarness(t, cleanPR("a1"))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "notify ENG-1: PR ready for review\n", out)
	assert.Empty(t, h.tools.Calls("pullrequest", "checks"))
}
