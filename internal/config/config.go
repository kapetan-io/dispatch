// Package config loads the dispatcher's settings from config.toml.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

var lastKey = regexp.MustCompile(`\(last key "([^"]*)"\)`)

var bareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Pull request phases a repo can open its PRs in.
const (
	OpenPRAsOpen  = "open"
	OpenPRAsDraft = "draft"
)

// Config mirrors ~/.config/dispatch/config.toml. docs/config.md documents every field.
type Config struct {
	IntervalMinutes    int `toml:"interval_minutes"`
	MaxInProgress      int `toml:"max_in_progress"`
	LaunchFailureLimit int `toml:"launch_failure_limit"`
	NudgeLimit         int `toml:"nudge_limit"`
	// BlockedRenotifyTicks is how many ticks pass between notifications for a
	// worker that stays blocked on a question.
	BlockedRenotifyTicks int    `toml:"blocked_renotify_ticks"`
	PluginRoot           string `toml:"plugin_root"`
	WorkerModel          string `toml:"worker_model"`
	DesignModel          string `toml:"design_model"`
	Linear               Linear `toml:"linear"`
	Repos                Repos  `toml:"repos"`
}

// Repo is the settings table of one repo, keyed by its Linear repo label.
type Repo struct {
	// Path is the local clone workers are launched from.
	Path string `toml:"path"`
	// OpenPRAs is the phase workers open this repo's PRs in, "open" or "draft".
	OpenPRAs             string   `toml:"open_pr_as"`
	IgnoreCommentAuthors []string `toml:"ignore_comment_authors"`
	IgnoreOptionalChecks bool     `toml:"ignore_optional_checks"`
	// AutoMerge makes dispatch merge a ready PR once a review approves its head commit.
	AutoMerge bool `toml:"auto_merge"`
}

// Repos maps a repo label to its settings. It decodes itself so that every
// failure names the repo and the fix, which the generic decoder can't.
type Repos map[string]Repo

type Linear struct {
	// APIKeyCommand is a shell command that prints the Linear API key. It wins
	// over APIKeyKeychain, so a machine that forbids the keychain never touches it.
	APIKeyCommand  string   `toml:"api_key_command"`
	APIKeyKeychain string   `toml:"api_key_keychain"`
	Teams          []string `toml:"teams"`
	AILabel        string   `toml:"ai_label"`
	RepoLabelGroup string   `toml:"repo_label_group"`
	Statuses       Statuses `toml:"statuses"`
}

type Statuses struct {
	// Todo is the status of a child waiting for promotion. CloseParent is the
	// status a parent gets once its children are finished. BlockerDone is the
	// status a blocker must reach before its child is promoted, which is where
	// a merged PR leaves a ticket.
	Todo        string `toml:"todo"`
	CloseParent string `toml:"close_parent"`
	BlockerDone string `toml:"blocker_done"`
	InProgress  string `toml:"in_progress"`
	InReview    string `toml:"in_review"`
	NeedsHuman  string `toml:"needs_human"`
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
		DesignModel:          "opus",
		Linear: Linear{
			APIKeyKeychain: "dispatch-linear-api-key",
			Teams:          []string{"ENG"},
			AILabel:        "ai",
			RepoLabelGroup: "repo",
			Statuses: Statuses{
				Todo:        "Todo",
				CloseParent: "Done",
				BlockerDone: "Done",
				InProgress:  "In Progress",
				InReview:    "In Review",
				NeedsHuman:  "Needs Human",
				Done:        []string{"Done", "Canceled", "Duplicate"},
				Paused:      []string{"Backlog", "Todo"},
			},
		},
	}
}

// Load reads path over the defaults and expands a leading ~ in every path. It
// fails on a setting dispatch does not know.
func Load(path string) (Config, error) {
	c := defaults()
	meta, err := toml.DecodeFile(path, &c)
	if msg, ok := repoProblem(err); ok {
		return Config{}, fmt.Errorf("while reading config %s: %s", path, msg)
	}
	if err != nil {
		return Config{}, fmt.Errorf("while reading config %s: %w", path, err)
	}
	if meta.IsDefined("auto_merge") {
		return Config{}, fmt.Errorf("while reading config %s: auto_merge moved into each repo's table: set auto_merge = true under [repos.<label>]", path)
	}
	// A misspelled key would otherwise fall back to its default without a word.
	// Repos validates its own keys while decoding, so its subkeys never count.
	var unknown []string
	for _, key := range meta.Undecoded() {
		if key[0] != "repos" {
			unknown = append(unknown, key.String())
		}
	}
	if len(unknown) > 0 {
		return Config{}, fmt.Errorf("while reading config %s: unknown setting %s", path, strings.Join(unknown, ", "))
	}
	c.PluginRoot = ExpandHome(c.PluginRoot)
	for _, label := range c.Repos.Labels() {
		repo := c.Repos[label]
		repo.Path = ExpandHome(repo.Path)
		c.Repos[label] = repo
	}
	if err := c.Repos.checkPaths(); err != nil {
		return Config{}, fmt.Errorf("while reading config %s: %w", path, err)
	}
	return c, nil
}

