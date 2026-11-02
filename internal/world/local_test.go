package world_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/lineartest"
	"github.com/kapetan-io/dispatch/internal/ticket"
	"github.com/kapetan-io/dispatch/internal/tooltest"
	"github.com/kapetan-io/dispatch/internal/world"
)

// A Linear-only setup whose Linear team key is LOC has Linear tickets with LOC- ids, and each read
// must hold one copy of them, as it did before local tickets existed.
func TestRefreshReadsOneCopyOfALinearTicketWhoseIDStartsWithLOC(t *testing.T) {
	tools := tooltest.New(t)
	tools.Linear.Seed(lineartest.Issue{
		Ticket: ticket.Ticket{
			ID: "id-5", Identifier: "LOC-5", Status: "In Progress", Team: "LOC",
			Labels: []ticket.Label{{Name: "ai"}},
		},
		Assignee: "viewer-1",
	})
	src := world.Sources{
		Linear: tools.Linear.Connect(lineartest.Key), Herdr: tools.Herdr, Treehouse: tools.Treehouse,
		PullRequests: tools.PullRequests, Stderr: io.Discard, Tolerant: true, Resilient: true,
	}
	scope := world.ScopeOf(config.Config{Plugins: config.Plugins{Linear: config.Linear{AILabel: "ai", Teams: []string{"LOC"}}}})
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

	prev, err := world.Refresh(context.Background(), src, scope, world.World{}, world.Everything, now)
	require.NoError(t, err)
	for range 3 {
		prev, err = world.Refresh(context.Background(), src, scope, prev, world.FromLinear, now)
		require.NoError(t, err)
	}

	assert.Len(t, prev.Tickets, 1)
}
