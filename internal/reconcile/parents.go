package reconcile

import (
	"fmt"
	"slices"

	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/worker"
)

// Parents returns the tickets whose children Plan acts on: In Progress parents
// that are known not to be design tickets, whose close rule waits for the
// grill and review skills. The tick reads the children of these only.
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
	return !slices.ContainsFunc(c.Blockers, func(b linear.Blocker) bool { return b.Status != in.BlockerDone })
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
// Every ticket without children counts, with or without a worker, since it
// has one or will get one. A parent never has a worker, except a design ticket
// whose grill has made children.
func (in Input) activeCount() int {
	n := 0
	for _, t := range in.Tickets {
		if t.Status != in.InProgress {
			continue
		}
		if !t.HasChildren || (t.HasLabel("design") && in.Workers[t.Identifier].State != worker.None) {
			n++
		}
	}
	return n
}

func (in Input) repoOf(t linear.Ticket) string {
	repo, _ := t.LabelIn(in.RepoLabelGroup)
	return repo
}
