// Package config loads the dispatcher's settings from config.toml.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/kapetan-io/dispatch/internal/environment"
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
	Dispatch Dispatch               `toml:"dispatch"`
	History  History                `toml:"history"`
	Repos    Repos                  `toml:"repos"`
	Plugins  Plugins                `toml:"plugins"`
	HTTP     map[string]HTTPService `toml:"http"`
	// Harness holds the [harness.<name>] tables. Rules name one as "harness.<name>"
	// and never see the tables themselves.
	Harness map[string]Harness `toml:"harness" ctx:"-"`
	// Rules is the user's own table. Dispatch never interprets it, apart from
	// the five keys `dispatch rules init` writes (see the accessors below).
	Rules Rules `toml:"rules"`
}

// Dispatch holds the settings of the dispatcher itself.
type Dispatch struct {
	// Rules is the directory of TypeScript rules.
	Rules         string   `toml:"rules"`
	MaxInProgress int      `toml:"max_in_progress"`
	Resync        Duration `toml:"resync"`
}

// History holds where and how long dispatch keeps its events.
type History struct {
	Dir    string   `toml:"dir"`
	Rotate Duration `toml:"rotate"`
	Keep   Duration `toml:"keep"`
}

// Plugins holds one table per plugin.
type Plugins struct {
	Linear    Linear    `toml:"linear"`
	Herdr     Herdr     `toml:"herdr"`
	GitHub    GitHub    `toml:"github"`
	Treehouse Treehouse `toml:"treehouse"`
	Notify    Notify    `toml:"notify"`
}

// Herdr is the herdr plugin's table.
type Herdr struct {
	// PluginRoot is the checkout of this repo that workers load their plugin from.
	PluginRoot string `toml:"plugin_root"`
}

// GitHub is the GitHub plugin's table. It has no settings yet.
type GitHub struct{}

// Treehouse is the treehouse plugin's table. It has no settings yet.
type Treehouse struct{}

// Notify is the notify plugin's table. It has no settings yet.
type Notify struct{}

// HTTPService is a service named for rules to call through ctx.http.
type HTTPService struct {
	BaseURL string `toml:"base_url"`
	// TokenCommand prints the token. ctx.config leaves it out, so a rule never sees how the token is found.
	TokenCommand string `toml:"token_command" ctx:"-"`
}

// Repo is the settings table of one repo, keyed by its Linear repo label.
type Repo struct {
	// Path is the local clone workers are launched from.
	Path string `toml:"path"`
	// OpenPRAs is the phase workers open this repo's PRs in, "open" or "draft".
	OpenPRAs             string   `toml:"open_pr_as"`
	IgnoreCommentAuthors []string `toml:"ignore_comment_authors"`
	IgnoreOptionalChecks bool     `toml:"ignore_optional_checks"`
	// ApprovalChecks names checks that pass only after a human approves the PR, such as Mergify's "Merge Protections".
	ApprovalChecks []string `toml:"approval_checks"`
	// AutoMerge makes dispatch merge a ready PR once a review approves its head commit.
	AutoMerge bool `toml:"auto_merge"`
	// Worker and Design name the harness table, as "harness.<name>", that this repo's build and
	// design workers launch on. Unset or empty means the [rules] value.
	Worker string `toml:"worker,omitempty"`
	Design string `toml:"design,omitempty"`
}

// Repos maps a repo label to its settings. It decodes itself so that every
// failure names the repo and the fix, which the generic decoder can't.
type Repos map[string]Repo

type Linear struct {
	// APIKeyCommand is a shell command that prints the Linear API key. It wins
	// over APIKeyKeychain, so a machine that forbids the keychain never touches it.
	APIKeyCommand  string   `toml:"api_key_command" ctx:"-"`
	APIKeyKeychain string   `toml:"api_key_keychain" ctx:"-"`
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
	// Finished lists the statuses of a finished ticket, whose worker and tree are
	// cleaned up. Paused lists the statuses of a ticket whose worker is stopped
	// and whose tree is kept.
	Finished []string `toml:"finished"`
	Paused   []string `toml:"paused"`
}

func defaults() Config {
	return Config{
		Dispatch: Dispatch{
			Rules:         "~/Development/dispatch/rules",
			MaxInProgress: 4,
			Resync:        Duration(5 * time.Minute),
		},
		History: History{
			Dir:    "~/.local/state/dispatch/events",
			Rotate: Duration(6 * time.Hour),
			Keep:   Duration(24 * time.Hour),
		},
		Plugins: Plugins{
			Herdr: Herdr{PluginRoot: "~/Development/dispatch"},
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
					Finished:    []string{"Done", "Canceled", "Duplicate"},
					Paused:      []string{"Backlog", "Todo"},
				},
			},
		},
	}
}

