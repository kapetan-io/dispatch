package dispatch_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
)

const prURL1 = "https://github.com/kapetan-io/dispatch/pull/7"

func prPrompt(problem string) string {
	return fmt.Sprintf("w-ENG-1:p1|PR %s for ENG-1 has %s. Handle it as your build skill's step 7 says, and push.", prURL1, problem)
}

// prState is the part of a PR the dispatcher reads.
type prState struct {
	head        string
	checks      string
	decision    string
	mergeable   string
	unresolved  int
	resolved    int
	moreThreads bool
	// approved lists the commits a review approves.
	approved []string
}

func cleanPR(head string) prState {
	return prState{head: head, checks: "SUCCESS", decision: "APPROVED", mergeable: "MERGEABLE"}
}

// pr makes the ticket's branch have an open PR in this state.
func (h *harness) pr(ticket string, p prState) {
	threads := []map[string]bool{}
	for range p.unresolved {
		threads = append(threads, map[string]bool{"isResolved": false})
	}
	for range p.resolved {
		threads = append(threads, map[string]bool{"isResolved": true})
	}
	reviews := []map[string]any{}
	for _, oid := range p.approved {
		reviews = append(reviews, map[string]any{"state": "APPROVED", "commit": map[string]string{"oid": oid}})
	}
	h.writeJSON("pr-"+strings.ToLower(ticket)+".json", map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequests": map[string]any{"nodes": []any{map[string]any{
		"number":                   7,
		"url":                      prURL1,
		"headRefOid":               p.head,
		"mergeable":                p.mergeable,
		"reviewDecision":           p.decision,
		"commits":                  map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{"statusCheckRollup": map[string]string{"state": p.checks}}}}},
		"latestOpinionatedReviews": map[string]any{"nodes": reviews},
		"reviewThreads":            map[string]any{"nodes": threads, "pageInfo": map[string]bool{"hasNextPage": p.moreThreads}},
	}}}}}})
}

func (h *harness) prReads() []string {
	return h.lines("gh.log")
}

// setWorkerAgent starts a Claude agent in a workspace made with an empty status.
func (h *harness) setWorkerAgent(ticket, status string) {
	var agents struct {
		Result struct {
			Agents []map[string]string `json:"agents"`
		} `json:"result"`
	}
	require.NoError(h.t, json.Unmarshal(h.readFile("agents.json"), &agents))
	agents.Result.Agents = append(agents.Result.Agents, map[string]string{
		"agent":        "claude",
		"agent_status": status,
		"pane_id":      "w-" + ticket + ":p1",
		"workspace_id": "w-" + ticket,
	})
	h.writeJSON("agents.json", agents)
}

func TestTickNudgesAnIdleInReviewWorkerNamingTheProblem(t *testing.T) {
	for _, test := range []struct {
		name    string
		pr      prState
		problem string
	}{
		{name: "FailedCheck", pr: prState{head: "a1", checks: "FAILURE", decision: "APPROVED", mergeable: "MERGEABLE"}, problem: "a failed check"},
		{name: "ErroredCheck", pr: prState{head: "a1", checks: "ERROR", mergeable: "MERGEABLE"}, problem: "a failed check"},
		{name: "ChangesRequested", pr: prState{head: "a1", checks: "SUCCESS", decision: "CHANGES_REQUESTED", mergeable: "MERGEABLE"}, problem: "changes requested"},
		{name: "UnresolvedThread", pr: prState{head: "a1", checks: "SUCCESS", mergeable: "MERGEABLE", unresolved: 1, resolved: 2}, problem: "an unresolved review thread"},
		{name: "MergeConflict", pr: prState{head: "a1", checks: "SUCCESS", mergeable: "CONFLICTING"}, problem: "a merge conflict"},
		{name: "TwoProblems", pr: prState{head: "a1", checks: "FAILURE", mergeable: "CONFLICTING"}, problem: "a failed check and a merge conflict"},
		{name: "ThreeProblems", pr: prState{head: "a1", checks: "FAILURE", decision: "CHANGES_REQUESTED", mergeable: "CONFLICTING"}, problem: "a failed check, changes requested and a merge conflict"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", "idle")
			h.pr("ENG-1", test.pr)

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.Equal(t, "nudge ENG-1: PR needs work: "+test.problem+"\n", out)
			assert.Equal(t, []string{prPrompt(test.problem)}, h.prompts())
			assert.Empty(t, h.linear.mutations())
			assert.Empty(t, h.launches())
		})
	}
}

