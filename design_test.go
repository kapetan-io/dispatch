package dispatch_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func designTicket(id, status string) fakeTicket {
	return withLabel(ticket(id, status, "dispatch"), "design")
}

func TestTickLaunchesADesignTicketWithTheDesignKickoff(t *testing.T) {
	h := newHarness(t)
	h.tickets(designTicket("ENG-1", "In Progress"), ticket("ENG-2", "In Progress", "dispatch"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{
		h.repo + " ENG-1 thrawn01/eng-1 --model opus --kickoff /dispatch:design ENG-1",
		h.repo + " ENG-2 thrawn01/eng-2 --model sonnet",
	}, h.launches())
}

func TestTickRunsTheDesignOnTheConfiguredModel(t *testing.T) {
	h := newHarness(t)
	h.config("[rules]\ndesign_model = \"fable\"\n")
	h.tickets(designTicket("ENG-1", "In Progress"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{h.repo + " ENG-1 thrawn01/eng-1 --model fable --kickoff /dispatch:design ENG-1"}, h.launches())
}

func TestTickTypesTheDesignKickoffWithTheBuiltInLauncher(t *testing.T) {
	h := newHarness(t)
	h.builtinLauncher = true
	h.tickets(designTicket("ENG-1", "In Progress"))
	h.pool()

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"w2:p2|/dispatch:design ENG-1"}, h.tools.Herdr.Kicked())
	require.Len(t, h.tools.Herdr.Started(), 1)
	assert.Equal(t, "opus", h.tools.Herdr.Started()[0].Model)
}

func TestTickNeverNudgesOrEscalatesADesignWorker(t *testing.T) {
	h := newHarness(t)
	h.tickets(designTicket("ENG-1", "In Progress"))
	h.worker("ENG-1", "idle")

	for range 8 {
		out, err := h.run("tick")
		require.NoError(t, err)
		assert.Empty(t, out)
		h.advance(3 * time.Hour)
	}

	// A comment on the ticket, a blocked question and a stalled session
	// are all normal for a design waiting on the user.
	h.tickets(withComment(designTicket("ENG-1", "In Progress"), comment1))
	for _, status := range []string{"idle", "blocked"} {
		h.setWorkerStatus("ENG-1", status)
		out, err := h.run("tick")
		require.NoError(t, err)
		assert.Empty(t, out)
		h.advance(3 * time.Hour)
	}

	assert.Empty(t, h.prompts())
	assert.Empty(t, h.notifications())
	assert.Empty(t, h.linear.mutations())
	assert.Empty(t, h.launches())
	assert.Empty(t, h.closes())
}

