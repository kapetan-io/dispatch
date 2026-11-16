// Package workers owns the worker rows in dispatch.db. A worker row is dispatch's record that it
// launched a worker for a ticket in a repo: the tree the worker runs in, its kickoff record, and the
// Stop records and closing messages of the sessions that ran there. A launch creates the row and a
// return deletes it with everything in it, so a record never outlives its worker.
//
// The store reads neither treehouse, git nor the multiplexer. Its callers pass in what a
// successful read showed.
package workers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/kapetan-io/dispatch/internal/dispatchdb"
	"github.com/kapetan-io/dispatch/internal/treekey"
)

var (
	workersBucket = []byte("workers")
	rowKey        = []byte("row")
	stopsBucket   = []byte("stops")
	closingBucket = []byte("closing")
)

// Local is where a worker runs until dispatch can run one anywhere else.
const Local = "local"

// ErrNoRow means the ticket has no worker row in the repo.
var ErrNoRow = errors.New("no worker row")

// ErrTreeTaken means another worker row names the tree.
var ErrTreeTaken = errors.New("another worker row names the tree")

// Kickoff is a worker's kickoff record: the kickoff dispatch last sent to start the worker and the
// session it went to. A launch that resumed a session which already took it typed the carry-on
// message instead, and the record still holds the kickoff. It stays until the return.
type Kickoff struct {
	Text string `json:"text"`
	// At is when the launch that typed it began, in milliseconds since the epoch.
	At int64 `json:"at"`
	// Open is true from before the harness starts until the launch has typed the kickoff or the
	// carry-on message, so only a launch that died in between leaves it true.
	Open bool `json:"open,omitempty"`
	// Session is the session the kickoff was typed into. Empty means it is not known.
	Session string `json:"session,omitempty"`
	// Taken is true when Session took this kickoff before the launch that wrote the record, which
	// resumed the session and typed the carry-on message instead. An open record then waits only on
	// that message, and a later resume of the session still knows the kickoff ran.
	Taken bool `json:"taken,omitempty"`
}

// Row is what dispatch records about one worker.
type Row struct {
	// Where says where the worker runs. Local is its only value.
	Where string `json:"where"`
	// Tree is the tree the worker runs in, empty until the launch has leased one.
	Tree string `json:"tree,omitempty"`
	// Since is when the row was made, or when its latest launch began while it had no tree, in
	// milliseconds since the epoch. A Stop that ended before it
	// belongs to an earlier worker.
	Since   int64    `json:"since"`
	Kickoff *Kickoff `json:"kickoff,omitempty"`
}

// Worker is a worker row with the records kept under it.
type Worker struct {
	Ticket string `json:"ticket"`
	// Repo is the label of the worker's [repos.<label>] table.
	Repo string `json:"repo"`
	Row  Row    `json:"row"`
	// Stops and Closing are keyed by SessionKey.
	Stops   map[string]Record  `json:"stops,omitempty"`
	Closing map[string]Closing `json:"closing,omitempty"`
	// Unreadable lists the session keys whose Stop record is stored but cannot be read.
	Unreadable []string `json:"unreadable,omitempty"`
}

// Snapshot is every worker row at one moment.
type Snapshot []Worker

// SessionKey is the key a session's Stop record and closing message are stored under. It is the
// hash the session log files are named by, because the import copies files named by it and cannot
// recover the id.
func SessionKey(session string) string { return treekey.Hash(session) }

// Snapshot is the rows themselves, so rows a running dispatch sent read like its open database.
func (s Snapshot) Snapshot() (Snapshot, error) { return s, nil }

// Of lists the ticket's workers, one per repo.
func (s Snapshot) Of(ticket string) []Worker {
	var found []Worker
	for _, w := range s {
		if strings.EqualFold(w.Ticket, ticket) {
			found = append(found, w)
		}
	}
	return found
}

// OnTree is the worker whose row names the tree.
func (s Snapshot) OnTree(tree string) (Worker, bool) {
	if tree == "" {
		return Worker{}, false
	}
	key := treekey.Of(tree)
	for _, w := range s {
		if w.Row.Tree != "" && treekey.Of(w.Row.Tree) == key {
			return w, true
		}
	}
	return Worker{}, false
}

