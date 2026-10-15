package dispatch_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdrtest"
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

// parent builds a ticket that becomes a parent once a test gives it children
// with setChildren, as Linear derives it.
func parent(id, status string) fakeTicket {
	return ticket(id, status, "dispatch")
}

// parentsRealLauncher makes launches create a herdr workspace, so a launched worker
// counts against the cap the way it does in production. The fake launcher leaves no trace.
func parentsRealLauncher(h *harness) {
	h.builtinLauncher = true
	h.pool()
}

func TestTickPromotesAnUnblockedChildAndHoldsTheBlockedOne(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))
	h.linear.setChildren(
		child("ENG-2", "ENG-1", "Todo"),
		child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", "Todo"}),
	)

	_, err := h.run("tick")
	require.NoError(t, err)

	mutations := h.linear.mutations()
	require.Len(t, mutations, 1)
	assert.Equal(t, lineartest.Move, mutations[0].Kind)
	assert.Equal(t, "uuid-ENG-2", mutations[0].TicketID)
	assert.Equal(t, inProgressID, mutations[0].StateID)
	for _, read := range h.linear.childReadParents() {
		assert.Equal(t, []string{"uuid-ENG-1"}, read)
	}
	// Linear shows the promoted child In Progress at the re-read, so it launches in the same tick.
	assert.Len(t, h.launches(), 1)
	assert.Contains(t, h.launches()[0], " ENG-2 ")
}

func TestTickLaunchesAPromotedChildOnceLinearShowsItInProgress(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"))
	_, err := h.run("tick")
	require.NoError(t, err)

	// A second tick promotes and launches nothing again.
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.launches(), 1)
	assert.Len(t, h.linear.mutations(), 1)
}

func TestTickPromotesABlockedChildOnTheFirstTickAfterItsBlockerIsDone(t *testing.T) {
	for _, test := range []struct {
		name    string
		blocker string
		want    bool
	}{
		{name: "BlockerInProgress", blocker: "In Progress"},
		{name: "BlockerInReview", blocker: "In Review"},
		{name: "BlockerNeedsHuman", blocker: "Needs Human"},
		{name: "BlockerDone", blocker: "Done", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.tickets(parent("ENG-1", "In Progress"))
			h.linear.setChildren(
				child("ENG-2", "ENG-1", test.blocker),
				child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", test.blocker}),
			)

			_, err := h.run("tick")
			require.NoError(t, err)

			if !test.want {
				assert.Empty(t, h.linear.mutations())
				return
			}
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

			_, err := h.run("tick")
			require.NoError(t, err)

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

	// The user moves it back to Todo with the blocker still attached, a minute later.
	h.advance(time.Minute)
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

	_, err := h.run("tick")
	require.NoError(t, err)

	mutations := h.linear.mutations()
	require.Len(t, mutations, 2)
	assert.Equal(t, needsHumanID, mutations[0].StateID)
	assert.Contains(t, mutations[1].Body, "its blocker ENG-4 is Canceled")
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
	h.config("[dispatch]\nmax_in_progress = 1\n")
	h.tickets(parent("ENG-1", "In Progress"), ticket("ENG-5", "In Progress", "dispatch"))
	h.linear.setChildren(child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", "Canceled"}))

	_, err := h.run("tick")
	require.NoError(t, err)

	mutations := h.linear.mutations()
	require.Len(t, mutations, 2)
	assert.Equal(t, "uuid-ENG-3", mutations[0].TicketID)
	assert.Equal(t, needsHumanID, mutations[0].StateID)
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

func TestTickPromotesTheFirstPieceOnceDesignIsOffAndAReviewChildIsDone(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))
	h.linear.setChildren(withLabel(child("ENG-2", "ENG-1", "Done"), "design-review"), child("ENG-3", "ENG-1", "Todo"))

	_, err := h.run("tick")
	require.NoError(t, err)

	mutations := h.linear.mutations()
	require.Len(t, mutations, 1)
	assert.Equal(t, "uuid-ENG-3", mutations[0].TicketID)
	assert.Equal(t, inProgressID, mutations[0].StateID)
}

func TestTickClosesAParentOnceItsDoneReviewChildAndBuildChildrenAreFinished(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))
	h.linear.setChildren(withLabel(child("ENG-2", "ENG-1", "Done"), "design-review"), child("ENG-3", "ENG-1", "Done"))

	_, err := h.run("tick")
	require.NoError(t, err)

	mutations := h.linear.mutations()
	require.Len(t, mutations, 1)
	assert.Equal(t, "uuid-ENG-1", mutations[0].TicketID)
	assert.Equal(t, doneID, mutations[0].StateID)
}

