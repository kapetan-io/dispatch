package dispatch_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/lineartest"

	"github.com/kapetan-io/dispatch/internal/linear"
)

const (
	inProgressID = "state-in-progress"
	doneID       = "state-done"
)

// child builds a child of parent. blockedBy pairs a blocker's identifier with
// its status.
func child(id, parent, status string, blockedBy ...[2]string) fakeTicket {
	t := ticket(id, status, "dispatch")
	t.Parent = parent
	for _, b := range blockedBy {
		t.Blockers = append(t.Blockers, linear.Blocker{Identifier: b[0], Status: b[1], Abandoned: stateType(b[1]) == "canceled"})
	}
	return t
}

func stateType(status string) string {
	if status == "Canceled" || status == "Duplicate" {
		return "canceled"
	}
	return "started"
}

func parent(id, status string) fakeTicket {
	return withChildren(ticket(id, status, "dispatch"))
}

func TestTickPromotesAnUnblockedChildAndHoldsTheBlockedOne(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))
	h.linear.setChildren(
		child("ENG-2", "ENG-1", "Todo"),
		child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", "Todo"}),
	)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "promote ENG-2: child of ENG-1\nskip ENG-1: has sub-issues, and a parent never gets a worker\n", out)
	mutations := h.linear.mutations()
	require.Len(t, mutations, 1)
	assert.Equal(t, lineartest.Move, mutations[0].Kind)
	assert.Equal(t, "uuid-ENG-2", mutations[0].TicketID)
	assert.Equal(t, inProgressID, mutations[0].StateID)
	assert.Equal(t, [][]string{{"uuid-ENG-1"}}, h.linear.childReadParents())
	assert.Empty(t, h.launches())
}

func TestTickLaunchesAPromotedChildOnTheNextTick(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"))
	_, err := h.run("tick")
	require.NoError(t, err)
	require.Empty(t, h.launches())

	// Linear now reports the promoted child as In Progress.
	h.tickets(parent("ENG-1", "In Progress"), ticket("ENG-2", "In Progress", "dispatch"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "In Progress"))
	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "launch ENG-2: ok")
	assert.Len(t, h.launches(), 1)
	assert.Len(t, h.linear.mutations(), 1)
}

func TestTickPromotesABlockedChildOnTheFirstTickAfterItsBlockerIsDone(t *testing.T) {
	for _, test := range []struct {
		name    string
		blocker string
		want    string
	}{
		{name: "BlockerInProgress", blocker: "In Progress"},
		{name: "BlockerInReview", blocker: "In Review"},
		{name: "BlockerNeedsHuman", blocker: "Needs Human"},
		{name: "BlockerDone", blocker: "Done", want: "promote ENG-3: child of ENG-1\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(parent("ENG-1", "In Progress"))
			h.linear.setChildren(
				child("ENG-2", "ENG-1", test.blocker),
				child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", test.blocker}),
			)

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.Contains(t, out, "skip ENG-1")
			if test.want == "" {
				assert.Empty(t, h.linear.mutations())
				assert.NotContains(t, out, "promote")
				return
			}
			assert.Contains(t, out, test.want)
			require.Len(t, h.linear.mutations(), 1)
			assert.Equal(t, "uuid-ENG-3", h.linear.mutations()[0].TicketID)
		})
	}
}

func TestTickEscalatesAChildWhoseBlockerWasAbandoned(t *testing.T) {
	for _, status := range []string{"Canceled", "Duplicate"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(parent("ENG-1", "In Progress"))
			h.linear.setChildren(
				child("ENG-2", "ENG-1", status),
				child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", status}),
			)

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.Contains(t, out, "escalate ENG-3")
			assert.NotContains(t, out, "promote")
			mutations := h.linear.mutations()
			require.Len(t, mutations, 2)
			assert.Equal(t, "uuid-ENG-3", mutations[0].TicketID)
			assert.Equal(t, needsHumanID, mutations[0].StateID)
			assert.Equal(t, "uuid-ENG-3", mutations[1].TicketID)
			assert.Contains(t, mutations[1].Body, "ENG-2 is "+status)
			assert.Contains(t, mutations[1].Body, "Remove the blocker relation")
			assert.Empty(t, h.launches())
		})
	}
}

