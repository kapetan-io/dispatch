// Package reconcile decides what a tick should do, from what Linear says
// should happen and what herdr and treehouse show. Plan has no side effects,
// so a dry run and a real tick act on the same decisions.
package reconcile

import (
	"fmt"

	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/worker"
)

// Kind names what an action does.
type Kind string

const (
	// Launch starts a worker for a ticket with none.
	Launch Kind = "launch"
	// Wait leaves a ticket for a later tick, because the worker cap is full.
	Wait Kind = "wait"
	// Skip leaves a ticket that this dispatcher never launches.
	Skip Kind = "skip"
	// Escalate moves a ticket to Needs Human with Reason as the comment.
	Escalate Kind = "escalate"
)

// Action is one decision about one ticket.
type Action struct {
	Kind    Kind
	Ticket  linear.Ticket
	RepoDir string
	Reason  string
}

func (a Action) String() string {
	s := fmt.Sprintf("%s %s", a.Kind, a.Ticket.Identifier)
	if a.Reason != "" {
		s += ": " + a.Reason
	}
	return s
}

// Input is everything Plan reads.
type Input struct {
	Tickets []linear.Ticket
	// Workers maps a ticket identifier to what the lookup found.
	Workers        map[string]worker.Worker
	InProgress     string
	MaxInProgress  int
	RepoLabelGroup string
	// Repos maps a repo label to its local clone.
	Repos map[string]string
}

// designLabels mark tickets whose sessions this piece of the dispatcher does
// not start: the grill's feature ticket and the review ticket.
var designLabels = []string{"design", "design-review"}

// Plan returns the actions for one tick, launches last, in ticket order.
func Plan(in Input) []Action {
	running := 0
	for _, t := range in.Tickets {
		if t.Status == in.InProgress && in.Workers[t.Identifier].State != worker.None {
			running++
		}
	}
	var actions, launches []Action
	for _, t := range in.Tickets {
		if t.Status != in.InProgress {
			continue
		}
		if in.Workers[t.Identifier].State != worker.None {
			continue
		}
		if t.HasChildren {
			actions = append(actions, Action{Kind: Skip, Ticket: t, Reason: "has sub-issues, and a parent never gets a worker"})
			continue
		}
		if l, ok := designLabel(t); ok {
			actions = append(actions, Action{Kind: Skip, Ticket: t, Reason: fmt.Sprintf("carries %q, which this dispatcher does not launch yet", l)})
			continue
		}
		repo, ok := t.LabelIn(in.RepoLabelGroup)
		if !ok {
			actions = append(actions, Action{Kind: Escalate, Ticket: t, Reason: fmt.Sprintf("it has no %q label, so the dispatcher can't tell which repo to build in. Add one and move it back to In Progress.", in.RepoLabelGroup)})
			continue
		}
		dir, ok := in.Repos[repo]
		if !ok {
			actions = append(actions, Action{Kind: Escalate, Ticket: t, Reason: fmt.Sprintf("its repo %q is not in the dispatcher's config. Add it under [repos] and move the ticket back to In Progress.", repo)})
			continue
		}
		if running >= in.MaxInProgress {
			actions = append(actions, Action{Kind: Wait, Ticket: t, Reason: fmt.Sprintf("%d of %d workers already running", running, in.MaxInProgress)})
			continue
		}
		running++
		launches = append(launches, Action{Kind: Launch, Ticket: t, RepoDir: dir})
	}
	return append(actions, launches...)
}

func designLabel(t linear.Ticket) (string, bool) {
	for _, l := range designLabels {
		if t.HasLabel(l) {
			return l, true
		}
	}
	return "", false
}
