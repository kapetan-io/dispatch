// Package dispatchdb owns the file dispatch.db in the state directory: opening it, its version,
// the rename of the tickets.db it replaces, the read-only open and the import marker of each repo.
// It hands the open file to the stores that own its buckets (localticket, ticketbranch and workers)
// and to nothing else.
package dispatchdb

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	bolt "go.etcd.io/bbolt"
)

const (
	// Name is the file's name in the state directory.
	Name = "dispatch.db"
	// OldName is the local ticket database an earlier dispatch kept, which the first start renames.
	OldName = "tickets.db"
	// version is the format this dispatch writes. A file with a higher one is refused.
	version = 1
	// defaultLockTimeout is how long an open waits for a file another process holds.
	defaultLockTimeout = 5 * time.Second
)

var (
	metaBucket     = []byte("meta")
	versionKey     = []byte("version")
	importedBucket = []byte("imported")
)

// ErrHeld means another process holds the file.
var ErrHeld = errors.New("held")

// Options is what an open needs.
type Options struct {
	StateDir string
	// LockTimeout defaults to defaultLockTimeout.
	LockTimeout time.Duration
	// Warn receives the line about a tickets.db left beside dispatch.db. Nil drops it.
	Warn io.Writer
	Hooks
}

// Hooks are for tests alone and are nil in production. A crash between two steps of an action is
// unreachable from the command line, and ADR 0010 rules out a database fake that could count
// commits for itself.
type Hooks struct {
	// BeforeCommit runs before each write transaction commits. An error rolls the transaction back
	// and fails the write, which leaves on disk what a kill before the commit would.
	BeforeCommit func() error
	// Opened is told the file dispatch run opened, so a test can state a row a launch it cannot
	// run would have written.
	Opened func(*File)
}

// File is the open database.
type File struct {
	db     *bolt.DB
	path   string
	commit func() error
}

// Path is where the file is.
func Path(stateDir string) string { return filepath.Join(stateDir, Name) }

// Open opens the file for dispatch run, which is its only writer. It renames tickets.db when
// dispatch.db does not exist yet, and creates a missing file with mode 0600. The first write
// through Update stamps the version into a file that has none. A file it cannot open, or one a
// newer dispatch wrote, is an error naming the file.
func Open(o Options) (*File, error) {
	path, old := Path(o.StateDir), filepath.Join(o.StateDir, OldName)
	if err := os.MkdirAll(o.StateDir, 0o700); err != nil {
		return nil, fmt.Errorf("while creating the directory of %s: %w", path, err)
	}
	_, pathErr := os.Stat(path)
	_, oldErr := os.Stat(old)
	switch {
	case errors.Is(pathErr, fs.ErrNotExist) && oldErr == nil:
		if err := os.Rename(old, path); err != nil {
			return nil, fmt.Errorf("while renaming %s to %s: %w", old, path, err)
		}
	case pathErr == nil && oldErr == nil && o.Warn != nil:
		_, _ = fmt.Fprintf(o.Warn, "dispatch: %s is in use and %s beside it is not read; remove %s once nothing in it is needed\n", path, old, old)
	}
	db, err := open(path, o.LockTimeout, false)
	if err != nil {
		return nil, err
	}
	f := &File{db: db, path: path, commit: o.BeforeCommit}
	// Opening writes nothing, so a start that is refused leaves the file's contents as it found
	// them. A tickets.db renamed above keeps its new name, which the next start reads.
	if err := db.View(func(tx *bolt.Tx) error { return checkVersion(tx, path) }); err != nil {
		_ = db.Close()
		return nil, unusable(path, err)
	}
	if o.Opened != nil {
		o.Opened(f)
	}
	return f, nil
}

// OpenReadOnly opens the file for a command that runs while dispatch run is stopped, under
// dispatch.lock. It never creates, renames or writes a file. Before the first start of this
// version it reads the tickets.db that start would rename. With neither file, or an empty one, it
// returns nil, because the database holds nothing yet.
func OpenReadOnly(o Options) (*File, error) {
	path := Path(o.StateDir)
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		path = filepath.Join(o.StateDir, OldName)
		info, err = os.Stat(path)
	}
	if errors.Is(err, fs.ErrNotExist) || (err == nil && info.Size() == 0) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("while opening %s: %w", path, err)
	}
	db, err := open(path, o.LockTimeout, true)
	if err != nil {
		return nil, err
	}
	if err := db.View(func(tx *bolt.Tx) error { return checkVersion(tx, path) }); err != nil {
		_ = db.Close()
		return nil, unusable(path, err)
	}
	return &File{db: db, path: path}, nil
}

func open(path string, timeout time.Duration, readOnly bool) (*bolt.DB, error) {
	if timeout <= 0 {
		timeout = defaultLockTimeout
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: timeout, ReadOnly: readOnly})
	if errors.Is(err, bolt.ErrTimeout) {
		return nil, fmt.Errorf("%s is %w by another process after %s; another dispatch run or a file sync tool may hold it", path, ErrHeld, timeout)
	}
	if err != nil {
		return nil, unusable(path, err)
	}
	return db, nil
}

func unusable(path string, err error) error {
	return fmt.Errorf("dispatch cannot use its database %s: %w", path, err)
}

