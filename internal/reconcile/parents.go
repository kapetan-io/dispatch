package reconcile

import (
	"fmt"
	"slices"
	"strings"

	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/worker"
)

// Parents returns the tickets whose children Plan acts on: In Progress parents
// that are known not to be design tickets, since the design worker and the review own
// the children and close of those. The tick reads the children of these only.
func (in Input) Parents() []linear.Ticket {
	var parents []linear.Ticket
	for _, t := range in.Tickets {
		if t.Status == in.InProgress && t.HasChildren && in.notDesign(t) {
			parents = append(parents, t)
		}
	}
	return parents
}

// planParents closes each parent whose children are all finished, then
// promotes the Todo children that are ready, oldest first, while the In
// Progress count stays under the cap. A Todo child whose blocker was abandoned
// is escalated instead. Only promotion writes In Progress.
func (in Input) planParents() []Action {
	var closes, starts []Action
	active := in.activeCount()
	for _, parent := range in.Parents() {
		children := in.Children[parent.Identifier]
		// No children means the read failed, and "every child is finished" would
		// otherwise close the parent.
		if len(children) == 0 {
			continue
		}
		if !slices.ContainsFunc(children, func(c linear.Ticket) bool { return !slices.Contains(in.Done, c.Status) }) {
			closes = append(closes, Action{Kind: Close, Ticket: parent, Reason: "every child is finished"})
			continue
		}
		for _, c := range children {
			// An unread pool hides which children hold a lease, and a paused child
			// must stay paused.
			if blocker, ok := in.abandonedBlocker(c); ok && !in.unread(c) {
				starts = append(starts, Action{Kind: Escalate, Ticket: c, Reason: fmt.Sprintf("its blocker %s is %s and will never be Done, so it will not start. Remove the blocker relation, cancel this ticket, or move it back to Todo yourself.", blocker.Identifier, blocker.Status)})
				continue
			}
			if !in.promotable(c) {
				continue
			}
			if in.unread(c) {
				starts = append(starts, Action{Kind: Wait, Ticket: c, Reason: fmt.Sprintf("the %s pool could not be read this tick", in.repoOf(c))})
				continue
			}
			// A child that is itself a parent never gets a worker, so it takes no slot.
			if !c.HasChildren {
				if active >= in.MaxInProgress {
					starts = append(starts, Action{Kind: Wait, Ticket: c, Reason: fmt.Sprintf("%d of %d In Progress tickets already", active, in.MaxInProgress)})
					continue
				}
				active++
			}
			starts = append(starts, Action{Kind: Promote, Ticket: c, Reason: "child of " + parent.Identifier})
		}
	}
	return append(closes, starts...)
}

// promotable reports whether a child is a Todo ticket of yours that is ready to
// start. A child that holds a lease was paused with its tree kept, and stays
// paused until you move it yourself.
func (in Input) promotable(c linear.Ticket) bool {
	if !in.startable(c) || c.BlockersTruncated {
		return false
	}
	return !slices.ContainsFunc(c.Blockers, in.blockerOpen)
}

// abandonedBlocker returns the first blocker of a startable child that was
// canceled or marked duplicate, so the child would wait for it forever. A
// blocker that is merely Done is not abandoned, since blocker_done may name a
// later status it still moves to. A truncated blocker list still escalates,
// since one abandoned blocker on the first page is enough.
func (in Input) abandonedBlocker(c linear.Ticket) (linear.Blocker, bool) {
	if !in.startable(c) {
		return linear.Blocker{}, false
	}
	i := slices.IndexFunc(c.Blockers, func(b linear.Blocker) bool { return b.Abandoned })
	if i < 0 {
		return linear.Blocker{}, false
	}
	return c.Blockers[i], true
}

// startable reports whether a child is a Todo ticket of yours that the
// dispatcher may act on, whatever its blockers say.
func (in Input) startable(c linear.Ticket) bool {
	return c.Status == in.Todo && slices.Contains(in.Teams, c.Team) && c.Mine && c.HasLabel(in.AILabel) && in.notDesign(c) && !in.Leased[c.Identifier]
}

// activeCount is the In Progress tickets that hold or will take a worker slot.
// A ticket the dispatcher launches counts before it has a worker, and a ticket
// in an unread repo counts since it may have a worker nobody could see. A
// parent takes no slot, except a design parent whose design worker is alive or may be,
// which holds its tree.
func (in Input) activeCount() int {
	n := 0
	for _, t := range in.Tickets {
		if t.Status != in.InProgress {
			continue
		}
		live := in.Workers[t.Identifier].State != worker.None
		switch {
		case t.HasChildren:
			if live || (t.HasLabel("design") && in.unread(t)) {
				n++
			}
		case live || in.unread(t) || !in.WaitsOnBlockers(t):
			n++
		}
	}
	return n
}

func (in Input) repoOf(t linear.Ticket) string {
	repo, _ := t.LabelIn(in.RepoLabelGroup)
	return repo
}

// WaitsOnBlockers reports whether a ticket has a blocker that is not yet at the
// blocker-done status, or more blockers than were read.
func (in Input) WaitsOnBlockers(t linear.Ticket) bool {
	return t.BlockersTruncated || slices.ContainsFunc(t.Blockers, in.blockerOpen)
}

func (in Input) blockerOpen(b linear.Blocker) bool { return b.Status != in.BlockerDone }

// blockerHold decides what a workerless In Progress ticket does while a blocker
// is unfinished. A ticket whose blocker was canceled would wait forever, so it
// goes to Needs Human. Otherwise it waits, and the Note says why on the ticket.
func (in Input) blockerHold(t linear.Ticket) (Action, bool) {
	if i := slices.IndexFunc(t.Blockers, func(b linear.Blocker) bool { return b.Abandoned }); i >= 0 {
		b := t.Blockers[i]
		return Action{Kind: Escalate, Ticket: t, Reason: fmt.Sprintf("its blocker %s is %s and will never be Done, so it will not start. Remove the blocker relation, then move this ticket back to In Progress.", b.Identifier, b.Status)}, true
	}
	if !in.WaitsOnBlockers(t) {
		return Action{}, false
	}
	var open, ids []string
	for _, b := range t.Blockers {
		if in.blockerOpen(b) {
			open = append(open, fmt.Sprintf("%s (%s)", b.Identifier, b.Status))
			ids = append(ids, b.Identifier)
		}
	}
	if t.BlockersTruncated {
		open = append(open, "relations past the first 10")
		ids = append(ids, "relations the dispatcher did not read")
	}
	reason := "blocked by " + strings.Join(open, ", ")
	slices.Sort(ids)
	note := "No worker is running because this ticket is blocked by " + strings.Join(ids, ", ")
	note += ". The dispatcher starts one on the first tick after every blocker is Done."
	return Action{Kind: Wait, Ticket: t, Reason: reason, Note: note}, true
}
