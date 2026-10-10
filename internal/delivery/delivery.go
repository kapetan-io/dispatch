// Package delivery carries the dispatcher's messages, a nudge to one worker or
// a notification to the user and the sessions following a ticket. Callers name who
// should hear it, and the Post picks the route.
package delivery

import (
	"context"
	"errors"
	"io"

	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/worker"
)

// ErrBlocked is returned for a worker waiting on a question. Typing into it
// would answer the question, so no route is tried.
var ErrBlocked = errors.New("worker is blocked on a question")

// Post delivers messages. Herdr and Osascript are the commands to run.
// StateDir holds the list of sessions following each ticket that Notify also
// messages, and Stderr is where a failed or dropped watcher message is reported.
type Post struct {
	Herdr     string
	Osascript string
	StateDir  string
	Stderr    io.Writer
}

// Event is something the user should hear about.
type Event struct {
	Ticket linear.Ticket
	Reason string
	Text   string
}

// Nudge types text into the worker. A refused nudge is not a failed one, and
// callers tell them apart by ErrBlocked.
func (p Post) Nudge(ctx context.Context, w worker.Worker, text string) error {
	// herdr refuses a blocked agent too, but another route would not.
	if w.State == worker.Blocked {
		return ErrBlocked
	}
	return herdr.Prompt(ctx, p.Herdr, w.PaneID, text)
}

// Notify tells the user, then every session following the event's ticket. The
// caller retries a failed alert next tick, so watchers hear only once it has
// shown, or a broken alert would wake them on every retry. The error is the
// alert's alone, and a watcher that can't be reached never makes the caller
// retry.
func (p Post) Notify(ctx context.Context, ev Event) error {
	if err := alert(ctx, p.Osascript, ev.Text); err != nil {
		return err
	}
	p.tellWatchers(ctx, ev)
	return nil
}

// NotifyOnce is Notify for an event the caller never raises again, such as a
// merge, which is already recorded when the alert goes out. Watchers hear it
// whether or not the alert showed, because no retry will carry it.
func (p Post) NotifyOnce(ctx context.Context, ev Event) error {
	err := alert(ctx, p.Osascript, ev.Text)
	p.tellWatchers(ctx, ev)
	return err
}
