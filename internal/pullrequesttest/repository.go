// Package pullrequesttest is an in-process stand-in for GitHub's pull requests
// as gh shows them. It holds the pages of GitHub's answer for each branch, so
// the real parser reads them, and refuses to merge a pull request whose head
// moved since the caller looked.
package pullrequesttest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/kapetan-io/dispatch/internal/journal"
	"github.com/kapetan-io/dispatch/internal/pullrequest"
)

// headMoved is gh's refusal of a merge whose --match-head-commit is no longer
// the head of the pull request.
const headMoved = "Head branch was modified. Review and try the merge again."

// Repository is a pullrequest.Repository backed by memory. Its refusals come
// only from pull request state. Failures that don't come from that state (an
// outage, bad credentials) go through FailRead, FailMerge and FailRepoName, and
// never change what a merge accepts.
type Repository struct {
	journal *journal.Journal

	mu        sync.Mutex
	pages     map[string]map[string]map[string]any
	pushes    map[string]string
	merged    []string
	name      string
	readFail  error
	mergeFail error
	nameFail  error
}

// New returns a Repository with no pull requests that records its calls in j.
func New(j *journal.Journal) *Repository {
	return &Repository{
		journal: j,
		pages:   map[string]map[string]map[string]any{},
		pushes:  map[string]string{},
		name:    "kapetan-io/dispatch",
	}
}

// Respond sets what GitHub answers for the PR query on branch, the first page
// when cursor is "" and the page after cursor otherwise. response is the
// decoded JSON body gh prints.
func (r *Repository) Respond(branch, cursor string, response any) {
	b, err := json.Marshal(response)
	if err != nil {
		panic(err)
	}
	var page map[string]any
	if err := json.Unmarshal(b, &page); err != nil {
		panic(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pages[branch] == nil {
		r.pages[branch] = map[string]map[string]any{}
	}
	r.pages[branch][cursor] = page
}

// Close closes the open pull requests of branch without merging them.
func (r *Repository) Close(branch string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.pages, branch)
}

// Merged lists the pull requests that merged, each as url|head, in order.
func (r *Repository) Merged() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.merged...)
}

// Head says the head commit of the worker's own pull request on branch, or ""
// when it has none.
func (r *Repository) Head(branch string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, page := range r.pages[branch] {
		for _, node := range nodes(page) {
			if cross, _ := node["isCrossRepository"].(bool); !cross {
				head, _ := node["headRefOid"].(string)
				return head
			}
		}
	}
	return ""
}

// PushAfterRead moves the head of the pull request on branch to head right
// after the next Read of that branch returns, which is a push that lands after
// dispatch looked.
func (r *Repository) PushAfterRead(branch, head string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pushes[branch] = head
}

// SetRepoName sets the owner/name that RepoName reports.
func (r *Repository) SetRepoName(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.name = name
}

// FailRead makes every Read fail with err, as gh does without credentials.
// FailRead(nil) restores normal reads.
func (r *Repository) FailRead(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.readFail = err
}

// FailMerge makes every Merge fail with err, as gh does when branch protection
// prohibits the merge. FailMerge(nil) restores normal merges.
func (r *Repository) FailMerge(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mergeFail = err
}

// FailRepoName makes every RepoName call fail with err, as gh does in a clone
// with no remote. FailRepoName(nil) restores normal lookups.
func (r *Repository) FailRepoName(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nameFail = err
}

func (r *Repository) Read(ctx context.Context, repoDir, branch string) (pullrequest.PullRequest, bool, error) {
	pr, found, err := pullrequest.Find(ctx, branch, func(ctx context.Context, branch, cursor string) ([]byte, error) {
		r.journal.Record("pullrequest", "read", branch, cursor)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.readFail != nil {
			return nil, r.readFail
		}
		page, ok := r.pages[branch][cursor]
		if !ok {
			page = map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequests": map[string]any{"nodes": []any{}}}}}
		}
		return json.Marshal(page)
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	if head, ok := r.pushes[branch]; ok {
		r.push(branch, head)
		delete(r.pushes, branch)
	}
	return pr, found, err
}

func (r *Repository) Merge(ctx context.Context, prURL, head string) error {
	r.journal.Record("pullrequest", "merge", prURL, head)
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.mergeFail != nil {
		return r.mergeFail
	}
	for branch, pages := range r.pages {
		for _, page := range pages {
			for _, node := range nodes(page) {
				if cross, _ := node["isCrossRepository"].(bool); cross || node["url"] != prURL {
					continue
				}
				if node["headRefOid"] != head {
					return fmt.Errorf("while running gh pr merge %s: exit status 1: %s", prURL, headMoved)
				}
				// A merged PR leaves the branch with no open PR; a fork PR on the
				// same name would not matter, since Find skips it either way.
				delete(r.pages, branch)
				r.merged = append(r.merged, prURL+"|"+head)
				return nil
			}
		}
	}
	return fmt.Errorf("while running gh pr merge %s: exit status 1: GraphQL: Could not resolve to a PullRequest with the URL '%s'.", prURL, strings.TrimSpace(prURL))
}

func (r *Repository) RepoName(ctx context.Context, repoDir string) (string, error) {
	r.journal.Record("pullrequest", "repo-name", repoDir)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.nameFail != nil {
		return "", r.nameFail
	}
	return r.name, nil
}

// push moves the head of the worker's own pull request on branch.
func (r *Repository) push(branch, head string) {
	for _, page := range r.pages[branch] {
		for _, node := range nodes(page) {
			if cross, _ := node["isCrossRepository"].(bool); !cross {
				node["headRefOid"] = head
			}
		}
	}
}

// nodes returns the pull requests of a page of GitHub's answer, which the
// caller may change in place.
func nodes(page map[string]any) []map[string]any {
	data, _ := page["data"].(map[string]any)
	repo, _ := data["repository"].(map[string]any)
	prs, _ := repo["pullRequests"].(map[string]any)
	list, _ := prs["nodes"].([]any)
	var out []map[string]any
	for _, n := range list {
		if node, ok := n.(map[string]any); ok {
			out = append(out, node)
		}
	}
	return out
}

var _ pullrequest.Repository = (*Repository)(nil)
