// Package reconcile decides what a tick should do, from what Linear says
// should happen and what herdr and treehouse show. Plan has no side effects,
// so a dry run and a real tick act on the same decisions.
package reconcile

import (
	"fmt"

	"github.com/kapetan-io/dispatch/internal/cache"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/pullrequest"
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
	// Teardown closes the workspace on a finished ticket's tree and returns the
	// tree to the pool.
	Teardown Kind = "teardown"
	// Stop closes the workspace of a paused ticket's live worker and keeps its tree.
	Stop Kind = "stop"
	// Keep closes the workspace of a finished ticket's live worker and leaves
	// its tree leased, because returning it would destroy work. A non-empty Text
	// is the one comment that says why.
	Keep Kind = "keep"
)

// Reasons are the labels `dispatch tick --dry-run` prints. The executor
// switches on ReasonEnteredNeedsHuman and ReasonResume to decide how to retry
// a failed notification or nudge.
const (
	ReasonEnteredNeedsHuman = "entered Needs Human"
	ReasonBlocked           = "worker is blocked on a question"
	ReasonStall             = "stall"
	ReasonResume            = "resume"
	ReasonPR                = "PR needs work"
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
	PRNudge     = "PR %s for %s has %s. Handle it as your build skill's step 7 says, and push."
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
	// WorkspaceID and TreePath name what a Teardown, Keep or Stop acts on.
	// WorkspaceID is empty when a Keep or Stop has no live worker to stop, so a
	// workspace someone opened by hand is left open.
	WorkspaceID string
	TreePath    string
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
	// PRs maps an In Review ticket identifier to its open PR. Tickets whose PR
	// was not read, or that have none, are absent.
	PRs map[string]pullrequest.PR
	// Cache is last tick's state. Plan only reads it.
	Cache          cache.Cache
	RepoLabelGroup string
	// Repos maps a repo label to its local clone.
	Repos map[string]string
	// UnreadRepos are repo labels whose treehouse pool could not be read this
	// tick. Their tickets' workers are unknown, not absent.
	UnreadRepos map[string]bool
	// Done and Paused are the statuses of a finished and a paused ticket.
	Done   []string
	Paused []string
	// LeaseHolders holds the managed tickets that hold a lease and are not in
	// Tickets, which is how finished and paused tickets are found.
	LeaseHolders []linear.Ticket
	// Leases maps a lease holder to a worker for each tree it holds.
	Leases map[string][]worker.Worker
	// Unfinished maps a tree path to what returning it would destroy, empty for
	// a clean, pushed tree. A path in neither map was not inspected and is never
	// returned. CheckErrors holds the trees that could not be read. Both cover
	// only the trees of tickets for which Retires is true.
	Unfinished  map[string]string
	CheckErrors map[string]error
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
	actions := in.retire()
	var launches []Action
	for _, t := range in.Tickets {
		if a, ok := in.watch(t); ok {
			actions = append(actions, a)
			continue
		}
		pr, relaunch := in.prRelaunch(t)
		if t.Status != in.InProgress && !relaunch {
			continue
		}
		if in.Workers[t.Identifier].State != worker.None {
			continue
		}
		// A relaunched worker that never pushes would be relaunched every tick,
		// so relaunches are counted against the same limit as PR nudges.
		if relaunch {
			if n := in.Cache[t.Identifier].RelaunchesAt(t.Status, pr.Head); n >= in.NudgeLimit {
				actions = append(actions, Action{Kind: Escalate, Ticket: t, Reason: fmt.Sprintf("its worker was relaunched %d times for %s without pushing. Look at its herdr workspace.", n, pr.URL)})
				continue
			}
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
		// In Review workers don't count against the cap, so a relaunch to
		// handle a PR is never held back by it.
		if relaunch {
			launches = append(launches, Action{Kind: Launch, Ticket: t, RepoDir: dir, Reason: ReasonPR + ": " + pr.Problem()})
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
// and a nudge for an idle In Progress worker. It skips the grill's feature
// ticket, which carries "design", and tickets whose labels were cut short or
// that have children, because it can't tell whether they are design tickets.
// A review ticket still notifies, since its hand-off is a move to Needs Human,
// but it is never nudged.
func (in Input) watch(t linear.Ticket) (Action, bool) {
	if t.LabelsTruncated || t.HasChildren || t.HasLabel("design") {
		return Action{}, false
	}
	prev := in.Cache[t.Identifier]
	if t.Status == in.NeedsHuman {
		if prev.LastStatus == in.NeedsHuman && !prev.NotifyPending {
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
		if t.Status == in.InReview {
			return in.prNudge(t, w, prev)
		}
		// A design-review ticket's worker is never nudged.
		if t.HasLabel("design-review") {
			return Action{}, false
		}
		nudges := prev.Nudges
		if prev.LastStatus != t.Status {
			nudges = 0
		}
		if nudges >= in.NudgeLimit {
			return Action{Kind: Escalate, Ticket: t, Reason: fmt.Sprintf("its worker stayed idle through %d nudges without the ticket changing status. Look at its herdr workspace.", nudges)}, true
		}
		if prev.ResumePending || prev.LastStatus == in.NeedsHuman {
			return Action{Kind: Nudge, Ticket: t, Reason: ReasonResume, PaneID: w.PaneID, Text: fmt.Sprintf(ResumeNudge, t.Identifier)}, true
		}
		return Action{Kind: Nudge, Ticket: t, Reason: ReasonStall, PaneID: w.PaneID, Text: fmt.Sprintf(StallNudge, t.Identifier)}, true
	}
	return Action{}, false
}

// ReadsPR reports whether Plan will use this In Review ticket's PR. Working and
// blocked workers are skipped so their PRs cost no call, and so are tickets
// whose labels or children make Plan unsure they are build tickets. PRs holds
// only tickets for which this is true.
func (in Input) ReadsPR(t linear.Ticket) bool {
	if t.Status != in.InReview || t.LabelsTruncated || t.HasChildren || t.HasLabel("design") || t.HasLabel("design-review") {
		return false
	}
	state := in.Workers[t.Identifier].State
	return state == worker.None || state == worker.Idle
}

// prRelaunch returns the PR of an In Review ticket that has no worker and whose
// PR needs work, which Plan relaunches a worker for.
func (in Input) prRelaunch(t linear.Ticket) (pullrequest.PR, bool) {
	pr, ok := in.PRs[t.Identifier]
	if !ok || !pr.NeedsWork() {
		return pullrequest.PR{}, false
	}
	return pr, in.Workers[t.Identifier].State == worker.None
}

// prNudge handles an idle worker on an In Review ticket. A push resets the
// count, so a review that arrives days later starts a fresh one.
func (in Input) prNudge(t linear.Ticket, w worker.Worker, prev cache.Entry) (Action, bool) {
	pr, ok := in.PRs[t.Identifier]
	if !ok || !pr.NeedsWork() {
		return Action{}, false
	}
	nudges := prev.NudgesAt(t.Status, pr.Head)
	if nudges >= in.NudgeLimit {
		return Action{Kind: Escalate, Ticket: t, Reason: fmt.Sprintf("its worker stayed idle through %d nudges about %s without pushing. Look at its herdr workspace.", nudges, pr.URL)}, true
	}
	return Action{Kind: Nudge, Ticket: t, Reason: ReasonPR + ": " + pr.Problem(), PaneID: w.PaneID, Text: fmt.Sprintf(PRNudge, pr.URL, t.Identifier, pr.Problem())}, true
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