func TestTickKeepsTheDesignWorkerOnceItHasChildren(t *testing.T) {
	h := newHarness(t)
	h.tickets(designTicket("ENG-1", "In Progress"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Backlog"))
	h.worker("ENG-1", "idle")

	for range 3 {
		out, err := h.run("tick")
		require.NoError(t, err)
		assert.Empty(t, out)
		h.advance(3 * time.Hour)
	}

	assert.Empty(t, h.prompts())
	assert.Empty(t, h.notifications())
	assert.Empty(t, h.linear.mutations())
	assert.Empty(t, h.launches())
	assert.Empty(t, h.closes())
}

func TestTickCountsADesignTicketAgainstTheWorkerCap(t *testing.T) {
	h := newHarness(t)
	h.config("[dispatch]\nmax_in_progress = 1\n")
	h.tickets(designTicket("ENG-1", "In Progress"), ticket("ENG-2", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")

	_, err := h.run("tick")
	require.NoError(t, err)

	plan, err := h.run("plan")
	require.NoError(t, err)
	assert.Regexp(t, planRow("waiting", "ENG-2"), plan)
	assert.Empty(t, h.launches())
}

func TestTickHoldsAPromotionForADesignTicketAboutToLaunch(t *testing.T) {
	h := newHarness(t)
	parentsRealLauncher(h)
	h.config("[dispatch]\nmax_in_progress = 1\n")
	h.tickets(parent("ENG-1", "In Progress"), designTicket("ENG-3", "In Progress"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"))

	_, err := h.run("tick")
	require.NoError(t, err)

	plan, err := h.run("plan")
	require.NoError(t, err)
	assert.Regexp(t, planRow("waiting", "ENG-2"), plan)
	assert.Empty(t, h.linear.mutations())
	assert.Len(t, h.tools.Herdr.Started(), 1)
}

func TestTickLaunchesADesignReviewTicketOnTheDesignModel(t *testing.T) {
	h := newHarness(t)
	h.config("[rules]\ndesign_model = \"haiku\"\n")
	h.tickets(withLabel(ticket("ENG-1", "In Progress", "dispatch"), "design-review"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{
		h.repo + " ENG-1 thrawn01/eng-1 --model haiku --kickoff /dispatch:design-review ENG-1",
	}, h.launches())
}

func TestTickLaunchesADesignReviewTicketThatAlsoCarriesDesignAsAReview(t *testing.T) {
	h := newHarness(t)
	h.tickets(withLabel(designTicket("ENG-1", "In Progress"), "design-review"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{
		h.repo + " ENG-1 thrawn01/eng-1 --model opus --kickoff /dispatch:design-review ENG-1",
	}, h.launches())
}

func TestTickCountsADesignReviewAgainstMaxInProgress(t *testing.T) {
	h := newHarness(t)
	parentsRealLauncher(h)
	h.config("[dispatch]\nmax_in_progress = 1\n")
	h.tickets(
		withLabel(ticket("ENG-1", "In Progress", "dispatch"), "design-review"),
		ticket("ENG-2", "In Progress", "dispatch"),
	)

	_, err := h.run("tick")
	require.NoError(t, err)

	plan, err := h.run("plan")
	require.NoError(t, err)
	assert.Regexp(t, planRow("waiting", "ENG-2"), plan)
	require.Len(t, h.tools.Herdr.Started(), 1)
	assert.Equal(t, "opus", h.tools.Herdr.Started()[0].Model)
}

func TestTickHoldsAPromotionWhileADesignReviewTakesTheLastSlot(t *testing.T) {
	h := newHarness(t)
	parentsRealLauncher(h)
	h.config("[dispatch]\nmax_in_progress = 1\n")
	h.tickets(parent("ENG-1", "In Progress"), withLabel(ticket("ENG-3", "In Progress", "dispatch"), "design-review"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"))

	_, err := h.run("tick")
	require.NoError(t, err)

	plan, err := h.run("plan")
	require.NoError(t, err)
	assert.Regexp(t, planRow("waiting", "ENG-2"), plan)
	assert.Empty(t, h.linear.mutations())
}

func TestTickHoldsAPromotionForADesignParentInAnUnreadRepo(t *testing.T) {
	h := newHarness(t)
	h.config("[dispatch]\nmax_in_progress = 1\n")
	h.addRepo("tiger", filepath.Join(h.dir, "moved"))
	h.tickets(parent("ENG-1", "In Progress"), withLabel(ticket("ENG-3", "In Progress", "tiger"), "design"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"), child("ENG-4", "ENG-3", "Backlog"))

	_, err := h.run("tick")
	require.NoError(t, err)

	// The design parent's worker is unknown in the unread repo, so it holds the only slot.
	plan, err := h.run("plan")
	require.NoError(t, err)
	assert.Regexp(t, planRow("waiting", "ENG-2"), plan)
	assert.Empty(t, h.linear.mutations())
}

func TestTickStopsADesignWhoseTicketIsPaused(t *testing.T) {
	h := newHarness(t)
	h.gitTree("ENG-1")
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(designTicket("ENG-1", "Backlog"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"w-ENG-1"}, h.closes())
}

func TestTickReturnsTheTreeOfACanceledDesignTicket(t *testing.T) {
	h := newHarness(t)
	h.gitTree("ENG-1")
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(designTicket("ENG-1", "Canceled"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{"w-ENG-1"}, h.closes())
	assert.Equal(t, []string{h.repo + "|" + filepath.Join(h.dir, "trees", "ENG-1") + "|ENG-1"}, h.returns())
}
