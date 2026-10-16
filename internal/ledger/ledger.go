// Package ledger holds what dispatch did, one receipt per action, keyed by the
// action's once key. A rule reads it to ask "has this already happened".
package ledger

import (
	"encoding/json"
	"slices"
)

// Outcome is how an action ended. An empty outcome means attempted.
type Outcome string

const (
	Done    Outcome = "done"
	Refused Outcome = "refused"
	Failed  Outcome = "failed"
)

// Step is one part of an action and how it ended.
type Step struct {
	Name    string  `json:"name"`
	Outcome Outcome `json:"outcome,omitempty"`
}

// Once is a once key: a topic and the world values that must change before the
// action runs again. Items are strings, numbers, booleans or nil.
type Once []any

// Receipt is the ledger's entry for one action.
type Receipt struct {
	Kind    string  `json:"kind"`
	Subject string  `json:"subject"`
	Once    Once    `json:"once"`
	Rule    string  `json:"rule"`
	Outcome Outcome `json:"outcome,omitempty"`
	Detail  string  `json:"detail,omitempty"`
	// Started and Ended are milliseconds since the epoch.
	Started int64 `json:"started"`
	Ended   int64 `json:"ended,omitempty"`
	// TicketID is the Linear id of the ticket an escalation moved, so a lost
	// comment can be finished after the ticket has left the tracked set.
	TicketID string `json:"ticketID,omitempty"`
	// Rest holds the steps still to run after this one, when a rule returned an
	// ordered list, as JSON the loop wrote. They run once this one is Done,
	// even after it made the rule's condition false.
	Rest json.RawMessage `json:"rest,omitempty"`
	// Steps are the separate parts of an action that has several, such as an
	// escalation's move and comment, each with its own outcome.
	Steps []Step `json:"steps,omitempty"`
	// Resync is the resync the action ran in. A Failed receipt from the resync
	// that is running holds the action back until the next one.
	Resync int `json:"resync"`
	// Reset marks a Done receipt written for something that was never sent, so counts and waits skip it.
	Reset bool `json:"reset,omitempty"`
}

// Key is the receipt's identity: kind, subject and once key as canonical JSON.
func (r Receipt) Key() string { return Key(r.Kind, r.Subject, r.Once) }

// Key is the canonical form of an action's identity.
func Key(kind, subject string, once Once) string {
	b, _ := json.Marshal([]any{kind, subject, once})
	return string(b)
}

// Topic is the first item of the once key.
func (r Receipt) Topic() string {
	if len(r.Once) == 0 {
		return ""
	}
	s, _ := r.Once[0].(string)
	return s
}

// Filter narrows a receipts query. Empty fields match anything.
type Filter struct {
	Kinds []string
	Topic string
}

// Ledger is the receipts dispatch has written, oldest first.
type Ledger struct {
	Entries []Receipt
}

// Receipts returns the subject's receipts that match filter, newest first.
func (l *Ledger) Receipts(subject string, filter Filter) []Receipt {
	var out []Receipt
	for _, r := range l.Entries {
		if r.Subject != subject {
			continue
		}
		if len(filter.Kinds) > 0 && !slices.Contains(filter.Kinds, r.Kind) {
			continue
		}
		if filter.Topic != "" && r.Topic() != filter.Topic {
			continue
		}
		out = append(out, r)
	}
	slices.SortStableFunc(out, func(a, b Receipt) int {
		switch {
		case a.Started > b.Started:
			return -1
		case a.Started < b.Started:
			return 1
		}
		return 0
	})
	return out
}

// Done reports whether the subject has a receipt for once that landed, or was
// attempted and may have landed, whatever the action kind.
func (l *Ledger) Done(subject string, once Once) bool {
	want, _ := json.Marshal(once)
	for _, r := range l.Entries {
		if r.Subject != subject || (r.Outcome != Done && r.Outcome != "") {
			continue
		}
		if got, _ := json.Marshal(r.Once); string(got) == string(want) {
			return true
		}
	}
	return false
}

// Add appends a receipt.
func (l *Ledger) Add(r Receipt) { l.Entries = append(l.Entries, r) }

// Upsert replaces the attempt with r's key that started when r did, which is how
// an attempt gains its outcome, and appends r when there is none. A receipt that
// already has an outcome is never replaced, so a retry is a new receipt.
func (l *Ledger) Upsert(r Receipt) {
	for i := range l.Entries {
		if l.Entries[i].Outcome == "" && l.Entries[i].Started == r.Started && l.Entries[i].Key() == r.Key() {
			l.Entries[i] = r
			return
		}
	}
	l.Entries = append(l.Entries, r)
}

// Unfinished reports whether the subject has an escalation whose move landed but
// whose comment did not, which retry-escalation-comment still has to post.
func (l *Ledger) Unfinished(subject string) bool {
	return slices.ContainsFunc(l.Entries, func(r Receipt) bool {
		if r.Subject != subject || r.Kind != "escalate" || len(r.Steps) != 2 || r.Steps[0].Outcome != Done || r.Steps[1].Outcome == Done {
			return false
		}
		// retry-escalation-comment records its success as a comment receipt of its own.
		retry := Key("comment", subject, Once{"escalation", r.Started})
		return !slices.ContainsFunc(l.Entries, func(c Receipt) bool { return c.Outcome == Done && c.Key() == retry })
	})
}

// Keep drops the receipts of subjects that keep rejects.
func (l *Ledger) Keep(keep func(subject string) bool) {
	l.Entries = slices.DeleteFunc(l.Entries, func(r Receipt) bool { return !keep(r.Subject) })
}
