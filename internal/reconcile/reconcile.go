// Package reconcile decides what a tick should do, from what Linear says
// should happen and what herdr and treehouse show. Plan has no side effects,
// so a dry run and a real tick act on the same decisions.
package reconcile

import (
	"fmt"
	"time"

	"github.com/kapetan-io/dispatch/internal/cache"
	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/pullrequest"
	"github.com/kapetan-io/dispatch/internal/worker"
	"github.com/kapetan-io/dispatch/internal/world"
)

// Kind names what an action does.
type Kind string

const (
	// Launch starts a worker for a ticket with none.
	Launch Kind = "launch"
	// Wait leaves a ticket for a later tick, because the worker cap is full or
	// its repo's pool could not be read.
	Wait Kind = "wait"
	// Skip leaves a ticket that this dispatcher never launches.
	Skip Kind = "skip"
	// Escalate moves a ticket to Needs Human with Reason as the comment.
	Escalate Kind = "escalate"
	// Nudge types Text into the ticket's idle worker. Reason names which nudge.
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
	// is the one comment that says why. A Keep with no Text and no
	// WorkspaceID only reports the held tree.
	Keep Kind = "keep"
	// Promote moves a Todo child of an In Progress parent to In Progress, which
	// is what starts it.
	Promote Kind = "promote"
	// Merge merges the PR at PRURL, because its repo is in auto_merge and a
	// review approves its head. Text is the notification sent once it merges.
	Merge Kind = "merge"
	// Close moves a parent ticket to its close_parent status, because every one
	// of its children is finished. It never touches a workspace.
	Close Kind = "close"
)

// Reasons are the labels `dispatch tick --dry-run` prints. The executor
// switches on them to decide what a landed nudge or notification records and
// how to retry a failed one.
const (
	ReasonEnteredNeedsHuman = "entered Needs Human"
	ReasonBlocked           = "worker is blocked on a question"
	ReasonStall             = "stall"
	ReasonResume            = "resume"
	ReasonPR                = "PR needs work"
	ReasonActivity          = "new PR activity"
	ReasonReady             = "PR ready for review"
	ReasonDraftReady        = "draft ready for review"
	ReasonReviewsHandled    = "reviews handled"
	ReasonMerge             = "PR approved"
	ReasonUnresponsive      = "worker hasn't responded to its nudge in an hour"
)

// UnresponsiveAfter is how long a worker may ignore a stall nudge, without
// stopping again, before the user is told.
const UnresponsiveAfter = time.Hour

// StallNotifyEvery is how many stall nudges without a status change pass
// between notifications to the user.
const StallNotifyEvery = 6

// NeedsHumanText is the notification message for a ticket in Needs Human.
func NeedsHumanText(t linear.Ticket) string {
	return fmt.Sprintf("%s is in Needs Human: %s", t.Identifier, t.Title)
}

// ReadyText is the notification message for a ticket whose PR is ready for review.
func ReadyText(t linear.Ticket, pr pullrequest.PullRequest) string {
	return fmt.Sprintf("%s is ready for review: %s", t.Identifier, pr.URL)
}

// DraftReadyText is the notification message for a draft PR that is ready for
// the user's review.
func DraftReadyText(t linear.Ticket, pr pullrequest.PullRequest) string {
	return fmt.Sprintf("%s's draft is ready for your review: %s", t.Identifier, pr.URL)
}

// ReviewsHandledText is the notification message for a PR the user opened
// from a draft once the other reviewers' comments are handled.
func ReviewsHandledText(t linear.Ticket, pr pullrequest.PullRequest) string {
	return fmt.Sprintf("%s's reviews are handled: %s", t.Identifier, pr.URL)
}

// MergedText is the notification message for a PR dispatch merged.
func MergedText(t linear.Ticket, pr pullrequest.PullRequest) string {
	return fmt.Sprintf("%s was approved and merged: %s", t.Identifier, pr.URL)
}

