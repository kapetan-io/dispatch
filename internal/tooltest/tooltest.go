// Package tooltest builds the in-process stand-ins for the tools dispatch
// drives, sharing one journal so a test can assert the order of calls across
// tools. A new tool's fake gets a field on Tools, built from the same journal.
package tooltest

import (
	"strings"
	"testing"

	"github.com/kapetan-io/dispatch/internal/journal"
	"github.com/kapetan-io/dispatch/internal/treehousetest"
)

// Tools holds one fake per tool and the journal they all write to.
type Tools struct {
	Journal   *journal.Journal
	Treehouse *treehousetest.Pool
}

// New returns fresh fakes. Fakes that need cleanup register it on t.
func New(t testing.TB) *Tools {
	t.Helper()
	j := &journal.Journal{}
	return &Tools{Journal: j, Treehouse: treehousetest.New(j)}
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