func TestTickEscalatesAChildOnlyOnceAndAgainWhenMovedBackToTodo(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))
	blocked := child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", "Canceled"})
	h.linear.setChildren(child("ENG-2", "ENG-1", "Canceled"), blocked)
	_, err := h.run("tick")
	require.NoError(t, err)
	require.Len(t, h.linear.mutations(), 2)

	// Needs Human leaves the Todo set, so the next tick stays quiet.
	h.linear.setChildren(child("ENG-2", "ENG-1", "Canceled"), child("ENG-3", "ENG-1", "Needs Human", [2]string{"ENG-2", "Canceled"}))
	_, err = h.run("tick")
	require.NoError(t, err)
	assert.Len(t, h.linear.mutations(), 2)

	// The user moves it back to Todo with the blocker still attached.
	h.linear.setChildren(child("ENG-2", "ENG-1", "Canceled"), child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", "Canceled"}))
	_, err = h.run("tick")
	require.NoError(t, err)
	assert.Len(t, h.linear.mutations(), 4)
}

func TestTickKeepsWaitingSilentlyOnAnOpenBlocker(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))
	h.linear.setChildren(
		child("ENG-2", "ENG-1", "In Progress"),
		child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", "In Progress"}),
	)

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.linear.mutations())
}

func TestTickEscalatesAChildWithAnAbandonedBlockerBesideAnOpenOne(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))
	h.linear.setChildren(
		child("ENG-2", "ENG-1", "In Progress"),
		child("ENG-4", "ENG-1", "Canceled"),
		child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", "In Progress"}, [2]string{"ENG-4", "Canceled"}),
	)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "escalate ENG-3: its blocker ENG-4 is Canceled")
	assert.Len(t, h.linear.mutations(), 2)
}

func TestTickEscalatesOnlyChildrenOfYoursThatCarryTheAILabel(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))
	blocker := [2]string{"ENG-9", "Canceled"}
	someoneElses := child("ENG-2", "ENG-1", "Todo", blocker)
	someoneElses.Assignee = "someone-else"
	unlabeled := child("ENG-3", "ENG-1", "Todo", blocker)
	unlabeled.Labels = []linear.Label{{Name: "dispatch", Group: "repo"}}
	leased := child("ENG-4", "ENG-1", "Todo", blocker)
	h.worker("ENG-4", "")
	h.linear.setElsewhere(child("ENG-4", "ENG-1", "Todo", blocker))
	h.linear.setChildren(someoneElses, unlabeled, leased)

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.linear.mutations())
}

func TestTickEscalatesAnAbandonedBlockerEvenAtTheWorkerCap(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))
	h.config("max_in_progress = 1\n")
	h.tickets(parent("ENG-1", "In Progress"), ticket("ENG-5", "In Progress", "dispatch"))
	h.linear.setChildren(child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", "Canceled"}))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "escalate ENG-3")
}

func TestTickDryRunPrintsTheEscalationOfAnAbandonedBlocker(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))
	h.linear.setChildren(
		child("ENG-2", "ENG-1", "Canceled"),
		child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", "Canceled"}),
	)

	out, err := h.run("tick", "--dry-run")
	require.NoError(t, err)

	assert.Contains(t, out, "escalate ENG-3: its blocker ENG-2 is Canceled")
	assert.Empty(t, h.linear.mutations())
}

func TestTickHoldsAChildWhoseBlockersWereCutShort(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))
	cut := child("ENG-2", "ENG-1", "Todo")
	cut.BlockersTruncated = true
	h.linear.setChildren(cut)

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.linear.mutations())
}

func TestTickDryRunPlansPromotionWithoutActing(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))
	h.linear.setChildren(
		child("ENG-2", "ENG-1", "Todo"),
		child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", "Todo"}),
	)

	out, err := h.run("tick", "--dry-run")
	require.NoError(t, err)

	assert.Equal(t, "promote ENG-2: child of ENG-1\nskip ENG-1: has sub-issues, and a parent never gets a worker\n", out)
	assert.Empty(t, h.linear.mutations())
}

func TestTickPromotesTheFirstPieceOnceDesignIsOffAndAReviewChildIsDone(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))
	h.linear.setChildren(withLabel(child("ENG-2", "ENG-1", "Done"), "design-review"), child("ENG-3", "ENG-1", "Todo"))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "promote ENG-3: child of ENG-1\n")
	assert.NotContains(t, out, "close ENG-1")
}