// Nudge texts are fixed because the build-worker skill and the tests rely on
// the exact wording. The worker treats them as coming from the user.
const (
	StallNudge  = "%s is still In Progress. Re-read the ticket and its latest comments, then keep going. If the PR is ready, move it to In Review. If you need a decision from me, move it to Needs Human and comment what you need."
	ResumeNudge = "I answered on %s. Read the latest comments and continue."
	PRNudge     = "PR %s for %s has %s. Handle it as your build skill's step 7 says, and push."
	// ActivityNudge never counts toward the stall or PR limits. Workers post as
	// the user, so a worker's own reply draws one too, and the last sentence
	// ends that chain.
	ActivityNudge = "PR %s for %s has new activity, %s. Read it and handle it as your build skill's step 7 says. If it asks for nothing, say nothing."
)

// Action is one decision about one ticket.
type Action struct {
	Kind    Kind
	Ticket  linear.Ticket
	RepoDir string
	Reason  string
	// Text is what a Nudge types, or what a Notify shows.
	Text string
	// Head is the PR head a ready-for-review Notify or a Merge is about, and
	// PRURL the PR a Merge merges.
	Head  string
	PRURL string
	// ActivityAt is the PR activity time an activity Nudge or a Launch puts to
	// the worker, which the executor records as seen.
	ActivityAt string
	// AnsweredStop is the Stop-hook time a stall or resume Nudge answers.
	AnsweredStop time.Time
	// AfterNudge marks a Notify that only makes sense once the Nudge before it
	// landed.
	AfterNudge bool
	// ThreadOnly marks a Launch made only for a new review thread comment, which
	// does not count toward the relaunch limit.
	ThreadOnly bool
	// WorkspaceID and TreePath name what a Teardown, Keep or Stop acts on.
	// WorkspaceID is empty when a Keep or Stop has no live worker to stop, so a
	// workspace someone opened by hand is left open. A Teardown always closes
	// its workspace, since a worker whose Claude exited leaves one behind.
	WorkspaceID string
	TreePath    string
	// Note is the comment a Wait posts once on a ticket held for its blockers.
	Note string
	// Untracked lists the files a Teardown deletes from its tree first.
	Untracked []string
	// Skill names what a Launch runs. The zero value is a build.
	Skill Skill
}

// Skill is the dispatch skill a launched worker runs. Design and review
// workers run on the design model with their own kickoff, and are never nudged.
type Skill int

const (
	Build Skill = iota
	Design
	DesignReview
)

// SkillOf maps a ticket's labels to the skill its worker runs. A ticket
// carrying both design labels is a review.
func SkillOf(t linear.Ticket) Skill {
	switch {
	case t.HasLabel("design-review"):
		return DesignReview
	case t.HasLabel("design"):
		return Design
	}
	return Build
}

// Name is the skill's name in a kickoff, or empty for a build, which keeps the
// launcher's default kickoff.
func (s Skill) Name() string {
	switch s {
	case Design:
		return "design"
	case DesignReview:
		return "design-review"
	}
	return ""
}

func (a Action) String() string {
	s := fmt.Sprintf("%s %s", a.Kind, a.Ticket.Identifier)
	if a.Reason != "" {
		s += ": " + a.Reason
	}
	return s
}

