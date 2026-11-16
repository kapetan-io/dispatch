// Package ticketbranch owns the branch rows in dispatch.db. A branch row is dispatch's record of
// the branch a ticket's work is on in one repo, with the ticket's stack record when it is stacked.
// A launch writes the row before it creates the branch, and dispatch never follows a branch
// renamed or deleted by hand: `dispatch ticket branch` records the new name.
//
// The store reads neither git nor treehouse. Its callers pass in what a successful read showed.
package ticketbranch

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/kapetan-io/dispatch/internal/dispatchdb"
)

var branchesBucket = []byte("branches")

// ErrTaken means another ticket's row names the branch in the repo, and a branch carries one
// ticket's work.
var ErrTaken = errors.New("another ticket's branch row names the branch")

// takenError names the ticket whose row holds the branch, and is ErrTaken.
type takenError struct{ branch, repo, owner, ticket string }

func (e takenError) Error() string {
	return fmt.Sprintf("branch %s in %s belongs to %s, not %s", e.branch, e.repo, e.owner, e.ticket)
}

func (takenError) Is(target error) bool { return target == ErrTaken }

// Record is a stack record: where a stacked ticket's branch started and from whose PR.
type Record struct {
	// Parent is the stack parent's ticket id.
	Parent string
	// ForkPoint is the full sha the branch started at.
	ForkPoint string
}

// Records is what the world holds of one repo's branch rows, keyed by branch name, in the shape
// history has always written.
type Records struct {
	Whole map[string]Record `json:"whole,omitempty"`
	// Partial is never set by a read of branch rows. It stays so history written while stack
	// records lived in git config, where a record could be half written, still replays.
	Partial map[string]bool `json:"partial,omitempty"`
	// Tickets maps a branch to the ticket whose row names it.
	Tickets map[string]string `json:"tickets,omitempty"`
}

// Of is the stack record of branch. Partial is true when history recorded only one of its two
// keys, which callers treat as unknown. A branch with no record is neither found nor partial.
func (r Records) Of(branch string) (rec Record, found, partial bool) {
	if r.Partial[branch] {
		return Record{}, false, true
	}
	rec, found = r.Whole[branch]
	return rec, found, false
}

// BranchOf is the branch whose row names id. It is false when no branch or several do, since
// two would be a guess.
func (r Records) BranchOf(id string) (string, bool) {
	var found []string
	for branch, owner := range r.Tickets {
		if strings.EqualFold(owner, id) {
			found = append(found, branch)
		}
	}
	if len(found) != 1 {
		return "", false
	}
	return found[0], true
}

// Row is what dispatch records about a ticket's branch in one repo.
type Row struct {
	Name  string  `json:"name"`
	Stack *Record `json:"stack,omitempty"`
	// Uncreated is true from the launch that recorded the branch until a read shows it exists, so
	// a launch killed before it created the branch is told from a branch deleted by hand.
	Uncreated bool `json:"uncreated,omitempty"`
}

// Branch is a branch row with the ticket and repo it is kept under.
type Branch struct {
	Ticket string `json:"ticket"`
	// Repo is the label of the ticket's [repos.<label>] table.
	Repo string `json:"repo"`
	Row  Row    `json:"row"`
}

// Snapshot is every branch row at one moment, with the repos whose branches are imported.
type Snapshot struct {
	Rows []Branch `json:"rows,omitempty"`
	// Imported holds the repo labels whose ticket branch keys and stack records were copied in
	// from git config. Until then a repo's rows are not all there.
	Imported map[string]bool `json:"imported,omitempty"`
}

// Snapshot is the rows themselves, so rows a running dispatch sent read like its open database.
func (s Snapshot) Snapshot() (Snapshot, error) { return s, nil }

// Records is the repo's rows in the world's shape. A row marked "not created yet" names its branch
// but shows no stack record, since a launch that failed before it created the branch left a record
// of a stack that does not exist.
func (s Snapshot) Records(repo string) Records {
	recs := Records{Whole: map[string]Record{}, Partial: map[string]bool{}, Tickets: map[string]string{}}
	for _, b := range s.Rows {
		if b.Repo != repo {
			continue
		}
		recs.Tickets[b.Row.Name] = b.Ticket
		if b.Row.Stack != nil && !b.Row.Uncreated {
			recs.Whole[b.Row.Name] = *b.Row.Stack
		}
	}
	return recs
}

// Of is the ticket's row in the repo.
func (s Snapshot) Of(ticket, repo string) (Row, bool) {
	for _, b := range s.Rows {
		if strings.EqualFold(b.Ticket, ticket) && b.Repo == repo {
			return b.Row, true
		}
	}
	return Row{}, false
}

// Store reads and writes the branch rows. Every write is one transaction.
type Store struct {
	file *dispatchdb.File
}

