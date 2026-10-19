package dispatch_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const approvalGate = "approval_checks = [\"Mergify Merge Protections\"]\n"

func approvalHarness(t *testing.T, settings string, p prState) *harness {
	h := newHarness(t)
	h.repoSettings("dispatch", settings)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", p)
	return h
}

func TestTickIgnoresAnApprovalCheckThatHasNotPassed(t *testing.T) {
	for _, test := range []struct {
		name     string
		settings string
		checks   string
		contexts []checkContext
	}{
		{name: "Failed", settings: approvalGate, checks: "FAILURE", contexts: []checkContext{{"Mergify Merge Protections", "FAILURE", true}, {"premerge", "SUCCESS", true}}},
		{name: "NameInOtherCase", settings: "approval_checks = [\"mergify merge protections\"]\n", checks: "FAILURE", contexts: []checkContext{{"Mergify Merge Protections", "FAILURE", true}, {"premerge", "SUCCESS", true}}},
		{name: "NeverFinishes", settings: approvalGate, checks: "PENDING", contexts: []checkContext{{"Mergify Merge Protections", "IN_PROGRESS", true}, {"premerge", "SUCCESS", true}}},
		{name: "NotStarted", settings: approvalGate, checks: "EXPECTED", contexts: []checkContext{{"Mergify Merge Protections", "EXPECTED", true}, {"premerge", "SUCCESS", true}}},
		{name: "BesideRequiredOnly", settings: approvalGate + requiredOnly, checks: "FAILURE", contexts: []checkContext{{"Mergify Merge Protections", "FAILURE", true}, {"premerge", "SUCCESS", true}, {"CodeRabbit", "FAILURE", false}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := approvalHarness(t, test.settings, prState{head: "a1", checks: test.checks, decision: "APPROVED", mergeable: "MERGEABLE", contexts: test.contexts})

			_, err := h.run("tick")
			require.NoError(t, err)

			assert.Equal(t, []string{readyNotice}, h.notifications())
			assert.Empty(t, h.prompts())
		})
	}
}

func TestTickCountsABuildCheckThatFailsBesideAnApprovalCheck(t *testing.T) {
	for _, test := range []struct {
		name     string
		settings string
	}{
		{name: "AllChecksCount", settings: approvalGate},
		{name: "RequiredOnly", settings: approvalGate + requiredOnly},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := approvalHarness(t, test.settings, prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE",
				contexts: []checkContext{{"Mergify Merge Protections", "FAILURE", true}, {"premerge", "FAILURE", true}}})

			_, err := h.run("tick")
			require.NoError(t, err)

			require.Len(t, h.prompts(), 1)
			assert.Contains(t, h.prompts()[0], "has a failed check")
			assert.Empty(t, h.notifications())
		})
	}
}

func TestTickWaitsOnABuildCheckThatRunsBesideAnApprovalCheck(t *testing.T) {
	h := approvalHarness(t, approvalGate, prState{head: "a1", checks: "FAILURE", decision: "APPROVED", mergeable: "MERGEABLE",
		contexts: []checkContext{{"Mergify Merge Protections", "FAILURE", true}, {"premerge", "IN_PROGRESS", true}}})

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.notifications())
	assert.Empty(t, h.prompts())
}

func TestTickCountsAnApprovalCheckInARepoThatDoesNotListIt(t *testing.T) {
	h := approvalHarness(t, "", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE",
		contexts: []checkContext{{"Mergify Merge Protections", "FAILURE", true}, {"premerge", "SUCCESS", true}}})

	_, err := h.run("tick")
	require.NoError(t, err)

	require.Len(t, h.prompts(), 1)
	assert.Contains(t, h.prompts()[0], "has a failed check")
}
