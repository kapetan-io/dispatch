package localticket

import (
	"slices"

	bolt "go.etcd.io/bbolt"

	"github.com/kapetan-io/dispatch/internal/ticket"
)

// Tracked is what one world read takes from the database.
type Tracked struct {
	// Tickets are the tickets outside a finished status, oldest first.
	Tickets []ticket.Ticket
	// Holders are the finished tickets among the lease holders asked for.
	Holders []ticket.Ticket
	// Children are the children, in every status, of each parent the caller wanted them for.
	Children map[string][]ticket.Ticket
}

// Tracked reads the tracked set in one view transaction. holders are the ids that hold a tree, and
// wantChildren picks the tickets whose children the caller acts on.
func (s *Store) Tracked(holders []string, wantChildren func(ticket.Ticket) bool) (out Tracked, err error) {
	defer recoverTo(&err)
	err = s.db.View(func(tx *bolt.Tx) error {
		all := map[uint64]ticket.Ticket{}
		parentOf := map[uint64]uint64{}
		var order []uint64
		c := tx.Bucket(ticketBucket).Cursor()
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
		for _, parent := range parentOf {
			if parent != 0 {
				hasChildren[parent] = true
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
			all[n] = t
		}
		for _, n := range order {
			t := all[n]
			switch {
			case !s.statuses.Has(t.Status, ticket.Finished):
				out.Tickets = append(out.Tickets, t)
			case slices.Contains(holders, t.Identifier):
				out.Holders = append(out.Holders, t)
			}
		}
		for _, parent := range out.Tickets {
			if !wantChildren(parent) {
				continue
			}
			pn, _ := ticket.LocalNumber(parent.Identifier)
			kids := []ticket.Ticket{}
			for _, n := range order {
				if parentOf[n] == pn {
					kids = append(kids, all[n])
				}
			}
			if out.Children == nil {
				out.Children = map[string][]ticket.Ticket{}
			}
			out.Children[parent.Identifier] = kids
		}
		return nil
	})
	return out, err
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