// New returns the store over the open database.
func New(file *dispatchdb.File) *Store { return &Store{file: file} }

func key(ticket, repo string) []byte {
	return []byte(strings.ToUpper(ticket) + "\x00" + repo)
}

func splitKey(k []byte) (ticket, repo string) {
	ticket, repo, _ = strings.Cut(string(k), "\x00")
	return ticket, repo
}

// each calls visit for every branch row.
func each(tx *bolt.Tx, visit func(ticket, repo string, row Row) error) error {
	all := tx.Bucket(branchesBucket)
	if all == nil {
		return nil
	}
	return all.ForEach(func(k, raw []byte) error {
		ticket, repo := splitKey(k)
		var row Row
		if err := json.Unmarshal(raw, &row); err != nil {
			return fmt.Errorf("the branch row of %s in %s is not readable: %w", ticket, repo, err)
		}
		return visit(ticket, repo, row)
	})
}

// taken fails with ErrTaken when a row other than the ticket's in the repo names the branch.
func taken(tx *bolt.Tx, ticket, repo, name string) error {
	return each(tx, func(t, r string, row Row) error {
		if r == repo && row.Name == name && !strings.EqualFold(t, ticket) {
			return takenError{branch: name, repo: repo, owner: t, ticket: ticket}
		}
		return nil
	})
}

func put(tx *bolt.Tx, ticket, repo string, row Row) error {
	raw, err := json.Marshal(row)
	if err != nil {
		return err
	}
	all, err := tx.CreateBucketIfNotExists(branchesBucket)
	if err != nil {
		return err
	}
	return all.Put(key(ticket, repo), raw)
}

func get(tx *bolt.Tx, ticket, repo string) (Row, bool, error) {
	all := tx.Bucket(branchesBucket)
	if all == nil {
		return Row{}, false, nil
	}
	raw := all.Get(key(ticket, repo))
	if raw == nil {
		return Row{}, false, nil
	}
	var row Row
	if err := json.Unmarshal(raw, &row); err != nil {
		return row, false, fmt.Errorf("the branch row of %s in %s is not readable: %w", ticket, repo, err)
	}
	return row, true, nil
}

// Snapshot reads every branch row and the repos whose branches are imported.
func (s *Store) Snapshot() (Snapshot, error) {
	snap := Snapshot{Imported: map[string]bool{}}
	err := s.file.View(func(tx *bolt.Tx) error {
		markers, err := dispatchdb.Markers(tx)
		if err != nil {
			return err
		}
		for repo, m := range markers {
			if m.Branches != 0 {
				snap.Imported[repo] = true
			}
		}
		return each(tx, func(ticket, repo string, row Row) error {
			snap.Rows = append(snap.Rows, Branch{Ticket: ticket, Repo: repo, Row: row})
			return nil
		})
	})
	return snap, err
}

// Of is the ticket's row in the repo.
func (s *Store) Of(ticket, repo string) (Row, bool, error) {
	var row Row
	var found bool
	err := s.file.View(func(tx *bolt.Tx) (err error) {
		row, found, err = get(tx, ticket, repo)
		return err
	})
	return row, found, err
}

// Record writes the ticket's row in the repo, replacing the one it had. It fails with ErrTaken
// when another ticket's row names the branch.
func (s *Store) Record(ticket, repo string, row Row) error {
	return s.file.Update(func(tx *bolt.Tx) error {
		if err := taken(tx, ticket, repo, row.Name); err != nil {
			return err
		}
		return put(tx, ticket, repo, row)
	})
}

// Rename records name as the ticket's branch in the repo, which a caller has seen exists. The
// stack record stays, except on a row marked not created, whose stack record says where a branch
// that was never forked would have started. A ticket with no row gets one. It fails with ErrTaken when another ticket's
// row names the branch.
func (s *Store) Rename(ticket, repo, name string) error {
	return s.file.Update(func(tx *bolt.Tx) error {
		if err := taken(tx, ticket, repo, name); err != nil {
			return err
		}
		row, _, err := get(tx, ticket, repo)
		if err != nil {
			return err
		}
		if row.Uncreated {
			row.Stack = nil
		}
		row.Name, row.Uncreated = name, false
		return put(tx, ticket, repo, row)
	})
}

// Unstack ends the ticket's stack record in the repo. A ticket with no row or no record is left
// as it is.
func (s *Store) Unstack(ticket, repo string) error {
	row, found, err := s.Of(ticket, repo)
	if err != nil || !found || row.Stack == nil {
		return err
	}
	return s.file.Update(func(tx *bolt.Tx) error {
		row, found, err := get(tx, ticket, repo)
		if err != nil || !found || row.Stack == nil {
			return err
		}
		row.Stack = nil
		return put(tx, ticket, repo, row)
	})
}

// Listing is the branches of one repo's clone, local or on origin, from a read that succeeded. A
// failed read builds none, so a row never changes because of a read that failed.
type Listing struct {
	repo  string
	names map[string]bool
}

