package launcher

import (
	"context"
	"sync"
)

// cloneHolds lets one launch at a time hold a clone. Every tree of a pool is a
// worktree of the user's clone, so they share one set of remote refs, and two
// fetches that move the same ref at once fail with "incorrect old value provided".
// It is shared by every launcher in the process, which is safe for parallel tests because
// each test launches from its own temp clone and so holds its own.
var cloneHolds sync.Map

// holdClone queues behind the launches holding the clone and returns the func that lets go of
// it, or false when ctx ends first.
func holdClone(ctx context.Context, clone string) (func(), bool) {
	v, _ := cloneHolds.LoadOrStore(clone, make(chan struct{}, 1))
	hold := v.(chan struct{})
	select {
	case hold <- struct{}{}:
		return func() { <-hold }, true
	case <-ctx.Done():
		return nil, false
	}
}