func TestTickClosesAParentOnceItsDoneReviewChildAndBuildChildrenAreFinished(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))
	h.linear.setChildren(withLabel(child("ENG-2", "ENG-1", "Done"), "design-review"), child("ENG-3", "ENG-1", "Done"))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "close ENG-1: every child is finished\n")
}

func TestTickDoesNotCountATicketWithTruncatedLabelsAgainstTheCap(t *testing.T) {
	h := newHarness(t)
	h.config("max_in_progress = 1\n")
	cut := ticket("ENG-3", "In Progress", "dispatch")
	cut.LabelsTruncated = true
	h.tickets(parent("ENG-1", "In Progress"), cut)
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "promote ENG-2: child of ENG-1\n")
}

func TestTickClosesAParentWhoseChildrenAreAllFinished(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))
	h.linear.setChildren(
		child("ENG-2", "ENG-1", "Done"),
		child("ENG-3", "ENG-1", "Canceled"),
		child("ENG-4", "ENG-1", "Duplicate"),
	)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "close ENG-1: every child is finished\nskip ENG-1: has sub-issues, and a parent never gets a worker\n", out)
	mutations := h.linear.mutations()
	require.Len(t, mutations, 1)
	assert.Equal(t, "uuid-ENG-1", mutations[0].TicketID)
	assert.Equal(t, doneID, mutations[0].StateID)
}

func TestTickKeepsAParentOpenWhileAnyChildIsUnfinished(t *testing.T) {
	for _, status := range []string{"Backlog", "Todo", "In Progress", "In Review", "Needs Human"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(parent("ENG-1", "In Progress"))
			// Todo and Backlog children are held back by a blocker that never finishes.
			h.linear.setChildren(
				child("ENG-2", "ENG-1", "Done"),
				child("ENG-3", "ENG-1", status, [2]string{"ENG-9", "Backlog"}),
			)

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.NotContains(t, out, "close ENG-1")
			// An In Progress child with no worker gets the blocked comment and nothing else.
			want := 0
			if status == "In Progress" {
				want = 1
			}
			assert.Len(t, h.linear.mutations(), want)
			for _, change := range h.linear.mutations() {
				assert.Equal(t, lineartest.Comment, change.Kind)
				assert.Equal(t, "uuid-ENG-3", change.TicketID)
			}
		})
	}
}

func TestTickCountsEveryChildBeforeClosingAParent(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))
	// A child without the ai label or your assignment is still the parent's child.
	stranger := child("ENG-3", "ENG-1", "Todo")
	stranger.Assignee = "someone-else"
	h.linear.setChildren(child("ENG-2", "ENG-1", "Done"), stranger)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.NotContains(t, out, "close ENG-1")
	assert.Empty(t, h.linear.mutations())
}

func TestTickNeverClosesAParentWhoseChildrenWereNotReturned(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.linear.mutations())
}

func TestTickPromotesOnlyChildrenOfYoursThatCarryTheAILabel(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))
	someoneElses := child("ENG-2", "ENG-1", "Todo")
	someoneElses.Assignee = "someone-else"
	unlabeled := child("ENG-3", "ENG-1", "Todo")
	unlabeled.Labels = []linear.Label{{Name: "dispatch", Group: "repo"}}
	backlog := child("ENG-4", "ENG-1", "Backlog")
	h.linear.setChildren(someoneElses, unlabeled, backlog, child("ENG-5", "ENG-1", "Todo"))

	_, err := h.run("tick")
	require.NoError(t, err)

	mutations := h.linear.mutations()
	require.Len(t, mutations, 1)
	assert.Equal(t, "uuid-ENG-5", mutations[0].TicketID)
}

func TestTickNeverPromotesPastTheWorkerCap(t *testing.T) {
	h := newHarness(t)
	h.config("max_in_progress = 2\n")
	h.tickets(parent("ENG-1", "In Progress"), ticket("ENG-9", "In Progress", "dispatch"))
	h.worker("ENG-9", "working")
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"), child("ENG-3", "ENG-1", "Todo"))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "promote ENG-2: child of ENG-1\n")
	assert.Contains(t, out, "wait ENG-3: 2 of 2 In Progress tickets already\n")
	mutations := h.linear.mutations()
	require.Len(t, mutations, 1)
	assert.Equal(t, "uuid-ENG-2", mutations[0].TicketID)
}

