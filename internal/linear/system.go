package linear

import (
	"context"
	"fmt"

	"github.com/kapetan-io/dispatch/internal/ticket"
)

// System writes Linear tickets back for the executor. Workspace is a function because the API key can
// rotate between calls.
type System struct {
	Workspace func() Workspace
	AILabel   string
	Teams     []string
	Statuses  ticket.Statuses
}

var _ ticket.System = System{}

// fresh reads the ticket again, whatever its status, so a decision made on an old read fails
// instead of landing.
func (s System) fresh(ctx context.Context, id string) (ticket.Ticket, error) {
	got, err := s.Workspace().LeasedTickets(ctx, s.AILabel, s.Teams, []string{id})
	if err != nil {
		return ticket.Ticket{}, err
	}
	for _, t := range got {
		if t.Identifier == id {
			return t, nil
		}
	}
	return ticket.Ticket{}, fmt.Errorf("%s was not found in Linear", id)
}

// Move sets the status doing job when the ticket still has the status seen had.
func (s System) Move(ctx context.Context, seen ticket.Ticket, job ticket.Job, note string) (string, error) {
	name, ok := s.Statuses.NameOf(job)
	if !ok {
		return "", fmt.Errorf("no Linear status does the job %s", job)
	}
	now, err := s.fresh(ctx, seen.Identifier)
	if err != nil {
		return "", err
	}
	if now.Status != seen.Status {
		return "", fmt.Errorf("%s is %s now, not the %s the cycle saw", seen.Identifier, now.Status, seen.Status)
	}
	ws := s.Workspace()
	state, err := ws.StateID(ctx, seen.Team, name)
	if err != nil {
		return "", err
	}
	if err := ws.Move(ctx, seen.ID, state); err != nil {
		return "", err
	}
	if note != "" {
		if err := ws.Comment(ctx, seen.ID, note); err != nil {
			return name, fmt.Errorf("%w: while commenting on %s: %v", ticket.ErrNoteLost, seen.Identifier, err)
		}
	}
	return name, nil
}

// Comment adds a comment to the ticket.
func (s System) Comment(ctx context.Context, ref ticket.Ticket, body string) error {
	if ref.ID == "" {
		return fmt.Errorf("%s was not read", ref.Identifier)
	}
	return s.Workspace().Comment(ctx, ref.ID, body)
}

// Unchanged compares the ticket's status and newest comment with what seen read.
func (s System) Unchanged(ctx context.Context, seen ticket.Ticket) error {
	tickets, err := s.Workspace().ManagedTickets(ctx, s.AILabel, s.Teams)
	if err != nil {
		return err
	}
	for _, now := range tickets {
		if now.Identifier == seen.Identifier && now.LastComment == seen.LastComment && now.Status == seen.Status {
			return nil
		}
	}
	return fmt.Errorf("%s: %w", seen.Identifier, ticket.ErrChanged)
}
