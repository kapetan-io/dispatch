package lineartest

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/linear"
)

// missingTicket is a version 4 UUID, which is all Linear accepts as an id, that no ticket has.
const missingTicket = "6f1c2a4e-9b3d-4c85-8a7e-2d5b0f4e1a93"

// Subject is a Linear workspace under test.
type Subject struct {
	// Connect opens the workspace with an API key.
	Connect linear.Connect
	// Key is a key the workspace accepts.
	Key string
	// Team is the key of a team the key's owner belongs to.
	Team string
	// Seed replaces the workspace's tickets with these. It is nil for the real
	// Linear, whose workspace the drift check must not change.
	Seed func(issues ...Issue)
	// Viewer is the account the key belongs to.
	Viewer linear.Viewer
}

// Scenario is one situation with the outcome both the fake and the real Linear
// must give. A Seeded scenario needs tickets the drift check can't create, so
// it runs against the fake only.
type Scenario struct {
	Name   string
	Seeded bool
	Run    func(t *testing.T, s Subject)
}

// Scenarios is the contract for linear.Workspace. The fake runs it in every
// test run, and the real adapter runs the unseeded ones under the drift build
// tag, reading the key owner's real workspace and changing nothing in it.
var Scenarios = []Scenario{
	{Name: "ViewerIsTheKeyOwner", Run: func(t *testing.T, s Subject) {
		viewer, err := s.Connect(s.Key).Viewer(context.Background())

		require.NoError(t, err)
		assert.NotEmpty(t, viewer.ID)
		assert.NotEmpty(t, viewer.Name)
	}},
	{Name: "ARejectedKeyIsUnauthorized", Run: func(t *testing.T, s Subject) {
		_, err := s.Connect("lin_api_rejected").Viewer(context.Background())

		require.ErrorIs(t, err, linear.ErrUnauthorized)
	}},
	{Name: "AMissingKeyIsRefused", Run: func(t *testing.T, s Subject) {
		_, err := s.Connect("").ManagedTickets(context.Background(), "ai", []string{s.Team})

		require.Error(t, err)
	}},
	{Name: "StateIDFindsAStatusTheTeamHas", Run: func(t *testing.T, s Subject) {
		id, err := s.Connect(s.Key).StateID(context.Background(), s.Team, "Todo")

		require.NoError(t, err)
		assert.NotEmpty(t, id)
	}},
	{Name: "StateIDRefusesAStatusTheTeamLacks", Run: func(t *testing.T, s Subject) {
		_, err := s.Connect(s.Key).StateID(context.Background(), s.Team, "No Such Status")

		require.ErrorContains(t, err, `has no "No Such Status" status`)
	}},
	{Name: "StateIDRefusesATeamTheKeyOwnerIsNotIn", Run: func(t *testing.T, s Subject) {
		_, err := s.Connect(s.Key).StateID(context.Background(), "NOSUCHTEAM", "Todo")

		require.ErrorContains(t, err, `team NOSUCHTEAM has no "Todo" status`)
	}},
	{Name: "ManagedTicketsWithAnUnusedLabelIsEmpty", Run: func(t *testing.T, s Subject) {
		found, err := s.Connect(s.Key).ManagedTickets(context.Background(), "dispatch-drift-unused-label", []string{s.Team})

		require.NoError(t, err)
		assert.Empty(t, found)
	}},
	{Name: "LeasedTicketsIgnoresIdentifiersItCannotPlace", Run: func(t *testing.T, s Subject) {
		found, err := s.Connect(s.Key).LeasedTickets(context.Background(), "ai", []string{s.Team}, []string{"NOSUCHTEAM-1", "garbage", s.Team + "-0"})

		require.NoError(t, err)
		assert.Empty(t, found)
	}},
	{Name: "LeasedTicketsReturnsOnlyTheTicketsNamed", Run: func(t *testing.T, s Subject) {
		if s.Seed != nil {
			s.Seed(
				owned(s, 1, "Done"), owned(s, 2, "Done"), owned(s, 3, "Done"), owned(s, 4, "In Progress"),
			)
		}

		found, err := s.Connect(s.Key).LeasedTickets(context.Background(), "ai", []string{s.Team}, []string{s.Team + "-1", s.Team + "-2"})

		require.NoError(t, err)
		for _, ticket := range found {
			assert.Contains(t, []string{s.Team + "-1", s.Team + "-2"}, ticket.Identifier)
		}
		if s.Seed != nil {
			assert.Len(t, found, 2)
		}
	}},
	{Name: "ParentChildrenOfAnUnknownParentIsEmpty", Run: func(t *testing.T, s Subject) {
		children, err := s.Connect(s.Key).ParentChildren(context.Background(), []string{missingTicket})

		require.NoError(t, err)
		assert.Empty(t, children)
	}},
	{Name: "MoveRefusesAMissingTicket", Run: func(t *testing.T, s Subject) {
		work := s.Connect(s.Key)
		state, err := work.StateID(context.Background(), s.Team, "Todo")
		require.NoError(t, err)

		err = work.Move(context.Background(), missingTicket, state)

		require.ErrorContains(t, err, "Entity not found")
	}},
	{Name: "CommentRefusesAMissingTicket", Run: func(t *testing.T, s Subject) {
		err := s.Connect(s.Key).Comment(context.Background(), missingTicket, "drift check")

		require.ErrorContains(t, err, "Entity not found")
	}},
	{Name: "ManagedTicketsAreTheStartedLabeledOnesAssignedToTheKeyOwner", Seeded: true, Run: func(t *testing.T, s Subject) {
		elsewhere := owned(s, 4, "In Progress")
		elsewhere.Team = "OTHER"
		stranger := owned(s, 5, "In Progress")
		stranger.Assignee = "someone-else"
		unlabeled := owned(s, 6, "In Progress")
		unlabeled.Labels = nil
		s.Seed(owned(s, 1, "In Progress"), owned(s, 2, "Todo"), owned(s, 3, "Done"), elsewhere, stranger, unlabeled)

		found, err := s.Connect(s.Key).ManagedTickets(context.Background(), "ai", []string{s.Team})

		require.NoError(t, err)
		require.Len(t, found, 1)
		assert.Equal(t, s.Team+"-1", found[0].Identifier)
	}},
	{Name: "ManagedTicketsFailWhenTheyOverflowAPage", Seeded: true, Run: func(t *testing.T, s Subject) {
		var issues []Issue
		for n := range linear.PageSize + 1 {
			issues = append(issues, owned(s, n+1, "In Progress"))
		}
		s.Seed(issues...)

		_, err := s.Connect(s.Key).ManagedTickets(context.Background(), "ai", []string{s.Team})

		require.ErrorContains(t, err, "more than 100 started")
	}},
	{Name: "MoveChangesTheStatus", Seeded: true, Run: func(t *testing.T, s Subject) {
		work := s.Connect(s.Key)
		ticket := owned(s, 1, "In Progress")
		s.Seed(ticket)
		state, err := work.StateID(context.Background(), s.Team, "Done")
		require.NoError(t, err)

		require.NoError(t, work.Move(context.Background(), ticket.ID, state))

		found, err := work.LeasedTickets(context.Background(), "ai", []string{s.Team}, []string{ticket.Identifier})
		require.NoError(t, err)
		require.Len(t, found, 1)
		assert.Equal(t, "Done", found[0].Status)
	}},
	{Name: "MoveRefusesAStateTheTeamLacks", Seeded: true, Run: func(t *testing.T, s Subject) {
		ticket := owned(s, 1, "In Progress")
		s.Seed(ticket)

		err := s.Connect(s.Key).Move(context.Background(), ticket.ID, "state-nowhere")

		require.Error(t, err)
	}},
	{Name: "CommentShowsAsTheNewestComment", Seeded: true, Run: func(t *testing.T, s Subject) {
		work := s.Connect(s.Key)
		ticket := owned(s, 1, "In Progress")
		s.Seed(ticket)

		require.NoError(t, work.Comment(context.Background(), ticket.ID, "hello"))

		found, err := work.ManagedTickets(context.Background(), "ai", []string{s.Team})
		require.NoError(t, err)
		require.Len(t, found, 1)
		assert.NotEmpty(t, found[0].LastComment)
	}},
	{Name: "ParentChildrenComeOldestFirst", Seeded: true, Run: func(t *testing.T, s Subject) {
		parent := owned(s, 1, "In Progress")
		newer, older := owned(s, 2, "Todo"), owned(s, 3, "Todo")
		newer.Parent, older.Parent = parent.Identifier, parent.Identifier
		newer.CreatedAt, older.CreatedAt = "2026-10-02T09:00:00.000Z", "2026-10-01T09:00:00.000Z"
		s.Seed(parent, newer, older)

		children, err := s.Connect(s.Key).ParentChildren(context.Background(), []string{parent.ID})

		require.NoError(t, err)
		require.Len(t, children[parent.Identifier], 2)
		assert.Equal(t, s.Team+"-3", children[parent.Identifier][0].Identifier)
		assert.Equal(t, s.Team+"-2", children[parent.Identifier][1].Identifier)
	}},
	{Name: "ParentChildrenReturnsEveryChildInEveryStatus", Seeded: true, Run: func(t *testing.T, s Subject) {
		parent := owned(s, 1, "In Progress")
		done, todo := owned(s, 2, "Done"), owned(s, 3, "Todo")
		done.Parent, todo.Parent = parent.Identifier, parent.Identifier
		stranger := owned(s, 4, "Todo")
		stranger.Parent, stranger.Assignee = parent.Identifier, "someone-else"
		s.Seed(parent, done, todo, stranger, owned(s, 5, "Todo"))

		children, err := s.Connect(s.Key).ParentChildren(context.Background(), []string{parent.ID})

		require.NoError(t, err)
		require.Len(t, children[parent.Identifier], 3)
		mine := map[string]bool{}
		for _, child := range children[parent.Identifier] {
			mine[child.Identifier] = child.Mine
		}
		assert.Equal(t, map[string]bool{s.Team + "-2": true, s.Team + "-3": true, s.Team + "-4": false}, mine)
	}},
}

// owned is ticket number n of the subject's team, assigned to the key owner,
// with the ai label.
func owned(s Subject, n int, status string) Issue {
	id := fmt.Sprintf("%s-%d", s.Team, n)
	return Issue{
		Ticket: linear.Ticket{
			ID: "id-" + id, Identifier: id, Status: status, Team: s.Team,
			Labels: []linear.Label{{Name: "ai"}},
		},
		Assignee: s.Viewer.ID,
	}
}
