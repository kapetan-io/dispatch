// Package inbox reads the dispatch owner's GitHub notifications inbox, nothing else. An error
// from a read means the inbox could not be read, never that it is empty.
package inbox

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/environment"
)

// Limit is how many items the world holds, the newest by UpdatedAt.
const Limit = 50

// Inbox is the owner's GitHub notifications inbox.
type Inbox interface {
	// Read returns the newest Limit items, read or unread, from any repo, newest first.
	Read(ctx context.Context) ([]Item, error)
}

// Item is one entry in the inbox, with the fields as GitHub listed them.
type Item struct {
	// ID is GitHub's thread id. It stays the same as notifications arrive on the thread.
	ID string `json:"id"`
	// Reason is why GitHub sent it, the newest one if the thread has had several.
	Reason string `json:"reason"`
	Unread bool   `json:"unread"`
	// UpdatedAt is GitHub's updated_at, RFC 3339 as written.
	UpdatedAt string `json:"updatedAt"`
	// RepoName is owner/name.
	RepoName string `json:"repoName"`
	// SubjectType is GitHub's subject.type: PullRequest, Issue, Release, ...
	SubjectType string `json:"subjectType"`
	Title       string `json:"title"`
	// APIURL is GitHub's subject.url, "" when GitHub gives none.
	APIURL string `json:"apiUrl,omitempty"`
}

// Newest sorts items newest first by UpdatedAt, ties by ID, and keeps the first Limit. Parse, the
// scenario fixtures and the in-process fake all end here, so every Inbox hands back the same set.
func Newest(items []Item) []Item {
	out := slices.Clone(items)
	when := func(i Item) time.Time {
		at, _ := time.Parse(time.RFC3339, i.UpdatedAt)
		return at
	}
	slices.SortStableFunc(out, func(a, b Item) int {
		if c := when(b).Compare(when(a)); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	if len(out) > Limit {
		out = out[:Limit]
	}
	return out
}

// thread is one element of GitHub's notifications list.
type thread struct {
	ID         string `json:"id"`
	Unread     bool   `json:"unread"`
	Reason     string `json:"reason"`
	UpdatedAt  string `json:"updated_at"`
	Repository *struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Subject struct {
		Title string  `json:"title"`
		URL   *string `json:"url"`
		Type  string  `json:"type"`
	} `json:"subject"`
}

// Parse reads the JSON list gh prints and returns the newest Limit items. It copies GitHub's
// fields and refuses the whole page when an element has no id, no repository or an updated_at
// that is not RFC 3339.
func Parse(b []byte) ([]Item, error) {
	var threads []thread
	if err := json.Unmarshal(b, &threads); err != nil {
		return nil, fmt.Errorf("while reading gh output for the inbox: %w", err)
	}
	items := make([]Item, 0, len(threads))
	for i, t := range threads {
		switch {
		case t.ID == "":
			return nil, fmt.Errorf("while reading gh output for the inbox: element %d has no id", i)
		case t.Repository == nil || t.Repository.FullName == "":
			return nil, fmt.Errorf("while reading gh output for the inbox: thread %s has no repository", t.ID)
		}
		if _, err := time.Parse(time.RFC3339, t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("while reading gh output for the inbox: thread %s has updated_at %q, which is not RFC 3339", t.ID, t.UpdatedAt)
		}
		item := Item{
			ID: t.ID, Reason: t.Reason, Unread: t.Unread, UpdatedAt: t.UpdatedAt, RepoName: t.Repository.FullName,
			SubjectType: t.Subject.Type, Title: t.Subject.Title,
		}
		if t.Subject.URL != nil {
			item.APIURL = *t.Subject.URL
		}
		items = append(items, item)
	}
	return Newest(items), nil
}

// CLI is the Inbox that runs the installed gh command.
type CLI struct {
	bin     string
	timeout time.Duration
	env     environment.Environment
}

// Option sets how a CLI runs gh.
type Option func(*CLI)

// WithEnv runs gh, and finds it, with env instead of the process's environment.
func WithEnv(env environment.Environment) Option {
	return func(c *CLI) { c.env = env }
}

const defaultTimeout = 30 * time.Second

// NewCLI returns an Inbox that runs bin.
func NewCLI(bin string, opts ...Option) *CLI {
	c := &CLI{bin: bin, timeout: defaultTimeout}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Read makes one gh call with no working directory, since the inbox belongs to the account and
// not to a clone. --method GET keeps -f from making it a POST, and all=true includes read items.
func (c *CLI) Read(ctx context.Context) ([]Item, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	cmd := c.env.Command(ctx, c.bin, "api", "notifications", "--method", "GET", "-f", "all=true", "-f", fmt.Sprintf("per_page=%d", Limit))
	// A child that inherits gh's pipes must not keep Output waiting after the kill.
	cmd.WaitDelay = time.Second
	b, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, fmt.Errorf("while running gh api notifications: %w: %s", err, strings.TrimSpace(string(exit.Stderr)))
		}
		return nil, fmt.Errorf("while running gh api notifications: %w", err)
	}
	return Parse(b)
}

var _ Inbox = (*CLI)(nil)