func TestTickPromotesAWaitingChildOnceASlotFrees(t *testing.T) {
	h := newHarness(t)
	h.config("max_in_progress = 1\n")
	h.tickets(parent("ENG-1", "In Progress"), ticket("ENG-9", "In Progress", "dispatch"))
	h.worker("ENG-9", "working")
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"))

	out, err := h.run("tick")
	require.NoError(t, err)
	assert.Contains(t, out, "wait ENG-2: 1 of 1 In Progress tickets already")
	require.Empty(t, h.linear.mutations())

	// ENG-9 merged.
	h.tickets(parent("ENG-1", "In Progress"))
	_, err = h.run("tick")
	require.NoError(t, err)

	mutations := h.linear.mutations()
	require.Len(t, mutations, 1)
	assert.Equal(t, "uuid-ENG-2", mutations[0].TicketID)
}

func TestTickCountsInProgressTicketsStillWaitingForAWorker(t *testing.T) {
	h := newHarness(t)
	h.config("max_in_progress = 1\n")
	// ENG-9 is In Progress but has no worker yet, since its launch waits.
	h.tickets(parent("ENG-1", "In Progress"), ticket("ENG-9", "In Progress", "dispatch"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.linear.mutations())
}

func TestTickDoesNotCountParentsAgainstTheWorkerCap(t *testing.T) {
	h := newHarness(t)
	h.config("max_in_progress = 1\n")
	h.tickets(parent("ENG-1", "In Progress"), parent("ENG-8", "In Progress"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.linear.mutations(), 1)
}

func TestTickNeverPromotesAChildThatHoldsALease(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))
	// ENG-2 was paused with its tree kept. Linear knows it only by identifier.
	h.worker("ENG-2", "")
	h.linear.setElsewhere(child("ENG-2", "ENG-1", "Todo"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"), child("ENG-3", "ENG-1", "Todo"))

	_, err := h.run("tick")
	require.NoError(t, err)

	mutations := h.linear.mutations()
	require.Len(t, mutations, 1)
	assert.Equal(t, "uuid-ENG-3", mutations[0].TicketID)
}

func TestTickStopsAChildYouPausedWithoutPromotingItAgain(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))
	h.worker("ENG-2", "working")
	h.linear.setElsewhere(child("ENG-2", "ENG-1", "Todo"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "stop ENG-2: moved back to Todo")
	assert.Equal(t, []string{"w-ENG-2"}, h.closes())
	assert.Empty(t, h.linear.mutations())
}

func TestTickNeverGivesAParentAWorker(t *testing.T) {
	for _, status := range []string{"In Progress", "In Review"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(parent("ENG-1", status))
			h.linear.setChildren(child("ENG-2", "ENG-1", "Todo", [2]string{"ENG-9", "Todo"}))

			_, err := h.run("tick")
			require.NoError(t, err)

			assert.Empty(t, h.launches())
			assert.Empty(t, h.prompts())
		})
	}
}

func TestTickNotifiesOnceWhenAParentEntersNeedsHuman(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "Needs Human"))

	out, err := h.run("tick")
	require.NoError(t, err)
	assert.Equal(t, "notify ENG-1: entered Needs Human\n", out)

	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1"}, h.notifications())
	assert.Empty(t, h.linear.childReadParents())
}

func TestTickLeavesDesignParentsAlone(t *testing.T) {
	for _, label := range []string{"design", "design-review"} {
		t.Run(label, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(withLabel(parent("ENG-1", "In Progress"), label))
			h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"), child("ENG-3", "ENG-1", "Done"))

			_, err := h.run("tick")
			require.NoError(t, err)

			assert.Empty(t, h.linear.mutations())
			assert.Empty(t, h.linear.childReadParents())
		})
	}
}

func TestTickReadsNoChildrenWhenNoParentIsInProgress(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"), parent("ENG-2", "In Review"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.linear.childReadParents())
}

