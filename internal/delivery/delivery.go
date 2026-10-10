// Package delivery carries the dispatcher's messages, a nudge to one worker or
// a notification to the user. Callers name who should hear it, and the Post
// picks the route.
package delivery

import (
	"context"
	"errors"

	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/worker"
)

// ErrBlocked is returned for a worker waiting on a question. Typing into it
// would answer the question, so no route is tried.
var ErrBlocked = errors.New("worker is blocked on a question")

// Post delivers messages. Herdr and Osascript are the commands to run.
type Post struct {
	Herdr     string
	Osascript string
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

// Notify tells the user. The error is the alert's.
func (p Post) Notify(ctx context.Context, ev Event) error {
	return alert(ctx, p.Osascript, ev.Text)
}
