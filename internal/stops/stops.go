// Package stops keeps the record of each worker's latest Stop, the moment
// Claude Code ended a turn. The plugin's Stop hook writes it and the tick
// reads it, because the hook is the only place Claude Code says whether an
// idle session is waiting on background work.
package stops

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// BusyExpiry is how long a Stop that listed running tasks still counts as
// waiting on them. A hung task never wakes its worker, so without it the
// last Stop would read busy forever.
const BusyExpiry = 60 * time.Minute

// Record is a worker's latest Stop.
type Record struct {
	At time.Time `json:"at"`
	// Reported is false when the Stop carried no background_tasks field, which
	// Claude Code omits when its task registry is unreachable. That means
	// unknown, not idle.
	Reported bool `json:"reported"`
	// Running counts the tasks the Stop listed as running.
	Running int `json:"running"`
}

// Busy reports whether the worker was waiting on its own background work at
// this Stop, and is still within BusyExpiry of it.
func (r Record) Busy(now time.Time) bool {
	return r.Reported && r.Running > 0 && now.Sub(r.At) < BusyExpiry
}

// Dir is where a state directory keeps its records.
func Dir(stateDir string) string {
	return filepath.Join(stateDir, "stops")
}

// treeDir holds the records of the worker in tree, one file for each Claude
// session that stopped there. Any session whose working directory is the tree
// fires the hook, so the tick must pick the worker's own session out. Two
// spellings of one tree must share records, so the tree is resolved first.
func treeDir(stateDir, tree string) string {
	if resolved, err := filepath.EvalSymlinks(tree); err == nil {
		tree = resolved
	}
	return filepath.Join(Dir(stateDir), hash(tree))
}

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// Write replaces the record of session in the tree.
func Write(stateDir, tree, session string, r Record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	dir := treeDir(stateDir, tree)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("while writing stop record: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".stop-*.tmp")
	if err != nil {
		return fmt.Errorf("while writing stop record: %w", err)
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("while writing stop record: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("while writing stop record: %w", err)
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, hash(session)+".json"))
}

// Read returns the latest Stop of session in the tree. With no session known,
// it returns the latest Stop of any session there. A missing or unreadable
// record is reported as absent, because the tick treats both as unknown.
func Read(stateDir, tree, session string) (Record, bool) {
	dir := treeDir(stateDir, tree)
	if session != "" {
		return readFile(filepath.Join(dir, hash(session)+".json"))
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	var latest Record
	for _, f := range files {
		if r, ok := readFile(f); ok && r.At.After(latest.At) {
			latest = r
		}
	}
	return latest, !latest.At.IsZero()
}

func readFile(name string) (Record, bool) {
	b, err := os.ReadFile(name)
	if err != nil {
		return Record{}, false
	}
	var r Record
	if json.Unmarshal(b, &r) != nil || r.At.IsZero() {
		return Record{}, false
	}
	return r, true
}

// Remove deletes the records of the tree, so the next ticket leased that tree
// does not inherit this one's last Stop.
func Remove(stateDir, tree string) {
	_ = os.RemoveAll(treeDir(stateDir, tree))
}