func TestTickLeavesAnInReviewTicketAloneWhenThePRNeedsNothing(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(h *harness)
	}{
		{name: "CleanPRNoWorker", setup: func(h *harness) { h.worker("ENG-1", ""); h.pr("ENG-1", cleanPR("a1")) }},
		{name: "PendingChecksResolvedThreads", setup: func(h *harness) {
			h.worker("ENG-1", "idle")
			h.pr("ENG-1", prState{head: "a1", checks: "PENDING", mergeable: "UNKNOWN", resolved: 3})
		}},
		{name: "NoOpenPR", setup: func(h *harness) { h.worker("ENG-1", "idle") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			test.setup(h)

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.Empty(t, out)
			assert.Empty(t, h.prompts())
			assert.Empty(t, h.launches())
			assert.Empty(t, h.linear.mutations())
		})
	}
}

func TestTickDoesNotReadThePRWhileTheInReviewWorkerIsBusy(t *testing.T) {
	for _, status := range []string{"working", "unknown"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(ticket("ENG-1", "In Review", "dispatch"))
			h.worker("ENG-1", status)
			h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.Empty(t, out)
			assert.Empty(t, h.prompts())
			assert.Empty(t, h.prReads())
		})
	}
}

func TestTickOnlyNotifiesForABlockedInReviewWorker(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "blocked")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "notify ENG-1: worker is blocked on a question\n", out)
	assert.Empty(t, h.prompts())
	assert.Empty(t, h.prReads())
}

func TestTickNeverNudgesDesignTicketsAboutPRs(t *testing.T) {
	for _, label := range []string{"design", "design-review"} {
		t.Run(label, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(withLabel(ticket("ENG-1", "In Review", "dispatch"), label))
			h.worker("ENG-1", "idle")
			h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.Empty(t, out)
			assert.Empty(t, h.prompts())
			assert.Empty(t, h.prReads())
		})
	}
}

func TestTickReadsOneInReviewPRPerTicketPerTick(t *testing.T) {
	h := newHarness(t)
	h.tickets(
		ticket("ENG-1", "In Review", "dispatch"),
		ticket("ENG-2", "In Review", "dispatch"),
	)
	h.worker("ENG-1", "idle")
	h.worker("ENG-2", "idle")

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"thrawn01/eng-1", "thrawn01/eng-2"}, h.prReads())
}

func TestTickDryRunNamesThePRNudgeWithoutSendingIt(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	out, err := h.run("tick", "--dry-run")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: PR needs work: a failed check\n", out)
	assert.Empty(t, h.prompts())
	assert.NoFileExists(t, h.state+"/cache.json")
}

func TestTickEscalatesAnInReviewTicketAfterTwoPRNudgesWithNoPush(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	for range 2 {
		_, err := h.run("tick")
		require.NoError(t, err)
	}
	assert.Equal(t, []string{prPrompt("a failed check"), prPrompt("a failed check")}, h.prompts())
	assert.Empty(t, h.linear.mutations())

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "escalate ENG-1: its worker stayed idle through 2 nudges about "+prURL1)
	assert.Len(t, h.prompts(), 2)
	mutations := h.linear.mutations()
	require.Len(t, mutations, 2)
	assert.Equal(t, "MoveTicket", mutations[0].operation)
	assert.Equal(t, needsHumanID, mutations[0].variables["state"])
	assert.Equal(t, "CommentTicket", mutations[1].operation)
	assert.Contains(t, mutations[1].variables["body"], "stayed idle through 2 nudges")

	// The ticket now reads Needs Human, so the dispatcher stays quiet.
	h.tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	for range 2 {
		_, err = h.run("tick")
		require.NoError(t, err)
	}
	assert.Len(t, h.linear.mutations(), 2)
	assert.Len(t, h.prompts(), 2)
}