// repoProblem extracts the message of a problem Repos found in a repo table.
// The decoder wraps it with a line number pointing at the [repos] header, which
// helps nobody, so Load prints the bare message. Decoder and syntax errors all
// start with "toml:", and Repos messages never do.
func repoProblem(err error) (string, bool) {
	var parse toml.ParseError
	if !errors.As(err, &parse) || parse.LastKey != "repos" || strings.HasPrefix(parse.Message, "toml:") {
		return "", false
	}
	return parse.Message, true
}

// Labels returns the repo labels in sorted order.
func (r Repos) Labels() []string {
	labels := make([]string, 0, len(r))
	for label := range r {
		labels = append(labels, label)
	}
	slices.Sort(labels)
	return labels
}

// UnmarshalTOML decodes the [repos] table. Each entry must be a table, and
// Load's error names the repo and what to write instead.
func (r *Repos) UnmarshalTOML(data any) error {
	entries, ok := data.(map[string]any)
	if !ok {
		return fmt.Errorf("[repos] must hold one [repos.<label>] table per repo")
	}
	*r = Repos{}
	labels := make([]string, 0, len(entries))
	for label := range entries {
		labels = append(labels, label)
	}
	slices.Sort(labels)
	for _, label := range labels {
		table, ok := entries[label].(map[string]any)
		if !ok {
			if path, isString := entries[label].(string); isString {
				return fmt.Errorf("[repos] entry %q must be a table: write [repos.%s] with path = %q", label, label, path)
			}
			return fmt.Errorf("[repos] entry %q must be a table: write [repos.%s] with a path", label, label)
		}
		repo, err := decodeRepo(label, table)
		if err != nil {
			return err
		}
		(*r)[label] = repo
	}
	return nil
}

// decodeRepo reads one repo table by encoding it back to TOML and decoding it
// into Repo, so the decoder's type checks apply to every key.
func decodeRepo(label string, table map[string]any) (Repo, error) {
	repo := Repo{OpenPRAs: OpenPRAsOpen, IgnoreCommentAuthors: []string{}}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(table); err != nil {
		return Repo{}, fmt.Errorf("repos.%s: %v", label, err)
	}
	meta, err := toml.Decode(buf.String(), &repo)
	if err != nil {
		msg := err.Error()
		key := ""
		if m := lastKey.FindStringSubmatch(msg); m != nil {
			key = "." + m[1]
		}
		if i := strings.Index(msg, "): "); i >= 0 {
			msg = msg[i+3:]
		}
		return Repo{}, fmt.Errorf("repos.%s%s: %s", label, key, msg)
	}
	if unknown := meta.Undecoded(); len(unknown) > 0 {
		return Repo{}, fmt.Errorf("unknown setting repos.%s.%s: remove it or fix the spelling", label, unknown[0])
	}
	if repo.Path == "" {
		return Repo{}, fmt.Errorf("repos.%s has no path: add path = \"<clone directory>\" under [repos.%s]", label, label)
	}
	if repo.OpenPRAs != OpenPRAsOpen && repo.OpenPRAs != OpenPRAsDraft {
		return Repo{}, fmt.Errorf("repos.%s.open_pr_as must be %q or %q, not %q", label, OpenPRAsOpen, OpenPRAsDraft, repo.OpenPRAs)
	}
	return repo, nil
}

// checkPaths fails when two repos share a path. A path that does not exist
// yet is compared as written.
func (r Repos) checkPaths() error {
	owner := map[string]string{}
	for _, label := range r.Labels() {
		path := filepath.Clean(r[label].Path)
		// A path that exists is compared by where it really points, so a symlink to a clone counts as that clone.
		real := path
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			real = resolved
		}
		if first, ok := owner[real]; ok {
			return fmt.Errorf("repos.%s and repos.%s have the same path %s: give each repo its own clone", first, label, path)
		}
		owner[real] = label
	}
	return nil
}

// Encode writes the repo as the TOML table [repos.<label>], in a form Load accepts.
func (r Repo) Encode(label string) (string, error) {
	var buf bytes.Buffer
	if !bareKey.MatchString(label) {
		label = strconv.Quote(label)
	}
	buf.WriteString("[repos." + label + "]\n")
	enc := toml.NewEncoder(&buf)
	enc.Indent = ""
	if err := enc.Encode(r); err != nil {
		return "", err
	}
	return buf.String(), nil
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
