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
	RepoDir     string
	WorkspaceID string
	PaneID      string
}

// Find looks up the worker on the ticket's first tree. FindAll returns one for
// every tree.
func Find(ticket string, trees []treehouse.Tree, snap herdr.Snapshot) Worker {
	if all := FindAll(ticket, trees, snap); len(all) > 0 {
		return all[0]
	}
	return Worker{State: None}
}

// FindAll looks up one worker for each tree the ticket holds a lease on. A
// ticket has several when its design includes a library in another repo.
func FindAll(ticket string, trees []treehouse.Tree, snap herdr.Snapshot) []Worker {
	var all []Worker
	for _, t := range trees {
		if t.LeaseHolder == ticket {
			all = append(all, onTree(t, snap))
		}
	}
	return all
}

func onTree(t treehouse.Tree, snap herdr.Snapshot) Worker {
	w := Worker{State: None, TreePath: t.Path, RepoDir: t.RepoDir}
	for _, ws := range snap.Workspaces {
		if ws.CheckoutPath == t.Path {
			w.WorkspaceID = ws.ID
			break
		}
	}
	if w.WorkspaceID == "" {
		return w
	}
	for _, a := range snap.Agents {
		if a.WorkspaceID == w.WorkspaceID && a.Kind == "claude" {
			w.PaneID = a.PaneID
			w.State = stateOf(a.Status)
			return w
		}
	}
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
