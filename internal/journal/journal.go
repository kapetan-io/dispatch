// Package journal records the calls dispatch makes to its tools, in order, so a
// test can assert what happened across tools.
package journal

import "sync"

// Call is one call to a tool.
type Call struct {
	Tool string
	Name string
	Args []string
}

// Journal is an ordered, concurrency-safe list of calls.
type Journal struct {
	mu    sync.Mutex
	calls []Call
}

// Record appends a call to tool's operation name.
func (j *Journal) Record(tool, name string, args ...string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.calls = append(j.calls, Call{Tool: tool, Name: name, Args: args})
}

// Calls returns every call recorded so far, in order.
func (j *Journal) Calls() []Call {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]Call(nil), j.calls...)
}
