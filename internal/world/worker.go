package world

import (
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/treehouse"
)

// A worker is found by the join from a ticket to its treehouse lease (the lease
// holder is the ticket id), to the herdr workspace showing the leased tree, to the
// Claude agent in that workspace. herdr agent names are not used, because they
// clear when an agent exits.

// WorkerState is how a ticket's worker is doing.
type WorkerState string

const (
	NoWorker WorkerState = "none"
	Working  WorkerState = "working"
	Idle     WorkerState = "idle"
	Blocked  WorkerState = "blocked"
)

// Worker is what the lookup found for one ticket.
type Worker struct {
	State       WorkerState
	TreePath    string
	RepoDir     string
	WorkspaceID string
	PaneID      string
	TabID       string
	// Session is the Claude session id herdr reports for the agent, or empty.
	Session string
	// UnconfirmedSince is when the launch that started this worker began, in
	// milliseconds, read from the kickoffs record. Zero means the launch returned.
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
	for _, a := range snap.Agents {
		if a.WorkspaceID == w.WorkspaceID && a.Kind == "claude" {
			w.PaneID = a.PaneID
			w.TabID = a.TabID
			w.Session = a.Session
			w.State = stateOf(a.Status)
			return w
		}
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