func TestTickResetsThePRNudgeCountWhenTheHeadMoves(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	for range 2 {
		_, err := h.run("tick")
		require.NoError(t, err)
	}

	// The worker pushed a fix, and a review that arrives later is a new problem.
	h.pr("ENG-1", prState{head: "b2", checks: "SUCCESS", decision: "CHANGES_REQUESTED", mergeable: "MERGEABLE"})
	for range 2 {
		out, err := h.run("tick")
		require.NoError(t, err)
		assert.Equal(t, "nudge ENG-1: PR needs work: changes requested\n", out)
	}

	assert.Len(t, h.prompts(), 4)
	assert.Empty(t, h.linear.mutations())

	out, err := h.run("tick")
	require.NoError(t, err)
	assert.Contains(t, out, "escalate ENG-1")
}

func TestTickKeepsThePRNudgeCountAcrossTicksWhereTheWorkerWasBusy(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	_, err := h.run("tick")
	require.NoError(t, err)

	// A busy tick reads no PR, and the unchanged head must not reset the count.
	h.setWorkerStatus("ENG-1", "working")
	_, err = h.run("tick")
	require.NoError(t, err)
	h.setWorkerStatus("ENG-1", "idle")
	_, err = h.run("tick")
	require.NoError(t, err)
	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "escalate ENG-1")
	assert.Len(t, h.prompts(), 2)
}

func TestTickResetsThePRNudgeCountWhenAPushLandsWhileTheWorkerWasBusy(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	for range 2 {
		_, err := h.run("tick")
		require.NoError(t, err)
	}

	h.setWorkerStatus("ENG-1", "working")
	_, err := h.run("tick")
	require.NoError(t, err)
	h.pr("ENG-1", prState{head: "b2", checks: "FAILURE", mergeable: "MERGEABLE"})
	h.setWorkerStatus("ENG-1", "idle")
	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: PR needs work: a failed check\n", out)
	assert.Empty(t, h.linear.mutations())
}

func TestTickRelaunchesAnInReviewWorkerThatIsGoneThenNudgesItOnceIdle(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "")
	h.pr("ENG-1", prState{head: "a1", checks: "SUCCESS", decision: "CHANGES_REQUESTED", mergeable: "MERGEABLE"})

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "launch ENG-1: PR needs work: changes requested: ok\n", out)
	assert.Equal(t, []string{h.repo + " ENG-1 thrawn01/eng-1 --model sonnet --no-focus"}, h.launches())
	assert.Empty(t, h.prompts())
	assert.Empty(t, h.linear.mutations())

	// The relaunched worker is up and finishes its kickoff.
	h.setWorkerAgent("ENG-1", "idle")
	out, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "nudge ENG-1: PR needs work: changes requested\n", out)
	assert.Equal(t, []string{prPrompt("changes requested")}, h.prompts())
	assert.Len(t, h.launches(), 1)
}

func TestTickRelaunchesAnInReviewWorkerWhenTheWorkerCapIsFull(t *testing.T) {
	h := newHarness(t)
	h.config("max_in_progress = 1\n")
	h.tickets(
		ticket("ENG-1", "In Progress", "dispatch"),
		ticket("ENG-2", "In Review", "dispatch"),
	)
	h.worker("ENG-1", "working")
	h.worker("ENG-2", "")
	h.pr("ENG-2", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "launch ENG-2: PR needs work: a failed check: ok\n", out)
	assert.Equal(t, []string{h.repo + " ENG-2 thrawn01/eng-2 --model sonnet --no-focus"}, h.launches())
}

func TestTickEscalatesAnInReviewTicketWhoseRelaunchKeepsFailing(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	h.launchResult(`{"status":"pool_error","detail":"treehouse lease failed"}`)

	_, err := h.run("tick")
	require.NoError(t, err)
	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "escalate ENG-1: launch failed 2 times")
	assert.Len(t, h.linear.mutations(), 2)
}

