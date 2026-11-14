package loop

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/executor"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/pullrequest"
	"github.com/kapetan-io/dispatch/internal/sessionlog"
	"github.com/kapetan-io/dispatch/internal/stops"
	"github.com/kapetan-io/dispatch/internal/ticket"
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

func label(emoji string, t ticket.Ticket) string { return herdr.WorkspaceLabel(emoji, t.Identifier) }

func tabLabel(t ticket.Ticket) string { return herdr.TabLabel(t.Identifier, t.Title) }

// differs is true when the workspace or tab needs renaming to want. A rename sent since the
// last resync to want needs none, whether or not it landed, and one sent to another label
// needs it even when the last read of the multiplexer shows want, because that read predates the rename.
func (l *Loop) differs(id, have, want string) bool {
	if sent, ok := l.renaming[id]; ok {
		return sent != want
	}
	return have != want
}

// workspaceRenames lists the workspaces whose label differs from the one their
// ticket's status calls for, and the tabs whose label differs from their ticket's.
// A workspace or tab the multiplexer didn't list is left alone, so a label changes only when
// the status does.
func (l *Loop) workspaceRenames() (workspaces, tabs []rename) {
	wd := l.wd
	scope := wd.Scope(l.o.Config)
	if wd.MultiplexerErr != nil {
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
	add := func(t ticket.Ticket, w world.Worker, emoji string) {
		have, ok := current[w.WorkspaceID]
		want := label(emoji, t)
		if w.WorkspaceID != "" && ok && l.differs(w.WorkspaceID, have, want) {
			workspaces = append(workspaces, rename{w.WorkspaceID, want})
		}
	}
	for _, t := range wd.Tickets {
		if t.HasChildren || wd.TicketsErrOf(t.Identifier) != nil {
			continue
		}
		w := wd.Workers[t.Identifier]
		if wd.Nudged[t.Identifier] && w.State == world.Idle {
			w.State = world.Working
		}
		switch {
		case scope.Is(t, ticket.InProgress):
			add(t, w, l.workspaceEmoji(w, pullrequest.PullRequest{}, ""))
		case scope.Is(t, ticket.InReview):
			pr, ok := wd.PRs[t.Identifier]
			// An unreadable PR must not change a label that came from reading it.
			if !ok && wd.PRReadFailed[t.Identifier] && slices.ContainsFunc(prLabels, func(e string) bool {
				return strings.HasPrefix(current[w.WorkspaceID], e)
			}) {
				continue
			}
			add(t, w, l.workspaceEmoji(w, pr, l.repoOf(t).OpenPRAs))
		case scope.Is(t, ticket.NeedsHuman):
			add(t, w, emojiNeedsHuman)
		case scope.Is(t, ticket.Paused) && w.State == world.NoWorker && heldChild(wd, scope, t):
			add(t, w, emojiPaused)
		}
		if scope.Is(t, ticket.InProgress) || scope.Is(t, ticket.InReview) || scope.Is(t, ticket.NeedsHuman) {
			if have, ok := currentTabs[w.TabID]; ok && w.TabID != "" && l.differs(w.TabID, have, tabLabel(t)) {
				tabs = append(tabs, rename{w.TabID, tabLabel(t)})
			}
		}
	}
	for _, t := range wd.LeaseHolders {
		if !scope.Is(t, ticket.Paused) || wd.TicketsErrOf(t.Identifier) != nil || !heldChild(wd, scope, t) {
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

// heldChild reports whether t is a Todo child of an In Progress parent that is not a design ticket,
// the same test as pausedChild in rules/shared.ts. Its tree stays leased to mark
// it paused on purpose, while any other paused ticket's tree is returned, so only a held child's
// workspace is worth a label.
func heldChild(wd world.World, scope world.Scope, t ticket.Ticket) bool {
	if !scope.Is(t, ticket.Todo) {
		return false
	}
	for parent, kids := range wd.Children {
		if slices.ContainsFunc(kids, func(k ticket.Ticket) bool { return k.Identifier == t.Identifier }) {
			return slices.ContainsFunc(wd.Tickets, func(p ticket.Ticket) bool {
				return p.Identifier == parent && scope.Is(p, ticket.InProgress) && world.NotDesign(p)
			})
		}
	}
	return false
}

// repoOf is the settings table of the ticket's repo label, empty when it has none.
func (l *Loop) repoOf(t ticket.Ticket) config.Repo {
	label, _ := world.ScopeOf(l.o.Config).RepoOf(t)
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
	// A log that can't be read leaves the Stop alone to decide, as the world does.
	log, _ := sessionlog.Read(l.o.Sources.StateDir, w.TreePath, w.Session)
	switch {
	case ok && stop.InTurn(log, l.clock.Now()):
		return emojiBusy
	case pr.ChecksRunning:
		return emojiChecksRunning
	case !ok || !stop.Reported:
		return emojiBusy
	}
	return emojiWaiting
}

// renameWorkspaces fixes the labels that differ, off the loop goroutine. A failed
// rename is tried again at the next resync, because the label still differs then.
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
			if err := l.o.Multiplexer.RenameWorkspace(ctx, r.id, r.label); err != nil {
				_, _ = fmt.Fprintf(l.o.Err, "dispatch: while renaming workspace %s: %v\n", r.id, err)
			}
		}
		for _, r := range tabs {
			if err := l.o.Multiplexer.RenameTab(ctx, r.id, r.label); err != nil {
				_, _ = fmt.Fprintf(l.o.Err, "dispatch: while renaming tab %s: %v\n", r.id, err)
			}
		}
		select {
		case l.inbox <- renameDone{}:
		case <-l.stopped:
		}
	}()
}