// NewListing is the listing of the repo with this label, from a read that succeeded.
func NewListing(repo string, names map[string]bool) Listing {
	return Listing{repo: repo, names: names}
}

// Missing is a branch row whose branch is gone while its ticket is unfinished. Dispatch warns
// about it and refuses to launch the ticket until a person names its branch.
type Missing struct {
	Ticket string
	Repo   string
	Name   string
}

// Resync is what a resync found and did.
type Resync struct {
	Missing []Missing
	// Changed is true when a row was written or deleted.
	Changed bool
}

// plan is the writes one resync makes.
type plan struct {
	missing []Missing
	created []Branch
	drop    []Branch
}

// Resync makes the rows agree with the listings that were read. A row whose branch a listing
// shows loses its "not created yet" mark. A row whose branch is gone is deleted once untracked
// says no ticket system lists its ticket, and is reported as missing while it is listed and the
// row is not marked. A row that busy names has a launch the listing may predate, and is left alone.
func (s *Store) Resync(listings []Listing, busy func(ticket string) bool, untracked func(ticket string) bool) (Resync, error) {
	var p plan
	err := s.file.View(func(tx *bolt.Tx) (err error) {
		p, err = planOf(tx, listings, busy, untracked)
		return err
	})
	if err != nil || len(p.created)+len(p.drop) == 0 {
		return Resync{Missing: p.missing}, err
	}
	// The plan is made again inside the write, so it is the rows as they are when it commits.
	err = s.file.Update(func(tx *bolt.Tx) error {
		var err error
		if p, err = planOf(tx, listings, busy, untracked); err != nil {
			return err
		}
		for _, b := range p.created {
			b.Row.Uncreated = false
			if err := put(tx, b.Ticket, b.Repo, b.Row); err != nil {
				return err
			}
		}
		all := tx.Bucket(branchesBucket)
		for _, b := range p.drop {
			if err := all.Delete(key(b.Ticket, b.Repo)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Resync{}, err
	}
	return Resync{Missing: p.missing, Changed: len(p.created)+len(p.drop) > 0}, nil
}

func planOf(tx *bolt.Tx, listings []Listing, busy func(string) bool, untracked func(string) bool) (plan, error) {
	var p plan
	err := each(tx, func(ticket, repo string, row Row) error {
		i := slices.IndexFunc(listings, func(l Listing) bool { return l.repo == repo })
		if i < 0 || busy(ticket) {
			return nil
		}
		b := Branch{Ticket: ticket, Repo: repo, Row: row}
		switch {
		case listings[i].names[row.Name]:
			if row.Uncreated {
				p.created = append(p.created, b)
			}
		case untracked(ticket):
			p.drop = append(p.drop, b)
		case !row.Uncreated:
			p.missing = append(p.missing, Missing{Ticket: ticket, Repo: repo, Name: row.Name})
		}
		return nil
	})
	return p, err
}

// Import adds, once for the repo, the rows found in its clone's git config, each only where the
// ticket has no row and no other row names the branch, and returns the branches it wrote a row
// for. Only their keys go on the repo's marker to be unset, written in the same transaction, since
// the keys of any other branch are the one copy of what they say. A second import adds nothing.
func (s *Store) Import(repo string, rows []Branch, at time.Time) (written []string, err error) {
	err = s.file.Update(func(tx *bolt.Tx) error {
		written = nil
		m, err := dispatchdb.ReadMarker(tx, repo)
		if err != nil || m.Branches != 0 {
			return err
		}
		for _, b := range rows {
			if _, found, err := get(tx, b.Ticket, repo); err != nil || found {
				if err != nil {
					return err
				}
				continue
			}
			if err := taken(tx, b.Ticket, repo, b.Row.Name); errors.Is(err, ErrTaken) {
				continue
			} else if err != nil {
				return err
			}
			if err := put(tx, b.Ticket, repo, b.Row); err != nil {
				return err
			}
			written = append(written, b.Row.Name)
		}
		return dispatchdb.MarkBranchesImported(tx, repo, at, written)
	})
	return written, err
}

// Imported reports whether the repo's branches were imported.
func (s *Store) Imported(repo string) (bool, error) { return s.file.BranchesImported(repo) }

// ToUnset lists the repo's branches whose git config keys an import found and are not unset yet.
func (s *Store) ToUnset(repo string) ([]string, error) {
	var list []string
	err := s.file.View(func(tx *bolt.Tx) error {
		m, err := dispatchdb.ReadMarker(tx, repo)
		list = m.Unset
		return err
	})
	return list, err
}

// KeysUnset takes the branches whose keys are gone off the repo's list.
func (s *Store) KeysUnset(repo string, branches []string) error {
	return s.file.KeysUnset(repo, branches)
}
