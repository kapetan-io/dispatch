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
// Progress count stays under the cap. Only promotion writes In Progress.
func (in Input) planParents() []Action {
	var closes, promotes []Action
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
			if !in.promotable(c) {
				continue
			}
			if in.unread(c) {
				promotes = append(promotes, Action{Kind: Wait, Ticket: c, Reason: fmt.Sprintf("the %s pool could not be read this tick", in.repoOf(c))})
				continue
			}
			// A child that is itself a parent never gets a worker, so it takes no slot.
			if !c.HasChildren {
				if active >= in.MaxInProgress {
					promotes = append(promotes, Action{Kind: Wait, Ticket: c, Reason: fmt.Sprintf("%d of %d In Progress tickets already", active, in.MaxInProgress)})
					continue
				}
				active++
			}
			promotes = append(promotes, Action{Kind: Promote, Ticket: c, Reason: "child of " + parent.Identifier})
		}
	}
	return append(closes, promotes...)
}

// promotable reports whether a child is a Todo ticket of yours that is ready to
// start. A child that holds a lease was paused with its tree kept, and stays
// paused until you move it yourself.
func (in Input) promotable(c linear.Ticket) bool {
	if c.Status != in.Todo || !slices.Contains(in.Teams, c.Team) || !c.Mine || !c.HasLabel(in.AILabel) || !in.notDesign(c) {
		return false
	}
	if in.Leased[c.Identifier] || c.BlockersTruncated {
		return false
	}
	// A Canceled blocker never becomes Done, so its child waits, as the design
	// has it today.
	return !slices.ContainsFunc(c.Blockers, func(b linear.Blocker) bool { return b.Status != in.BlockerDone })
}

// activeCount is the In Progress tickets that hold or will take a worker slot.
// A parent never has a worker, and a design ticket with no live worker takes
// no slot because the dispatcher skips it. A ticket in an unread repo counts,
// since it may have a worker nobody could see.
func (in Input) activeCount() int {
	n := 0
	for _, t := range in.Tickets {
		if t.Status != in.InProgress || t.HasChildren {
			continue
		}
		if in.Workers[t.Identifier].State != worker.None || in.unread(t) || in.notDesign(t) {
			n++
		}
	}
	return n
}

func (in Input) repoOf(t linear.Ticket) string {
	repo, _ := t.LabelIn(in.RepoLabelGroup)
	return repo
}