func TestTickKeepsLaunchingWhenTheChildrenCannotBeRead(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"), ticket("ENG-2", "In Progress", "dispatch"))
	h.linear.failChildren()

	out, stderr, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)

	assert.Contains(t, out, "launch ENG-2: ok")
	assert.Contains(t, stderr, "while reading the children of open parents")
	assert.Empty(t, h.linear.mutations())
}

func TestTickSurvivesAFailedPromotion(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"), ticket("ENG-9", "In Progress", "dispatch"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"))
	h.linear.failMoves()

	out, stderr, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)

	assert.Contains(t, out, "launch ENG-9: ok")
	assert.NotContains(t, out, "promote")
	assert.Contains(t, stderr, "while promoting ENG-2")
}

func TestTickHoldsAChildWhoseRepoPoolCannotBeRead(t *testing.T) {
	h := newHarness(t)
	h.addRepo("tiger", h.dir+"/moved")
	h.tickets(parent("ENG-1", "In Progress"))
	tiger := child("ENG-2", "ENG-1", "Todo")
	tiger.Labels = []linear.Label{{Name: "ai"}, {Name: "tiger", Group: "repo"}}
	h.linear.setChildren(tiger)

	out, _, err := h.runContext(t.Context(), "tick")
	require.NoError(t, err)

	// Whether the child holds a lease in that pool is unknown, so it waits.
	assert.Contains(t, out, "wait ENG-2: the tiger pool could not be read this tick")
	assert.Empty(t, h.linear.mutations())
}

func TestTickDoesNotEscalateAChildWhoseRepoPoolCannotBeRead(t *testing.T) {
	h := newHarness(t)
	h.addRepo("tiger", h.dir+"/moved")
	h.tickets(parent("ENG-1", "In Progress"))
	tiger := child("ENG-2", "ENG-1", "Todo", [2]string{"ENG-9", "Canceled"})
	tiger.Labels = []linear.Label{{Name: "ai"}, {Name: "tiger", Group: "repo"}}
	h.linear.setChildren(tiger)

	_, err := h.run("tick")
	require.NoError(t, err)

	// A paused child may hold a lease in the unread pool, and escalating it would unpause it.
	assert.Empty(t, h.linear.mutations())
}

func TestTickDoesNotEscalateAChildWhoseBlockerIsDoneButNotBlockerDone(t *testing.T) {
	h := newHarness(t)
	h.config("[linear.statuses]\nblocker_done = \"Released\"\n")
	h.tickets(parent("ENG-1", "In Progress"))
	h.linear.setChildren(
		child("ENG-2", "ENG-1", "Done"),
		child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", "Done"}),
	)

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.linear.mutations())
}

func TestTickSharesTheWorkerCapAcrossParents(t *testing.T) {
	h := newHarness(t)
	h.config("max_in_progress = 1\n")
	h.tickets(parent("ENG-1", "In Progress"), parent("ENG-4", "In Progress"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"), child("ENG-5", "ENG-4", "Todo"))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "promote ENG-2: child of ENG-1\n")
	assert.Contains(t, out, "wait ENG-5: 1 of 1 In Progress tickets already\n")
	mutations := h.linear.mutations()
	require.Len(t, mutations, 1)
	assert.Equal(t, "uuid-ENG-2", mutations[0].TicketID)
}

func TestTickClosesOneParentAndPromotesForAnotherInTheSameTick(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"), parent("ENG-4", "In Progress"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Done"), child("ENG-5", "ENG-4", "Todo"))

	_, err := h.run("tick")
	require.NoError(t, err)

	mutations := h.linear.mutations()
	require.Len(t, mutations, 2)
	states := map[any]any{}
	for _, m := range mutations {
		assert.Equal(t, lineartest.Move, m.Kind)
		states[m.TicketID] = m.StateID
	}
	assert.Equal(t, map[any]any{"uuid-ENG-1": doneID, "uuid-ENG-5": inProgressID}, states)
}

