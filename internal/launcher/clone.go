package launcher

import (
	"context"
	"sync"
)

// cloneTurns hands out one turn at a time per clone. Every tree of a pool is a
// worktree of the user's clone, so they share one set of remote refs, and two
// fetches that move the same ref at once fail with "incorrect old value provided".
// It is shared by every launcher in the process, which is safe for parallel tests because
// each test launches from its own temp clone and so takes its own turn.
var cloneTurns sync.Map

// takeTurn waits for the clone's turn and returns the func that ends it, or false
// when ctx ends first.
func takeTurn(ctx context.Context, clone string) (func(), bool) {
	v, _ := cloneTurns.LoadOrStore(clone, make(chan struct{}, 1))
	turn := v.(chan struct{})
	select {
	case turn <- struct{}{}:
		return func() { <-turn }, true
	case <-ctx.Done():
		return nil, false
	}
}