// Stop is the latest Stop of session. With no session known it is the latest Stop of any session
// of the worker. A missing or unreadable record is reported as absent.
func (w Worker) Stop(session string) (Record, bool) {
	if session != "" {
		r, ok := w.Stops[SessionKey(session)]
		return r, ok
	}
	var latest Record
	for _, r := range w.Stops {
		if r.At.After(latest.At) {
			latest = r
		}
	}
	return latest, !latest.At.IsZero()
}

// StopUnreadable reports whether the Stop record of session, or of any session when it is empty,
// is stored and cannot be read. An unreadable record might be the newest Stop, so that is unknown,
// not absent, and a caller that would act on a missing Stop holds instead.
func (w Worker) StopUnreadable(session string) bool {
	if session == "" {
		return len(w.Unreadable) > 0
	}
	return slices.Contains(w.Unreadable, SessionKey(session))
}

// ClosingAt is the closing message of session's turn that ended at at. A missing message, and one
// that belongs to another turn, are reported as absent.
func (w Worker) ClosingAt(session string, at int64) (Closing, bool) {
	c, ok := w.Closing[SessionKey(session)]
	return c, ok && c.At == at
}

// Unconfirmed is when the launch whose kickoff is still open began, and false once the kickoff is
// closed or a Stop of session is later. An unreadable Stop counts as later, because it may be.
func (w Worker) Unconfirmed(session string) (time.Time, bool) {
	k := w.Row.Kickoff
	if k == nil || !k.Open || w.StopUnreadable(session) {
		return time.Time{}, false
	}
	at := time.UnixMilli(k.At)
	if stop, ok := w.Stop(session); ok && stop.At.After(at) {
		return time.Time{}, false
	}
	return at, true
}

// Store reads and writes the worker rows. Every write is one transaction.
type Store struct {
	file *dispatchdb.File
}

// New returns the store over the open database.
func New(file *dispatchdb.File) *Store { return &Store{file: file} }

// Imported reports whether the repo's workers were imported from the files an earlier dispatch kept.
func (s *Store) Imported(repo string) (bool, error) { return s.file.WorkersImported(repo) }

func key(ticket, repo string) []byte {
	return []byte(strings.ToUpper(ticket) + "\x00" + repo)
}

func splitKey(k []byte) (ticket, repo string) {
	ticket, repo, _ = strings.Cut(string(k), "\x00")
	return ticket, repo
}

func readRow(worker *bolt.Bucket) (Row, error) {
	var row Row
	if err := json.Unmarshal(worker.Get(rowKey), &row); err != nil {
		return row, fmt.Errorf("its row is not readable: %w", err)
	}
	return row, nil
}

func writeRow(worker *bolt.Bucket, row Row) error {
	raw, err := json.Marshal(row)
	if err != nil {
		return err
	}
	return worker.Put(rowKey, raw)
}

// each calls visit for every worker row.
func each(tx *bolt.Tx, visit func(ticket, repo string, worker *bolt.Bucket, row Row) error) error {
	all := tx.Bucket(workersBucket)
	if all == nil {
		return nil
	}
	return all.ForEachBucket(func(k []byte) error {
		ticket, repo := splitKey(k)
		worker := all.Bucket(k)
		row, err := readRow(worker)
		if err != nil {
			return fmt.Errorf("worker of %s in %s: %w", ticket, repo, err)
		}
		return visit(ticket, repo, worker, row)
	})
}

// onTree finds the worker whose row names the tree.
func onTree(tx *bolt.Tx, tree string) (ticket, repo string, worker *bolt.Bucket, row Row, err error) {
	if tree == "" {
		return "", "", nil, Row{}, nil
	}
	want := treekey.Of(tree)
	err = each(tx, func(t, r string, b *bolt.Bucket, found Row) error {
		if worker == nil && found.Tree != "" && treekey.Of(found.Tree) == want {
			ticket, repo, worker, row = t, r, b, found
		}
		return nil
	})
	return ticket, repo, worker, row, err
}

// change rewrites the ticket's row in the repo.
func (s *Store) change(ticket, repo string, edit func(tx *bolt.Tx, row *Row) error) error {
	return s.file.Update(func(tx *bolt.Tx) error {
		all := tx.Bucket(workersBucket)
		if all == nil || all.Bucket(key(ticket, repo)) == nil {
			return fmt.Errorf("%s in %s: %w", ticket, repo, ErrNoRow)
		}
		worker := all.Bucket(key(ticket, repo))
		row, err := readRow(worker)
		if err != nil {
			return fmt.Errorf("worker of %s in %s: %w", ticket, repo, err)
		}
		if err := edit(tx, &row); err != nil {
			return err
		}
		return writeRow(worker, row)
	})
}

