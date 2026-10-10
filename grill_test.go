package dispatch_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func designTicket(id, status string) fakeTicket {
	return withLabel(ticket(id, status, "dispatch"), "design")
}

func TestTickDryRunLaunchesADesignTicket(t *testing.T) {
	h := newHarness(t)
	h.tickets(designTicket("ENG-1", "In Progress"))

	out, err := h.run("tick", "--dry-run")
	require.NoError(t, err)

	assert.Equal(t, "launch ENG-1\n", out)
	assert.Empty(t, h.launches())
}

func TestTickLaunchesADesignTicketWithTheGrillKickoff(t *testing.T) {
	h := newHarness(t)
	h.tickets(designTicket("ENG-1", "In Progress"), ticket("ENG-2", "In Progress", "dispatch"))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "launch ENG-1: ok\nlaunch ENG-2: ok\n", out)
	assert.Equal(t, []string{
		h.repo + " ENG-1 thrawn01/eng-1 --model opus --kickoff /dispatch:grill ENG-1 --no-focus",
		h.repo + " ENG-2 thrawn01/eng-2 --model sonnet --no-focus",
	}, h.launches())
}

func TestTickRunsTheGrillOnTheConfiguredModel(t *testing.T) {
	h := newHarness(t)
	h.config("grill_model = \"fable\"\n")
	h.tickets(designTicket("ENG-1", "In Progress"))

	_, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, []string{h.repo + " ENG-1 thrawn01/eng-1 --model fable --kickoff /dispatch:grill ENG-1 --no-focus"}, h.launches())
}

func TestTickTypesTheGrillKickoffWithTheBuiltInLauncher(t *testing.T) {
	h := newHarness(t)
	h.builtinLauncher = true
	h.tickets(designTicket("ENG-1", "In Progress"))
	tree := filepath.Join(h.dir, "tree")
	require.NoError(t, os.WriteFile(filepath.Join(h.repo, "treehouse.toml"), []byte("max_trees = 4\n"), 0o644))
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "init"},
		{"worktree", "add", "-q", "--detach", tree},
	} {
		out, err := exec.Command("git", append([]string{"-C", h.repo}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	h.script("herdr", fakeHerdr(h.dir))
	h.script("treehouse", fakeTreehouse(h.dir, tree))
	for _, name := range []string{"open-works", "tab-works", "start-works"} {
		h.writeFile(name, "")
	}

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "launch ENG-1: ok\n", out)
	assert.Equal(t, "w9:p1 /dispatch:grill ENG-1\n", string(h.readFile("runs.log")))
	assert.Contains(t, string(h.readFile("starts.log")), "--model opus")
}

func TestTickNeverNudgesOrEscalatesAGrillWorker(t *testing.T) {
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

func TestTickKeepsTheGrillWorkerOnceItHasChildren(t *testing.T) {
	h := newHarness(t)
	h.tickets(withChildren(designTicket("ENG-1", "In Progress")))
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

func TestTickLeavesABacklogChildOfADesignTicketAlone(t *testing.T) {
	h := newHarness(t)
	h.tickets(withChildren(designTicket("ENG-1", "In Progress")), ticket("ENG-2", "Backlog", "dispatch"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Backlog"))
	h.worker("ENG-1", "idle")

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Empty(t, out)
	assert.Empty(t, h.launches())
	assert.Empty(t, h.linear.mutations())
}

func TestTickLaunchesAChildOfADesignTicketAsABuild(t *testing.T) {
	h := newHarness(t)
	h.tickets(withChildren(designTicket("ENG-1", "In Progress")), ticket("ENG-2", "In Progress", "dispatch"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "In Progress"))
	h.worker("ENG-1", "idle")

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "launch ENG-2: ok\n", out)
	assert.Equal(t, []string{h.repo + " ENG-2 thrawn01/eng-2 --model sonnet --no-focus"}, h.launches())
}

func TestTickCountsADesignTicketAgainstTheWorkerCap(t *testing.T) {
	h := newHarness(t)
	h.config("max_in_progress = 1\n")
	h.tickets(designTicket("ENG-1", "In Progress"), ticket("ENG-2", "In Progress", "dispatch"))
	h.worker("ENG-1", "idle")

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "wait ENG-2: 1 of 1 workers already running\n", out)
	assert.Empty(t, h.launches())
}

func TestTickHoldsAPromotionForADesignTicketAboutToLaunch(t *testing.T) {
	h := newHarness(t)
	h.config("max_in_progress = 1\n")
	h.tickets(parent("ENG-1", "In Progress"), designTicket("ENG-3", "In Progress"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "wait ENG-2: 1 of 1 In Progress tickets already\n")
	assert.Empty(t, h.linear.mutations())
}

func TestTickStillSkipsADesignReviewTicketThatAlsoCarriesDesign(t *testing.T) {
	h := newHarness(t)
	h.tickets(withLabel(designTicket("ENG-1", "In Progress"), "design-review"))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Equal(t, "skip ENG-1: carries \"design-review\", which this dispatcher does not launch yet\n", out)
	assert.Empty(t, h.launches())
}

func TestTickDoesNotHoldAPromotionForADesignReviewTicket(t *testing.T) {
	h := newHarness(t)
	h.config("max_in_progress = 1\n")
	h.tickets(parent("ENG-1", "In Progress"), withLabel(ticket("ENG-3", "In Progress", "dispatch"), "design-review"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Todo"))

	out, err := h.run("tick")
	require.NoError(t, err)

	assert.Contains(t, out, "promote ENG-2: child of ENG-1\n")
}
