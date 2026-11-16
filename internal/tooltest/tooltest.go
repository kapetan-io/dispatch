// Package tooltest builds the in-process stand-ins for the tools dispatch
// drives, sharing one journal so a test can assert the order of calls across
// tools. A new tool's fake gets a field on Tools, built from the same journal.
package tooltest

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/kapetan-io/dispatch/internal/harnesstest"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
	"github.com/kapetan-io/dispatch/internal/inboxtest"
	"github.com/kapetan-io/dispatch/internal/journal"
	"github.com/kapetan-io/dispatch/internal/launchertest"
	"github.com/kapetan-io/dispatch/internal/lineartest"
	"github.com/kapetan-io/dispatch/internal/pullrequesttest"
	"github.com/kapetan-io/dispatch/internal/treehousetest"
)

// Tools holds one fake per tool and the journal they all write to.
type Tools struct {
	Journal      *journal.Journal
	Treehouse    *treehousetest.Pool
	PullRequests *pullrequesttest.Repository
	Inbox        *inboxtest.Inbox
	Multiplexer  *herdrtest.Multiplexer
	Harness      *harnesstest.Harness
	Launcher     *launchertest.Launcher
	Linear       *lineartest.Workspace
	// Kill ends an action between two of its steps, as killing dispatch run would.
	Kill *KillSwitch
}

// ErrKilled is what every counted call fails with once the kill switch has thrown.
var ErrKilled = errors.New("killed by the test's kill switch")

// KillSwitch counts the calls that change something: a lease, a return, a workspace opened or
// closed, a worker started, a kickoff typed, and each commit to dispatch.db once Call is given to
// dispatch as its commit hook. From the call it is set to throw at, every counted call fails, so
// the action under test ends at its next step and what it tries to clean up fails too. The reads
// the loop runs beside an action are not counted, so a count is the same on every run.
type KillSwitch struct {
	mu    sync.Mutex
	count int
	at    int
}

// Call counts one call and fails it once the switch has thrown.
func (k *KillSwitch) Call() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.count++
	if k.at > 0 && k.count >= k.at {
		return ErrKilled
	}
	return nil
}

// Count is how many calls were counted since the switch was last set.
func (k *KillSwitch) Count() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.count
}

// ThrowAt starts the count again and fails the nth counted call and every one after it. Zero
// starts the count again and fails none.
func (k *KillSwitch) ThrowAt(n int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.count, k.at = 0, n
}

// New returns fresh fakes. Fakes that need cleanup register it on t.
func New(t testing.TB) *Tools {
	t.Helper()
	j := &journal.Journal{}
	tools := &Tools{Kill: &KillSwitch{}, Journal: j, Treehouse: treehousetest.New(j), PullRequests: pullrequesttest.New(j), Inbox: inboxtest.New(j), Multiplexer: herdrtest.New(j), Harness: harnesstest.New(), Launcher: launchertest.New(j), Linear: lineartest.New(j)}
	tools.Treehouse.BeforeWrite(tools.Kill.Call)
	tools.Multiplexer.BeforeWrite(tools.Kill.Call)
	return tools
}

// Calls lists the calls to a tool's operation, each as its arguments joined by
// "|".
func (t *Tools) Calls(tool, name string) []string {
	var found []string
	for _, call := range t.Journal.Calls() {
		if call.Tool == tool && call.Name == name {
			found = append(found, strings.Join(call.Args, "|"))
		}
	}
	return found
}