// create adds the row when the ticket has none in the repo.
func create(tx *bolt.Tx, ticket, repo string, row Row) (*bolt.Bucket, bool, error) {
	all, err := tx.CreateBucketIfNotExists(workersBucket)
	if err != nil {
		return nil, false, err
	}
	if worker := all.Bucket(key(ticket, repo)); worker != nil {
		return worker, false, nil
	}
	worker, err := all.CreateBucket(key(ticket, repo))
	if err != nil {
		return nil, false, err
	}
	return worker, true, writeRow(worker, row)
}

// Ensure creates the ticket's worker row in the repo, with no tree, when it has none. A row a
// failed launch left with no tree is dated again, so a Stop from the tree's last worker is older
// than it.
func (s *Store) Ensure(ticket, repo string, at time.Time) error {
	return s.file.Update(func(tx *bolt.Tx) error {
		worker, made, err := create(tx, ticket, repo, Row{Where: Local, Since: at.UnixMilli()})
		if err != nil || made {
			return err
		}
		row, err := readRow(worker)
		if err != nil || row.Tree != "" {
			return err
		}
		row.Since = at.UnixMilli()
		return writeRow(worker, row)
	})
}

// RecordTree records the tree the ticket's worker runs in. It fails with ErrTreeTaken when
// another row names the tree, because a tree holds one worker.
func (s *Store) RecordTree(ticket, repo, tree string) error {
	return s.change(ticket, repo, func(tx *bolt.Tx, row *Row) error {
		if err := treeTaken(tx, ticket, repo, tree); err != nil {
			return err
		}
		row.Tree = tree
		return nil
	})
}

// treeTaken fails when a row other than the ticket's in the repo names the tree.
func treeTaken(tx *bolt.Tx, ticket, repo, tree string) error {
	holder, in, worker, _, err := onTree(tx, tree)
	if err != nil {
		return err
	}
	if worker != nil && !bytes.Equal(key(holder, in), key(ticket, repo)) {
		return fmt.Errorf("%s is recorded as the tree of %s in %s: %w", tree, holder, in, ErrTreeTaken)
	}
	return nil
}

// ForgetTree clears the tree of the ticket's row, for a launch that gave back the lease it took.
func (s *Store) ForgetTree(ticket, repo string) error {
	return s.change(ticket, repo, func(_ *bolt.Tx, row *Row) error {
		row.Tree = ""
		return nil
	})
}

// KickoffOf is the ticket's kickoff record in the repo, nil when it has none.
func (s *Store) KickoffOf(ticket, repo string) (*Kickoff, error) {
	var found *Kickoff
	err := s.file.View(func(tx *bolt.Tx) error {
		all := tx.Bucket(workersBucket)
		if all == nil || all.Bucket(key(ticket, repo)) == nil {
			return nil
		}
		row, err := readRow(all.Bucket(key(ticket, repo)))
		found = row.Kickoff
		return err
	})
	return found, err
}

// SetKickoff replaces the whole kickoff record of the ticket's worker. Nil deletes it.
func (s *Store) SetKickoff(ticket, repo string, k *Kickoff) error {
	return s.change(ticket, repo, func(_ *bolt.Tx, row *Row) error {
		row.Kickoff = k
		return nil
	})
}

// RecordSession records the session the kickoff is typed into, and whether that session took the
// kickoff in an earlier launch.
func (s *Store) RecordSession(ticket, repo, session string, taken bool) error {
	return s.change(ticket, repo, func(_ *bolt.Tx, row *Row) error {
		if row.Kickoff != nil {
			row.Kickoff.Session, row.Kickoff.Taken = session, taken
		}
		return nil
	})
}

// CloseKickoff closes the kickoff record of the ticket's worker, keeping its text, time and session.
func (s *Store) CloseKickoff(ticket, repo string) error {
	return s.change(ticket, repo, func(_ *bolt.Tx, row *Row) error {
		if row.Kickoff != nil {
			row.Kickoff.Open = false
		}
		return nil
	})
}

// CloseKickoffOn closes the kickoff record of the worker on the tree. A tree no row names has none.
func (s *Store) CloseKickoffOn(tree string) error {
	return s.file.Update(func(tx *bolt.Tx) error {
		_, _, worker, row, err := onTree(tx, tree)
		if err != nil || worker == nil || row.Kickoff == nil || !row.Kickoff.Open {
			return err
		}
		row.Kickoff.Open = false
		return writeRow(worker, row)
	})
}

