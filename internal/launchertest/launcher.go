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
	stalled  bool
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

// Stall makes every launch from now on wait until its context ends and then report
// agent_start_failed, which is a launch that outlasts its deadline.
func (l *Launcher) Stall() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stalled = true
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
	l.launched = append(l.launched, r)
	stalled := l.stalled
	l.mu.Unlock()
	if stalled {
		<-ctx.Done()
		return launcher.Result{Status: "agent_start_failed", Detail: "the launch outlasted its deadline", Ticket: r.Ticket}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.answer != nil {
		return *l.answer
	}
	return launcher.Result{Status: launcher.StatusOK, Ticket: r.Ticket, Branch: r.Branch, Stacked: r.Stack != nil}
}

var _ launcher.Launcher = (*Launcher)(nil)
