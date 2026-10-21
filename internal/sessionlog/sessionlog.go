// Package sessionlog keeps the file of what one worker session did, one line
// for each turn it ended. The running dispatch appends to it from the Stop hook's report and only
// the world reads it, so a rule sees every turn of the session and not just the latest.
package sessionlog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/kapetan-io/dispatch/internal/treekey"
)

// Turn is one reply the session finished, ending in a Stop.
type Turn struct {
	// At is when the turn ended, in milliseconds since the epoch.
	At int64 `json:"at"`
	// Running is true when the Stop reported at least one running background task.
	Running bool `json:"running"`
}

// Log holds a session's turns, oldest first. Turns is never nil, so it encodes as [] and not null.
type Log struct {
	Turns []Turn `json:"turns"`
}

// line is one event in the file.
type line struct {
	Turn *Turn `json:"turn,omitempty"`
}

// Dir is where a state directory keeps its session logs.
func Dir(stateDir string) string {
	return filepath.Join(stateDir, "sessions")
}

func treeDir(stateDir, tree string) string {
	return filepath.Join(Dir(stateDir), treekey.Of(tree))
}

func file(stateDir, tree, session string) string {
	return filepath.Join(treeDir(stateDir, tree), treekey.Hash(session)+".jsonl")
}

// AppendTurn adds a turn to the log of session in the tree.
func AppendTurn(stateDir, tree, session string, t Turn) error {
	b, err := json.Marshal(line{Turn: &t})
	if err != nil {
		return err
	}
	name := file(stateDir, tree, session)
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return fmt.Errorf("while recording the turn: %w", err)
	}
	f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("while recording the turn: %w", err)
	}
	defer func() { _ = f.Close() }()
	// A writer killed mid-line leaves no newline. Starting a fresh line keeps this turn
	// parseable but leaves that half line as a hole, so Read reports the log unreadable from then on.
	b = append(b, '\n')
	if info, err := f.Stat(); err == nil && info.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, info.Size()-1); err == nil && last[0] != '\n' {
			b = append([]byte{'\n'}, b...)
		}
	}
	if _, err := f.Write(b); err != nil {
		return fmt.Errorf("while recording the turn: %w", err)
	}
	return nil
}

// Read returns the log of session in the tree. A missing file is an empty log.
// A line that can't be decoded is an error, because a log with a hole in it
// would pass for a shorter one. The exception is a last line with no newline,
// which is a write still in progress.
func Read(stateDir, tree, session string) (Log, error) {
	log := Log{Turns: []Turn{}}
	b, err := os.ReadFile(file(stateDir, tree, session))
	if errors.Is(err, fs.ErrNotExist) {
		return log, nil
	}
	if err != nil {
		return Log{}, fmt.Errorf("while reading the session log: %w", err)
	}
	// Cutting at the last newline drops a final line still being written.
	complete := b[:bytes.LastIndexByte(b, '\n')+1]
	var n int
	for raw := range bytes.Lines(complete) {
		n++
		var l line
		if err := json.Unmarshal(raw, &l); err != nil {
			return Log{}, fmt.Errorf("while reading the session log: line %d: %w", n, err)
		}
		if l.Turn != nil {
			log.Turns = append(log.Turns, *l.Turn)
		}
	}
	return log, nil
}

// Remove deletes the logs of the tree, so the next ticket leased that tree
// does not inherit this one's sessions.
func Remove(stateDir, tree string) {
	_ = os.RemoveAll(treeDir(stateDir, tree))
}
