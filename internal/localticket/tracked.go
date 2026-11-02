package localticket

import (
	"slices"

	bolt "go.etcd.io/bbolt"

	"github.com/kapetan-io/dispatch/internal/ticket"
)

// Tracked is what one world read takes from the database.
type Tracked struct {
	// Tickets are the tickets outside a finished status, oldest first.
	Tickets []ticket.Ticket `json:"tickets"`
	// Holders are the finished tickets among the lease holders asked for.
	Holders []ticket.Ticket
	// Children are the children, in every status, of each parent the caller wanted them for.
	Children map[string][]ticket.Ticket
}

// Listing is every ticket of the database at one moment, in every status. It is what `dispatch rules
// check` and `dispatch state reset` read, because neither opens the file for writing.
type Listing struct {
	// Table are the database's statuses.
	Table ticket.Statuses `json:"table"`
	// Highest is the highest ticket number the file has issued.
	Highest uint64 `json:"highest"`
	// Tickets are all tickets, oldest first, with HasChildren and each blocker's status filled in.
	Tickets []ticket.Ticket `json:"tickets"`
	// Parents maps a child's id to its parent's id.
	Parents map[string]string `json:"parents,omitempty"`
}

// Listing reads every ticket in one view transaction.
func (s *Store) Listing() (out Listing, err error) {
	defer recoverTo(&err)
	out = Listing{Table: s.Statuses()}
	err = s.db.View(func(tx *bolt.Tx) error {
		all := map[uint64]ticket.Ticket{}
		parentOf := map[uint64]uint64{}
		var order []uint64
		tb := tx.Bucket(ticketBucket)
		if tb == nil {
			return nil
		}
		out.Highest = tb.Sequence()
		c := tb.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			t, err := decode(k, v)
			if err != nil {
				return err
			}
			n := number(k)
			all[n], parentOf[n] = s.record(n, t), t.Parent
			order = append(order, n)
		}
		hasChildren := map[uint64]bool{}
		for n, parent := range parentOf {
			if parent != 0 {
				hasChildren[parent] = true
				if out.Parents == nil {
					out.Parents = map[string]string{}
				}
				out.Parents[ticket.LocalID(n)] = ticket.LocalID(parent)
			}
		}
		for _, n := range order {
			t := all[n]
			t.HasChildren = hasChildren[n]
			for i, b := range t.Blockers {
				if st, ok := s.statuses.Find(all[blockerNumber(b.Identifier)].Status); ok {
					t.Blockers[i].Status = st.Name
					t.Blockers[i].Abandoned = st.Does(ticket.Finished) && !st.Does(ticket.BlockerDone)
				}
			}
			out.Tickets = append(out.Tickets, t)
		}
		return nil
	})
	return out, err
}

// Statuses are the statuses the listing was read with.
func (l Listing) Statuses() ticket.Statuses { return slices.Clone(l.Table) }

// Tracked picks the tracked set out of the listing. It returns no error; the signature matches Store.Tracked so either serves as a world.LocalTickets. holders are the ids that hold a tree, and
// wantChildren picks the tickets whose children the caller acts on.
func (l Listing) Tracked(holders []string, wantChildren func(ticket.Ticket) bool) (Tracked, error) {
	var out Tracked
	for _, t := range l.Tickets {
		switch {
		case !l.Table.Has(t.Status, ticket.Finished):
			out.Tickets = append(out.Tickets, t)
		case slices.Contains(holders, t.Identifier):
			out.Holders = append(out.Holders, t)
		}
	}
	for _, parent := range out.Tickets {
		if !wantChildren(parent) {
			continue
		}
		kids := []ticket.Ticket{}
		for _, t := range l.Tickets {
			if l.Parents[t.Identifier] == parent.Identifier {
				kids = append(kids, t)
			}
		}
		if out.Children == nil {
			out.Children = map[string][]ticket.Ticket{}
		}
		out.Children[parent.Identifier] = kids
	}
	return out, nil
}

// Tracked reads the tracked set. holders are the ids that hold a tree, and wantChildren picks the
// tickets whose children the caller acts on.
func (s *Store) Tracked(holders []string, wantChildren func(ticket.Ticket) bool) (Tracked, error) {
	snap, err := s.Listing()
	if err != nil {
		return Tracked{}, err
	}
	return snap.Tracked(holders, wantChildren)
}

func blockerNumber(id string) uint64 {
	n, _ := ticket.LocalNumber(id)
	return n
}

// record is the ticket the rest of dispatch reads. Its blockers carry only their ids until Tracked
// fills in their statuses.
func (s *Store) record(n uint64, t stored) ticket.Ticket {
	out := ticket.Ticket{
		Identifier: ticket.LocalID(n), Title: t.Title, Branch: t.Branch, Status: t.Status,
		Mine: true, CreatedAt: t.CreatedAt, LastComment: t.LastComment, Repo: t.Repo,
		Attributes: t.Attributes, StatusAt: t.StatusAt, StartPR: t.StartPR,
	}
	for _, l := range t.Labels {
		out.Labels = append(out.Labels, ticket.Label{Name: l})
	}
	for _, b := range t.BlockedBy {
		out.Blockers = append(out.Blockers, ticket.Blocker{Identifier: ticket.LocalID(b)})
	}
	return out
}
