package world

import (
	"slices"

	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/treehouse"
	"github.com/kapetan-io/dispatch/internal/treekey"
	"github.com/kapetan-io/dispatch/internal/workers"
)

// A worker is found by the join from a ticket's worker row to the tree the row names, to the lease
// on that tree held by the ticket, to the workspace showing that tree, to the session of a
// registered harness that the multiplexer reports there. A row with no tree, or whose tree is not
// leased to the ticket, is no worker, and neither is a lease no row names. The multiplexer's own
// agent names are not used, because they clear when the program exits.

// WorkerState is what the worker's session is doing. NoWorker is the saved
// value "none": no session runs in the worker, or the ticket holds no tree.
type WorkerState string

const (
	NoWorker WorkerState = "none"
	Working  WorkerState = "working"
	Idle     WorkerState = "idle"
	Blocked  WorkerState = "blocked"
)

// Worker is the ticket's place on one tree it holds: the tree, the workspace
// showing it, and the session there now, if any.
type Worker struct {
	State       WorkerState
	TreePath    string
	RepoDir     string
	WorkspaceID string
	PaneID      string
	TabID       string
	// Session is the id the multiplexer reports for the session running there, or empty.
	Session string
	// Kind is the agent kind the multiplexer detected there, such as "claude".
	Kind string
	// UnconfirmedSince is when the latest launch on this tree began, in milliseconds, while its
	// kickoff record is open and no Stop is later. Zero means the launch typed its kickoff.
	UnconfirmedSince int64
	// noStatus is set while the session reports no status, as Claude does at a startup dialog.
	noStatus bool
}

// StatusKnown reports whether the multiplexer can tell what the session is doing. Claude reports
// no status while it shows a startup dialog.
func (w Worker) StatusKnown() bool { return !w.noStatus }

// WorkerOf is the worker on the first tree the ticket's worker rows name. WorkersOf returns one
// for every such tree.
func (w World) WorkerOf(ticket string) Worker {
	if all := w.WorkersOf(ticket); len(all) > 0 {
		return all[0]
	}
	return Worker{State: NoWorker}
}

// WorkersOf looks up one worker for each tree a worker row of the ticket names and the ticket
// holds the lease on. A ticket has several when its design includes a library in another repo.
func (w World) WorkersOf(ticket string) []Worker { return w.workersIn(ticket, w.Snapshot) }

// workersIn is WorkersOf against a snapshot of the caller's choosing.
func (w World) workersIn(ticket string, snap herdr.Snapshot) []Worker {
	if !w.Recorded {
		return workersByLease(ticket, w.Trees, snap)
	}
	rows := w.Rows.Of(ticket)
	var all []Worker
	for _, t := range w.Trees {
		if t.LeaseHolder != ticket {
			continue
		}
		// The resync matches a row to a lease by the resolved path, so two spellings of one tree
		// are one tree here too.
		if slices.ContainsFunc(rows, func(row workers.Worker) bool { return treekey.Of(row.Row.Tree) == treekey.Of(t.Path) }) {
			all = append(all, onTree(t, snap))
		}
	}
	return all
}

// workersByLease looks up one worker for each tree the ticket holds a lease on, which is how a
// world with no worker rows, read from history written before them, finds its workers.
func workersByLease(ticket string, trees []treehouse.Tree, snap herdr.Snapshot) []Worker {
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
		w.Kind = a.Kind
		w.State = stateOf(a.Status)
		w.noStatus = a.Status == "unknown"
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
		// working, and unknown, which the multiplexer reports while it can't tell.
		return Working
	}
}
