// Package sends keeps the record of each slash command dispatch typed into a
// worker tree. Dispatch writes one before typing and the prompt hook claims it
// when the command reaches Claude, which is how a command typed by dispatch
// differs from one typed by a person. A record holds a hash, never the text.
package sends

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/sessionlog"
	"github.com/kapetan-io/dispatch/internal/treekey"
)

// record is one send on disk. Session is empty when the Claude session is not known yet.
type record struct {
	Session string `json:"session"`
	Sum     string `json:"sum"`
}

// Dir is where a state directory keeps its send records.
func Dir(stateDir string) string {
	return filepath.Join(stateDir, "sends")
}

func treeDir(stateDir, tree string) string {
	return filepath.Join(Dir(stateDir), treekey.Of(tree))
}

const (
	recordSuffix  = ".json"
	claimedSuffix = ".claimed"
)

func sum(text string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return hex.EncodeToString(h[:])
}

// Record notes that text is about to be typed into the Claude session in the tree, and
// returns the id Withdraw takes. Text that is not a slash command needs no record, so it
// returns an empty id. An empty session matches any session.
func Record(stateDir, tree, session, text string) (string, error) {
	if _, ok := sessionlog.ParseCommand(text); !ok {
		return "", nil
	}
	b, err := json.Marshal(record{Session: session, Sum: sum(text)})
	if err != nil {
		return "", err
	}
	dir := treeDir(stateDir, tree)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("while recording a send: %w", err)
	}
	var suffix [4]byte
	_, _ = rand.Read(suffix[:])
	id := fmt.Sprintf("%020d-%s%s", time.Now().UnixNano(), hex.EncodeToString(suffix[:]), recordSuffix)
	// Claim skips anything that isn't a finished .json file, so it never reads half a record.
	pending := filepath.Join(dir, id+".tmp")
	if err := os.WriteFile(pending, b, 0o644); err != nil {
		return "", fmt.Errorf("while recording a send: %w", err)
	}
	if err := os.Rename(pending, filepath.Join(dir, id)); err != nil {
		return "", fmt.Errorf("while recording a send: %w", err)
	}
	return id, nil
}

// Withdraw removes a record whose text herdr refused to type.
func Withdraw(stateDir, tree, id string) {
	if id != "" {
		_ = os.Remove(filepath.Join(treeDir(stateDir, tree), id))
	}
}

// Claim consumes the oldest record in the tree that matches prompt and the session, and
// reports whether there was one. The claim is an exclusive create of a marker file beside the
// record, because removing the record is not exclusive: two callers can both succeed in
// removing one file on APFS. A caller that loses the create tries the next record, and the
// marker stays until Clear so a caller that read the record before it was removed still loses.
func Claim(stateDir, tree, session, prompt string) bool {
	dir := treeDir(stateDir, tree)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	want := sum(prompt)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), recordSuffix) {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	for _, name := range names {
		path := filepath.Join(dir, name)
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var r record
		if json.Unmarshal(b, &r) != nil || r.Sum != want || (r.Session != "" && r.Session != session) {
			continue
		}
		marker, err := os.OpenFile(path+claimedSuffix, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			continue
		}
		_ = marker.Close()
		_ = os.Remove(path)
		return true
	}
	return false
}

// Clear removes every record of the tree, so the next worker there does not claim them.
func Clear(stateDir, tree string) {
	_ = os.RemoveAll(treeDir(stateDir, tree))
}
