// Package worker finds the Claude worker serving a ticket.
//
// The lookup goes from the ticket to its treehouse lease (the lease holder is
// the ticket id), to the herdr workspace showing the leased tree, to the Claude
// agent in that workspace. herdr agent names are not used, because they clear
// when an agent exits.
package worker

import (
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/treehouse"
)

// State is how a ticket's worker is doing.
type State string

const (
	None    State = "none"
	Working State = "working"
	Idle    State = "idle"
	Blocked State = "blocked"
)

// Worker is what the lookup found for one ticket.
type Worker struct {
	State       State
	TreePath    string
	WorkspaceID string
	PaneID      string
}

// Find looks up the worker for ticket in the pool trees and herdr snapshot.
func Find(ticket string, trees []treehouse.Tree, snap herdr.Snapshot) Worker {
	var w Worker
	for _, t := range trees {
		if t.LeaseHolder == ticket {
			w.TreePath = t.Path
			break
		}
	}
	if w.TreePath == "" {
		w.State = None
		return w
	}
	for _, ws := range snap.Workspaces {
		if ws.CheckoutPath == w.TreePath {
			w.WorkspaceID = ws.ID
			break
		}
	}
	if w.WorkspaceID == "" {
		w.State = None
		return w
	}
	for _, a := range snap.Agents {
		if a.WorkspaceID == w.WorkspaceID && a.Kind == "claude" {
			w.PaneID = a.PaneID
			w.State = stateOf(a.Status)
			return w
		}
	}
	w.State = None
	return w
}

func stateOf(status string) State {
	switch status {
	case "idle", "done":
		return Idle
	case "blocked":
		return Blocked
	default:
		// working, and unknown, which herdr reports while it can't tell.
		return Working
	}
}
