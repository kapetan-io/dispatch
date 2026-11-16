package workers

import (
	"fmt"
	"slices"
	"strings"

	bolt "go.etcd.io/bbolt"

	"github.com/kapetan-io/dispatch/internal/treekey"
)

// Lease is one tree of a pool and the holder of its lease, empty for a free tree.
type Lease struct {
	Tree   string
	Holder string
}

// Pool is the leases of one repo as a pool read that succeeded showed them. A failed read builds
// none, so a row never changes because of a read that failed.
type Pool struct {
	repo   string
	leases []Lease
}

// ReadPool is the pool of the repo with this label, from a read that succeeded.
func ReadPool(repo string, leases []Lease) Pool { return Pool{repo: repo, leases: leases} }

// Repo is the label of the pool's repo.
func (p Pool) Repo() string { return p.repo }

// has reports whether the tree is one of the pool's.
func (p Pool) has(tree string) bool {
	return slices.ContainsFunc(p.leases, func(l Lease) bool { return treekey.Of(l.Tree) == treekey.Of(tree) })
}

// holds reports whether the ticket holds the lease on the tree.
func (p Pool) holds(ticket, tree string) bool {
	key := treekey.Of(tree)
	for _, l := range p.leases {
		if strings.EqualFold(l.Holder, ticket) && treekey.Of(l.Tree) == key {
			return true
		}
	}
	return false
}

// Gone is a worker row a resync deleted because its tree no longer holds the ticket's lease.
type Gone struct {
	Ticket string `json:"ticket"`
	Repo   string `json:"repo"`
	Tree   string `json:"tree"`
	Why    string `json:"why"`
}

// Stray is a lease a ticket holds on a tree no worker row names. Dispatch warns about it, adds no
// row for it and never returns it.
type Stray struct {
	Ticket string
	Repo   string
	Tree   string
}

// Resync is what a resync found and did.
type Resync struct {
	Gone   []Gone
	Strays []Stray
	// Changed is true when a row was written or deleted.
	Changed bool
}

// placed is a row and the tree a resync gives it, or no tree for a row it drops.
type placed struct{ ticket, repo, tree string }

// moved is a row that goes from a label the config no longer has to the label of the pool its
// tree is in.
type moved struct{ ticket, from, to, tree string }

// plan is the writes one resync makes: gone and drop are rows it deletes, fill rows it gives a tree.
type plan struct {
	gone   []Gone
	strays []Stray
	fill   []placed
	drop   []placed
	move   []moved
}