// checkVersion refuses a file a newer dispatch wrote. A file with no version predates this
// package and reads as the current one.
func checkVersion(tx *bolt.Tx, path string) error {
	meta := tx.Bucket(metaBucket)
	if meta == nil || meta.Get(versionKey) == nil {
		return nil
	}
	found, err := strconv.Atoi(string(meta.Get(versionKey)))
	if err != nil {
		return fmt.Errorf("its version %q is not a number", meta.Get(versionKey))
	}
	if found > version {
		return fmt.Errorf("it was written by a newer dispatch (format %d, and this dispatch reads up to %d); upgrade dispatch to use %s", found, version, path)
	}
	return nil
}

// Close releases the file.
func (f *File) Close() error { return f.db.Close() }

// Path is where the open file is.
func (f *File) Path() string { return f.path }

// Bolt is the open file, for the stores that own its buckets.
func (f *File) Bolt() *bolt.DB { return f.db }

// Update runs one write transaction. The worker store writes through it, so a test can fail a
// commit. The local ticket store writes through Bolt.
func (f *File) Update(write func(tx *bolt.Tx) error) error {
	return f.db.Update(func(tx *bolt.Tx) error {
		// The first write stamps the format version.
		meta, err := tx.CreateBucketIfNotExists(metaBucket)
		if err != nil {
			return err
		}
		if meta.Get(versionKey) == nil {
			if err := meta.Put(versionKey, []byte(strconv.Itoa(version))); err != nil {
				return err
			}
		}
		if err := write(tx); err != nil {
			return err
		}
		if f.commit != nil {
			return f.commit()
		}
		return nil
	})
}

// View runs one read transaction.
func (f *File) View(read func(tx *bolt.Tx) error) error { return f.db.View(read) }

// Marker is a repo's import marker: which halves of what an earlier dispatch kept outside this
// file were copied in, and the branches whose git config keys are still to be unset.
type Marker struct {
	// Workers is when the repo's workers were imported, in milliseconds since the epoch.
	Workers int64 `json:"workers,omitempty"`
	// Branches is when the repo's ticket branch keys and stack records were imported.
	Branches int64 `json:"branches,omitempty"`
	// Unset lists the branches whose dispatch keys are still in the clone's git config.
	Unset []string `json:"unset,omitempty"`
}

// WorkersImported reports whether the repo's workers were imported from the files an earlier
// dispatch kept.
func (f *File) WorkersImported(repo string) (bool, error) {
	var done bool
	err := f.db.View(func(tx *bolt.Tx) error {
		m, err := ReadMarker(tx, repo)
		done = m.Workers != 0
		return err
	})
	return done, err
}

// BranchesImported reports whether the repo's ticket branch keys and stack records were imported
// from its clone's git config.
func (f *File) BranchesImported(repo string) (bool, error) {
	var done bool
	err := f.db.View(func(tx *bolt.Tx) error {
		m, err := ReadMarker(tx, repo)
		done = m.Branches != 0
		return err
	})
	return done, err
}

// MarkWorkersImported records, in the import's own transaction, that the repo's workers are
// imported. A marker already there keeps its time.
func MarkWorkersImported(tx *bolt.Tx, repo string, at time.Time) error {
	return editMarker(tx, repo, func(m *Marker) {
		if m.Workers == 0 {
			m.Workers = at.UnixMilli()
		}
	})
}

// MarkBranchesImported records, in the import's own transaction, that the repo's branches are
// imported and which branches still carry keys to unset. A marker already there keeps its time
// and list.
func MarkBranchesImported(tx *bolt.Tx, repo string, at time.Time, unset []string) error {
	return editMarker(tx, repo, func(m *Marker) {
		if m.Branches == 0 {
			m.Branches, m.Unset = at.UnixMilli(), unset
		}
	})
}

// KeysUnset takes the branches whose keys were unset off the repo's list.
func (f *File) KeysUnset(repo string, branches []string) error {
	return f.Update(func(tx *bolt.Tx) error {
		return editMarker(tx, repo, func(m *Marker) {
			m.Unset = slices.DeleteFunc(m.Unset, func(b string) bool { return slices.Contains(branches, b) })
		})
	})
}

// Markers reads the import marker of every repo that has one.
func Markers(tx *bolt.Tx) (map[string]Marker, error) {
	found := map[string]Marker{}
	bucket := tx.Bucket(importedBucket)
	if bucket == nil {
		return found, nil
	}
	err := bucket.ForEach(func(k, raw []byte) error {
		var m Marker
		if err := json.Unmarshal(raw, &m); err != nil {
			return fmt.Errorf("the import marker of %s is not readable: %w", k, err)
		}
		found[string(k)] = m
		return nil
	})
	return found, err
}

func editMarker(tx *bolt.Tx, repo string, edit func(*Marker)) error {
	m, err := ReadMarker(tx, repo)
	if err != nil {
		return err
	}
	edit(&m)
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	bucket, err := tx.CreateBucketIfNotExists(importedBucket)
	if err != nil {
		return err
	}
	return bucket.Put([]byte(repo), raw)
}

// ReadMarker is the repo's import marker, empty when it has none.
func ReadMarker(tx *bolt.Tx, repo string) (Marker, error) {
	var m Marker
	bucket := tx.Bucket(importedBucket)
	if bucket == nil {
		return m, nil
	}
	raw := bucket.Get([]byte(repo))
	if raw == nil {
		return m, nil
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, fmt.Errorf("the import marker of %s is not readable: %w", repo, err)
	}
	return m, nil
}
