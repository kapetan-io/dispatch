package reconcile

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/worker"
)

// Retires reports whether Plan will tear down this lease holder's trees: its
// status is a finished one and it is not a design ticket. The tick reads the
// git state of those trees only.
func (in Input) Retires(t linear.Ticket) bool {
	return slices.Contains(in.Done, t.Status) && in.notDesign(t)
}

// notDesign reports whether the ticket is known not to be a design ticket,
// whose trees belong to the grill and the review. A ticket whose labels were
// cut short might be one.
func (in Input) notDesign(t linear.Ticket) bool {
	return !t.LabelsTruncated && !slices.ContainsFunc(designLabels, t.HasLabel)
}

// retire plans the cleanup of lease holders that are finished or paused. A
// finished ticket's pushed tree with no tracked changes is returned, after its
// untracked files are deleted. One with work in it keeps its tree, loses its
// worker, and gets one comment. A paused ticket loses its
// worker and keeps its tree. Plan puts these first, so a returned tree is free
// for a launch in the same tick.
func (in Input) retire() []Action {
	var actions []Action
	for _, t := range in.LeaseHolders {
		done, paused := slices.Contains(in.Done, t.Status), slices.Contains(in.Paused, t.Status)
		if !done && !paused {
			continue
		}
		if t.LabelsTruncated {
			actions = append(actions, Action{Kind: Skip, Ticket: t, Reason: "it has more labels than the dispatcher reads, so it can't tell whether the ticket is a design ticket that owns its trees"})
			continue
		}
		if !in.notDesign(t) {
			continue
		}
		if done {
			actions = append(actions, in.retireFinished(t)...)
			continue
		}
		for _, w := range in.Leases[t.Identifier] {
			if w.State != worker.None {
				actions = append(actions, Action{Kind: Stop, Ticket: t, Reason: fmt.Sprintf("moved back to %s", t.Status), WorkspaceID: w.WorkspaceID, TreePath: w.TreePath})
			}
		}
	}
	return actions
}

// retireFinished plans each tree of a finished ticket. Kept trees share one
// comment, carried on the first Keep. Every kept tree gets a Keep on every
// tick, so the log says why the pool is short a tree.
func (in Input) retireFinished(t linear.Ticket) []Action {
	var actions, keeps []Action
	var why []string
	for _, w := range in.Leases[t.Identifier] {
		unfinished, inspected := in.Unfinished[w.TreePath]
		if err := in.CheckErrors[w.TreePath]; err != nil {
			actions = append(actions, Action{Kind: Skip, Ticket: t, Reason: fmt.Sprintf("can't tell whether %s holds unpushed work, so it stays leased: %v", w.TreePath, err)})
			continue
		}
		if !inspected {
			actions = append(actions, Action{Kind: Skip, Ticket: t, Reason: fmt.Sprintf("%s was not inspected, so it stays leased", w.TreePath)})
			continue
		}
		if unfinished == "" {
			reason, untracked := t.Status+", clean and pushed", in.Untracked[w.TreePath]
			if len(untracked) > 0 {
				reason = t.Status + ", would remove untracked " + NameList(untracked)
			}
			actions = append(actions, Action{Kind: Teardown, Ticket: t, Reason: reason, RepoDir: w.RepoDir, WorkspaceID: w.WorkspaceID, TreePath: w.TreePath, Untracked: untracked})
			continue
		}
		why = append(why, fmt.Sprintf("`%s` has %s", w.TreePath, unfinished))
		keep := Action{Kind: Keep, Ticket: t, Reason: t.Status + ", but its tree " + w.TreePath + " has " + unfinished, TreePath: w.TreePath}
		if w.State != worker.None {
			keep.WorkspaceID = w.WorkspaceID
		}
		keeps = append(keeps, keep)
	}
	if len(keeps) == 0 {
		return actions
	}
	if !in.Cache[t.Identifier].TeardownWarned {
		keeps[0].Text = "The dispatcher kept this ticket's tree leased, because " + strings.Join(why, " and ") +
			". Returning the tree would discard that work. Push or discard it, and the next tick returns the tree."
	}
	return append(actions, keeps...)
}

// NameList joins file names for a log line, quoting any name that holds
// whitespace or a control character so it can't read as two files.
func NameList(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = name
		if strings.ContainsFunc(name, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
			quoted[i] = strconv.Quote(name)
		}
	}
	return strings.Join(quoted, " ")
}
