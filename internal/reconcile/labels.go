package reconcile

import (
	"slices"
	"strings"

	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/worker"
)

// Status emoji at the front of a worker's workspace label.
const (
	EmojiInProgress = "🟢"
	EmojiInReview   = "🟡"
	EmojiReady      = "👀"
	EmojiNeedsHuman = "🔴"
	EmojiPaused     = "⏸️"
	EmojiQuiet      = "💤"
)

// Rename asks for a herdr workspace to take a new label.
type Rename struct {
	WorkspaceID string
	Label       string
}

// Label is `<emoji> <ticket>`.
func Label(emoji string, t linear.Ticket) string {
	return emoji + " " + t.Identifier
}

// Renames lists the workspaces whose label differs from the one their ticket's
// status calls for. current maps a workspace id to its label now, so a label
// changes only when the status does. A workspace missing from current is left
// alone.
func (in Input) Renames(current map[string]string) []Rename {
	var renames []Rename
	add := func(t linear.Ticket, w worker.Worker, emoji string) {
		have, ok := current[w.WorkspaceID]
		if want := Label(emoji, t); w.WorkspaceID != "" && ok && have != want {
			renames = append(renames, Rename{WorkspaceID: w.WorkspaceID, Label: want})
		}
	}
	for _, t := range in.Tickets {
		if t.HasChildren {
			continue
		}
		w := in.Workers[t.Identifier]
		switch t.Status {
		case in.InProgress:
			emoji := EmojiInProgress
			if in.quiet(t, w) {
				emoji = EmojiQuiet
			}
			add(t, w, emoji)
		case in.InReview:
			emoji := EmojiInReview
			pr, ok := in.PRs[t.Identifier]
			if ok && pr.Ready && w.State == worker.Idle {
				emoji = EmojiReady
			}
			// An unreadable PR must not flip a 👀 label to 🟡 for a tick.
			if !ok && in.PRReadFailed[t.Identifier] && strings.HasPrefix(current[w.WorkspaceID], EmojiReady) {
				continue
			}
			add(t, w, emoji)
		case in.NeedsHuman:
			add(t, w, EmojiNeedsHuman)
		}
	}
	for _, t := range in.LeaseHolders {
		if !slices.Contains(in.Paused, t.Status) {
			continue
		}
		for _, w := range in.Leases[t.Identifier] {
			// A live worker's workspace is closed by this tick's Stop.
			if w.State == worker.None {
				add(t, w, EmojiPaused)
			}
		}
	}
	return renames
}

// quiet reports whether t is a design or review ticket whose worker ended its
// last turn at least UnresponsiveAfter ago. The dispatcher never nudges these,
// so the label is the only sign the worker stopped without asking anything.
// The Stop record catches a turn that began and ended between two ticks. It
// reads the idle clock that settle updates, so Renames must run after settle.
func (in Input) quiet(t linear.Ticket, w worker.Worker) bool {
	if w.State != worker.Idle || !slices.ContainsFunc(designLabels, t.HasLabel) {
		return false
	}
	stop, busy := in.lastStop(t, in.Cache[t.Identifier])
	return !busy && in.Now.Sub(stop) >= UnresponsiveAfter
}

// TabRename asks for a herdr tab to take a new label.
type TabRename struct {
	TabID string
	Label string
}

// TabLabel is `<ticket> <title>`, or the ticket alone when it has no title.
func TabLabel(t linear.Ticket) string {
	if t.Title == "" {
		return t.Identifier
	}
	return t.Identifier + " " + t.Title
}

// TabRenames lists the tabs holding a worker's Claude pane whose label differs
// from its ticket's. current maps a tab id to its label now. A tab missing
// from current is left alone.
func (in Input) TabRenames(current map[string]string) []TabRename {
	var renames []TabRename
	for _, t := range in.Tickets {
		if t.HasChildren || !slices.Contains([]string{in.InProgress, in.InReview, in.NeedsHuman}, t.Status) {
			continue
		}
		w := in.Workers[t.Identifier]
		if have, ok := current[w.TabID]; ok && w.TabID != "" && have != TabLabel(t) {
			renames = append(renames, TabRename{TabID: w.TabID, Label: TabLabel(t)})
		}
	}
	return renames
}
