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

// isDraft is gh's refusal of a merge of a pull request still in draft.
const isDraft = "Pull request is still a draft"

// Repository is a pullrequest.Repository backed by memory. Its refusals come
// only from pull request state. Failures that don't come from that state (an
// outage, bad credentials) go through FailRead and FailMerge, and
// never change what a merge accepts.
type Repository struct {
	journal *journal.Journal

	mu        sync.Mutex
	pages     map[string]map[string]map[string]any
	older     map[string]map[string]any
	pushes    map[string]string
	merged    []string
	readFail  error
	mergeFail error
}

// New returns a Repository with no pull requests that records its calls in j.
func New(j *journal.Journal) *Repository {
	return &Repository{
		journal: j,
		pages:   map[string]map[string]map[string]any{},
		older:   map[string]map[string]any{},
		pushes:  map[string]string{},
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

// RespondOlder sets what GitHub answers when asked for the page of PR number's
// reviews or comments (kind is "reviews" or "comments") that ends before the
// cursor. A page that was not set fails the read, because asking for it was a
// call the real tool would have made.
func (r *Repository) RespondOlder(number int, kind, before string, response any) {
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
	r.older[fmt.Sprintf("%d|%s|%s", number, kind, before)] = page
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
// after the next Read of that branch that finds the PR returns, which is a push
// that lands after dispatch looked. A failed read leaves the push queued.
func (r *Repository) PushAfterRead(branch, head string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pushes[branch] = head
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

func (r *Repository) Read(ctx context.Context, repoDir, branch string, rules pullrequest.Rules) (pullrequest.PullRequest, bool, error) {
	pr, found, err := pullrequest.Find(ctx, branch, source{r, branch}, rules)
	r.mu.Lock()
	defer r.mu.Unlock()
	if head, ok := r.pushes[branch]; ok && err == nil && found {
		r.push(branch, head)
		delete(r.pushes, branch)
	}
	return pr, found, err
}

// source answers Find from the pages the test set.
type source struct {
	repo   *Repository
	branch string
}

func (s source) PRPage(ctx context.Context, branch, cursor string) ([]byte, error) {
	r := s.repo
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
}

func (s source) Older(ctx context.Context, number int, kind, before string) ([]byte, error) {
	r := s.repo
	r.journal.Record("pullrequest", "older", fmt.Sprint(number), kind, before)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.readFail != nil {
		return nil, r.readFail
	}
	page, ok := r.older[fmt.Sprintf("%d|%s|%s", number, kind, before)]
	if !ok {
		return nil, fmt.Errorf("no %s page of PR %d before %s was set", kind, number, before)
	}
	return json.Marshal(page)
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
				if draft, _ := node["isDraft"].(bool); draft {
					return fmt.Errorf("while running gh pr merge %s: exit status 1: %s", prURL, isDraft)
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
