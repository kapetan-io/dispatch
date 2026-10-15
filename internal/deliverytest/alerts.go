// Package deliverytest is an in-process stand-in for the notification center
// that records what it was asked to show.
package deliverytest

import (
	"context"
	"slices"
	"sync"

	"github.com/kapetan-io/dispatch/internal/delivery"
	"github.com/kapetan-io/dispatch/internal/journal"
)

// Alerts is a delivery.Alerts backed by memory. It shows every alert unless
// Fail says the notification center is down.
type Alerts struct {
	journal *journal.Journal

	mu    sync.Mutex
	shown []string
	fail  error
	stall bool
}

// New returns Alerts that record their calls in j.
func New(j *journal.Journal) *Alerts { return &Alerts{journal: j} }

// Fail makes every Show fail with err, as osascript does when the user has
// denied notifications. Fail(nil) restores normal alerts.
func (a *Alerts) Fail(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.fail = err
}

// Stall makes every Show wait until its context ends, as osascript does when it hangs.
func (a *Alerts) Stall(on bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stall = on
}

// Shown lists the alerts shown, oldest first.
func (a *Alerts) Shown() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.shown)
}

func (a *Alerts) Show(ctx context.Context, text string) error {
	a.journal.Record("alerts", "show", text)
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	stall := a.stall
	a.mu.Unlock()
	if stall {
		<-ctx.Done()
		return ctx.Err()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fail != nil {
		return a.fail
	}
	a.shown = append(a.shown, text)
	return nil
}

var _ delivery.Alerts = (*Alerts)(nil)
