package world

import (
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/treehouse"
)

// A worker is found by the join from a ticket to each tree it holds (the lease
// holder is the ticket id), to the herdr workspace showing that tree, to the
// session of a registered harness that herdr reports there. herdr's own agent names are not used,
// because they clear when the program exits.

// WorkerState is what the worker's session is doing. NoWorker is the saved
// value "none": no session runs in the worker, or the ticket holds no tree.
type WorkerState string

const (
	NoWorker WorkerState = "none"
	Working  WorkerState = "working"
	Idle     WorkerState = "idle"
	Blocked  WorkerState = "blocked"
)

// Worker is the ticket's place on one tree it holds: the tree, the herdr workspace
// showing it, and the session there now, if any.
type Worker struct {
	State       WorkerState
	TreePath    string
	RepoDir     string
	WorkspaceID string
	PaneID      string
	TabID       string
	// Session is the id herdr reports for the session running there, or empty.
	Session string
	// UnconfirmedSince is when the latest launch on this tree began, in
	// milliseconds, while its kickoff is unconfirmed, read from the kickoffs record. Zero means the launch returned.
	UnconfirmedSince int64
}

// FindWorker looks up the worker on the ticket's first tree. FindWorkers returns one for
// every tree.
func FindWorker(ticket string, trees []treehouse.Tree, snap herdr.Snapshot) Worker {
	if all := FindWorkers(ticket, trees, snap); len(all) > 0 {
		return all[0]
	}
	return Worker{State: NoWorker}
}

// FindWorkers looks up one worker for each tree the ticket holds a lease on. A
// ticket has several when its design includes a library in another repo.
func FindWorkers(ticket string, trees []treehouse.Tree, snap herdr.Snapshot) []Worker {
	var all []Worker
	for _, t := range trees {
		if t.LeaseHolder == ticket {
			all = append(all, onTree(t, snap))
		}
	}
	return all
}

func onTree(t treehouse.Tree, snap herdr.Snapshot) Worker {
	w := Worker{State: NoWorker, TreePath: t.Path, RepoDir: t.RepoDir}
	for _, ws := range snap.Workspaces {
		if ws.CheckoutPath == t.Path {
			w.WorkspaceID = ws.ID
			break
		}
	}
	if w.WorkspaceID == "" {
		return w
	}
	if a, ok := snap.SessionIn(w.WorkspaceID); ok {
		w.PaneID = a.PaneID
		w.TabID = a.TabID
		w.Session = a.Session
		w.State = stateOf(a.Status)
	}
	return w
}

func stateOf(status string) WorkerState {
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
