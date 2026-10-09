package reconcile

import (
	"fmt"
	"slices"

	"github.com/kapetan-io/dispatch/internal/linear"
)

// Retires reports whether Plan will tear down this leased ticket's trees, which
// is when its status is a finished one. The tick reads the git state of those
// trees only.
func (in Input) Retires(t linear.Ticket) bool {
	return slices.Contains(in.Done, t.Status) && in.settled(t)
}

// settled reports whether Plan can tell that a leased ticket is not a design
// ticket, whose trees belong to the grill and the review.
func (in Input) settled(t linear.Ticket) bool {
	return !t.LabelsTruncated && !t.HasLabel("design") && !t.HasLabel("design-review")
}

// retire plans the cleanup of leased tickets that are finished or paused. A
// finished ticket's clean, pushed tree is returned. One with work in it keeps
// its tree, loses its worker, and gets one comment. A paused ticket loses its
// worker and keeps its tree. Plan puts these first, so a returned tree is free
// for a launch in the same tick.
func (in Input) retire() []Action {
	var actions []Action
	for _, t := range in.Leased {
		finished, paused := slices.Contains(in.Done, t.Status), slices.Contains(in.Paused, t.Status)
		if !finished && !paused {
			continue
		}
		if t.LabelsTruncated {
			actions = append(actions, Action{Kind: Skip, Ticket: t, Reason: "it has more labels than the dispatcher reads, so it can't tell whether the ticket is a design ticket that owns its trees"})
			continue
		}
		if !in.settled(t) {
			continue
		}
		if paused {
			for _, w := range in.Leases[t.Identifier] {
				if w.WorkspaceID != "" {
					actions = append(actions, Action{Kind: Stop, Ticket: t, Reason: fmt.Sprintf("moved back to %s", t.Status), WorkspaceID: w.WorkspaceID, TreePath: w.TreePath})
				}
			}
			continue
		}
		actions = append(actions, in.finished(t)...)
	}
	return actions
}

func (in Input) finished(t linear.Ticket) []Action {
	var actions, keeps []Action
	var why []string
	for _, w := range in.Leases[t.Identifier] {
		if err := in.CheckErrors[w.TreePath]; err != nil {
			actions = append(actions, Action{Kind: Skip, Ticket: t, Reason: fmt.Sprintf("can't tell whether %s holds unpushed work, so it stays leased: %v", w.TreePath, err)})
			continue
		}
		unfinished := in.Unfinished[w.TreePath]
		if unfinished == "" {
			actions = append(actions, Action{Kind: Teardown, Ticket: t, Reason: t.Status + ", clean and pushed", RepoDir: w.RepoDir, WorkspaceID: w.WorkspaceID, TreePath: w.TreePath})
			continue
		}
		why = append(why, fmt.Sprintf("`%s` has %s", w.TreePath, unfinished))
		keeps = append(keeps, Action{Kind: Keep, Ticket: t, Reason: t.Status + ", but its tree has " + unfinished, WorkspaceID: w.WorkspaceID, TreePath: w.TreePath})
	}
	if len(keeps) == 0 {
		return actions
	}
	if !in.Cache[t.Identifier].TeardownWarned {
		keeps[0].Text = KeepComment(why)
	}
	for _, k := range keeps {
		// A tree already warned about and with no workspace left has nothing to do.
		if k.WorkspaceID != "" || k.Text != "" {
			actions = append(actions, k)
		}
	}
	return actions
}

// KeepComment is the one comment on a finished ticket whose trees were kept.
func KeepComment(why []string) string {
	body := "The dispatcher stopped this ticket's worker but kept its tree leased, because "
	for i, w := range why {
		if i > 0 {
			body += " and "
		}
		body += w
	}
	return body + ". Returning the tree would discard that work. Push or discard it, and the next tick returns the tree."
}