// Resync makes the rows agree with the pools that were read. A row whose tree no longer holds its
// ticket's lease is deleted with its records. A row with no tree whose ticket holds a lease gets
// that tree. A row with no tree and no lease is deleted once untracked says no ticket system lists its ticket. A row
// or lease that busy names has a launch or a return the pool read may predate, and is left alone.
// A row whose repo is not one of repos, the labels in the config, moves to the label of the pool
// that shows its ticket's lease on its tree.
func (s *Store) Resync(pools []Pool, busy func(ticket, tree string) bool, untracked func(ticket string) bool, repos []string) (Resync, error) {
	var p plan
	// Most resyncs change nothing, and those open no write transaction.
	err := s.file.View(func(tx *bolt.Tx) error {
		var err error
		p, err = planOf(tx, pools, busy, untracked, repos)
		return err
	})
	if err != nil || len(p.gone)+len(p.fill)+len(p.drop)+len(p.move) == 0 {
		return Resync{Strays: p.strays}, err
	}
	// The plan is made again inside the write, so it is the rows as they are when it commits.
	err = s.file.Update(func(tx *bolt.Tx) error {
		var err error
		if p, err = planOf(tx, pools, busy, untracked, repos); err != nil {
			return err
		}
		for _, row := range p.gone {
			if err := drop(tx, row.Ticket, row.Repo); err != nil {
				return err
			}
		}
		for _, row := range p.drop {
			if err := drop(tx, row.ticket, row.repo); err != nil {
				return err
			}
		}
		for _, row := range p.move {
			if err := rename(tx, row); err != nil {
				return err
			}
		}
		for _, row := range p.fill {
			worker := tx.Bucket(workersBucket).Bucket(key(row.ticket, row.repo))
			found, err := readRow(worker)
			if err != nil {
				return err
			}
			found.Tree = row.tree
			if err := writeRow(worker, found); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Resync{}, err
	}
	return Resync{Gone: p.gone, Strays: p.strays, Changed: true}, nil
}

func planOf(tx *bolt.Tx, pools []Pool, busy func(ticket, tree string) bool, untracked func(ticket string) bool, repos []string) (plan, error) {
	var p plan
	// A row under a label the config no longer has matches no pool below. A renamed label leaves
	// one, and it follows its tree to the label of the pool that shows the tree.
	err := each(tx, func(ticket, repo string, _ *bolt.Bucket, row Row) error {
		if slices.Contains(repos, repo) || row.Tree == "" {
			return nil
		}
		// A tree is in one pool, so the first pool that shows it decides.
		for _, pool := range pools {
			switch {
			case pool.holds(ticket, row.Tree):
				p.move = append(p.move, moved{ticket: ticket, from: repo, to: pool.repo, tree: row.Tree})
				return nil
			case pool.has(row.Tree) && !busy(ticket, row.Tree):
				p.gone = append(p.gone, Gone{
					Ticket: ticket, Repo: repo, Tree: row.Tree,
					Why: fmt.Sprintf("tree %s is no longer leased to %s", row.Tree, ticket),
				})
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return plan{}, err
	}
	for _, pool := range pools {
		named := map[string]bool{}
		var empty []string
		err := each(tx, func(ticket, repo string, _ *bolt.Bucket, row Row) error {
			if repo != pool.repo {
				return nil
			}
			switch {
			case row.Tree == "":
				empty = append(empty, ticket)
			case busy(ticket, row.Tree) || pool.holds(ticket, row.Tree):
				named[treekey.Of(row.Tree)] = true
			default:
				p.gone = append(p.gone, Gone{
					Ticket: ticket, Repo: repo, Tree: row.Tree,
					Why: fmt.Sprintf("tree %s is no longer leased to %s", row.Tree, ticket),
				})
			}
			return nil
		})
		if err != nil {
			return plan{}, err
		}
		// A moved row takes the place of a row with no tree, so that row is not filled as well.
		for _, row := range p.move {
			if row.to == pool.repo {
				named[treekey.Of(row.tree)] = true
				empty = slices.DeleteFunc(empty, func(ticket string) bool { return ticket == row.ticket })
			}
		}
		filled := map[string]bool{}
		for _, lease := range pool.leases {
			holder := strings.ToUpper(lease.Holder)
			if !IsTicket(lease.Holder) || named[treekey.Of(lease.Tree)] || busy(holder, lease.Tree) {
				continue
			}
			if has(empty, holder) && !filled[holder] {
				filled[holder] = true
				p.fill = append(p.fill, placed{ticket: holder, repo: pool.repo, tree: lease.Tree})
				continue
			}
			p.strays = append(p.strays, Stray{Ticket: lease.Holder, Repo: pool.repo, Tree: lease.Tree})
		}
		for _, ticket := range empty {
			if !filled[ticket] && !busy(ticket, "") && untracked(ticket) {
				p.drop = append(p.drop, placed{ticket: ticket, repo: pool.repo})
			}
		}
	}
	return p, nil
}

func has(tickets []string, ticket string) bool {
	for _, t := range tickets {
		if strings.EqualFold(t, ticket) {
			return true
		}
	}
	return false
}

// rename moves the row and its records to the label its tree's pool has. A row with no tree
// that a launch left under that label gives way, and one with a tree keeps its place.
func rename(tx *bolt.Tx, row moved) error {
	all := tx.Bucket(workersBucket)
	if held := all.Bucket(key(row.ticket, row.to)); held != nil {
		found, err := readRow(held)
		if err != nil || found.Tree != "" {
			return err
		}
		if err := all.DeleteBucket(key(row.ticket, row.to)); err != nil {
			return err
		}
	}
	to, err := all.CreateBucket(key(row.ticket, row.to))
	if err != nil {
		return err
	}
	if err := copyBucket(to, all.Bucket(key(row.ticket, row.from))); err != nil {
		return err
	}
	return all.DeleteBucket(key(row.ticket, row.from))
}

func copyBucket(to, from *bolt.Bucket) error {
	return from.ForEach(func(k, v []byte) error {
		if v != nil {
			return to.Put(k, v)
		}
		nested, err := to.CreateBucket(k)
		if err != nil {
			return err
		}
		return copyBucket(nested, from.Bucket(k))
	})
}
