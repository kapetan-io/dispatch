package workers

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/kapetan-io/dispatch/internal/dispatchdb"
	"github.com/kapetan-io/dispatch/internal/sessionlog"
	"github.com/kapetan-io/dispatch/internal/treekey"
)

// This file reads the files an earlier dispatch kept in the state directory in place of worker
// rows: stops/<tree key>/<session key>.json with a "returned" time beside them, kickoffs/<tree key>
// and a <session key>.closing beside each session log. It goes when the import does.

func oldStops(stateDir, tree string) string {
	return filepath.Join(stateDir, "stops", treekey.Of(tree))
}

func oldKickoff(stateDir, tree string) string {
	return filepath.Join(stateDir, "kickoffs", treekey.Of(tree))
}

func oldClosings(stateDir, tree string) []string {
	files, _ := filepath.Glob(filepath.Join(sessionlog.Dir(stateDir), treekey.Of(tree), "*.closing"))
	return files
}

// millis reads a file that starts with a time in milliseconds since the epoch.
func millis(name string) (int64, bool) {
	raw, err := os.ReadFile(name)
	if err != nil {
		return 0, false
	}
	first, _, _ := strings.Cut(strings.TrimSpace(string(raw)), " ")
	ms, err := strconv.ParseInt(first, 10, 64)
	return ms, err == nil
}

// Import adds, once for each repo, a worker row for every lease in its pool whose holder is a
// ticket id, so a worker that was running before dispatch kept rows stays one. The row takes the
// tree's old kickoff time, Stop records and closing messages. A row already there is left as it
// is, and the repo's marker is written in the same transaction, so a second import adds nothing.
func (s *Store) Import(pool Pool, stateDir string, at time.Time) error {
	return s.file.Update(func(tx *bolt.Tx) error {
		for _, lease := range pool.leases {
			if !IsTicket(lease.Holder) {
				continue
			}
			// A tree another ticket's row names keeps that row, and this lease reads as held by hand.
			if err := treeTaken(tx, lease.Holder, pool.repo, lease.Tree); errors.Is(err, ErrTreeTaken) {
				continue
			} else if err != nil {
				return err
			}
			// The worker began some time after the tree's last return, which is the one bound the
			// old files give. A Stop that ended while dispatch was stopped for the upgrade is its own.
			row := Row{Where: Local, Tree: lease.Tree}
			if returned, ok := millis(filepath.Join(oldStops(stateDir, lease.Tree), "returned")); ok {
				row.Since = returned + 1
			}
			// The old record was removed when its launch returned, so one still there is open. It
			// never held the text or the session.
			if ms, ok := millis(oldKickoff(stateDir, lease.Tree)); ok {
				row.Kickoff = &Kickoff{At: ms, Open: true}
			}
			worker, made, err := create(tx, lease.Holder, pool.repo, row)
			if err != nil {
				return err
			}
			if !made {
				continue
			}
			if err := importRecords(worker, stateDir, lease.Tree); err != nil {
				return err
			}
		}
		return dispatchdb.MarkWorkersImported(tx, pool.repo, at)
	})
}

// importRecords copies the tree's old Stop records that ended after its last return, and its
// closing messages, under their session keys.
func importRecords(worker *bolt.Bucket, stateDir, tree string) error {
	returned, _ := millis(filepath.Join(oldStops(stateDir, tree), "returned"))
	files, _ := filepath.Glob(filepath.Join(oldStops(stateDir, tree), "*.json"))
	for _, name := range files {
		raw, err := os.ReadFile(name)
		if err != nil {
			continue
		}
		var rec Record
		if json.Unmarshal(raw, &rec) == nil && !rec.At.IsZero() && rec.At.UnixMilli() <= returned {
			continue
		}
		stops, err := worker.CreateBucketIfNotExists(stopsBucket)
		if err != nil {
			return err
		}
		// A record that does not parse is copied as it is, so the worker still reads as one whose
		// latest Stop is unknown.
		if err := stops.Put([]byte(strings.TrimSuffix(filepath.Base(name), ".json")), raw); err != nil {
			return err
		}
	}
	for _, name := range oldClosings(stateDir, tree) {
		raw, err := os.ReadFile(name)
		if err != nil {
			continue
		}
		messages, err := worker.CreateBucketIfNotExists(closingBucket)
		if err != nil {
			return err
		}
		if err := messages.Put([]byte(strings.TrimSuffix(filepath.Base(name), ".closing")), raw); err != nil {
			return err
		}
	}
	return nil
}

// RemoveOldFiles deletes the files an earlier dispatch kept for the trees of an imported repo.
// It is safe to repeat, so a start that was killed before it ran is finished by the next one.
func RemoveOldFiles(stateDir string, pool Pool) {
	for _, lease := range pool.leases {
		_ = os.RemoveAll(oldStops(stateDir, lease.Tree))
		_ = os.Remove(oldKickoff(stateDir, lease.Tree))
		_ = os.Remove(oldKickoff(stateDir, lease.Tree) + ".lock")
		for _, name := range oldClosings(stateDir, lease.Tree) {
			_ = os.Remove(name)
		}
	}
	// Each directory goes once the last repo's files have left it.
	_ = os.Remove(filepath.Join(stateDir, "stops"))
	_ = os.Remove(filepath.Join(stateDir, "kickoffs"))
}
