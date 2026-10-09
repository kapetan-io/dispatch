// Package reconcile decides what a tick should do, from what Linear says
// should happen and what herdr and treehouse show. Plan has no side effects,
// so a dry run and a real tick act on the same decisions.
package reconcile

import (
	"fmt"

	"github.com/kapetan-io/dispatch/internal/cache"
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
	// Nudge types Text into the idle worker in PaneID. Reason names which nudge.
	Nudge Kind = "nudge"
	// Notify raises a macOS notification with Text as the message.
	Notify Kind = "notify"
)

// Reasons are the labels `dispatch tick --dry-run` prints. The executor
// switches on ReasonEnteredNeedsHuman and ReasonResume to decide how to retry
// a failed notification or nudge.
const (
	ReasonEnteredNeedsHuman = "entered Needs Human"
	ReasonBlocked           = "worker is blocked on a question"
	ReasonStall             = "stall"
	ReasonResume            = "resume"
)

// NeedsHumanText is the notification message for a ticket in Needs Human.
func NeedsHumanText(t linear.Ticket) string {
	return fmt.Sprintf("%s is in Needs Human: %s", t.Identifier, t.Title)
}

// Nudge texts are fixed because the build-worker skill and the tests rely on
// the exact wording. The worker treats them as coming from the user.
const (
	StallNudge  = "%s is still In Progress. Re-read the ticket and its latest comments, then keep going. If the PR is ready, move it to In Review. If you need a decision from me, move it to Needs Human and comment what you need."
	ResumeNudge = "I answered on %s. Read the latest comments and continue."
)

// Action is one decision about one ticket.
type Action struct {
	Kind    Kind
	Ticket  linear.Ticket
	RepoDir string
	Reason  string
	// PaneID is set for a Nudge only. Text is what a Nudge types, or what a
	// Notify shows.
	PaneID string
	Text   string
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
	Workers       map[string]worker.Worker
	InProgress    string
	InReview      string
	NeedsHuman    string
	MaxInProgress int
	NudgeLimit    int
	// RenotifyTicks is how many ticks pass between notifications for a worker
	// that stays blocked.
	RenotifyTicks int
	// Cache is last tick's state. Plan only reads it.
	Cache          cache.Cache
	RepoLabelGroup string
	// Repos maps a repo label to its local clone.
	Repos map[string]string
	// UnreadRepos are repo labels whose treehouse pool could not be read this
	// tick. Their tickets' workers are unknown, not absent.
	UnreadRepos map[string]bool
}

// designLabels mark tickets whose sessions this piece of the dispatcher does
// not start: the grill's feature ticket and the review ticket.
var designLabels = []string{"design", "design-review"}

// Plan returns the actions for one tick, launches last, in ticket order.
func Plan(in Input) []Action {
	// A ticket in an unread repo counts as running, so the cap errs low rather
	// than launching past a live worker nobody could see.
	running := 0
	for _, t := range in.Tickets {
		if t.Status == in.InProgress && (in.Workers[t.Identifier].State != worker.None || in.unread(t)) {
			running++
		}
	}
	var actions, launches []Action
	for _, t := range in.Tickets {
		if a, ok := in.watch(t); ok {
			actions = append(actions, a)
			continue
		}
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
		// Every decision below reads labels, and a missing design label would
		// launch a design ticket as a build.
		if t.LabelsTruncated {
			actions = append(actions, Action{Kind: Escalate, Ticket: t, Reason: "it has more labels than the dispatcher reads, so it can't reliably tell its repo or whether it is a design ticket. Remove some labels and move it back to In Progress."})
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
		if in.UnreadRepos[repo] {
			actions = append(actions, Action{Kind: Wait, Ticket: t, Reason: fmt.Sprintf("the %s pool could not be read this tick", repo)})
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

// watch decides what a ticket needs from the user or its live worker: a
// notification for a ticket entering Needs Human or a worker asking a question,
// and a nudge for an idle In Progress worker. It skips design and review
// tickets, which the grill and the review own, and tickets whose labels were
// cut short or that have children, because it can't tell whether they are
// design tickets.
func (in Input) watch(t linear.Ticket) (Action, bool) {
	if _, design := designLabel(t); design || t.LabelsTruncated || t.HasChildren {
		return Action{}, false
	}
	prev := in.Cache[t.Identifier]
	if t.Status == in.NeedsHuman {
		if prev.LastStatus == in.NeedsHuman {
			return Action{}, false
		}
		return Action{Kind: Notify, Ticket: t, Reason: ReasonEnteredNeedsHuman, Text: NeedsHumanText(t)}, true
	}
	if t.Status != in.InProgress && t.Status != in.InReview {
		return Action{}, false
	}
	w := in.Workers[t.Identifier]
	switch w.State {
	case worker.Blocked:
		if in.RenotifyTicks > 0 && prev.BlockedTicks%in.RenotifyTicks != 0 {
			return Action{}, false
		}
		return Action{Kind: Notify, Ticket: t, Reason: ReasonBlocked, Text: fmt.Sprintf("%s is waiting on your answer in its herdr workspace", t.Identifier)}, true
	case worker.Idle:
		if t.Status != in.InProgress {
			return Action{}, false
		}
		nudges := prev.Nudges
		if prev.LastStatus != t.Status {
			nudges = 0
		}
		if nudges >= in.NudgeLimit {
			return Action{Kind: Escalate, Ticket: t, Reason: fmt.Sprintf("its worker stayed idle through %d nudges without the ticket changing status. Look at its herdr workspace.", nudges)}, true
		}
		if prev.LastStatus == in.NeedsHuman {
			return Action{Kind: Nudge, Ticket: t, Reason: ReasonResume, PaneID: w.PaneID, Text: fmt.Sprintf(ResumeNudge, t.Identifier)}, true
		}
		return Action{Kind: Nudge, Ticket: t, Reason: ReasonStall, PaneID: w.PaneID, Text: fmt.Sprintf(StallNudge, t.Identifier)}, true
	}
	return Action{}, false
}

func (in Input) unread(t linear.Ticket) bool {
	repo, ok := t.LabelIn(in.RepoLabelGroup)
	return ok && in.UnreadRepos[repo]
}

func designLabel(t linear.Ticket) (string, bool) {
	for _, l := range designLabels {
		if t.HasLabel(l) {
			return l, true
		}
	}
	return "", false
}
