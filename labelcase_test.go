package dispatch_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	tkt "github.com/kapetan-io/dispatch/internal/ticket"

	"github.com/stretchr/testify/assert"
)

// labeledAs builds an In Progress ticket whose labels are spelled as given, the way a
// workspace that had AI, Design and Repo before dispatch was set up spells them.
func labeledAs(id string, labels ...tkt.Label) fakeTicket {
	t := ticket(id, "In Progress", "")
	t.Labels = labels
	return t
}

func withStatus(t fakeTicket, status string) fakeTicket {
	t.Status = status
	return t
}

func TestTickLaunchesADesignTicketWhoseLabelsDifferInCase(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(labeledAs("ENG-1",
		tkt.Label{Name: "AI"},
		tkt.Label{Name: "Design"},
		tkt.Label{Name: "Dispatch", Group: "Repo"},
	))

	d.Tick()

	assert.Equal(t, []string{d.Repo + " ENG-1 thrawn01/eng-1 --model opus --kickoff /dispatch:design ENG-1"}, d.Workers.Launched())
}

func TestTickLaunchesADesignReviewTicketLabeledDesignReview(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(labeledAs("ENG-1",
		tkt.Label{Name: "AI"},
		tkt.Label{Name: "Design-Review"},
		tkt.Label{Name: "dispatch", Group: "repo"},
	))

	d.Tick()

	assert.Equal(t, []string{d.Repo + " ENG-1 thrawn01/eng-1 --model opus --kickoff /dispatch:design-review ENG-1"}, d.Workers.Launched())
}

func TestTickReadsThePROfATicketWhoseRepoLabelDiffersInCase(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(withStatus(labeledAs("ENG-1",
		tkt.Label{Name: "ai"},
		tkt.Label{Name: "Dispatch", Group: "Repo"},
	), "In Review"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))

	d.Tick()

	assert.Equal(t, []string{readyText1}, d.Followed.Told())
}

func TestTickLaunchesInTheTableSpelledLikeTheTicketsLabel(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	other := t.TempDir()
	d.addRepo("Dispatch", other)
	d.Linear.Tickets(labeledAs("ENG-1",
		tkt.Label{Name: "ai"},
		tkt.Label{Name: "Dispatch", Group: "repo"},
	))

	d.Tick()

	assert.Equal(t, []string{other + " ENG-1 thrawn01/eng-1 --model sonnet"}, d.Workers.Launched())
}

func TestRulesCheckHoldsATicketWhoseRepoLabelDiffersInCaseWhileItsPoolCannotBeRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig})
	d.Linear.Tickets(labeledAs("ENG-1",
		tkt.Label{Name: "ai"},
		tkt.Label{Name: "Dispatch", Group: "repo"},
	))
	d.Tools().Treehouse.FailTrees(errors.New("exit status 1"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `launch-worker +whenever +ticket +wants nothing now; held for ENG-1\n`, res.Stdout)
	assert.Empty(t, d.Workers.Launched())
}

func TestTickLaunchesInTheTableSpelledExactlyLikeTheLabelOverOneThatDiffersInCase(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.addRepo("Dispatch", t.TempDir())
	d.Linear.Tickets(labeledAs("ENG-1",
		tkt.Label{Name: "ai"},
		tkt.Label{Name: "dispatch", Group: "repo"},
	))

	d.Tick()

	assert.Equal(t, []string{d.Repo + " ENG-1 thrawn01/eng-1 --model sonnet"}, d.Workers.Launched())
}

func TestRulesFindTheRepoTableOfALabelThatDiffersInCase(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{
		Rules: rule(`whenever({ name: "peek", each: "ticket", if: () => true,
  then: (t, ctx) => ctx.notify.queueSend(t, String(ctx.config.hasRepo("Dispatch")) + " " + String(t.hasLabel("AI")), { once: ["peek", t.id] }) });`),
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	d.Tick()

	assert.Equal(t, []string{"true true"}, d.Followed.Told())
}
