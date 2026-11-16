package workers

import (
	"time"

	"github.com/kapetan-io/dispatch/internal/sessionlog"
)

// BusyExpiry is how long a Stop that listed running tasks still counts as
// waiting on them. A hung task never wakes its worker, so without it the
// last Stop would read busy forever.
const BusyExpiry = 60 * time.Minute

// Record is a Stop record: the latest Stop of one session of a worker, the moment its harness
// ended a turn. The hook is the only place the harness says whether an idle session is waiting
// on background work.
type Record struct {
	At time.Time `json:"at"`
	// Reported is false when the Stop carried no background_tasks field, which
	// only a Claude Code version that predates it omits. That means unknown,
	// not idle.
	Reported bool `json:"reported"`
	// Running counts the tasks the Stop listed as running.
	Running int `json:"running"`
}

// Busy reports whether the worker was waiting on its own background work at
// this Stop, and is still within BusyExpiry of it.
func (r Record) Busy(now time.Time) bool {
	return r.Reported && r.Running > 0 && now.Sub(r.At) < BusyExpiry
}

// InTurn reports whether the worker is in a turn by what dispatch holds: its latest Stop
// listed a background task still running within BusyExpiry, or its session took a message after
// that Stop. Only a reported Stop can say so, since a worker with no Stop record is not known to
// have paused. The label and the world's waiting fact both ask it, so they never disagree.
func (r Record) InTurn(log sessionlog.Log, now time.Time) bool {
	return r.Busy(now) || (r.Reported && log.TookMessageAfter(r.At.UnixMilli()))
}
