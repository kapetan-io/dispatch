// Package config loads the dispatcher's settings from config.toml.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config mirrors ~/.config/dispatch/config.toml. The blueprint's "Config"
// section documents every field.
type Config struct {
	IntervalMinutes    int `toml:"interval_minutes"`
	MaxInProgress      int `toml:"max_in_progress"`
	LaunchFailureLimit int `toml:"launch_failure_limit"`
	NudgeLimit         int `toml:"nudge_limit"`
	// BlockedRenotifyTicks is how many ticks pass between notifications for a
	// worker that stays blocked on a question.
	BlockedRenotifyTicks int               `toml:"blocked_renotify_ticks"`
	PluginRoot           string            `toml:"plugin_root"`
	WorkerModel          string            `toml:"worker_model"`
	Linear               Linear            `toml:"linear"`
	Repos                map[string]string `toml:"repos"`
}

type Linear struct {
	APIKeyKeychain string   `toml:"api_key_keychain"`
	Teams          []string `toml:"teams"`
	AILabel        string   `toml:"ai_label"`
	RepoLabelGroup string   `toml:"repo_label_group"`
	Statuses       Statuses `toml:"statuses"`
}

type Statuses struct {
	InProgress string `toml:"in_progress"`
	InReview   string `toml:"in_review"`
	NeedsHuman string `toml:"needs_human"`
	// Done lists the statuses of a finished ticket, whose worker and tree are
	// cleaned up. Paused lists the statuses of a ticket whose worker is stopped
	// and whose tree is kept.
	Done   []string `toml:"done"`
	Paused []string `toml:"paused"`
}

func defaults() Config {
	return Config{
		IntervalMinutes:      5,
		MaxInProgress:        4,
		LaunchFailureLimit:   2,
		NudgeLimit:           2,
		BlockedRenotifyTicks: 3,
		PluginRoot:           "~/Development/dispatch",
		WorkerModel:          "sonnet",
		Linear: Linear{
			APIKeyKeychain: "dispatch-linear-api-key",
			Teams:          []string{"ENG"},
			AILabel:        "ai",
			RepoLabelGroup: "repo",
			Statuses: Statuses{
				InProgress: "In Progress",
				InReview:   "In Review",
				NeedsHuman: "Needs Human",
				Done:       []string{"Done", "Canceled", "Duplicate"},
				Paused:     []string{"Backlog", "Todo"},
			},
		},
	}
}

// Load reads path over the defaults and expands a leading ~ in every path.
func Load(path string) (Config, error) {
	c := defaults()
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return Config{}, fmt.Errorf("while reading config %s: %w", path, err)
	}
	c.PluginRoot = ExpandHome(c.PluginRoot)
	for label, dir := range c.Repos {
		c.Repos[label] = ExpandHome(dir)
	}
	return c, nil
}

// ExpandHome replaces a leading ~ with the user's home directory.
func ExpandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~"))
}
