package loop

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/worker"
	"github.com/kapetan-io/dispatch/internal/world"
)

// Status emoji at the front of a worker's workspace label.
const (
	emojiInProgress = "🟢"
	emojiInReview   = "🟡"
	emojiReady      = "👀"
	emojiNeedsHuman = "🔴"
	emojiPaused     = "⏸️"
	emojiQuiet      = "💤"
)

// unresponsiveAfter is how long a design worker sits idle before its label says so.
const unresponsiveAfter = time.Hour

type rename struct{ id, label string }

func workerOf(wd world.World, ticket string) string {
	return string(worker.Find(ticket, wd.Trees, wd.Snapshot).State)
}

func label(emoji string, t linear.Ticket) string { return emoji + " " + t.Identifier }

func tabLabel(t linear.Ticket) string {
	if t.Title == "" {
		return t.Identifier
	}
	return t.Identifier + " " + t.Title
}

// workspaceRenames lists the workspaces whose label differs from the one their
// ticket's status calls for, and the tabs whose label differs from their ticket's.
// A workspace or tab herdr didn't list is left alone, so a label changes only when
// the status does.
func (l *Loop) workspaceRenames() (workspaces, tabs []rename) {
	wd, cfg := l.wd, l.o.Config.Plugins.Linear.Statuses
	if wd.HerdrErr != nil || wd.LinearErr != nil {
		return nil, nil
	}
	current := map[string]string{}
	for _, ws := range wd.Snapshot.Workspaces {
		current[ws.ID] = ws.Label
	}
	currentTabs := map[string]string{}
	for _, tab := range wd.Snapshot.Tabs {
		currentTabs[tab.ID] = tab.Label
	}
	add := func(t linear.Ticket, w worker.Worker, emoji string) {
		have, ok := current[w.WorkspaceID]
		want := label(emoji, t)
		if w.WorkspaceID != "" && ok && have != want && l.renaming[w.WorkspaceID] != want {
			workspaces = append(workspaces, rename{w.WorkspaceID, want})
		}
	}
	for _, t := range wd.Tickets {
		if t.HasChildren {
			continue
		}
		w := wd.Workers[t.Identifier]
		if wd.Nudged[t.Identifier] && w.State == worker.Idle {
			w.State = worker.Working
		}
		switch t.Status {
		case cfg.InProgress:
			emoji := emojiInProgress
			if l.quiet(t, w) {
				emoji = emojiQuiet
			}
			add(t, w, emoji)
		case cfg.InReview:
			emoji := emojiInReview
			pr, ok := wd.PRs[t.Identifier]
			if ok && pr.Ready && w.State == worker.Idle {
				emoji = emojiReady
			}
			// An unreadable PR must not flip a 👀 label to 🟡 until it is read again.
			if !ok && wd.PRReadFailed[t.Identifier] && strings.HasPrefix(current[w.WorkspaceID], emojiReady) {
				continue
			}
			add(t, w, emoji)
		case cfg.NeedsHuman:
			add(t, w, emojiNeedsHuman)
		}
		if slices.Contains([]string{cfg.InProgress, cfg.InReview, cfg.NeedsHuman}, t.Status) {
			if have, ok := currentTabs[w.TabID]; ok && w.TabID != "" && have != tabLabel(t) && l.renaming[w.TabID] != tabLabel(t) {
				tabs = append(tabs, rename{w.TabID, tabLabel(t)})
			}
		}
	}
	paused := l.o.Config.Plugins.Linear.Statuses.Paused
	for _, t := range wd.LeaseHolders {
		if !slices.Contains(paused, t.Status) {
			continue
		}
		for _, w := range wd.Leases[t.Identifier] {
			// A live worker's workspace is closed by its own rule.
			if w.State == worker.None {
				add(t, w, emojiPaused)
			}
		}
	}
	return workspaces, tabs
}

// quiet reports whether t is a design or review ticket whose worker ended its
// last turn at least an hour ago. Nothing nudges these, so the label is the only
// sign the worker stopped without asking anything.
func (l *Loop) quiet(t linear.Ticket, w worker.Worker) bool {
	if w.State != worker.Idle || !slices.ContainsFunc(world.DesignLabels, t.HasLabel) || l.state == nil {
		return false
	}
	stop, busy, ok := l.state.LastStop(t.Identifier)
	return ok && !busy && l.clock.Now().Sub(time.UnixMilli(stop)) >= unresponsiveAfter
}

// renameWorkspaces fixes the labels that differ, off the loop goroutine. A failed
// rename is tried again at the next cycle, because the label still differs.
func (l *Loop) renameWorkspaces() {
	workspaces, tabs := l.workspaceRenames()
	if len(workspaces)+len(tabs) == 0 {
		return
	}
	for _, r := range workspaces {
		l.renaming[r.id] = r.label
	}
	for _, r := range tabs {
		l.renaming[r.id] = r.label
	}
	l.renames++
	go func() {
		ctx := context.Background()
		for _, r := range workspaces {
			if err := l.o.Herdr.RenameWorkspace(ctx, r.id, r.label); err != nil {
				_, _ = fmt.Fprintf(l.o.Err, "dispatch: while renaming herdr workspace %s: %v\n", r.id, err)
			}
		}
		for _, r := range tabs {
			if err := l.o.Herdr.RenameTab(ctx, r.id, r.label); err != nil {
				_, _ = fmt.Fprintf(l.o.Err, "dispatch: while renaming herdr tab %s: %v\n", r.id, err)
			}
		}
		l.inbox <- renameDone{}
	}()
}