func TestTickDoesNotCountATicketWithTruncatedLabelsAgainstTheCap(t *testing.T) {
	h := newHarness(t)
	h.config("[dispatch]\nmax_in_progress = 1\n")
	cut := ticket("ENG-3", "In Progress", "dispatch")
	cut.LabelsTruncated = true
	h.tickets(parent("ENG-1", "In Progress"), cut)
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"))

	_, err := h.run("tick")
	require.NoError(t, err)

	// The cut-short ticket goes to Needs Human and holds no slot, so the child is still promoted.
	mutations := h.linear.mutations()
	require.Len(t, mutations, 3)
	assert.Equal(t, "uuid-ENG-3", mutations[0].TicketID)
	assert.Equal(t, needsHumanID, mutations[0].StateID)
	assert.Equal(t, "uuid-ENG-2", mutations[2].TicketID)
	assert.Equal(t, inProgressID, mutations[2].StateID)
}

func TestTickClosesAParentWhoseChildrenAreAllFinished(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))
	h.linear.setChildren(
		child("ENG-2", "ENG-1", "Done"),
		child("ENG-3", "ENG-1", "Canceled"),
		child("ENG-4", "ENG-1", "Duplicate"),
	)

	_, err := h.run("tick")
	require.NoError(t, err)

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

			_, err := h.run("tick")
			require.NoError(t, err)

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

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.linear.mutations())
}

func TestTickNeverClosesAParentWhoseChildrenWereNotReturned(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Done"))
	h.linear.failChildren()

	_, _, err := h.runContext(t.Context(), "tick")
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
	parentsRealLauncher(h)
	h.config("[dispatch]\nmax_in_progress = 2\n")
	h.tickets(parent("ENG-1", "In Progress"), ticket("ENG-9", "In Progress", "dispatch"))
	h.worker("ENG-9", "working")
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"), child("ENG-3", "ENG-1", "Todo"))

	_, err := h.run("tick")
	require.NoError(t, err)

	plan, err := h.run("plan")
	require.NoError(t, err)
	assert.Regexp(t, planRow("waiting", "ENG-3"), plan)
	mutations := h.linear.mutations()
	require.Len(t, mutations, 1)
	assert.Equal(t, "uuid-ENG-2", mutations[0].TicketID)
}

