package dispatch_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const moveThenMove = `whenever({ name: "first", each: "ticket", if: (t) => t.system === "local" && t.status === "todo",
  then: (t, ctx) => ctx.ticket.queueMove(t, "in_review", "first", { once: ["first", t.id] }) });
whenever({ name: "second", each: "ticket", if: (t) => t.system === "local" && t.status === "todo",
  then: (t, ctx) => ctx.ticket.queueMove(t, "needs_human", "second", { once: ["second", t.id] }) });`

const moveThenEscalate = `whenever({ name: "first", each: "ticket", if: (t) => t.system === "local" && t.status === "todo",
  then: (t, ctx) => ctx.ticket.queueMove(t, "in_review", "first", { once: ["first", t.id] }) });
whenever({ name: "second", each: "ticket", if: (t) => t.system === "local" && t.status === "todo",
  then: (t, ctx) => ctx.ticket.queueEscalate(t, "second", { once: ["second", t.id] }) });`

const commentOnLocalByAttribute = `whenever({ name: "ask", each: "ticket", if: (t) => t.system === "local" && t.attributes.area === "webhooks",
  then: (t, ctx) => ctx.ticket.queueComment(t, "seen", { once: ["ask", t.id] }) });`

// The two actions run side by side, so either may land first. Whichever does, the other sees a status
// it did not read and fails without writing.
func TestTickMovesALocalTicketOnlyOnceWhenTwoRulesMoveItFromTheSameRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn, Rules: rule(moveThenMove)})
	d.StartBeforeTheEvents()
	d.fileLocal("Todo")

	_ = d.Run("tick")

	// The loop may read the first move before it starts the second, and then the second is not stale.
	assert.Contains(t, []string{"In Review", "Needs Human"}, d.statusOf("LOC-1"))
	moves := d.ReceiptsOf("move", "LOC-1")
	require.Len(t, moves, 2)
	for _, m := range moves {
		if m.State == "failed" {
			assert.Contains(t, m.Detail, "now, not the Todo the cycle saw")
			assert.NotContains(t, m.Detail, "Linear")
		}
	}
	assert.Contains(t, []string{moves[0].State, moves[1].State}, "done")
}

func TestTickEscalatingALocalTicketThatMovedSinceTheCycleReadWritesNothing(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn, Rules: rule(moveThenEscalate)})
	d.StartBeforeTheEvents()
	d.fileLocal("Todo")

	_ = d.Run("tick")

	status, show := d.statusOf("LOC-1"), d.ticketOut("show", "LOC-1")
	switch status {
	case "In Review":
		assert.NotContains(t, show, "dispatch  ")
	case "Needs Human":
		assert.Equal(t, 1, strings.Count(show, "dispatch  "))
	default:
		t.Fatalf("LOC-1 is %s", status)
	}
}

func TestTickRulesReadTheSystemAndAttributesOfALocalTicket(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn, Rules: rule(commentOnLocalByAttribute)})
	d.Linear.Tickets(ticket("ENG-1", "Todo", "dispatch"))
	d.StartBeforeTheEvents()
	d.fileLocal("Todo", "--attr", "area=webhooks")
	d.fileLocal("Todo", "--attr", "area=billing")

	d.Tick()

	assert.Contains(t, d.ticketOut("show", "LOC-1"), "seen")
	assert.NotContains(t, d.ticketOut("show", "LOC-2"), "seen")
}
