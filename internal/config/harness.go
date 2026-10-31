package config

import (
	"fmt"
	"slices"
	"strings"
)

// KindClaudeCode is the kind of the Claude Code harness.
const KindClaudeCode = "claude-code"

// harnessPrefix starts the name a rule or a role key uses for a [harness.<name>] table.
const harnessPrefix = "harness."

// roles are the keys of [rules] and [repos.<label>] that name a harness table.
var roles = []string{"worker", "design"}

// Harness is one [harness.<name>] table, which pairs a harness kind with a model.
type Harness struct {
	Kind  string `toml:"kind"`
	Model string `toml:"model"`
}

// check fails on a table that cannot launch anything. The kinds are the harnesses the caller
// drives. Nil accepts any kind, for config text read back from history.
func (h Harness) check(name string, kinds []string) error {
	sortedKinds := slices.Sorted(slices.Values(kinds))
	if h.Kind == "" {
		return fmt.Errorf("harness.%s: kind is missing (%s)", name, strings.Join(sortedKinds, ", "))
	}
	if kinds != nil && !slices.Contains(kinds, h.Kind) {
		return fmt.Errorf("harness.%s: kind %q is not a harness this dispatch drives (%s)", name, h.Kind, strings.Join(sortedKinds, ", "))
	}
	if h.Model == "" {
		return fmt.Errorf("harness.%s: model is missing", name)
	}
	return nil
}

// Table returns the harness table that ref, spelled "harness.<name>", names.
func (c Config) Table(ref string) (Harness, error) {
	name, ok := strings.CutPrefix(ref, harnessPrefix)
	if !ok || name == "" {
		return Harness{}, fmt.Errorf("%q must be \"harness.<name>\"", ref)
	}
	table, ok := c.Harness[name]
	if !ok {
		return Harness{}, fmt.Errorf("%s has no [%s] table", ref, ref)
	}
	return table, nil
}

// checkHarnessRef fails when the config key names no harness table, naming the key. Its messages
// are the ones docs/config.md shows, so they differ from Table's, which a rule's or flag's ref gets.
func (c Config) checkHarnessRef(key, ref string) error {
	if !strings.HasPrefix(ref, harnessPrefix) {
		return fmt.Errorf("%s must be \"harness.<name>\", not %q", key, ref)
	}
	if _, err := c.Table(ref); err != nil {
		return fmt.Errorf("%s names %s, which has no [%s] table", key, ref, ref)
	}
	return nil
}
