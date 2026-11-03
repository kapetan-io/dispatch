// Package notify tells the sessions following a ticket that something happened on it.
package notify

import (
	"context"
	"io"

	"github.com/kapetan-io/dispatch/internal/harness"
	"github.com/kapetan-io/dispatch/internal/ticket"
)

// Post delivers notifications to the sessions in Followers. Stderr is where a failed or
// dropped follower message is reported.
type Post struct {
	Followers *Followers
	// Harnesses deliver to a follower, by the kind it runs.
	Harnesses harness.Registry
	Stderr    io.Writer
}

// Notification is something the sessions following a ticket should hear about.
type Notification struct {
	Ticket ticket.Ticket
	Reason string
	Text   string
}

// Tell sends n to every session following its ticket. A follower that can't be reached
// is reported on Stderr and never fails the caller, so nothing retries a notification.
func (p Post) Tell(ctx context.Context, n Notification) {
	if p.Followers == nil {
		return
	}
	p.deliver(ctx, p.Followers.Of(n.Ticket.Identifier), n)
}

// TellAll sends text, which is about dispatch rather than a ticket, to every session
// following at least one ticket, once each however many it follows.
func (p Post) TellAll(ctx context.Context, text string) {
	if p.Followers == nil {
		return
	}
	p.deliver(ctx, p.Followers.Sessions(), Notification{Text: text})
}