// Delete removes the ticket's worker row in the repo with its Stop records and closing messages.
func (s *Store) Delete(ticket, repo string) error {
	return s.file.Update(func(tx *bolt.Tx) error { return drop(tx, ticket, repo) })
}

func drop(tx *bolt.Tx, ticket, repo string) error {
	all := tx.Bucket(workersBucket)
	if all == nil || all.Bucket(key(ticket, repo)) == nil {
		return nil
	}
	return all.DeleteBucket(key(ticket, repo))
}

// OnTree is the worker whose row names the tree, with its Stop records and closing messages.
func (s *Store) OnTree(tree string) (Worker, bool, error) {
	var found Worker
	var ok bool
	err := s.file.View(func(tx *bolt.Tx) error {
		ticket, repo, worker, row, err := onTree(tx, tree)
		if err != nil || worker == nil {
			return err
		}
		found, ok = read(ticket, repo, worker, row), true
		return nil
	})
	return found, ok, err
}

// RecordStop stores the Stop a session in the tree reported, and what the worker said as the turn
// ended. It reports false, storing nothing, when no row names the tree, when the Stop ended before
// the row was made (it is an earlier worker's), and when the session's stored Stop is newer. A
// stored Stop ahead of now came from a clock that has since stepped back, and shuts out no report.
func (s *Store) RecordStop(tree, session string, stop Record, closing Closing, now time.Time) (bool, error) {
	var stored bool
	err := s.file.Update(func(tx *bolt.Tx) error {
		_, _, worker, row, err := onTree(tx, tree)
		if err != nil || worker == nil || stop.At.UnixMilli() < row.Since {
			return err
		}
		stops, err := worker.CreateBucketIfNotExists(stopsBucket)
		if err != nil {
			return err
		}
		var held Record
		if raw := stops.Get([]byte(SessionKey(session))); raw != nil && json.Unmarshal(raw, &held) == nil {
			if stop.At.Before(held.At) && !held.At.After(now) {
				return nil
			}
		}
		if err := put(stops, session, stop); err != nil {
			return err
		}
		messages, err := worker.CreateBucketIfNotExists(closingBucket)
		if err != nil {
			return err
		}
		stored = true
		return put(messages, session, closing)
	})
	return stored && err == nil, err
}

func put(bucket *bolt.Bucket, session string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return bucket.Put([]byte(SessionKey(session)), raw)
}

// Snapshot reads every worker row with its Stop records. It leaves the closing messages out,
// because a read of the world decides from none of them and each can be 64 KiB. OnTree reads them.
func (s *Store) Snapshot() (Snapshot, error) {
	var snap Snapshot
	err := s.file.View(func(tx *bolt.Tx) error {
		return each(tx, func(ticket, repo string, worker *bolt.Bucket, row Row) error {
			found := read(ticket, repo, worker, row)
			found.Closing = nil
			snap = append(snap, found)
			return nil
		})
	})
	return snap, err
}

func read(ticket, repo string, worker *bolt.Bucket, row Row) Worker {
	w := Worker{Ticket: ticket, Repo: repo, Row: row}
	if stops := worker.Bucket(stopsBucket); stops != nil {
		_ = stops.ForEach(func(k, raw []byte) error {
			var rec Record
			if json.Unmarshal(raw, &rec) != nil || rec.At.IsZero() {
				w.Unreadable = append(w.Unreadable, string(k))
				return nil
			}
			if w.Stops == nil {
				w.Stops = map[string]Record{}
			}
			w.Stops[string(k)] = rec
			return nil
		})
	}
	if messages := worker.Bucket(closingBucket); messages != nil {
		_ = messages.ForEach(func(k, raw []byte) error {
			var c Closing
			if json.Unmarshal(raw, &c) != nil {
				return nil
			}
			if w.Closing == nil {
				w.Closing = map[string]Closing{}
			}
			w.Closing[string(k)] = c
			return nil
		})
	}
	return w
}

// ticketID is the shape of a ticket id, which tells a ticket's lease from a person's.
var ticketID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*-[0-9]+$`)

// IsTicket reports whether a lease holder is a ticket id.
func IsTicket(holder string) bool { return ticketID.MatchString(holder) }
