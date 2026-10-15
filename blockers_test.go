package dispatch_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/lineartest"

	"github.com/kapetan-io/dispatch/internal/linear"
)

// blockedBy gives a standalone ticket blockers, each a blocker identifier and status.
func blockedBy(t fakeTicket, more bool, blockers ...[2]string) fakeTicket {
	t.Blockers = nil
	for _, b := range blockers {
		t.Blockers = append(t.Blockers, linear.Blocker{Identifier: b[0], Status: b[1], Abandoned: stateType(b[1]) == "canceled"})
	}
	t.BlockersTruncated = more
	return t
}

func comments(h *harness) []string {
	var bodies []string
	for _, m := range h.linear.mutations() {
		if m.Kind == lineartest.Comment {
			bodies = append(bodies, m.Body)
		}
	}
	return bodies
}

func TestTickHoldsABlockedTicketAndSaysWhyOnce(t *testing.T) {
	h := newHarness(t)
	h.tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Review"}))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "wait ENG-2: blocked by ENG-1 (In Review)\n", out)
	assert.Empty(t, h.launches())
	assert.Equal(t, []string{"No worker is running because this ticket is blocked by ENG-1. The dispatcher starts one on the first tick after every blocker is Done."}, comments(h))

	// The blocker moves status but is still open, so the ticket is not told again.
	h.tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "In Progress"}))
	_, err = h.run("tick")
	require.NoError(t, err)
	assert.Len(t, comments(h), 1)
	assert.Empty(t, h.launches())
}

func TestTickDryRunShowsTheHoldWithoutCommenting(t *testing.T) {
	h := newHarness(t)
	h.tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "Todo"}))

	out, err := h.run("tick", "--dry-run")
	require.NoError(t, err)

	assert.Equal(t, "wait ENG-2: blocked by ENG-1 (Todo)\n", out)
	assert.Empty(t, h.linear.mutations())
}

func TestTickLaunchesAHeldTicketOnceEveryBlockerIsDone(t *testing.T) {
	h := newHarness(t)
	h.tickets(blockedBy(ticket("ENG-3", "In Progress", "dispatch"), false, [2]string{"ENG-1", "Done"}, [2]string{"ENG-2", "Todo"}))
	_, err := h.run("tick")
	require.NoError(t, err)
	require.Empty(t, h.launches())

	h.tickets(blockedBy(ticket("ENG-3", "In Progress", "dispatch"), false, [2]string{"ENG-1", "Done"}, [2]string{"ENG-2", "Done"}))
	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "launch ENG-3: ok")
	assert.Len(t, h.launches(), 1)
}

func TestTickLaunchesTicketsWithNoOpenBlockers(t *testing.T) {
	h := newHarness(t)
	h.tickets(
		ticket("ENG-1", "In Progress", "dispatch"),
		blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-9", "Done"}),
	)

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "launch ENG-1: ok")
	assert.Contains(t, out, "launch ENG-2: ok")
	assert.Empty(t, comments(h))
}

func TestTickSendsAHeldTicketWithACanceledBlockerToNeedsHuman(t *testing.T) {
	h := newHarness(t)
	h.tickets(blockedBy(ticket("ENG-2", "In Progress", "dispatch"), false, [2]string{"ENG-1", "Canceled"}))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "escalate ENG-2: its blocker ENG-1 is Canceled")
	mutations := h.linear.mutations()
	require.Len(t, mutations, 2)
	assert.Equal(t, lineartest.Move, mutations[0].Kind)
	assert.Equal(t, needsHumanID, mutations[0].StateID)
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

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "wait ENG-2: blocked by relations past the first 10\n", out)
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

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "promote ENG-2: child of ENG-1")
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

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "escalate ENG-2: its blocker ENG-1 is Duplicate")
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

	assert.Equal(t, []string{"No worker is running because this ticket is blocked by ENG-1, ENG-2. The dispatcher starts one on the first tick after every blocker is Done."}, comments(h))
}