// Load reads path over the defaults and expands a leading ~ in every path to the HOME of env. It
// fails on a setting dispatch does not know.
func Load(path string, env environment.Environment) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("while reading config %s: %w", path, err)
	}
	return parse(string(data), path, env, true)
}

// Parse reads config text as Load reads a file, without requiring the repos' clones to
// exist, because the text comes from history and a clone may have moved since.
func Parse(text string, env environment.Environment) (Config, error) {
	return parse(text, "recorded in history", env, false)
}

func parse(text, path string, env environment.Environment, checkPaths bool) (Config, error) {
	c := defaults()
	meta, err := toml.Decode(text, &c)
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
	// Repos validates its own keys while decoding, and [rules] is the user's
	// own table, so neither's subkeys count.
	var unknown []string
	for _, key := range meta.Undecoded() {
		if key[0] != "repos" && key[0] != "rules" {
			unknown = append(unknown, key.String())
		}
	}
	if len(unknown) == 1 {
		return Config{}, fmt.Errorf("while reading config %s: unknown setting %s", path, unknown[0])
	}
	if len(unknown) > 1 {
		return Config{}, fmt.Errorf("while reading config %s: unknown settings:\n  %s", path, strings.Join(unknown, "\n  "))
	}
	if err := c.check(); err != nil {
		return Config{}, fmt.Errorf("while reading config %s: %w", path, err)
	}
	c.Plugins.Herdr.PluginRoot = env.ExpandHome(c.Plugins.Herdr.PluginRoot)
	c.Dispatch.Rules = env.ExpandHome(c.Dispatch.Rules)
	c.History.Dir = env.ExpandHome(c.History.Dir)
	for _, label := range c.Repos.Labels() {
		repo := c.Repos[label]
		repo.Path = env.ExpandHome(repo.Path)
		c.Repos[label] = repo
	}
	if checkPaths {
		if err := c.Repos.checkPaths(); err != nil {
			return Config{}, fmt.Errorf("while reading config %s: %w", path, err)
		}
	}
	return c, nil
}

// check rejects values that decode but make no sense.
func (c Config) check() error {
	if c.History.Rotate <= 0 || c.History.Keep <= 0 {
		return errors.New("history.rotate and history.keep must be above zero")
	}
	for name, service := range c.HTTP {
		if service.BaseURL == "" {
			return fmt.Errorf("http.%s has no base_url", name)
		}
		if u, err := url.Parse(service.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
			u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(service.BaseURL, "#") {
			return fmt.Errorf("http.%s base_url must be an http or https URL with a host, no user, no query and no fragment, got %q", name, service.BaseURL)
		}
	}
	if err := c.Rules.check(); err != nil {
		return err
	}
	for _, name := range slices.Sorted(maps.Keys(c.Harness)) {
		if err := c.Harness[name].check(name); err != nil {
			return err
		}
	}
	for _, role := range roles {
		if ref, _ := c.Rules[role].(string); ref != "" {
			if err := c.checkHarnessRef("rules."+role, ref); err != nil {
				return err
			}
		}
	}
	for _, label := range c.Repos.Labels() {
		repo := c.Repos[label]
		for i, ref := range []string{repo.Worker, repo.Design} {
			if ref == "" {
				continue
			}
			if err := c.checkHarnessRef("repos."+label+"."+roles[i], ref); err != nil {
				return err
			}
		}
	}
	return nil
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

// Find returns the table a ticket's repo label names and the table's own
// spelling of it. A table spelled exactly like the label wins over one that
// differs only in case.
func (r Repos) Find(label string) (string, Repo, bool) {
	if repo, ok := r[label]; ok {
		return label, repo, true
	}
	for _, name := range r.Labels() {
		if strings.EqualFold(name, label) {
			return name, r[name], true
		}
	}
	return "", Repo{}, false
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
	repo := Repo{OpenPRAs: OpenPRAsOpen, IgnoreCommentAuthors: []string{}, ApprovalChecks: []string{}}
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

// checkPaths fails when two repos share a path, compared as written after
// cleaning. Symlinks are not resolved, since a path may not exist yet and the
// same config must load on a machine that hasn't cloned the repos.
func (r Repos) checkPaths() error {
	owner := map[string]string{}
	for _, label := range r.Labels() {
		path := filepath.Clean(r[label].Path)
		if first, ok := owner[path]; ok {
			return fmt.Errorf("repos.%s and repos.%s have the same path %s: give each repo its own clone", first, label, path)
		}
		owner[path] = label
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
