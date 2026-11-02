package world

import (
	"fmt"
	"maps"
	"slices"

	"github.com/kapetan-io/dispatch/internal/ticket"
)

// readLocal reads the local tickets, and the ones among the lease holders and the children of open
// local parents. A failed read keeps the local entries read before and sets LocalErr, and leaves
// Linear's entries alone.
func (w *World) readLocal(src Sources, scope Scope) {
	if src.Local == nil {
		return
	}
	var holders []string
	for _, tree := range w.Trees {
		if tree.LeaseHolder != "" && ticket.SystemOf(tree.LeaseHolder) == ticket.SystemLocal && !slices.Contains(holders, tree.LeaseHolder) {
			holders = append(holders, tree.LeaseHolder)
		}
	}
	got, err := src.Local.Tracked(holders, func(t ticket.Ticket) bool {
		return len(scope.Parents([]ticket.Ticket{t})) > 0
	})
	if err != nil {
		w.LocalErr = err
		_, _ = fmt.Fprintf(src.Stderr, "dispatch: while reading the local tickets: %v\n", err)
		return
	}
	w.LocalErr = nil
	w.Tickets = withSystem(w.Tickets, ticket.SystemLocal, got.Tickets)
	w.LeaseHolders = withSystem(w.LeaseHolders, ticket.SystemLocal, got.Holders)
	// The map is shared with earlier reads, so a new one replaces it.
	children := map[string][]ticket.Ticket{}
	for id, kids := range w.Children {
		if ticket.SystemOf(id) != ticket.SystemLocal {
			children[id] = kids
		}
	}
	maps.Copy(children, got.Children)
	if w.Children = children; len(children) == 0 {
		w.Children = nil
	}
}

// withSystem replaces the tickets of one ticket system in list with fresh, leaving the other system's.
func withSystem(list []ticket.Ticket, system string, fresh []ticket.Ticket) []ticket.Ticket {
	var out []ticket.Ticket
	if system == ticket.SystemLinear {
		out = append(out, fresh...)
	}
	for _, t := range list {
		if ticket.SystemOf(t.Identifier) != system {
			out = append(out, t)
		}
	}
	if system == ticket.SystemLocal {
		out = append(out, fresh...)
	}
	return out
}

// linearOnly are the tickets of list that came from Linear.
func linearOnly(list []ticket.Ticket) []ticket.Ticket {
	return slices.DeleteFunc(slices.Clone(list), func(t ticket.Ticket) bool {
		return ticket.SystemOf(t.Identifier) == ticket.SystemLocal
	})
}

// localChildren are the entries of children whose parent is a local ticket.
func localChildren(children map[string][]ticket.Ticket) map[string][]ticket.Ticket {
	var out map[string][]ticket.Ticket
	for id, kids := range children {
		if ticket.SystemOf(id) == ticket.SystemLocal {
			if out == nil {
				out = map[string][]ticket.Ticket{}
			}
			out[id] = kids
		}
	}
	return out
}

// HoldersErrOf is why the holder of a tree could not be looked up: the local read for a local id,
// otherwise the lease lookup in Linear.
func (w World) HoldersErrOf(id string) error {
	if ticket.SystemOf(id) == ticket.SystemLocal {
		return w.LocalErr
	}
	return w.HoldersErr
}

// TicketsErrOf is why the tickets of the id's ticket system are unknown: the last read of that
// system failed. Nil means its tickets are known.
func (w World) TicketsErrOf(id string) error {
	if ticket.SystemOf(id) == ticket.SystemLocal {
		return w.LocalErr
	}
	return w.LinearErr
}

// TrackedSubjects are the ids and tree paths a receipt or a memory entry may name and still be
// kept at rotation: every ticket read from either system, every ticket that holds a lease, and every tree.
func (w World) TrackedSubjects() map[string]bool {
	live := map[string]bool{}
	for _, t := range w.Tickets {
		live[t.Identifier] = true
	}
	for id := range w.Leased {
		live[id] = true
	}
	for _, tree := range w.Trees {
		live[tree.Path] = true
	}
	return live
}
