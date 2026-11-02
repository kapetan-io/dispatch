package clock_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/clock"
)

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	clock *fakeClock
	c     chan time.Time
	at    time.Time
	done  bool
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) NewTimer(d time.Duration) clock.Timer { return c.NewDeadline("", d) }

func (c *fakeClock) NewDeadline(_ string, d time.Duration) clock.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{clock: c, c: make(chan time.Time, 1), at: c.now.Add(d)}
	c.timers = append(c.timers, t)
	return t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	for _, t := range c.timers {
		if !t.done && !t.at.After(c.now) {
			t.done = true
			t.c <- c.now
		}
	}
}

func (t *fakeTimer) C() <-chan time.Time { return t.c }

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	was := !t.done
	t.done = true
	return was
}

func TestPausedTimeoutResumesWithTheTimeItHadLeft(t *testing.T) {
	t.Parallel()
	fake := &fakeClock{now: time.Unix(1000, 0)}
	ctx, cancel, pause := clock.WithPausableTimeout(context.Background(), fake, "launch ENG-1", 100*time.Millisecond)
	defer cancel()

	// 70ms of the 100ms budget is spent before the pause, and the hour paused costs nothing.
	fake.advance(70 * time.Millisecond)
	resume := pause()
	fake.advance(time.Hour)
	require.NoError(t, ctx.Err())
	resume()

	fake.advance(29 * time.Millisecond)
	assert.NoError(t, ctx.Err())
	fake.advance(time.Millisecond)
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the timeout never ended the context")
	}
	assert.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
}
