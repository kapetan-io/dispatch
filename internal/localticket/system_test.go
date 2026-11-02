package localticket_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/localticket"
	"github.com/kapetan-io/dispatch/internal/ticket"
)

// seen is the ticket as a world read hands it to the executor.
func seen(t *testing.T, store *localticket.Store, id string) ticket.Ticket {
	t.Helper()
	got, err := store.Tracked(nil, func(ticket.Ticket) bool { return false })
	require.NoError(t, err)
	for _, tk := range got.Tickets {
		if tk.Identifier == id {
			return tk
		}
	}
	t.Fatalf("%s is not tracked", id)
	return ticket.Ticket{}
}

func TestSystemMoveWritesTheNoteInTheSameTransaction(t *testing.T) {
	store := open(t, options(t))
	id, err := store.Create(localticket.Draft{Title: "t", Repo: "api", Status: "In Progress"})
	require.NoError(t, err)

	status, err := store.System().Move(context.Background(), seen(t, store, id), ticket.NeedsHuman, "because it is stuck")
	require.NoError(t, err)

	assert.Equal(t, "Needs Human", status)
	detail, err := store.Show(id)
	require.NoError(t, err)
	assert.Equal(t, "Needs Human", detail.Status)
	require.Len(t, detail.Comments, 1)
	assert.Equal(t, "dispatch", detail.Comments[0].Author)
	assert.Equal(t, "because it is stuck", detail.Comments[0].Body)
}

func TestSystemMoveOfATicketThatMovedSinceLeavesItUnchanged(t *testing.T) {
	store := open(t, options(t))
	id, err := store.Create(localticket.Draft{Title: "t", Repo: "api", Status: "In Progress"})
	require.NoError(t, err)
	cycle := seen(t, store, id)
	_, err = store.Move(id, "In Review")
	require.NoError(t, err)

	_, err = store.System().Move(context.Background(), cycle, ticket.NeedsHuman, "because it is stuck")

	require.ErrorContains(t, err, "LOC-1 is In Review now, not the In Progress the cycle saw")
	detail, err := store.Show(id)
	require.NoError(t, err)
	assert.Equal(t, "In Review", detail.Status)
	assert.Empty(t, detail.Comments)
}

func TestSystemUnchangedSeesAMoveAndAComment(t *testing.T) {
	store := open(t, options(t))
	id, err := store.Create(localticket.Draft{Title: "t", Repo: "api", Status: "In Review"})
	require.NoError(t, err)
	cycle := seen(t, store, id)
	require.NoError(t, store.System().Unchanged(context.Background(), cycle))

	require.NoError(t, store.Comment(id, "you", "one more thing"))
	err = store.System().Unchanged(context.Background(), cycle)

	require.ErrorIs(t, err, ticket.ErrChanged)
	assert.NotContains(t, err.Error(), "Linear")

	cycle = seen(t, store, id)
	_, err = store.Move(id, "Needs Human")
	require.NoError(t, err)
	require.ErrorIs(t, store.System().Unchanged(context.Background(), cycle), ticket.ErrChanged)
}

func TestTrackedReturnsOpenTicketsHoldersAndChildren(t *testing.T) {
	store := open(t, options(t))
	_, err := store.Create(localticket.Draft{Title: "parent", Repo: "api", Status: "In Progress"})
	require.NoError(t, err)
	_, err = store.Create(localticket.Draft{Title: "done child", Repo: "api", Status: "Done", Parent: "LOC-1"})
	require.NoError(t, err)
	_, err = store.Create(localticket.Draft{Title: "canceled", Repo: "api", Status: "Canceled"})
	require.NoError(t, err)
	_, err = store.Create(localticket.Draft{Title: "blocked", Repo: "api", Parent: "LOC-1", BlockedBy: []string{"LOC-3"}})
	require.NoError(t, err)

	got, err := store.Tracked([]string{"LOC-3"}, func(t ticket.Ticket) bool { return t.HasChildren })

	require.NoError(t, err)
	require.Len(t, got.Tickets, 2)
	assert.Equal(t, "LOC-1", got.Tickets[0].Identifier)
	assert.True(t, got.Tickets[0].HasChildren)
	assert.Equal(t, "LOC-4", got.Tickets[1].Identifier)
	assert.Equal(t, []ticket.Blocker{{Identifier: "LOC-3", Status: "Canceled", Abandoned: true}}, got.Tickets[1].Blockers)
	require.Len(t, got.Holders, 1)
	assert.Equal(t, "LOC-3", got.Holders[0].Identifier)
	require.Len(t, got.Children["LOC-1"], 2)
	assert.Equal(t, "LOC-2", got.Children["LOC-1"][0].Identifier)
}