func TestTickSkipsATicketWhosePRCannotBeRead(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.writeFile("gh-fails", "")

	out, stderr, err := h.runContext(context.Background(), "tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Contains(t, stderr, "while reading the PR for ENG-1")
	assert.Empty(t, h.prompts())
	assert.Empty(t, h.linear.mutations())
}

func TestTickEscalatesAnInReviewTicketWhoseRelaunchedWorkerNeverPushes(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	// Each relaunched worker exits without pushing, so the next tick finds none.
	for range 2 {
		out, err := h.run("tick")
		require.NoError(t, err)
		assert.Equal(t, "launch ENG-1: PR needs work: a failed check: ok\n", out)
	}
	assert.Empty(t, h.linear.mutations())

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "escalate ENG-1: its worker was relaunched 2 times for "+prURL1)
	assert.Len(t, h.launches(), 2)
	assert.Len(t, h.linear.mutations(), 2)
}

func TestTickNeverCountsAFullPoolAsAnInReviewRelaunch(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	h.launchResult(`{"status":"pool_exhausted","detail":"all 4/4 pool trees are taken"}`)

	for range 4 {
		_, err := h.run("tick")
		require.NoError(t, err)
	}

	assert.Len(t, h.launches(), 4)
	assert.Empty(t, h.linear.mutations())
}

func TestTickStartsThePRNudgeCountFreshWhenTheTicketEntersInReview(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	_, err := h.run("tick")
	require.NoError(t, err)

	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	for range 2 {
		out, err := h.run("tick")
		require.NoError(t, err)
		assert.Equal(t, "nudge ENG-1: PR needs work: a failed check\n", out)
	}
	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "escalate ENG-1")
	assert.Len(t, h.prompts(), 3)
}

func TestTickNotifiesOnceWhenAPRNudgeEscalationReachesNeedsHuman(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	for range 3 {
		_, err := h.run("tick")
		require.NoError(t, err)
	}

	h.tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	for range 3 {
		_, err := h.run("tick")
		require.NoError(t, err)
	}

	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1|dispatch"}, h.notifications())
}

func TestTickReportsAPRWithMoreReviewThreadsThanItReads(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE", moreThreads: true})

	out, stderr, err := h.runContext(context.Background(), "tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Contains(t, stderr, "more than 100 review threads")
	assert.Empty(t, h.prompts())
}

func TestTickIgnoresInReviewParentTickets(t *testing.T) {
	h := newHarness(t)
	h.tickets(withChildren(ticket("ENG-1", "In Review", "dispatch")))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Empty(t, h.prReads())
}

func TestTickGivesARelaunchedWorkerTwoPRNudgesBeforeEscalating(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "")
	h.pr("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	_, err := h.run("tick")
	require.NoError(t, err)
	h.setWorkerAgent("ENG-1", "idle")

	for range 2 {
		out, err := h.run("tick")
		require.NoError(t, err)
		assert.Equal(t, "nudge ENG-1: PR needs work: a failed check\n", out)
	}
	assert.Empty(t, h.linear.mutations())

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "escalate ENG-1: its worker stayed idle through 2 nudges")
	assert.Len(t, h.prompts(), 2)
}

func TestTickStopsWaitingOnAGHCallThatHangs(t *testing.T) {
	h := newHarness(t)
	h.config("")
	h.tickets(ticket("ENG-1", "In Review", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")
	h.script("gh", `exec sleep 20`)

	start := time.Now()
	out, stderr, err := h.runWith(context.Background(), dispatch.Options{PRTimeout: 300 * time.Millisecond}, "tick")
	require.NoError(t, err)

	assert.Less(t, time.Since(start), 5*time.Second)
	assert.Contains(t, stderr, "while reading the PR for ENG-1")
	assert.Equal(t, "launch ENG-2: ok\n", out)
}

func TestTickReportsWhatGHSaidWhenItFails(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.script("gh", `echo "HTTP 401: Bad credentials" >&2; exit 1`)

	_, stderr, err := h.runContext(context.Background(), "tick")
	require.NoError(t, err)

	assert.Contains(t, stderr, "HTTP 401: Bad credentials")
}
