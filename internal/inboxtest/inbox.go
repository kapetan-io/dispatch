// Package inboxtest is an in-process stand-in for the owner's GitHub notifications inbox. It
// holds GitHub-shaped threads and answers through the real parser, so a thread GitHub could
// not list is refused here as it is by the adapter.
package inboxtest

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/kapetan-io/dispatch/internal/inbox"
	"github.com/kapetan-io/dispatch/internal/journal"
)

// Inbox is an inbox.Inbox backed by memory.
type Inbox struct {
	journal *journal.Journal

	mu      sync.Mutex
	threads map[string]map[string]any
	order   []string
	fail    error
}

// New returns an Inbox with no threads that records its reads in j.
func New(j *journal.Journal) *Inbox {
	return &Inbox{journal: j, threads: map[string]map[string]any{}}
}

// Put sets the thread GitHub lists under id, replacing any earlier one. thread is the decoded JSON
// of one element of GitHub's notifications list; its "id" is set to id.
func (i *Inbox) Put(id string, thread map[string]any) {
	b, err := json.Marshal(thread)
	if err != nil {
		panic(err)
	}
	var copied map[string]any
	if err := json.Unmarshal(b, &copied); err != nil {
		panic(err)
	}
	copied["id"] = id
	i.mu.Lock()
	defer i.mu.Unlock()
	if _, ok := i.threads[id]; !ok {
		i.order = append(i.order, id)
	}
	i.threads[id] = copied
}

// Remove drops the thread, as when GitHub stops listing it.
func (i *Inbox) Remove(id string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.threads, id)
	for n, have := range i.order {
		if have == id {
			i.order = append(i.order[:n], i.order[n+1:]...)
			break
		}
	}
}

// FailRead makes every later Read return err, or none when err is nil.
func (i *Inbox) FailRead(err error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.fail = err
}

// Read answers with the threads in the order they were first put, not sorted, so the sort in
// inbox.Newest is exercised.
func (i *Inbox) Read(ctx context.Context) ([]inbox.Item, error) {
	i.journal.Record("inbox", "read")
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.fail != nil {
		return nil, i.fail
	}
	page := make([]map[string]any, 0, len(i.order))
	for _, id := range i.order {
		page = append(page, i.threads[id])
	}
	b, err := json.Marshal(page)
	if err != nil {
		return nil, err
	}
	return inbox.Parse(b)
}

var _ inbox.Inbox = (*Inbox)(nil)
