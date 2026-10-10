package reconcile

import (
	"slices"

	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/worker"
)

// Status emoji at the front of a worker's workspace label.
const (
	EmojiInProgress  = "🟢"
	EmojiInReview    = "🟡"
	EmojiReady       = "👀"
	EmojiNeedsHuman  = "🔴"
	EmojiPaused      = "⏸️"
	maxLabelRunes    = 40
	truncationMarker = "…"
)

// Rename sets a herdr workspace's label.
type Rename struct {
	WorkspaceID string
	Label       string
}

// Label is `<emoji> <ticket> <title>`, cut to maxLabelRunes with an ellipsis.
func Label(emoji string, t linear.Ticket) string {
	label := []rune(emoji + " " + t.Identifier + " " + t.Title)
	if len(label) > maxLabelRunes {
		label = append(label[:maxLabelRunes-1], []rune(truncationMarker)...)
	}
	return string(label)
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
			add(t, w, EmojiInProgress)
		case in.InReview:
			emoji := EmojiInReview
			if pr, ok := in.PRs[t.Identifier]; ok && pr.Ready && w.State == worker.Idle {
				emoji = EmojiReady
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