func TestTickPromotesAWaitingChildOnceASlotFrees(t *testing.T) {
	h := newHarness(t)
	h.config("[dispatch]\nmax_in_progress = 1\n")
	h.tickets(parent("ENG-1", "In Progress"), ticket("ENG-9", "In Progress", "dispatch"))
	h.worker("ENG-9", "working")
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"))

	_, err := h.run("tick")
	require.NoError(t, err)
	plan, err := h.run("plan")
	require.NoError(t, err)
	assert.Regexp(t, planRow("waiting", "ENG-2"), plan)
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
	h.config("[dispatch]\nmax_in_progress = 1\n")
	// ENG-9 is In Progress but has no worker yet, since its launch waits.
	parentsRealLauncher(h)
	h.tickets(parent("ENG-1", "In Progress"), ticket("ENG-9", "In Progress", "dispatch"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.linear.mutations())
}

func TestTickDoesNotCountParentsAgainstTheWorkerCap(t *testing.T) {
	h := newHarness(t)
	h.config("[dispatch]\nmax_in_progress = 1\n")
	h.tickets(parent("ENG-1", "In Progress"), parent("ENG-8", "In Progress"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"), child("ENG-9", "ENG-8", "Backlog"))

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

	_, err := h.run("tick")
	require.NoError(t, err)

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

	_, err := h.run("tick")
	require.NoError(t, err)

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
	h.linear.setChildren(child("ENG-3", "ENG-1", "Backlog"))
	h.linear.failChildren()

	_, err := h.run("tick")
	require.NoError(t, err)

	plan, err := h.run("plan")
	require.NoError(t, err)
	assert.Len(t, h.launches(), 1)
	assert.Regexp(t, planRow("held", "ENG-1"), plan)
	assert.Contains(t, plan, "the children of ENG-1 were not read")
	assert.Empty(t, h.linear.mutations())
}

func TestTickSurvivesAFailedPromotion(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"), ticket("ENG-9", "In Progress", "dispatch"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"))
	h.linear.failMoves()

	_, err := h.run("tick")
	require.ErrorContains(t, err, "ENG-2 move failed")

	assert.Len(t, h.launches(), 1)
	assert.Contains(t, h.launches()[0], " ENG-9 ")
	assert.Empty(t, h.linear.mutations())
}

func TestTickHoldsAChildWhoseRepoPoolCannotBeRead(t *testing.T) {
	h := newHarness(t)
	h.addRepo("tiger", h.dir+"/moved")
	h.tickets(parent("ENG-1", "In Progress"))
	tiger := child("ENG-2", "ENG-1", "Todo")
	tiger.Labels = []linear.Label{{Name: "ai"}, {Name: "tiger", Group: "repo"}}
	h.linear.setChildren(tiger)

	_, err := h.run("tick")
	require.NoError(t, err)

	// Whether the child holds a lease in that pool is unknown, so it waits.
	plan, err := h.run("plan")
	require.NoError(t, err)
	assert.Regexp(t, planRow("held", "ENG-2"), plan)
	assert.Contains(t, plan, "treehouse pool tiger unread")
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
	h.config("[plugins.linear.statuses]\nblocker_done = \"Released\"\n")
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
	parentsRealLauncher(h)
	h.config("[dispatch]\nmax_in_progress = 1\n")
	h.tickets(parent("ENG-1", "In Progress"), parent("ENG-4", "In Progress"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"), child("ENG-5", "ENG-4", "Todo"))

	_, err := h.run("tick")
	require.NoError(t, err)

	plan, err := h.run("plan")
	require.NoError(t, err)
	assert.Regexp(t, planRow("waiting", "ENG-5"), plan)
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
	parentsRealLauncher(h)
	h.config("[dispatch]\nmax_in_progress = 1\n")
	h.tickets(parent("ENG-1", "In Progress"))
	older, newer := child("ENG-2", "ENG-1", "Todo"), child("ENG-3", "ENG-1", "Todo")
	older.CreatedAt = "2026-10-01T09:00:00.000Z"
	newer.CreatedAt = "2026-10-02T09:00:00.000Z"
	// Linear may return either order.
	h.linear.setChildren(newer, older)

	_, err := h.run("tick")
	require.NoError(t, err)

	plan, err := h.run("plan")
	require.NoError(t, err)
	assert.Regexp(t, planRow("waiting", "ENG-3"), plan)
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

func TestTickPromotesAChildWithNoRepoLabelAndEscalatesItOnceLinearShowsItInProgress(t *testing.T) {
	h := newHarness(t)
	h.tickets(parent("ENG-1", "In Progress"))
	lost := child("ENG-2", "ENG-1", "Todo")
	lost.Labels = []linear.Label{{Name: "ai"}}
	h.linear.setChildren(lost)

	_, err := h.run("tick")
	require.NoError(t, err)

	// The promotion holds its slot until Linear shows the child In Progress, then the user hears why it can't start.
	mutations := h.linear.mutations()
	require.Len(t, mutations, 3)
	assert.Equal(t, inProgressID, mutations[0].StateID)
	assert.Equal(t, needsHumanID, mutations[1].StateID)
	assert.Contains(t, mutations[2].Body, "it has no repo label")
}
func TestTickReleasesAChildWhenItsBlockerIsDoneWhateverCloseParentIs(t *testing.T) {
	h := newHarness(t)
	h.config("[plugins.linear.statuses]\nclose_parent = \"Closed\"\n")
	h.tickets(parent("ENG-1", "In Progress"))
	h.linear.setChildren(
		child("ENG-2", "ENG-1", "Done"),
		child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", "Done"}),
	)

	_, err := h.run("tick")
	require.NoError(t, err)

	mutations := h.linear.mutations()
	require.Len(t, mutations, 1)
	assert.Equal(t, "uuid-ENG-3", mutations[0].TicketID)
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
			h.config("[plugins.linear.statuses]\nblocker_done = \"Released\"\n")
			h.tools.Linear.AddState("Released", "completed")
			h.tickets(parent("ENG-1", "In Progress"))
			h.linear.setChildren(
				child("ENG-2", "ENG-1", test.blocker),
				child("ENG-3", "ENG-1", "Todo", [2]string{"ENG-2", test.blocker}),
			)

			_, err := h.run("tick")
			require.NoError(t, err)

			assert.Equal(t, test.want, len(h.linear.mutations()) == 1)
		})
	}
}

func TestTickAdmitsOnlyTheOldestWaitingTicketToOneFreeSlot(t *testing.T) {
	h := newHarness(t)
	parentsRealLauncher(h)
	h.config("[dispatch]\nmax_in_progress = 1\n")
	newest, middle, oldest := ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"), ticket("ENG-3", "In Progress", "dispatch")
	newest.CreatedAt = "2026-10-03T09:00:00.000Z"
	middle.CreatedAt = "2026-10-02T09:00:00.000Z"
	oldest.CreatedAt = "2026-10-01T09:00:00.000Z"
	h.tickets(newest, middle, oldest)

	_, err := h.run("tick")
	require.NoError(t, err)

	var launched []string
	for _, receipt := range h.ledgerNow() {
		if receipt.Kind == "launch" {
			launched = append(launched, receipt.Subject)
		}
	}
	assert.Equal(t, []string{"ENG-3"}, launched)
	plan, err := h.run("plan")
	require.NoError(t, err)
	assert.Regexp(t, planRow("waiting", "ENG-2"), plan)
	assert.Regexp(t, planRow("waiting", "ENG-1"), plan)
}

func TestTickCountsATicketWhoseWorkerIsUnknownAgainstTheCap(t *testing.T) {
	h := newHarness(t)
	h.config("[dispatch]\nmax_in_progress = 1\n")
	h.tickets(parent("ENG-1", "In Progress"), ticket("ENG-5", "In Progress", "dispatch"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"))
	h.tools.Herdr.Fail(herdrtest.OpRead, errors.New("server not running"))

	_, err := h.run("tick")
	require.NoError(t, err)

	// ENG-5 may already have a worker, so it holds the only slot and the promotion waits.
	plan, err := h.run("plan")
	require.NoError(t, err)
	assert.Regexp(t, planRow("waiting", "ENG-2"), plan)
	assert.Empty(t, h.linear.mutations())
	assert.Empty(t, h.launches())
}

func TestTickGivesOneFreeSlotToEitherAPromotionOrALaunchNeverBoth(t *testing.T) {
	h := newHarness(t)
	parentsRealLauncher(h)
	h.config("[dispatch]\nmax_in_progress = 1\n")
	h.tickets(parent("ENG-1", "In Progress"), ticket("ENG-5", "In Progress", "dispatch"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"))

	_, err := h.run("tick")
	require.NoError(t, err)

	// A promotion holds its slot until Linear shows the child In Progress and its worker is up,
	// so the launch that wanted the same slot in the same cycle waits.
	assert.Len(t, h.tools.Herdr.Started(), 1)
	plan, err := h.run("plan")
	require.NoError(t, err)
	assert.Contains(t, plan, "waiting")
}

func TestTickNeverStartsAWorkerWhileHerdrIsUnread(t *testing.T) {
	h := newHarness(t)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.tools.Herdr.Fail(herdrtest.OpRead, errors.New("server not running"))

	_, err := h.run("tick")
	require.NoError(t, err)

	plan, err := h.run("plan")
	require.NoError(t, err)
	assert.Regexp(t, planRow("held", "ENG-1"), plan)
	assert.Empty(t, h.launches())

	h.tools.Herdr.Fail(herdrtest.OpRead, nil)
	_, err = h.run("tick")
	require.NoError(t, err)
	assert.Len(t, h.launches(), 1)
}

func TestTickNeverStartsAWorkerWhileTheReposPoolIsUnread(t *testing.T) {
	h := newHarness(t)
	h.addRepo("tiger", filepath.Join(h.dir, "moved"))
	h.tickets(ticket("ENG-1", "In Progress", "tiger"), ticket("ENG-2", "In Progress", "dispatch"))

	_, err := h.run("tick")
	require.NoError(t, err)

	// Only the repo whose pool could not be read is held.
	plan, err := h.run("plan")
	require.NoError(t, err)
	assert.Regexp(t, planRow("held", "ENG-1"), plan)
	require.Len(t, h.launches(), 1)
	assert.Contains(t, h.launches()[0], " ENG-2 ")
}
