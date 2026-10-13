// Package launchertest is an in-process stand-in for the launcher that records
// the launches it was asked for, so a test of the dispatcher sees which workers
// it started without leasing a tree or opening a workspace.
package launchertest

import (
	"context"
	"slices"
	"sync"

	"github.com/kapetan-io/dispatch/internal/journal"
	"github.com/kapetan-io/dispatch/internal/launcher"
)

// Launcher is a launcher.Launcher that starts a worker for every request and
// reports it ok, unless Answer says otherwise.
type Launcher struct {
	journal *journal.Journal

	mu       sync.Mutex
	launched []launcher.Request
	answer   *launcher.Result
}

// New returns a Launcher that records its calls in j.
func New(j *journal.Journal) *Launcher { return &Launcher{journal: j} }

// Answer makes every launch from now on end with r, such as pool_exhausted when
// the pool has no free tree. Ok launches keep their ticket and branch.
func (l *Launcher) Answer(r launcher.Result) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.answer = &r
}

// Launched lists the requests, oldest first.
func (l *Launcher) Launched() []launcher.Request {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.launched)
}

func (l *Launcher) Launch(ctx context.Context, r launcher.Request) launcher.Result {
	l.journal.Record("launcher", "launch", r.Ticket, r.Branch)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.launched = append(l.launched, r)
	if l.answer != nil {
		return *l.answer
	}
	return launcher.Result{Status: launcher.StatusOK, Ticket: r.Ticket, Branch: r.Branch}
}

var _ launcher.Launcher = (*Launcher)(nil)
