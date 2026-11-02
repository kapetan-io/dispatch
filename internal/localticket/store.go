package localticket

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/kapetan-io/dispatch/internal/ticket"
)

// Bucket names. Only seeding and the read path touch statusBucket.
var (
	statusBucket  = []byte("statuses")
	ticketBucket  = []byte("tickets")
	commentBucket = []byte("comments")
)

// defaultLockTimeout is how long Open waits for a file another process holds.
const defaultLockTimeout = 5 * time.Second

// Options is what Open needs.
type Options struct {
	Path string
	// Repos are the labels of the run's [repos.<label>] tables.
	Repos []string
	// Now is the run's clock.
	Now func() time.Time
	// Highest is the highest LOC-<n> that history and leases name. A file whose next number is at or
	// below it is refused, because a replaced or restored file would give the id a second time.
	Highest uint64
	// LockTimeout defaults to defaultLockTimeout.
	LockTimeout time.Duration
}

// Store owns the database file and every write to it. It has no delete method.
type Store struct {
	db       *bolt.DB
	repos    []string
	now      func() time.Time
	statuses ticket.Statuses
}

func refuse(format string, args ...any) error {
	return &Error{Kind: ErrRefused, Msg: fmt.Sprintf(format, args...)}
}

func notFound(id string) error {
	return &Error{Kind: ErrNotFound, Msg: fmt.Sprintf("%s does not exist", id)}
}

func unreadable(format string, args ...any) error {
	return &Error{Kind: ErrUnreadable, Msg: fmt.Sprintf(format, args...)}
}

// Open opens the file for dispatch run. It creates a missing file with mode 0600, seeds the default
// statuses into a file that has none, and checks the file. A file another process holds past the
// lock timeout, or one that fails a check, is an error naming the file and the problem.
func Open(o Options) (*Store, error) {
	timeout := o.LockTimeout
	if timeout <= 0 {
		timeout = defaultLockTimeout
	}
	if err := os.MkdirAll(filepath.Dir(o.Path), 0o700); err != nil {
		return nil, fmt.Errorf("while creating the directory of %s: %w", o.Path, err)
	}
	_, statErr := os.Stat(o.Path)
	existed := statErr == nil
	db, err := bolt.Open(o.Path, 0o600, &bolt.Options{Timeout: timeout})
	if errors.Is(err, bolt.ErrTimeout) {
		return nil, &Error{Kind: ErrHeld, Msg: fmt.Sprintf("%s is held by another process after %s; another dispatch run or a file sync tool may hold it", o.Path, timeout)}
	}
	if err != nil {
		return nil, fmt.Errorf("while opening %s: %w", o.Path, err)
	}
	s := &Store{db: db, repos: o.Repos, now: o.Now}
	if err := s.start(o.Highest); err != nil {
		_ = db.Close()
		// A refused start leaves no file it made itself.
		if !existed {
			_ = os.Remove(o.Path)
		}
		return nil, fmt.Errorf("local tickets in %s: %w", o.Path, err)
	}
	return s, nil
}

