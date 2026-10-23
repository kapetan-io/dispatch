// Package stops keeps the record of each worker's latest Stop, the moment
// Claude Code ended a turn. The running dispatch writes it from the Stop hook's
// report and the rules read it, because the hook is the only place Claude Code
// says whether an idle session is waiting on background work. It also keeps when each
// tree was last returned, so a Stop from the session that ended there is not taken for
// the next one's.
package stops

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/kapetan-io/dispatch/internal/treekey"
)

// BusyExpiry is how long a Stop that listed running tasks still counts as
// waiting on them. A hung task never wakes its worker, so without it the
// last Stop would read busy forever.
const BusyExpiry = 60 * time.Minute

// Record is a worker's latest Stop.
type Record struct {
	At time.Time `json:"at"`
	// Reported is false when the Stop carried no background_tasks field, which
	// only a Claude Code version that predates it omits. That means unknown,
	// not idle.
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
// session that stopped there, and after a return the time of that return. Any session whose
// working directory is the tree fires the hook, so the reader must pick the worker's own
// session out.
func treeDir(stateDir, tree string) string {
	return filepath.Join(Dir(stateDir), treekey.Of(tree))
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
	return os.Rename(tmp.Name(), filepath.Join(dir, treekey.Hash(session)+".json"))
}

// Read returns the latest Stop of session in the tree that ended after the last return.
// With no session known, it returns the latest Stop of any session there. A missing or unreadable
// record is reported as absent, because the rules treat both as unknown.
func Read(stateDir, tree, session string) (Record, bool) {
	dir := treeDir(stateDir, tree)
	returned, _ := ReturnedAt(stateDir, tree)
	if session != "" {
		r, ok := readFile(filepath.Join(dir, treekey.Hash(session)+".json"))
		return r, ok && r.At.After(returned)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	var latest Record
	for _, f := range files {
		if r, ok := readFile(f); ok && r.At.After(latest.At) && r.At.After(returned) {
			latest = r
		}
	}
	return latest, !latest.At.IsZero()
}

// Unreadable reports whether any record for session, or for any session when it is empty,
// exists and cannot be read, even when another one can. An unreadable record might be the
// newest Stop, so that is unknown, not absent, and a caller that would act on a missing
// Stop holds instead.
func Unreadable(stateDir, tree, session string) bool {
	dir := treeDir(stateDir, tree)
	files := []string{filepath.Join(dir, treekey.Hash(session)+".json")}
	if session == "" {
		files, _ = filepath.Glob(filepath.Join(dir, "*.json"))
	}
	var broken bool
	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			continue
		}
		if _, ok := readFile(f); !ok {
			broken = true
		}
	}
	return broken
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

// returnedFile holds the time the tree was last given back, in Unix milliseconds. It is not a
// .json file, so Read and Unreadable never take it for a Stop.
const returnedFile = "returned"

// MarkReturned records that the tree was given back at at, so no Stop that ended by then is
// read again, and deletes the files written by then. The hook reports after a turn ends, so
// the ended session's Stop can reach the loop after this runs. That Stop is stored but
// ignored, which is why the records are not simply deleted. A new session's Stop ends after at.
func MarkReturned(stateDir, tree string, at time.Time) {
	dir := treeDir(stateDir, tree)
	if os.MkdirAll(dir, 0o755) == nil {
		writeReturned(dir, at)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	for _, f := range files {
		if info, err := os.Stat(f); err == nil && !info.ModTime().After(at) {
			_ = os.Remove(f)
		}
	}
}

func writeReturned(dir string, at time.Time) {
	tmp, err := os.CreateTemp(dir, ".returned-*.tmp")
	if err != nil {
		return
	}
	_, werr := tmp.WriteString(strconv.FormatInt(at.UnixMilli(), 10))
	if cerr := tmp.Close(); werr != nil || cerr != nil || os.Rename(tmp.Name(), filepath.Join(dir, returnedFile)) != nil {
		_ = os.Remove(tmp.Name())
	}
}

// ReturnedAt is when the tree was last given back.
func ReturnedAt(stateDir, tree string) (time.Time, bool) {
	b, err := os.ReadFile(filepath.Join(treeDir(stateDir, tree), returnedFile))
	if err != nil {
		return time.Time{}, false
	}
	ms, err := strconv.ParseInt(string(b), 10, 64)
	return time.UnixMilli(ms), err == nil
}
