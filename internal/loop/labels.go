package loop

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/executor"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/pullrequest"
	"github.com/kapetan-io/dispatch/internal/stops"
	"github.com/kapetan-io/dispatch/internal/world"
)

// Emoji at the front of a worker's workspace label, by who the ticket is waiting on.
const (
	emojiBusy             = "🟢"
	emojiChecksRunning    = "🟡"
	emojiReadyForReview   = "👀"
	emojiAwaitingApproval = "⏳"
	emojiAwaitingMerge    = "🔀"
	emojiNeedsHuman       = "🔴"
	emojiPaused           = "⏸️"
	emojiWaiting          = "💤"
)

type rename struct{ id, label string }

func workerOf(wd world.World, ticket string) string {
	return string(world.FindWorker(ticket, wd.Trees, wd.Snapshot).State)
}

func label(emoji string, t linear.Ticket) string { return emoji + " " + t.Identifier }

func tabLabel(t linear.Ticket) string { return herdr.TabLabel(t.Identifier, t.Title) }

// differs is true when the workspace or tab needs renaming to want. A rename already
// on its way to want needs none, and one on its way to another label needs it even when
// the last read of herdr shows want, because that read predates the rename.
func (l *Loop) differs(id, have, want string) bool {
	if sent, ok := l.renaming[id]; ok {
		return sent != want
	}
	return have != want
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
	add := func(t linear.Ticket, w world.Worker, emoji string) {
		have, ok := current[w.WorkspaceID]
		want := label(emoji, t)
		if w.WorkspaceID != "" && ok && l.differs(w.WorkspaceID, have, want) {
			workspaces = append(workspaces, rename{w.WorkspaceID, want})
		}
	}
	for _, t := range wd.Tickets {
		if t.HasChildren {
			continue
		}
		w := wd.Workers[t.Identifier]
		if wd.Nudged[t.Identifier] && w.State == world.Idle {
			w.State = world.Working
		}
		switch t.Status {
		case cfg.InProgress:
			add(t, w, l.workspaceEmoji(w, pullrequest.PullRequest{}, ""))
		case cfg.InReview:
			pr, ok := wd.PRs[t.Identifier]
			// An unreadable PR must not change a label that came from reading it.
			if !ok && wd.PRReadFailed[t.Identifier] && slices.ContainsFunc(prLabels, func(e string) bool {
				return strings.HasPrefix(current[w.WorkspaceID], e)
			}) {
				continue
			}
			add(t, w, l.workspaceEmoji(w, pr, l.repoOf(t).OpenPRAs))
		case cfg.NeedsHuman:
			add(t, w, emojiNeedsHuman)
		}
		if slices.Contains([]string{cfg.InProgress, cfg.InReview, cfg.NeedsHuman}, t.Status) {
			if have, ok := currentTabs[w.TabID]; ok && w.TabID != "" && l.differs(w.TabID, have, tabLabel(t)) {
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
			if w.State == world.NoWorker {
				add(t, w, emojiPaused)
			}
		}
	}
	return workspaces, tabs
}

// repoOf is the settings table of the ticket's repo label, empty when it has none.
func (l *Loop) repoOf(t linear.Ticket) config.Repo {
	label, _ := t.LabelIn(l.o.Config.Plugins.Linear.RepoLabelGroup)
	_, repo, _ := l.o.Config.Repos.Find(label)
	return repo
}

// prLabels are the labels a read PR can give an In Review worker.
var prLabels = []string{emojiReadyForReview, emojiAwaitingApproval, emojiAwaitingMerge, emojiChecksRunning, emojiWaiting, emojiBusy}

// workspaceEmoji is the label for a ticket in progress or in review. A worker with no
// Stop record, or one whose Stop did not list its tasks, may still be busy, so it stays
// green. A Stop that lists a running task keeps it green even while the PR's checks run.
// A ready PR shows who it waits on once the worker is idle or gone: 👀 for you, ⏳ for
// its approvers and 🔀 for whoever merges it. openAs is the PR's repo setting.
func (l *Loop) workspaceEmoji(w world.Worker, pr pullrequest.PullRequest, openAs string) string {
	if w.State == world.Blocked {
		return emojiNeedsHuman
	}
	gone := w.State == world.NoWorker || w.State == world.Idle && w.UnconfirmedSince != 0
	idle := w.State == world.Idle && !gone
	if pr.Ready && (idle || gone) {
		switch pr.WaitingOn(openAs) {
		case pullrequest.WaitingOnApprovers:
			return emojiAwaitingApproval
		case pullrequest.WaitingOnMerge:
			return emojiAwaitingMerge
		}
		return emojiReadyForReview
	}
	if !idle {
		if gone && pr.ChecksRunning {
			return emojiChecksRunning
		}
		return emojiBusy
	}
	stop, ok := stops.Read(l.o.Sources.StateDir, w.TreePath, w.Session)
	switch {
	case ok && stop.Busy(l.clock.Now()):
		return emojiBusy
	case pr.ChecksRunning:
		return emojiChecksRunning
	case !ok || !stop.Reported:
		return emojiBusy
	}
	return emojiWaiting
}

// renameWorkspaces fixes the labels that differ, off the loop goroutine. A failed
// rename is tried again after the next read of herdr, because the label still differs.
// One batch runs at a time, and renameDone checks again against the latest read, so a
// slow rename of an older label can't land after a newer one.
func (l *Loop) renameWorkspaces() {
	if l.renames > 0 {
		return
	}
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
		ctx, cancel := context.WithTimeout(l.runCtx, executor.DefaultDeadline)
		defer cancel()
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
		select {
		case l.inbox <- renameDone{}:
		case <-l.stopped:
		}
	}()
}
