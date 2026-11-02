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
	"slices"
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
	return mark(stateDir, tree, at, true)
}

// MarkUnlessHeld records the launch like Mark, but when a record is already there it adds this
// launch as a further owner instead of replacing the others. The time moves to this launch's
// start, because a record left by a dead launch would otherwise date this one as already past
// its grace period. A launch that may be refused uses it, so its release never erases the
// record while another launch still holds it.
func MarkUnlessHeld(stateDir, tree string, at time.Time) (string, error) {
	return mark(stateDir, tree, at, false)
}

func mark(stateDir, tree string, at time.Time, replace bool) (string, error) {
	name := file(stateDir, tree)
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return "", fmt.Errorf("while recording the launch: %w", err)
	}
	unlock, err := lockTree(name)
	if err != nil {
		return "", err
	}
	defer unlock()
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	id := hex.EncodeToString(raw[:])
	owners := []string{id}
	if _, heldOwners, ok := read(stateDir, tree); ok && !replace {
		owners = append(heldOwners, id)
	}
	return id, write(name, at, owners)
}

func write(name string, at time.Time, owners []string) error {
	tmp, err := os.CreateTemp(filepath.Dir(name), ".kickoff-*.tmp")
	if err != nil {
		return fmt.Errorf("while recording the launch: %w", err)
	}
	_, werr := tmp.WriteString(strconv.FormatInt(at.UnixMilli(), 10) + " " + strings.Join(owners, " "))
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("while recording the launch: %w", werr)
	}
	return os.Rename(tmp.Name(), name)
}

// Release drops the launch with id from the owners of the record of tree, and removes the record
// once no owner is left. A launch that a newer one replaced leaves the newer one's record alone.
func Release(stateDir, tree, id string) {
	name := file(stateDir, tree)
	unlock, err := lockTree(name)
	if err != nil {
		return
	}
	defer unlock()
	at, owners, ok := read(stateDir, tree)
	if !ok || !slices.Contains(owners, id) {
		return
	}
	if owners = slices.DeleteFunc(owners, func(owner string) bool { return owner == id }); len(owners) == 0 {
		_ = os.Remove(name)
		return
	}
	_ = write(name, at, owners)
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

func read(stateDir, tree string) (at time.Time, owners []string, ok bool) {
	b, err := os.ReadFile(file(stateDir, tree))
	if err != nil {
		return time.Time{}, nil, false
	}
	millis, rest, _ := strings.Cut(strings.TrimSpace(string(b)), " ")
	ms, err := strconv.ParseInt(millis, 10, 64)
	if err != nil {
		return time.Time{}, nil, false
	}
	return time.UnixMilli(ms), strings.Fields(rest), true
}