// Input is everything Plan reads: what the world says, and the settings and
// the cache that say what to make of it.
type Input struct {
	world.World
	InProgress    string
	InReview      string
	NeedsHuman    string
	MaxInProgress int
	NudgeLimit    int
	// Now is the time of this tick.
	Now time.Time
	// RenotifyTicks is how many ticks pass between notifications for a worker
	// that stays blocked.
	RenotifyTicks int
	// Cache is last tick's state. Plan only reads it.
	Cache          cache.Cache
	RepoLabelGroup string
	// Repos maps a repo label to its settings.
	Repos map[string]config.Repo
	// Done and Paused are the statuses of a finished and a paused ticket.
	Done   []string
	Paused []string
	// Todo is the status of a child waiting for promotion, and AILabel the label
	// a child needs to be promoted.
	Todo    string
	AILabel string
	// BlockerDone is the status a blocker must reach before its child starts.
	BlockerDone string
	// Teams are the team keys the dispatcher manages.
	Teams []string
}

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
	actions := append(in.retire(), in.planParents()...)
	var launches []Action
	for _, t := range in.Tickets {
		if as := in.watch(t); len(as) > 0 {
			actions = append(actions, as...)
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
		// A relaunch for a thread comment is not counted: the worker answers a
		// comment without pushing, so no push would ever reset the count.
		if relaunch && pr.NeedsWork() {
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
		// launch that ticket as a build.
		if t.LabelsTruncated {
			actions = append(actions, Action{Kind: Escalate, Ticket: t, Reason: "it has more labels than the dispatcher reads, so it can't reliably tell its repo or whether it is a design ticket. Remove some labels and move it back to In Progress."})
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
		settings, ok := in.Repos[repo]
		dir := settings.Path
		if !ok {
			actions = append(actions, Action{Kind: Escalate, Ticket: t, Reason: fmt.Sprintf("its repo %q is not in the dispatcher's config. Add it under [repos] and move the ticket back to In Progress.", repo)})
			continue
		}
		// In Review workers don't count against the cap, so a relaunch to
		// handle a PR is never held back by it.
		if relaunch {
			launch := Action{Kind: Launch, Ticket: t, RepoDir: dir, Reason: ReasonPR + ": " + pr.Problem()}
			// The kickoff reads the whole PR, so a thread comment owed to the
			// worker is handled once it is up, whatever else prompted the launch.
			if in.threadOwed(t, pr) {
				launch.ActivityAt = pr.ActivityAt
			}
			if !pr.NeedsWork() {
				launch.Reason, launch.ThreadOnly = ReasonActivity+": "+pr.ActivitySummary, true
			}
			launches = append(launches, launch)
			continue
		}
		if a, held := in.blockerHold(t); held {
			actions = append(actions, a)
			continue
		}
		if running >= in.MaxInProgress {
			actions = append(actions, Action{Kind: Wait, Ticket: t, Reason: fmt.Sprintf("%d of %d workers already running", running, in.MaxInProgress)})
			continue
		}
		running++
		launches = append(launches, Action{Kind: Launch, Ticket: t, RepoDir: dir, Skill: SkillOf(t)})
	}
	return append(actions, launches...)
}

// watch decides what a ticket needs from the user or its live worker: a
// notification for a ticket entering Needs Human or a worker asking a question,
// and a nudge for an idle In Progress worker. It skips the design skill's feature
// ticket, which carries "design" and whose session waits on the user for as
// long as the design takes, and tickets whose labels were cut short,
// because it can't tell whether they are design tickets. A review ticket still
// notifies, since its hand-off is a move to Needs Human, but it is never nudged.
// A parent notifies on Needs Human like any ticket, and never gets a worker.
func (in Input) watch(t linear.Ticket) []Action {
	if t.LabelsTruncated || SkillOf(t) == Design {
		return nil
	}
	prev := in.Cache[t.Identifier]
	if t.Status == in.NeedsHuman {
		if prev.LastStatus == in.NeedsHuman && !prev.NotifyPending {
			return nil
		}
		return []Action{{Kind: Notify, Ticket: t, Reason: ReasonEnteredNeedsHuman, Text: NeedsHumanText(t)}}
	}
	if t.HasChildren || (t.Status != in.InProgress && t.Status != in.InReview) {
		return nil
	}
	w := in.Workers[t.Identifier]
	switch w.State {
	case worker.Blocked:
		if in.RenotifyTicks > 0 && prev.BlockedTicks%in.RenotifyTicks != 0 {
			return nil
		}
		return []Action{{Kind: Notify, Ticket: t, Reason: ReasonBlocked, Text: fmt.Sprintf("%s is waiting on your answer in its herdr workspace", t.Identifier)}}
	case worker.Idle:
		if t.Status == in.InReview {
			if a, ok := in.inReviewIdle(t, w, prev); ok {
				return []Action{a}
			}
			return nil
		}
		// A design-review ticket's worker is never nudged.
		if t.HasLabel("design-review") {
			return nil
		}
		return in.stalled(t, w, prev)
	}
	return nil
}

// stalled decides what an idle In Progress worker needs. It is never
// escalated, because a worker may run for hours. Its latest Stop says whether
// it is waiting on its own background work, and each Stop gets at most one
// nudge, so a worker that keeps stopping is reminded once per stop.
func (in Input) stalled(t linear.Ticket, w worker.Worker, prev cache.Entry) []Action {
	stop, busy := in.lastStop(t, prev)
	nudged := prev.LastStatus == t.Status && prev.NudgedStop.Equal(stop)
	if prev.ResumePending || prev.LastStatus == in.NeedsHuman {
		return []Action{{Kind: Nudge, Ticket: t, Reason: ReasonResume, Text: fmt.Sprintf(ResumeNudge, t.Identifier), AnsweredStop: stop}}
	}
	var as []Action
	if prev.LastStatus == t.Status && prev.StallNotifyPending {
		as = append(as, stallCount(t, prev.StallNudges))
	}
	if busy {
		return as
	}
	if nudged {
		if !prev.NudgedAt.IsZero() && !prev.NudgeNotified && in.Now.Sub(prev.NudgedAt) >= UnresponsiveAfter {
			as = append(as, Action{Kind: Notify, Ticket: t, Reason: ReasonUnresponsive, Text: fmt.Sprintf("%s: the worker hasn't responded to its nudge in an hour", t.Identifier)})
		}
		return as
	}
	as = append(as, Action{Kind: Nudge, Ticket: t, Reason: ReasonStall, Text: fmt.Sprintf(StallNudge, t.Identifier), AnsweredStop: stop})
	if n := prev.StallNudges + 1; n%StallNotifyEvery == 0 {
		as = append(as, stallCount(t, n))
	}
	return as
}

func stallCount(t linear.Ticket, n int) Action {
	return Action{Kind: Notify, Ticket: t, AfterNudge: true,
		Reason: fmt.Sprintf("%d stall nudges with no status change", n),
		Text:   fmt.Sprintf("%s has had %d stall nudges with no status change", t.Identifier, n)}
}

// lastStop returns when the worker last ended a turn, and whether that Stop
// still counts as waiting on running tasks. A worker with no usable Stop
// record is treated as stopped when the dispatcher first saw it idle.
func (in Input) lastStop(t linear.Ticket, prev cache.Entry) (time.Time, bool) {
	if rec, ok := in.Stops[t.Identifier]; ok && rec.Reported {
		return rec.At, rec.Busy(in.Now)
	}
	if prev.LastStatus == t.Status && !prev.IdleSince.IsZero() {
		return prev.IdleSince, false
	}
	return in.Now, false
}

// ReadsPR reports whether Plan will use this In Review ticket's PR.
func (in Input) ReadsPR(t linear.Ticket) bool {
	return in.scope().ReadsPR(t, in.Workers[t.Identifier].State)
}

// scope is the part of the settings the world was read with.
func (in Input) scope() world.Scope {
	return world.Scope{
		Statuses: config.Statuses{InProgress: in.InProgress, InReview: in.InReview, Finished: in.Done},
		Repos:    in.Repos, RepoLabelGroup: in.RepoLabelGroup,
	}
}

// prRelaunch returns the PR of an In Review ticket that has no worker and whose
// PR needs work or has an open review thread with an unseen comment, which Plan
// relaunches a worker for.
func (in Input) prRelaunch(t linear.Ticket) (pullrequest.PullRequest, bool) {
	pr, ok := in.PRs[t.Identifier]
	if !ok || !pr.NeedsWork() && !in.threadOwed(t, pr) {
		return pullrequest.PullRequest{}, false
	}
	return pr, in.Workers[t.Identifier].State == worker.None
}

// threadOwed reports whether an open review thread has a comment dispatch has
// not yet put to the worker. A thread is not a PR problem, so without this a
// PR whose worker is gone would never get one back to read it.
func (in Input) threadOwed(t linear.Ticket, pr pullrequest.PullRequest) bool {
	seen := in.Cache[t.Identifier].ActivityBaseline(t.Status, in.InReview)
	return seen != "" && pr.ThreadAt > seen
}

// inReviewIdle handles an idle worker on an In Review ticket. New PR activity
// is nudged first, so a merge or ready notice never outruns a review nobody has
// read. A ready PR
// notifies once per head, or merges when its repo is in auto_merge and a review
// approves its head. A draft is never merged, and in an open_pr_as = "draft"
// repo each phase notifies once per head on its own. A PR that needs work gets a nudge. A push resets the
// count, so a review that arrives days later starts a fresh one.
func (in Input) inReviewIdle(t linear.Ticket, w worker.Worker, prev cache.Entry) (Action, bool) {
	pr, ok := in.PRs[t.Identifier]
	// A PR that needs work gets the PR nudge instead, and activity waits until
	// that is cleared so the worker is never told it was handled when it wasn't.
	if seen := prev.ActivityBaseline(t.Status, in.InReview); ok && seen != "" && pr.ActivityAt > seen && !pr.NeedsWork() {
		return Action{
			Kind: Nudge, Ticket: t, Reason: ReasonActivity + ": " + pr.ActivitySummary,
			Text: fmt.Sprintf(ActivityNudge, pr.URL, t.Identifier, pr.ActivitySummary), ActivityAt: pr.ActivityAt,
		}, true
	}
	// An approval can land after the ready notification, so this runs before
	// the once-per-head check. A head whose merge failed waits for a push.
	label, _ := t.LabelIn(in.RepoLabelGroup)
	repo := in.Repos[label]
	if ok && pr.Ready && pr.Approved && repo.AutoMerge && !pr.Draft {
		if prev.MergeFailedHead == pr.Head {
			return Action{}, false
		}
		return Action{Kind: Merge, Ticket: t, Reason: ReasonMerge, Text: MergedText(t, pr), Head: pr.Head, PRURL: pr.URL}, true
	}
	if ok && pr.Ready {
		switch {
		case repo.OpenPRAs == config.OpenPRAsDraft && pr.Draft && prev.DraftReadyHead != pr.Head:
			return Action{Kind: Notify, Ticket: t, Reason: ReasonDraftReady, Text: DraftReadyText(t, pr), Head: pr.Head}, true
		case repo.OpenPRAs == config.OpenPRAsDraft && !pr.Draft && prev.ReadyHead != pr.Head:
			return Action{Kind: Notify, Ticket: t, Reason: ReasonReviewsHandled, Text: ReviewsHandledText(t, pr), Head: pr.Head}, true
		case repo.OpenPRAs != config.OpenPRAsDraft && prev.ReadyHead != pr.Head:
			return Action{Kind: Notify, Ticket: t, Reason: ReasonReady, Text: ReadyText(t, pr), Head: pr.Head}, true
		}
	}
	if !ok || !pr.NeedsWork() {
		return Action{}, false
	}
	nudges := prev.NudgesAt(t.Status, pr.Head)
	if nudges >= in.NudgeLimit {
		return Action{Kind: Escalate, Ticket: t, Reason: fmt.Sprintf("its worker stayed idle through %d nudges about %s without pushing. Look at its herdr workspace.", nudges, pr.URL)}, true
	}
	return Action{Kind: Nudge, Ticket: t, Reason: ReasonPR + ": " + pr.Problem(), Text: fmt.Sprintf(PRNudge, pr.URL, t.Identifier, pr.Problem())}, true
}

func (in Input) unread(t linear.Ticket) bool {
	repo, ok := t.LabelIn(in.RepoLabelGroup)
	return ok && in.UnreadRepos[repo]
}
