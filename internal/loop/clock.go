package loop

import "time"

// Clock owns every timer the loop sets, the resync, and stamps ingest times, so a
// test can fire a resync without waiting for one.
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
}

// Timer fires once on C unless stopped.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

// WallClock is the real clock.
type WallClock struct{}

// Now is the current time.
func (WallClock) Now() time.Time { return time.Now() }

// NewTimer starts a timer on the real clock.
func (WallClock) NewTimer(d time.Duration) Timer { return wallTimer{time.NewTimer(d)} }

type wallTimer struct{ t *time.Timer }

func (w wallTimer) C() <-chan time.Time { return w.t.C }
func (w wallTimer) Stop() bool          { return w.t.Stop() }