func TestTickNeverGivesAParentAWorkerOrANudge(t *testing.T) {
	for _, status := range []string{"Todo", "Backlog", "Needs Human"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(parent("ENG-1", status))

			_, err := h.run("tick")
			require.NoError(t, err)

			assert.Empty(t, h.launches())
			assert.Empty(t, h.prompts())
			assert.Empty(t, h.linear.mutations())
		})
	}

	t.Run("IdleWorkerOnAnInProgressParent", func(t *testing.T) {
		h := newHarness(t)
		h.tickets(parent("ENG-1", "In Progress"))
		h.worker("ENG-1", "idle")
		h.worker("ENG-2", "working")
		h.linear.setChildren(child("ENG-2", "ENG-1", "In Progress"))

		_, err := h.run("tick")
		require.NoError(t, err)

		assert.Empty(t, h.launches())
		assert.Empty(t, h.prompts())
	})
}

func TestTickPromotesNothingForAParentThatIsNotInProgress(t *testing.T) {
	for _, status := range []string{"Needs Human", "In Review", "Todo"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(parent("ENG-1", status))
			h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"), child("ENG-3", "ENG-1", "Done"))

			_, err := h.run("tick")
			require.NoError(t, err)

			assert.Empty(t, h.linear.mutations())
			assert.Empty(t, h.linear.childReadParents())
		})
	}
}

func TestTickPromotesTheOldestChildFirstWhenOneSlotIsFree(t *testing.T) {
	h := newHarness(t)
	h.config("max_in_progress = 1\n")
	h.tickets(parent("ENG-1", "In Progress"))
	older, newer := child("ENG-2", "ENG-1", "Todo"), child("ENG-3", "ENG-1", "Todo")
	older.CreatedAt = "2026-10-01T09:00:00.000Z"
	newer.CreatedAt = "2026-10-02T09:00:00.000Z"
	// Linear may return either order.
	h.linear.setChildren(newer, older)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "wait ENG-3: 1 of 1 In Progress tickets already")
	mutations := h.linear.mutations()
	require.Len(t, mutations, 1)
	assert.Equal(t, "uuid-ENG-2", mutations[0].TicketID)
}

func TestTickNeverPromotesAChildOutsideTheConfiguredTeams(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))
	other := child("OTH-2", "ENG-1", "Todo")
	other.Team = "OTH"
	h.linear.setChildren(other)

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.linear.mutations())
}

func TestTickPromotesAChildWithNoRepoLabelAndTheNextTickEscalatesIt(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))
	lost := child("ENG-2", "ENG-1", "Todo")
	lost.Labels = []linear.Label{{Name: "ai"}}
	h.linear.setChildren(lost)

	out, err := h.run("tick")
	require.NoError(t, err)
	assert.Contains(t, out, "promote ENG-2: child of ENG-1")

	// Linear now reports ENG-2 In Progress, so the user hears why it can't start.
	moved := ticket("ENG-2", "In Progress", "")
	h.tickets(parent("ENG-1", "In Progress"), moved)
	h.linear.setChildren(child("ENG-2", "ENG-1", "In Progress"))
	_, err = h.run("tick")
	require.NoError(t, err)

	mutations := h.linear.mutations()
	require.Len(t, mutations, 3)
	assert.Equal(t, needsHumanID, mutations[1].StateID)
	assert.Contains(t, mutations[2].Body, `has no "repo" label`)
}

func TestTickReleasesAChildWhenItsBlockerIsDoneWhateverCloseParentIs(t *testing.T) {
	h := newHarness(t)
	h.config("[linear.statuses]\nclose_parent = \"Closed\"\n")
	h.tickets(parent("ENG-1", "In Progress"))
	h.linear.setChildren(
		child("ENG-2", "ENG-1", "Done"),
		child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", "Done"}),
	)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "promote ENG-3: child of ENG-1")
}

func TestTickReleasesAChildOnTheConfiguredBlockerStatus(t *testing.T) {
	for _, test := range []struct {
		name    string
		blocker string
		want    bool
	}{
		{name: "ConfiguredStatus", blocker: "Released", want: true},
		{name: "DefaultStatus", blocker: "Done", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.config("[linear.statuses]\nblocker_done = \"Released\"\n")
			h.tools.Linear.AddState("Released", "completed")
			h.tickets(parent("ENG-1", "In Progress"))
			h.linear.setChildren(
				child("ENG-2", "ENG-1", test.blocker),
				child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", test.blocker}),
			)

			out, err := h.run("tick")
			require.NoError(t, err)

			assert.Equal(t, test.want, strings.Contains(out, "promote ENG-3"))
		})
	}
}
