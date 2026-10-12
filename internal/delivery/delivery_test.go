package delivery_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/delivery"
	"github.com/kapetan-io/dispatch/internal/deliverytest"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
	"github.com/kapetan-io/dispatch/internal/journal"
	"github.com/kapetan-io/dispatch/internal/worker"
)

func idleAgent() *herdrtest.Herdr {
	fake := herdrtest.New(&journal.Journal{})
	fake.AddWorkspace(herdr.Workspace{ID: "w1"})
	fake.AddAgent(herdr.Agent{Kind: "claude", Status: "idle", PaneID: "w1:p1", WorkspaceID: "w1"})
	return fake
}

func TestNudgeTypesIntoThePane(t *testing.T) {
	fake := idleAgent()
	post := delivery.Post{Herdr: fake}

	err := post.Nudge(context.Background(), worker.Worker{State: worker.Idle, PaneID: "w1:p1"}, "keep going")

	require.NoError(t, err)
	assert.Equal(t, []string{"w1:p1|keep going"}, fake.Prompted())
}

func TestNudgeRefusesABlockedWorkerBeforeAskingHerdr(t *testing.T) {
	fake := idleAgent()
	post := delivery.Post{Herdr: fake}

	err := post.Nudge(context.Background(), worker.Worker{State: worker.Blocked, PaneID: "w1:p1"}, "keep going")

	require.ErrorIs(t, err, delivery.ErrBlocked)
	assert.Empty(t, fake.Prompted())
}

func TestNudgeReportsAWorkerThatBlockedAfterTheSnapshot(t *testing.T) {
	fake := idleAgent()
	fake.BlockAfterSnapshot("w1:p1")
	post := delivery.Post{Herdr: fake}

	err := post.Nudge(context.Background(), worker.Worker{State: worker.Idle, PaneID: "w1:p1"}, "keep going")

	require.ErrorContains(t, err, "agent_blocked")
	require.ErrorContains(t, err, "while prompting herdr pane w1:p1")
	assert.NotErrorIs(t, err, delivery.ErrBlocked)
	assert.Empty(t, fake.Prompted())
}

func TestNudgeReportsAPaneHerdrDoesNotKnow(t *testing.T) {
	post := delivery.Post{Herdr: idleAgent()}

	err := post.Nudge(context.Background(), worker.Worker{State: worker.Idle, PaneID: "w9:p9"}, "hi")

	require.ErrorContains(t, err, "agent target w9:p9 not found")
	assert.NotErrorIs(t, err, delivery.ErrBlocked)
}

func TestNotifyShowsTheAlert(t *testing.T) {
	alerts := deliverytest.New(&journal.Journal{})
	post := delivery.Post{Alerts: alerts}

	err := post.Notify(context.Background(), delivery.Event{Text: `ENG-1 needs "you"`})

	require.NoError(t, err)
	assert.Equal(t, []string{`ENG-1 needs "you"`}, alerts.Shown())
}

func TestNotifyReturnsTheAlertsError(t *testing.T) {
	alerts := deliverytest.New(&journal.Journal{})
	alerts.Fail(errors.New("exit status 1: denied"))
	post := delivery.Post{Alerts: alerts}

	err := post.Notify(context.Background(), delivery.Event{Text: "x"})

	require.ErrorContains(t, err, "while notifying")
	assert.ErrorContains(t, err, "denied")
}
