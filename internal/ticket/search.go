package ticket

import (
	"slices"
	"strings"
)

// Query selects local tickets by what their status does and what they carry. Every field set must match.
type Query struct {
	// Job selects the tickets whose status does this job.
	Job Job `json:"job,omitempty"`
	// Labels match ignoring case.
	Labels []string `json:"labels,omitempty"`
	// Attributes match exactly.
	Attributes map[string]string `json:"attributes,omitempty"`
	// Open leaves out the tickets whose status does the finished job.
	Open bool `json:"open,omitempty"`
}

// Found is a ticket a search returned, read-only.
type Found struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	Status     string            `json:"status"`
	Finished   bool              `json:"finished"`
	Labels     []string          `json:"labels"`
	Attributes map[string]string `json:"attributes"`
}

// Searcher answers a Query over the local tickets.
type Searcher interface {
	Search(Query) ([]Found, error)
}

// Match reports whether t, in a system with these statuses, satisfies q.
func (q Query) Match(statuses Statuses, t Ticket) bool {
	if q.Job != "" && !statuses.Has(t.Status, q.Job) {
		return false
	}
	if q.Open && statuses.Has(t.Status, Finished) {
		return false
	}
	for _, want := range q.Labels {
		if !slices.ContainsFunc(t.Labels, func(l Label) bool { return strings.EqualFold(l.Name, strings.TrimSpace(want)) }) {
			return false
		}
	}
	for k, v := range q.Attributes {
		if have, ok := t.Attributes[k]; !ok || have != v {
			return false
		}
	}
	return true
}

// FoundFrom is the read-only view of t that a search returns.
func FoundFrom(statuses Statuses, t Ticket) Found {
	out := Found{
		ID: t.Identifier, Title: t.Title, Status: t.Status, Finished: statuses.Has(t.Status, Finished),
		Labels: []string{}, Attributes: map[string]string{},
	}
	for _, l := range t.Labels {
		out.Labels = append(out.Labels, l.Name)
	}
	for k, v := range t.Attributes {
		out.Attributes[k] = v
	}
	return out
}
