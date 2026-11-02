package dispatch_test

import (
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

func TestTickMovesALocalTicketOnlyOnceWhenTwoRulesMoveItFromTheSameRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn, Rules: rule(moveThenMove)})
	d.StartBeforeTheEvents()
	d.fileLocal("Todo")

	res := d.Run("tick")

	require.ErrorContains(t, res.Err, "LOC-1 is In Review now, not the Todo the cycle saw")
	assert.Equal(t, "In Review", d.statusOf("LOC-1"))
	moves := d.ReceiptsOf("move", "LOC-1")
	require.Len(t, moves, 2)
	assert.Equal(t, "done", moves[0].State)
	assert.Equal(t, "failed", moves[1].State)
	assert.Contains(t, moves[1].Detail, "LOC-1 is In Review now, not the Todo the cycle saw")
	assert.NotContains(t, moves[1].Detail, "Linear")
}

func TestTickEscalatesNothingWhenALocalTicketMovedSinceTheCycleRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: localOn, Rules: rule(moveThenEscalate)})
	d.StartBeforeTheEvents()
	d.fileLocal("Todo")

	res := d.Run("tick")

	require.ErrorContains(t, res.Err, "LOC-1 is In Review now, not the Todo the cycle saw")
	assert.Equal(t, "In Review", d.statusOf("LOC-1"))
	assert.NotContains(t, d.ticketOut("show", "LOC-1"), "dispatch  ")
	second := d.ReceiptsOf("escalate", "LOC-1")
	require.Len(t, second, 1)
	assert.Equal(t, "failed", second[0].State)
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
