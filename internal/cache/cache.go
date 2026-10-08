// Package cache holds the per-ticket counters a tick carries to the next one.
// Deleting the cache loses little: every field is a counter that may reset,
// a last-seen value that may repeat one notification, or an escalation
// comment Linear has not yet accepted, which would then never be posted.
package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Entry is one ticket's state between ticks.
type Entry struct {
	LastStatus     string `json:"last_status"`
	LaunchFailures int    `json:"launch_failures"`
	// PendingComment is an escalation comment Linear has not accepted yet.
	// The ticket is already in Needs Human, and each tick retries it.
	PendingComment string `json:"pending_comment,omitempty"`
}

// Cache maps a ticket identifier such as ENG-123 to its entry.
type Cache map[string]Entry

// Load reads the cache at path. A missing file is an empty cache.
func Load(path string) (Cache, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Cache{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("while reading cache %s: %w", path, err)
	}
	c := Cache{}
	if err := json.Unmarshal(b, &c); err != nil {
		// A corrupt cache is treated as empty, which the design allows.
		return Cache{}, nil
	}
	return c, nil
}

// Save writes the cache atomically, so a crash never leaves half a file.
func (c Cache) Save(path string) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cache-*.json")
	if err != nil {
		return fmt.Errorf("while writing cache: %w", err)
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("while writing cache: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("while writing cache: %w", err)
	}
	return os.Rename(tmp.Name(), path)
}
