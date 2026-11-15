// Package panerecords keeps the pane records: one file per Rex pane of the user's, naming the
// Claude session last seen there. A Rex restart brings a pane back as a bare shell, and the
// record is what says which conversation to resume in it.
package panerecords

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Record is what dispatch keeps of one pane.
type Record struct {
	// Session is the harness session last started in the pane.
	Session string `json:"session"`
	// CWD is the directory the session runs in, which a resume has to start from.
	CWD string `json:"cwd"`
	// Instance is the start of the id of the Rex server process that was running when the
	// record was written. A record from the running server's instance has nothing to restore.
	Instance string    `json:"instance"`
	At       time.Time `json:"at"`
}

// Stored is a record as All found it on disk.
type Stored struct {
	// Pane is the pane id, `<rex session id>/<block id>`.
	Pane string
	Path string
	Record
	// Err says why the file could not be read as a record.
	Err error
}

const dirName = "panes"

// path is the record's file, `<state dir>/panes/<rex session id>/<block id>.json`.
func path(stateDir, pane string) (string, error) {
	session, block, ok := strings.Cut(pane, "/")
	for _, part := range []string{session, block} {
		if !ok || part == "" || part == "." || part == ".." || strings.ContainsAny(part, `/\`) {
			return "", fmt.Errorf("%q is not a pane id", pane)
		}
	}
	return filepath.Join(stateDir, dirName, session, block+".json"), nil
}

// Write replaces the pane's record. A pane has one writer at a time, the Claude running in it,
// so the rename is all that keeps a reader from a half-written file.
func Write(stateDir, pane string, r Record) error {
	name, err := path(stateDir, pane)
	if err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return fmt.Errorf("while writing the pane record: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(name), ".pane-*.tmp")
	if err != nil {
		return fmt.Errorf("while writing the pane record: %w", err)
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("while writing the pane record: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("while writing the pane record: %w", err)
	}
	return os.Rename(tmp.Name(), name)
}

// Read returns the pane's record. A missing or unreadable file is reported as absent.
func Read(stateDir, pane string) (Record, bool) {
	name, err := path(stateDir, pane)
	if err != nil {
		return Record{}, false
	}
	r, err := readFile(name)
	return r, err == nil
}

func readFile(name string) (Record, error) {
	b, err := os.ReadFile(name)
	if err != nil {
		return Record{}, err
	}
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return Record{}, err
	}
	if r.Session == "" {
		return Record{}, errors.New("the record names no session")
	}
	return r, nil
}

// Remove deletes the pane's record. A pane with none is not an error.
func Remove(stateDir, pane string) error {
	name, err := path(stateDir, pane)
	if err != nil {
		return err
	}
	if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// The directory of a Rex session goes with its last record.
	_ = os.Remove(filepath.Dir(name))
	return nil
}

// RemoveIf deletes the pane's record when it names session. A session that ends after another
// started in the pane must not take the newer session's record with it.
func RemoveIf(stateDir, pane, session string) error {
	if r, ok := Read(stateDir, pane); !ok || r.Session != session {
		return nil
	}
	return Remove(stateDir, pane)
}

// All lists every record, ordered by pane id. A file that does not read as a record is listed with its Err.
func All(stateDir string) ([]Stored, error) {
	names, err := filepath.Glob(filepath.Join(stateDir, dirName, "*", "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var out []Stored
	for _, name := range names {
		s := Stored{Path: name, Pane: filepath.Base(filepath.Dir(name)) + "/" + strings.TrimSuffix(filepath.Base(name), ".json")}
		s.Record, s.Err = readFile(name)
		out = append(out, s)
	}
	return out, nil
}
