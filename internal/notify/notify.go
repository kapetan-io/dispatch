// Package notify tells the user, and the sessions following a ticket, that something happened on it.
package notify

import (
	"context"
	"io"

	"github.com/kapetan-io/dispatch/internal/linear"
)

// Post delivers notifications through Alerts and Followers. Followers lists the
// sessions following each ticket that Notify also messages, and Stderr is where
// a failed or dropped follower message is reported.
type Post struct {
	Alerts    Alerts
	Followers *Followers
	Stderr    io.Writer
}

// Notification is something the user should hear about.
type Notification struct {
	Ticket linear.Ticket
	Reason string
	Text   string
}

// Notify tells the user, then every session following the notification's ticket. The
// caller retries a failed alert at the next resync, so followers hear only once it has
// shown, or a broken alert would wake them on every retry. The error is the
// alert's alone, and a follower that can't be reached never makes the caller
// retry.
func (p Post) Notify(ctx context.Context, n Notification) error {
	if err := alert(ctx, p.Alerts, n.Text); err != nil {
		return err
	}
	p.tellFollowers(ctx, n)
	return nil
}

// NotifyOnce is Notify for a notification the caller never raises again, such as a
// merge, which is already recorded when the alert goes out. Followers hear it
// whether or not the alert showed, because no retry will carry it.
func (p Post) NotifyOnce(ctx context.Context, n Notification) error {
	err := alert(ctx, p.Alerts, n.Text)
	p.tellFollowers(ctx, n)
	return err
}
