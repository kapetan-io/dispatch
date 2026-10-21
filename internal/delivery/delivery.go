// Package delivery carries the dispatcher's messages, a nudge to one worker or
// a notification to the user and the sessions following a ticket. Callers name who
// should hear it, and the Post picks the route.
package delivery

import (
	"context"
	"errors"
	"fmt"
	"github.com/kapetan-io/dispatch/internal/world"
	"io"

	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/typed"
)

// ErrBlocked is returned for a worker waiting on a question. Typing into it
// would answer the question, so no route is tried.
var ErrBlocked = errors.New("worker is blocked on a question")

// Post delivers messages through Herdr and Alerts. Followers lists the sessions
// following each ticket that Notify also messages, and Stderr is where a failed
// or dropped watcher message is reported.
type Post struct {
	Herdr     herdr.Herdr
	Alerts    Alerts
	Followers *Followers
	Stderr    io.Writer
	// Typed notes each nudge, so the prompt hook can tell it from a person's message.
	Typed *typed.Log
}

// Event is something the user should hear about.
type Event struct {
	Ticket linear.Ticket
	Reason string
	Text   string
}

// Nudge types text into the worker. A refused nudge is not a failed one, and
// callers tell them apart by ErrBlocked.
func (p Post) Nudge(ctx context.Context, w world.Worker, text string) error {
	// herdr refuses a blocked agent too, but another route would not.
	if w.State == world.Blocked {
		return ErrBlocked
	}
	p.Typed.Note(w.TreePath, text)
	if err := p.Herdr.Prompt(ctx, w.PaneID, text); err != nil {
		// Only a refusal proves the text was not typed. Any other failure, a timeout included, may have typed it.
		var refusal *herdr.Refusal
		if errors.As(err, &refusal) && (refusal.Code == "agent_blocked" || refusal.Code == "agent_not_found") {
			p.Typed.Withdraw(w.TreePath, text)
		}
		return fmt.Errorf("while prompting herdr pane %s: %w", w.PaneID, err)
	}
	return nil
}

// Notify tells the user, then every session following the event's ticket. The
// caller retries a failed alert at the next resync, so watchers hear only once it has
// shown, or a broken alert would wake them on every retry. The error is the
// alert's alone, and a watcher that can't be reached never makes the caller
// retry.
func (p Post) Notify(ctx context.Context, ev Event) error {
	if err := alert(ctx, p.Alerts, ev.Text); err != nil {
		return err
	}
	p.tellWatchers(ctx, ev)
	return nil
}

// NotifyOnce is Notify for an event the caller never raises again, such as a
// merge, which is already recorded when the alert goes out. Watchers hear it
// whether or not the alert showed, because no retry will carry it.
func (p Post) NotifyOnce(ctx context.Context, ev Event) error {
	err := alert(ctx, p.Alerts, ev.Text)
	p.tellWatchers(ctx, ev)
	return err
}
