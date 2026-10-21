// Package kickoffs keeps the record of launches that have not returned. The
// launcher writes it before Claude starts and clears it when the launch
// returns, so only a launch killed in between leaves it behind. The rules read
// it to tell a Claude that never got its kickoff from a worker that is running.
package kickoffs

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kapetan-io/dispatch/internal/treekey"
)

// dir is where a state directory keeps its records.
func dir(stateDir string) string {
	return filepath.Join(stateDir, "kickoffs")
}

// file names the record of the launch on tree.
func file(stateDir, tree string) string {
	return filepath.Join(dir(stateDir), treekey.Of(tree))
}

// Mark records that a launch on tree started at the given time and returns the
// id that launch releases it with. A second Mark replaces the first, because a
// relaunch starts over.
func Mark(stateDir, tree string, at time.Time) (string, error) {
	name := file(stateDir, tree)
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return "", fmt.Errorf("while recording the launch: %w", err)
	}
	unlock, err := lockTree(name)
	if err != nil {
		return "", err
	}
	defer unlock()
	tmp, err := os.CreateTemp(filepath.Dir(name), ".kickoff-*.tmp")
	if err != nil {
		return "", fmt.Errorf("while recording the launch: %w", err)
	}
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	id := hex.EncodeToString(raw[:])
	_, werr := tmp.WriteString(strconv.FormatInt(at.UnixMilli(), 10) + " " + id)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("while recording the launch: %w", werr)
	}
	return id, os.Rename(tmp.Name(), name)
}

// Release removes the record of tree if the launch with id still owns it. A
// launch that a newer one replaced leaves the newer one's record alone.
func Release(stateDir, tree, id string) {
	name := file(stateDir, tree)
	unlock, err := lockTree(name)
	if err != nil {
		return
	}
	defer unlock()
	if _, owner, ok := read(stateDir, tree); ok && owner == id {
		_ = os.Remove(name)
	}
}

// lockTree holds an exclusive flock beside the record while Mark replaces it or Release
// checks its owner, so a Mark landing between Release's check and its removal cannot lose
// the newer launch's record. Both hold it for microseconds, so waiting is fine, and the OS
// drops it if the holder dies.
func lockTree(name string) (func(), error) {
	f, err := os.OpenFile(name+".lock", os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("while recording the launch: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("while recording the launch: %w", err)
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}

// Clear removes the record of tree whoever owns it, because its worker is gone.
func Clear(stateDir, tree string) {
	_ = os.Remove(file(stateDir, tree))
}

// Since is when the launch on tree started and has not been confirmed. A
// missing or unreadable record reports false.
func Since(stateDir, tree string) (time.Time, bool) {
	at, _, ok := read(stateDir, tree)
	return at, ok
}

func read(stateDir, tree string) (at time.Time, owner string, ok bool) {
	b, err := os.ReadFile(file(stateDir, tree))
	if err != nil {
		return time.Time{}, "", false
	}
	millis, owner, _ := strings.Cut(strings.TrimSpace(string(b)), " ")
	ms, err := strconv.ParseInt(millis, 10, 64)
	if err != nil {
		return time.Time{}, "", false
	}
	return time.UnixMilli(ms), owner, true
}