func (s *Store) start(highest uint64) (err error) {
	defer recoverTo(&err)
	// A file that needs nothing is only read, so a refused start leaves it as it was.
	var seed bool
	err = s.db.View(func(tx *bolt.Tx) error {
		sb := tx.Bucket(statusBucket)
		seed = sb == nil || tx.Bucket(ticketBucket) == nil || tx.Bucket(commentBucket) == nil
		if !seed {
			k, _ := sb.Cursor().First()
			seed = k == nil
		}
		return nil
	})
	if err != nil {
		return err
	}
	if seed {
		err = s.db.Update(func(tx *bolt.Tx) error {
			for _, name := range [][]byte{statusBucket, ticketBucket, commentBucket} {
				if _, err := tx.CreateBucketIfNotExists(name); err != nil {
					return err
				}
			}
			// Seeding is all or nothing and runs only into a file with no statuses.
			sb := tx.Bucket(statusBucket)
			if k, _ := sb.Cursor().First(); k != nil {
				return nil
			}
			for i, st := range ticket.DefaultStatuses() {
				raw, err := json.Marshal(st)
				if err != nil {
					return err
				}
				if err := sb.Put(key(uint64(i+1)), raw); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return s.db.View(func(tx *bolt.Tx) error {
		statuses, err := readStatuses(tx)
		if err != nil {
			return err
		}
		if err := statuses.Check(); err != nil {
			return err
		}
		s.statuses = statuses
		return check(tx, statuses, highest)
	})
}

// Close releases the file.
func (s *Store) Close() error { return s.db.Close() }

// Statuses are the database's statuses, in order. They are read once at Open, because dispatch never changes them.
func (s *Store) Statuses() ticket.Statuses { return slices.Clone(s.statuses) }

// Highest is the highest ticket number the file has issued.
func (s *Store) Highest() (n uint64, err error) {
	defer recoverTo(&err)
	err = s.db.View(func(tx *bolt.Tx) error {
		n = tx.Bucket(ticketBucket).Sequence()
		return nil
	})
	return n, err
}

// Create files a ticket and returns its id.
func (s *Store) Create(d Draft) (id string, err error) {
	defer recoverTo(&err)
	err = s.db.Update(func(tx *bolt.Tx) error {
		tb := tx.Bucket(ticketBucket)
		n, err := tb.NextSequence()
		if err != nil {
			return err
		}
		id = ticket.LocalID(n)
		status := s.statuses.With(ticket.Todo)[0]
		if d.Status != "" {
			var ok bool
			if status, ok = s.statuses.Find(d.Status); !ok {
				return s.noStatus(d.Status)
			}
		}
		title := strings.TrimSpace(d.Title)
		if title == "" {
			return refuse("a ticket needs a title")
		}
		repo, err := s.repo(d.Repo)
		if err != nil {
			return err
		}
		if d.StartPR < 0 {
			return refuse("%s has start PR %d; a PR number is positive", id, d.StartPR)
		}
		labels, err := cleanLabels(d.Labels)
		if err != nil {
			return err
		}
		attrs, err := cleanAttributes(d.Attributes)
		if err != nil {
			return err
		}
		at := s.stamp()
		t := stored{
			Title: title, Description: d.Description, Status: status.Name, Repo: repo,
			Branch: branch(n, title), Labels: labels, Attributes: attrs, StartPR: d.StartPR,
			FiledBy: d.FiledBy, CreatedAt: at, StatusAt: at,
		}
		if t.FiledBy == "" {
			t.FiledBy = "you"
		}
		if d.Parent != "" {
			if t.Parent, err = link(tb, id, d.Parent); err != nil {
				return err
			}
		}
		if t.BlockedBy, err = links(tb, id, d.BlockedBy); err != nil {
			return err
		}
		return put(tb, n, t)
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

// Edit changes the fields e names on the ticket id.
func (s *Store) Edit(id string, e Edit) (err error) {
	defer recoverTo(&err)
	return s.db.Update(func(tx *bolt.Tx) error {
		tb := tx.Bucket(ticketBucket)
		n, t, err := get(tb, id)
		if err != nil {
			return err
		}
		if e.Title != nil {
			if t.Title = strings.TrimSpace(*e.Title); t.Title == "" {
				return refuse("%s needs a title", id)
			}
		}
		if e.Description != nil {
			t.Description = *e.Description
		}
		if e.Repo != nil {
			if t.Repo, err = s.repo(*e.Repo); err != nil {
				return err
			}
		}
		if e.StartPR != nil {
			if *e.StartPR < 0 {
				return refuse("%s has start PR %d; a PR number is positive", id, *e.StartPR)
			}
			t.StartPR = *e.StartPR
		}
		add, err := cleanLabels(e.AddLabels)
		if err != nil {
			return err
		}
		for _, l := range add {
			if !hasFold(t.Labels, l) {
				t.Labels = append(t.Labels, l)
			}
		}
		for _, l := range e.RemoveLabels {
			t.Labels = slices.DeleteFunc(t.Labels, func(have string) bool { return strings.EqualFold(have, strings.TrimSpace(l)) })
		}
		set, err := cleanAttributes(e.SetAttributes)
		if err != nil {
			return err
		}
		for k, v := range set {
			if t.Attributes == nil {
				t.Attributes = map[string]string{}
			}
			t.Attributes[k] = v
		}
		for _, k := range e.UnsetAttributes {
			delete(t.Attributes, k)
		}
		if len(t.Attributes) == 0 {
			t.Attributes = nil
		}
		if e.Parent != nil {
			t.Parent = 0
			if *e.Parent != "" {
				if t.Parent, err = link(tb, id, *e.Parent); err != nil {
					return err
				}
				if err := noLoop(tb, id, n, t.Parent); err != nil {
					return err
				}
			}
		}
		if e.BlockedBy != nil {
			if t.BlockedBy, err = links(tb, id, *e.BlockedBy); err != nil {
				return err
			}
		}
		return put(tb, n, t)
	})
}

// Move moves the ticket to the status named name, whatever status it is in now, and returns the
// status in the database's spelling.
func (s *Store) Move(id, name string) (status string, err error) {
	defer recoverTo(&err)
	st, ok := s.statuses.Find(name)
	if !ok {
		return "", s.noStatus(name)
	}
	err = s.db.Update(func(tx *bolt.Tx) error {
		tb := tx.Bucket(ticketBucket)
		n, t, err := get(tb, id)
		if err != nil {
			return err
		}
		if t.Status != st.Name {
			t.Status, t.StatusAt = st.Name, s.stamp()
		}
		return put(tb, n, t)
	})
	return st.Name, err
}

// Comment adds a note by author. The author is dispatch, you, or a worker's ticket id.
func (s *Store) Comment(id, author, body string) (err error) {
	defer recoverTo(&err)
	if strings.TrimSpace(body) == "" {
		return refuse("a comment needs text")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		tb := tx.Bucket(ticketBucket)
		n, t, err := get(tb, id)
		if err != nil {
			return err
		}
		cb, err := tx.Bucket(commentBucket).CreateBucketIfNotExists(key(n))
		if err != nil {
			return err
		}
		c, err := cb.NextSequence()
		if err != nil {
			return err
		}
		at := s.stamp()
		raw, err := json.Marshal(storedComment{Author: author, At: at, Body: body})
		if err != nil {
			return err
		}
		if err := cb.Put(key(c), raw); err != nil {
			return err
		}
		t.LastComment = at
		return put(tb, n, t)
	})
}

// Find lists the tickets f selects, newest first.
func (s *Store) Find(f Filter) (out []Summary, err error) {
	defer recoverTo(&err)
	if f.Status != "" {
		st, ok := s.statuses.Find(f.Status)
		if !ok {
			return nil, s.noStatus(f.Status)
		}
		f.Status = st.Name
	}
	err = s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(ticketBucket).Cursor()
		for k, v := c.Last(); k != nil; k, v = c.Prev() {
			t, err := decode(k, v)
			if err != nil {
				return err
			}
			if f.Status != "" && t.Status != f.Status || f.Open && s.statuses.Has(t.Status, ticket.Finished) || !matches(t, f) {
				continue
			}
			out = append(out, Summary{ID: ticket.LocalID(number(k)), Status: t.Status, Repo: t.Repo, Title: t.Title})
		}
		return nil
	})
	return out, err
}

// Show returns the ticket with its comments, oldest first.
func (s *Store) Show(id string) (d Detail, err error) {
	defer recoverTo(&err)
	err = s.db.View(func(tx *bolt.Tx) error {
		n, t, err := get(tx.Bucket(ticketBucket), id)
		if err != nil {
			return err
		}
		d = Detail{
			ID: id, Title: t.Title, Description: t.Description, Status: t.Status, Repo: t.Repo, Branch: t.Branch,
			FiledBy: t.FiledBy, Labels: t.Labels, Attributes: t.Attributes, StartPR: t.StartPR,
		}
		if t.Parent != 0 {
			d.Parent = ticket.LocalID(t.Parent)
		}
		for _, b := range t.BlockedBy {
			d.BlockedBy = append(d.BlockedBy, ticket.LocalID(b))
		}
		cb := tx.Bucket(commentBucket).Bucket(key(n))
		if cb == nil {
			return nil
		}
		return cb.ForEach(func(_, v []byte) error {
			var c storedComment
			if err := json.Unmarshal(v, &c); err != nil {
				return unreadable("a comment on %s does not decode: %v", id, err)
			}
			at, err := time.Parse(timeForm, c.At)
			if err != nil {
				return unreadable("a comment on %s has the time %q: %v", id, c.At, err)
			}
			d.Comments = append(d.Comments, Comment{Author: c.Author, At: at, Body: c.Body})
			return nil
		})
	})
	return d, err
}

func (s *Store) noStatus(name string) error {
	return refuse("no status named %q; the statuses are %s", name, strings.Join(s.statuses.Names(), ", "))
}

func (s *Store) repo(label string) (string, error) {
	for _, have := range s.repos {
		if strings.EqualFold(have, label) {
			return have, nil
		}
	}
	return "", refuse("no [repos.%s] table in config.toml", label)
}

func (s *Store) stamp() string { return s.now().UTC().Format(timeForm) }

// recoverTo turns a bbolt panic, such as a corrupt page, into an unreadable error.
func recoverTo(err *error) {
	if r := recover(); r != nil {
		*err = unreadable("the database failed: %v", r)
	}
}

func key(n uint64) []byte {
	k := make([]byte, 8)
	binary.BigEndian.PutUint64(k, n)
	return k
}

func number(k []byte) uint64 { return binary.BigEndian.Uint64(k) }

func decode(k, v []byte) (stored, error) {
	var t stored
	if err := json.Unmarshal(v, &t); err != nil {
		return t, unreadable("%s does not decode: %v", ticket.LocalID(number(k)), err)
	}
	return t, nil
}

func get(tb *bolt.Bucket, id string) (uint64, stored, error) {
	n, ok := ticket.LocalNumber(id)
	if !ok {
		return 0, stored{}, notFound(id)
	}
	v := tb.Get(key(n))
	if v == nil {
		return 0, stored{}, notFound(id)
	}
	t, err := decode(key(n), v)
	return n, t, err
}

func put(tb *bolt.Bucket, n uint64, t stored) error {
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return tb.Put(key(n), raw)
}

func readStatuses(tx *bolt.Tx) (ticket.Statuses, error) {
	var out ticket.Statuses
	err := tx.Bucket(statusBucket).ForEach(func(_, v []byte) error {
		var st ticket.Status
		if err := json.Unmarshal(v, &st); err != nil {
			return unreadable("a status does not decode: %v", err)
		}
		out = append(out, st)
		return nil
	})
	return out, err
}

// link resolves ref, a link from the ticket id, to a ticket number that exists.
func link(tb *bolt.Bucket, id, ref string) (uint64, error) {
	n, ok := ticket.LocalNumber(ref)
	if !ok {
		if ticket.SystemOf(ref) == ticket.SystemLinear {
			return 0, refuse("%s is a Linear ticket; links join local tickets only", ref)
		}
		return 0, refuse("%s links %q, which is not a ticket id such as LOC-3", id, ref)
	}
	if ref == id {
		return 0, refuse("%s links itself", id)
	}
	if tb.Get(key(n)) == nil {
		return 0, refuse("%s links %s, which does not exist", id, ref)
	}
	return n, nil
}

func links(tb *bolt.Bucket, id string, refs []string) ([]uint64, error) {
	var out []uint64
	for _, ref := range refs {
		n, err := link(tb, id, ref)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out, nil
}

// noLoop refuses a parent whose chain of parents reaches the ticket n.
func noLoop(tb *bolt.Bucket, id string, n, parent uint64) error {
	seen := map[uint64]bool{}
	for p := parent; p != 0 && !seen[p]; {
		if p == n {
			return refuse("%s under %s would make a parent cycle", id, ticket.LocalID(parent))
		}
		seen[p] = true
		v := tb.Get(key(p))
		if v == nil {
			return nil
		}
		t, err := decode(key(p), v)
		if err != nil {
			return err
		}
		p = t.Parent
	}
	return nil
}

func cleanLabels(in []string) ([]string, error) {
	var out []string
	for _, l := range in {
		l = strings.TrimSpace(l)
		if l == "" {
			return nil, refuse("a label has no name")
		}
		if !hasFold(out, l) {
			out = append(out, l)
		}
	}
	return out, nil
}

func cleanAttributes(in map[string]string) (map[string]string, error) {
	var out map[string]string
	for k, v := range in {
		if strings.TrimSpace(k) == "" {
			return nil, refuse("an attribute has no key")
		}
		if out == nil {
			out = map[string]string{}
		}
		out[k] = v
	}
	return out, nil
}

func hasFold(list []string, s string) bool {
	return slices.ContainsFunc(list, func(have string) bool { return strings.EqualFold(have, s) })
}

func matches(t stored, f Filter) bool {
	for _, l := range f.Labels {
		if !hasFold(t.Labels, l) {
			return false
		}
	}
	for k, v := range f.Attributes {
		if have, ok := t.Attributes[k]; !ok || have != v {
			return false
		}
	}
	return true
}

// maxBranch is the longest branch name a ticket gets.
const maxBranch = 40

// branch is loc-<n>-<slug of the title>, made once at create so editing the title never renames a pushed branch.
func branch(n uint64, title string) string {
	var slug strings.Builder
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			slug.WriteRune(r)
		case !strings.HasSuffix(slug.String(), "-"):
			slug.WriteByte('-')
		}
	}
	out := fmt.Sprintf("loc-%d-%s", n, slug.String())
	if len(out) > maxBranch {
		out = out[:maxBranch]
	}
	return strings.Trim(out, "-")
}
