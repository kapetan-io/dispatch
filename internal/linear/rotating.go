package linear

import (
	"context"
	"errors"
	"sync"
)

// RotatingKey is a Linear workspace whose API key can rotate. It connects with the key
// find returns on first use, and again after Linear rejects the key. find runs only
// while there is no connection, so a password manager that prompts to unlock asks once
// per connection, not on every read.
type RotatingKey struct {
	mu      sync.Mutex
	find    func(context.Context) (string, error)
	connect Connect
	cur     Workspace
}

// NewRotatingKey returns a RotatingKey that opens each connection with connect.
func NewRotatingKey(find func(context.Context) (string, error), connect Connect) *RotatingKey {
	return &RotatingKey{find: find, connect: connect}
}

// Workspace returns the current connection, connecting first with the key find returns when there is none.
func (k *RotatingKey) Workspace(ctx context.Context) (Workspace, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.cur != nil {
		return k.cur, nil
	}
	key, err := k.find(ctx)
	if err != nil {
		return nil, err
	}
	k.cur = k.connect(key)
	return k.cur, nil
}

// rotated drops the connection when Linear rejected its key, so the next call finds the key again.
func (k *RotatingKey) rotated(err error) {
	if !errors.Is(err, ErrUnauthorized) {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.cur = nil
}

func (k *RotatingKey) ManagedTickets(ctx context.Context, label string, teams []string) ([]Ticket, error) {
	ws, err := k.Workspace(ctx)
	if err != nil {
		return nil, err
	}
	out, err := ws.ManagedTickets(ctx, label, teams)
	k.rotated(err)
	return out, err
}

func (k *RotatingKey) LeasedTickets(ctx context.Context, label string, teams, identifiers []string) ([]Ticket, error) {
	ws, err := k.Workspace(ctx)
	if err != nil {
		return nil, err
	}
	out, err := ws.LeasedTickets(ctx, label, teams, identifiers)
	k.rotated(err)
	return out, err
}

func (k *RotatingKey) ParentChildren(ctx context.Context, parents []string) (map[string][]Ticket, error) {
	ws, err := k.Workspace(ctx)
	if err != nil {
		return nil, err
	}
	out, err := ws.ParentChildren(ctx, parents)
	k.rotated(err)
	return out, err
}

func (k *RotatingKey) StateID(ctx context.Context, team, name string) (string, error) {
	ws, err := k.Workspace(ctx)
	if err != nil {
		return "", err
	}
	out, err := ws.StateID(ctx, team, name)
	k.rotated(err)
	return out, err
}

func (k *RotatingKey) Move(ctx context.Context, ticketID, stateID string) error {
	ws, err := k.Workspace(ctx)
	if err != nil {
		return err
	}
	err = ws.Move(ctx, ticketID, stateID)
	k.rotated(err)
	return err
}

func (k *RotatingKey) Comment(ctx context.Context, ticketID, body string) error {
	ws, err := k.Workspace(ctx)
	if err != nil {
		return err
	}
	err = ws.Comment(ctx, ticketID, body)
	k.rotated(err)
	return err
}

func (k *RotatingKey) Viewer(ctx context.Context) (Viewer, error) {
	ws, err := k.Workspace(ctx)
	if err != nil {
		return Viewer{}, err
	}
	out, err := ws.Viewer(ctx)
	k.rotated(err)
	return out, err
}

func (k *RotatingKey) Usage() Stats {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.cur == nil {
		return Stats{}
	}
	return k.cur.Usage()
}
