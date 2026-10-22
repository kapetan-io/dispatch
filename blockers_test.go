package dispatch_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/lineartest"
)

func TestTickHoldsABlockedTicketAndSaysWhyOnce(t *testing.T) {
	h := newHarness(t)
	h.tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Review"}))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, h.launches())
	assert.Equal(t, []string{"No worker is running because this ticket is blocked by ENG-1. A worker starts once every blocker is Done."}, comments(h))

	// The blocker moves status but is still open, so the ticket is not told again.
	h.tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Progress"}))
	_, err = h.run("tick")
	require.NoError(t, err)
	assert.Len(t, comments(h), 1)
	assert.Empty(t, h.launches())
}

func TestTickLaunchesAHeldTicketOnceEveryBlockerIsDone(t *testing.T) {
	h := newHarness(t)
	h.tickets(blockedBy(ticket("ENG-3", "In Progress", "dispatch"), false, [2]string{"ENG-1", "Done"}, [2]string{"ENG-2", "Todo"}))
	_, err := h.run("tick")
	require.NoError(t, err)
	require.Empty(t, h.launches())

	h.tickets(blockedBy(ticket("ENG-3", "In Progress", "dispatch"), false, [2]string{"ENG-1", "Done"}, [2]string{"ENG-2", "Done"}))
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.launches(), 1)
}

func TestTickLaunchesTicketsWithNoOpenBlockers(t *testing.T) {
	h := newHarness(t)
	h.tickets(
		ticket("ENG-1", "In Progress", "dispatch"),
		blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-9", "Done"}),
	)

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Len(t, h.launches(), 2)
	assert.Empty(t, comments(h))
}

func TestTickSendsAHeldTicketWithACanceledBlockerToNeedsHuman(t *testing.T) {
	h := newHarness(t)
	h.tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "Canceled"}))

	_, err := h.run("tick")
	require.NoError(t, err)

	mutations := h.linear.mutations()
	require.Len(t, mutations, 2)
	assert.Equal(t, lineartest.Move, mutations[0].Kind)
	assert.Equal(t, needsHumanID, mutations[0].StateID)
	assert.Contains(t, mutations[1].Body, "its blocker ENG-1 is Canceled")
	assert.Empty(t, h.launches())
}

func TestTickLeavesARunningWorkerAloneWhenABlockerIsAdded(t *testing.T) {
	h := newHarness(t)
	h.tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "Todo"}))
	h.worker("ENG-2", "working")

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Empty(t, h.closes())
	assert.Empty(t, h.launches())
	assert.Empty(t, h.linear.mutations())
}

func TestTickHoldsATicketWhoseBlockersWereCutShort(t *testing.T) {
	h := newHarness(t)
	h.tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), true, [2]string{"ENG-1", "Done"}))

	_, err := h.run("tick")
	require.NoError(t, err)

	plan, err := h.run("plan")
	require.NoError(t, err)
	assert.Contains(t, plan, "held")
	assert.Contains(t, plan, "more relations than dispatch reads")
	assert.Empty(t, h.launches())
}

func TestTickDoesNotLetAHeldTicketTakeAPromotionSlot(t *testing.T) {
	h := newHarness(t)
	h.config("[dispatch]\nmax_in_progress = 1\n")
	h.tickets(
		parent("ENG-1", "In Progress"),
		blockedBy(ticket("ENG-5", "In Progress", "dispatch"), false, [2]string{"ENG-4", "Todo"}),
	)
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"))

	_, err := h.run("tick")
	require.NoError(t, err)

	var promoted []any
	for _, m := range h.linear.mutations() {
		if m.Kind == lineartest.Move {
			promoted = append(promoted, m.TicketID)
		}
	}
	assert.Equal(t, []any{"uuid-ENG-2"}, promoted)
}

func TestTickLeavesARunningWorkerAloneWhenADoneBlockerReopens(t *testing.T) {
	h := newHarness(t)
	h.worker("ENG-2", "working")
	h.tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "Done"}))
	_, err := h.run("tick")
	require.NoError(t, err)

	h.tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Progress"}))
	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Empty(t, h.closes())
	assert.Empty(t, h.launches())
	assert.Empty(t, h.linear.mutations())
}

func TestTickDoesNotRepeatTheHoldCommentAfterAnUnreadPool(t *testing.T) {
	h := newHarness(t)
	held := func() fakeTicket {
		return blockedBy(ticket("ENG-2", "In Progress", "tiger"), false, [2]string{"ENG-1", "Todo"})
	}
	tiger := filepath.Join(h.dir, "tiger")
	require.NoError(t, os.MkdirAll(tiger, 0o755))
	h.addRepo("tiger", tiger)
	h.tickets(held())
	_, err := h.run("tick")
	require.NoError(t, err)
	require.Len(t, comments(h), 1)

	// The clone moves, so the pool can't be read and Plan never reaches the blocker check.
	h.resetRepos()
	h.addRepo("tiger", filepath.Join(h.dir, "moved"))
	_, err = h.run("tick")
	require.NoError(t, err)

	h.resetRepos()
	h.addRepo("tiger", tiger)
	_, err = h.run("tick")
	require.NoError(t, err)
	assert.Len(t, comments(h), 1)
}

func TestTickSendsAHeldTicketWithADuplicateBlockerToNeedsHuman(t *testing.T) {
	h := newHarness(t)
	h.tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "Duplicate"}))

	_, err := h.run("tick")
	require.NoError(t, err)

	require.Len(t, comments(h), 1)
	assert.Contains(t, comments(h)[0], "its blocker ENG-1 is Duplicate")
	assert.Empty(t, h.launches())
}

func TestTickDoesNotRepeatTheHoldCommentWhenLinearReordersBlockers(t *testing.T) {
	h := newHarness(t)
	h.tickets(blockedBy(ticket("ENG-3", "In Progress", "dispatch"), false, [2]string{"ENG-1", "Todo"}, [2]string{"ENG-2", "Todo"}))
	_, err := h.run("tick")
	require.NoError(t, err)

	h.tickets(blockedBy(ticket("ENG-3", "In Progress", "dispatch"), false, [2]string{"ENG-2", "Todo"}, [2]string{"ENG-1", "Todo"}))
	_, err = h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"No worker is running because this ticket is blocked by ENG-1, ENG-2. A worker starts once every blocker is Done."}, comments(h))
}
