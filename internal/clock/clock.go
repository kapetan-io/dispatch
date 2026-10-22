// Package clock is the time dispatch reads and the timers it sets. A test supplies its own Clock
// and moves it, so a timeout fires when the clock passes it instead of after a real wait.
package clock

import (
	"context"
	"sync"
	"time"
)

// Clock owns every timer the loop sets, the resync, and the deadlines on the work it
// starts, and stamps ingest times.
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
	// NewDeadline is NewTimer for a timeout. what names it so a fake clock can expire this
	// one and no other. Names in use are "<action kind> <subject>" ("herdr.launch ENG-1"),
	// "rule <name> <if|then>", "rule <name> http.get", "rules load", "launch preflight",
	// "alert", "record-stop" and "api_key_command". Several live deadlines may share a name,
	// such as one "rule loops if" per ticket.
	NewDeadline(what string, d time.Duration) Timer
}

// Timer fires once on C unless stopped.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

// Wall is the real clock.
type Wall struct{}

// Now is the current time.
func (Wall) Now() time.Time { return time.Now() }

// NewTimer starts a timer on the real clock.
func (Wall) NewTimer(d time.Duration) Timer { return wallTimer{time.NewTimer(d)} }

// NewDeadline starts a timer on the real clock and ignores the name.
func (Wall) NewDeadline(_ string, d time.Duration) Timer { return wallTimer{time.NewTimer(d)} }

type wallTimer struct{ t *time.Timer }

func (w wallTimer) C() <-chan time.Time { return w.t.C }
func (w wallTimer) Stop() bool          { return w.t.Stop() }

// WithTimeout is context.WithTimeout on c, which a fake clock can pass without waiting. The
// context ends with context.DeadlineExceeded once c passes d, and its Deadline is the parent's. The caller
// must call the returned cancel.
func WithTimeout(parent context.Context, c Clock, what string, d time.Duration) (context.Context, context.CancelFunc) {
	ctx := &timeoutContext{Context: parent, done: make(chan struct{})}
	// Already over, as context.WithTimeout returns it, so work that checks Err first never starts.
	if err := parent.Err(); err != nil {
		ctx.end(err)
	} else if d <= 0 {
		ctx.end(context.DeadlineExceeded)
	}
	timer := c.NewDeadline(what, d)
	released := make(chan struct{})
	go func() {
		select {
		case <-timer.C():
			ctx.end(context.DeadlineExceeded)
		case <-parent.Done():
			ctx.end(parent.Err())
		case <-released:
		}
	}()
	var once sync.Once
	return ctx, func() {
		timer.Stop()
		ctx.end(context.Canceled)
		once.Do(func() { close(released) })
	}
}

type timeoutContext struct {
	context.Context
	mu   sync.Mutex
	done chan struct{}
	err  error
}

func (c *timeoutContext) Done() <-chan struct{} { return c.done }

func (c *timeoutContext) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *timeoutContext) end(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		c.err = err
		close(c.done)
	}
}

// AfterFunc is time.AfterFunc on c. The returned stop reports whether the timer was still pending.
func AfterFunc(c Clock, what string, d time.Duration, f func()) (stop func() bool) {
	timer := c.NewDeadline(what, d)
	stopped := make(chan struct{})
	var once sync.Once
	go func() {
		select {
		case <-timer.C():
			select {
			case <-stopped:
			default:
				f()
			}
		case <-stopped:
		}
	}()
	return func() bool {
		was := timer.Stop()
		once.Do(func() { close(stopped) })
		return was
	}
}
