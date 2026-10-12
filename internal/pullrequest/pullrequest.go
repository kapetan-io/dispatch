// Package pullrequest reads the state of a ticket's open PR from GitHub, in
// the terms the dispatcher needs to decide whether the PR needs work.
package pullrequest

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

// Repository is the GitHub repo a clone belongs to, where a ticket's branch has
// at most one open pull request.
type Repository interface {
	// Read finds the open PR whose head branch is branch in the repo cloned at
	// repoDir. It returns false when the branch has no open PR from this repo.
	Read(ctx context.Context, repoDir, branch string) (PullRequest, bool, error)
	// Merge merges the PR at prURL with a merge commit. It refuses, and merges
	// nothing, when the PR's head is no longer head, so a push after the
	// approval was read is never merged unreviewed.
	Merge(ctx context.Context, prURL, head string) error
	// RepoName returns the owner/name of the repo cloned at repoDir.
	RepoName(ctx context.Context, repoDir string) (string, error)
}

// PullRequest is an open pull request and what is wrong with it.
type PullRequest struct {
	Number int
	URL    string
	// Head is the SHA of the PR's head commit. A push moves it.
	Head string
	// Repo is the GitHub repo the PR belongs to, as owner/name, read from the
	// PR's own URL so it is right whichever remote gh resolved.
	Repo string
	// Approved means a reviewer's latest review approves the head commit and no
	// reviewer's latest review requests changes. An approval of an earlier
	// commit does not count, so a push clears it.
	Approved bool
	// ThreadAt is the RFC 3339 time of the newest comment on an unresolved
	// review thread, "" when there is none.
	ThreadAt string
	// ActivityAt is the RFC 3339 time of the newest review, conversation
	// comment or comment on an unresolved review thread, "" when there is none.
	// It skips approvals without a body,
	// dismissals and drafts, which ask the worker for nothing. A review with
	// only a body opens no thread and sets no reviewDecision, so this is the
	// only way the dispatcher sees it.
	ActivityAt string
	// ActivitySummary describes the item at ActivityAt, such as "a review by alice".
	ActivitySummary string
	// UnresolvedThreads means a review thread is still open. It keeps the PR from
	// being ready but is not a Problem, because the reviewer closes the thread,
	// and nudging every tick tells the worker about work it already did. The
	// thread's newest comment reaches the worker through ActivityAt instead.
	UnresolvedThreads bool
	// Problems are the reasons the PR needs work, phrased to follow "has".
	Problems []string
	// Ready means the PR needs no work, has no open review thread and nothing is still running: every check
	// has passed (a PR with no checks counts as passed) and GitHub says it
	// merges cleanly. A running check, such as a bot review still in progress,
	// or a merge state GitHub has not computed yet, is not ready.
	Ready bool
}

// NeedsWork reports whether the PR has a failed check, changes requested on its
// head or a merge conflict.
func (p PullRequest) NeedsWork() bool {
	return len(p.Problems) > 0
}

// Problem joins the problems into one phrase, such as "a failed check and a
// merge conflict".
func (p PullRequest) Problem() string {
	switch len(p.Problems) {
	case 0:
		return ""
	case 1:
		return p.Problems[0]
	}
	last := len(p.Problems) - 1
	return strings.Join(p.Problems[:last], ", ") + " and " + p.Problems[last]
}

// maxThreads is the review thread page size. A second page is an error, because
// an unresolved thread beyond the first page would go unnoticed.
const maxThreads = 100

// prPages bounds how many pages of same-named PRs Find walks looking for one
// from this repo. A branch with 20 fork PRs ahead of the worker's is the case
// this exists for, and more than prPages of them is an error rather than a miss.
const prPages = 5

const query = `query PullRequest($owner: String!, $repo: String!, $branch: String!, $threads: Int!, $after: String) {
  repository(owner: $owner, name: $repo) {
    pullRequests(headRefName: $branch, states: OPEN, first: 20, after: $after, orderBy: {field: UPDATED_AT, direction: DESC}) {
      pageInfo { hasNextPage endCursor }
      nodes {
        number url isCrossRepository headRefOid mergeable
        commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
        reviewThreads(first: $threads) { nodes { isResolved comments(last: 1) { nodes { createdAt author { login } } } } pageInfo { hasNextPage } }
        latestOpinionatedReviews(first: 100) { nodes { state commit { oid } } }
        reviews(last: 30) { nodes { state body createdAt submittedAt author { login } } }
        comments(last: 30) { nodes { createdAt author { login } } }
      }
    }
  }
}`

type prNode struct {
	Number            int    `json:"number"`
	IsCrossRepository bool   `json:"isCrossRepository"`
	URL               string `json:"url"`
	HeadRefOid        string `json:"headRefOid"`
	Mergeable         string `json:"mergeable"`
	Commits           struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
	LatestOpinionatedReviews struct {
		Nodes []struct {
			State  string `json:"state"`
			Commit struct {
				Oid string `json:"oid"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"latestOpinionatedReviews"`
	Reviews struct {
		Nodes []struct {
			State       string  `json:"state"`
			Body        string  `json:"body"`
			CreatedAt   string  `json:"createdAt"`
			SubmittedAt string  `json:"submittedAt"`
			Author      *author `json:"author"`
		} `json:"nodes"`
	} `json:"reviews"`
	Comments struct {
		Nodes []struct {
			CreatedAt string  `json:"createdAt"`
			Author    *author `json:"author"`
		} `json:"nodes"`
	} `json:"comments"`
	ReviewThreads struct {
		Nodes []struct {
			IsResolved bool `json:"isResolved"`
			Comments   struct {
				Nodes []struct {
					CreatedAt string  `json:"createdAt"`
					Author    *author `json:"author"`
				} `json:"nodes"`
			} `json:"comments"`
		} `json:"nodes"`
		PageInfo struct {
			HasNextPage bool `json:"hasNextPage"`
		} `json:"pageInfo"`
	} `json:"reviewThreads"`
}

type prPage struct {
	Data struct {
		Repository struct {
			PullRequests struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []prNode `json:"nodes"`
			} `json:"pullRequests"`
		} `json:"repository"`
	} `json:"data"`
}

// Fetch returns one page of GitHub's answer to the PR query for a branch, the
// page after cursor when cursor is not empty.
type Fetch func(ctx context.Context, branch, cursor string) ([]byte, error)

// Find walks the pages fetch returns for branch and picks the open PR from this
// repo, since a fork PR can share the branch name. It returns false when the
// branch has no such PR, and an error when the PR has more than maxThreads
// review threads.
func Find(ctx context.Context, branch string, fetch Fetch) (PullRequest, bool, error) {
	// headRefName matches fork PRs too, and anyone can open one on a branch
	// name Linear makes predictable. Only a PR from this repo is the worker's.
	var n prNode
	after := ""
	for page := 0; ; page++ {
		if page == prPages {
			return PullRequest{}, false, fmt.Errorf("branch %s has more than %d PRs and none from this repo in the first %d pages", branch, prPages*20, prPages)
		}
		b, err := fetch(ctx, branch, after)
		if err != nil {
			return PullRequest{}, false, err
		}
		var resp prPage
		if err := json.Unmarshal(b, &resp); err != nil {
			return PullRequest{}, false, fmt.Errorf("while reading gh output for %s: %w", branch, err)
		}
		prs := resp.Data.Repository.PullRequests
		found := false
		for _, node := range prs.Nodes {
			if !node.IsCrossRepository {
				n, found = node, true
				break
			}
		}
		if found {
			break
		}
		if !prs.PageInfo.HasNextPage {
			return PullRequest{}, false, nil
		}
		after = prs.PageInfo.EndCursor
	}
	if n.ReviewThreads.PageInfo.HasNextPage {
		return PullRequest{}, false, fmt.Errorf("PR %s has more than %d review threads; dispatch reads one page and would miss unresolved ones", n.URL, maxThreads)
	}
	checksPassed, checksRunning := true, false
	pr := PullRequest{Number: n.Number, URL: n.URL, Head: n.HeadRefOid, Repo: RepoOf(n.URL)}
	// GitHub's reviewDecision is empty on an unprotected branch and can't say
	// which commit a request is on, so the reviews are read directly. One
	// reviewer's standing request vetoes another's approval.
	for _, r := range n.LatestOpinionatedReviews.Nodes {
		if r.State == "APPROVED" && r.Commit.Oid == n.HeadRefOid {
			pr.Approved = true
		}
	}
	// A request on an older commit is the worker's to have answered by pushing,
	// so only one on the head counts.
	changesRequested := false
	for _, r := range n.LatestOpinionatedReviews.Nodes {
		if r.State == "CHANGES_REQUESTED" {
			pr.Approved = false
			changesRequested = changesRequested || r.Commit.Oid == n.HeadRefOid
		}
	}
	// PENDING and EXPECTED are not failures, because the worker has nothing to
	// fix yet, but they are not passed either, so the PR is not ready.
	for _, c := range n.Commits.Nodes {
		if r := c.Commit.StatusCheckRollup; r != nil && (r.State == "FAILURE" || r.State == "ERROR") {
			pr.Problems = append(pr.Problems, "a failed check")
		}
		if r := c.Commit.StatusCheckRollup; r != nil && r.State != "SUCCESS" {
			checksPassed = false
			checksRunning = checksRunning || r.State == "PENDING" || r.State == "EXPECTED"
		}
	}
	// RFC 3339 UTC times sort as strings, and only the newest item matters, so
	// the newest 30 of each are enough.
	note := func(at, summary string) {
		if at > pr.ActivityAt {
			pr.ActivityAt, pr.ActivitySummary = at, summary
		}
	}
	for _, r := range n.Reviews.Nodes {
		// A draft review is the token owner's own, and createdAt is when it was
		// started, not when it was submitted.
		if r.State == "DISMISSED" || r.State == "PENDING" || (r.State == "APPROVED" && strings.TrimSpace(r.Body) == "") {
			continue
		}
		note(cmp.Or(r.SubmittedAt, r.CreatedAt), "a review by "+r.Author.name())
	}
	for _, c := range n.Comments.Nodes {
		note(c.CreatedAt, "a comment by "+c.Author.name())
	}
	// A review bot still checking the head may be about to answer the request.
	if changesRequested && !checksRunning {
		pr.Problems = append(pr.Problems, "changes requested")
	}
	for _, t := range n.ReviewThreads.Nodes {
		if t.IsResolved {
			continue
		}
		pr.UnresolvedThreads = true
		for _, c := range t.Comments.Nodes {
			pr.ThreadAt = max(pr.ThreadAt, c.CreatedAt)
			note(c.CreatedAt, "a review thread comment by "+c.Author.name())
		}
	}
	if n.Mergeable == "CONFLICTING" {
		pr.Problems = append(pr.Problems, "a merge conflict")
	}
	pr.Ready = !pr.NeedsWork() && !pr.UnresolvedThreads && checksPassed && n.Mergeable == "MERGEABLE"
	return pr, true, nil
}

// author is null for a deleted account.
type author struct {
	Login string `json:"login"`
}

func (a *author) name() string {
	if a == nil {
		return "a deleted account"
	}
	return a.Login
}

// RepoOf returns the owner/name in a PR URL such as
// https://github.com/owner/name/pull/7, or "" when the URL is not one.
func RepoOf(prURL string) string {
	u, err := url.Parse(prURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || parts[2] != "pull" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

// CLI is the Repository that runs the installed gh command.
type CLI struct {
	bin     string
	timeout time.Duration
}

// Option sets how a CLI runs gh.
type Option func(*CLI)

// WithTimeout cuts each gh call off after d, so a stalled network can't hold
// the tick that is waiting on it.
func WithTimeout(d time.Duration) Option {
	return func(c *CLI) { c.timeout = d }
}

const defaultTimeout = 30 * time.Second

// NewCLI returns a Repository that runs bin.
func NewCLI(bin string, opts ...Option) *CLI {
	c := &CLI{bin: bin, timeout: defaultTimeout}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Read makes one `gh api graphql` call per page of same-named PRs.
func (c *CLI) Read(ctx context.Context, repoDir, branch string) (PullRequest, bool, error) {
	return Find(ctx, branch, func(ctx context.Context, branch, after string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(ctx, c.timeout)
		defer cancel()
		args := []string{"api", "graphql",
			"-F", "owner={owner}", "-F", "repo={repo}", "-f", "branch=" + branch,
			"-F", fmt.Sprintf("threads=%d", maxThreads), "-f", "query=" + query}
		if after != "" {
			args = append(args, "-f", "after="+after)
		}
		cmd := exec.CommandContext(ctx, c.bin, args...)
		cmd.Dir = repoDir
		// A child that inherits gh's pipes must not keep Output waiting after the kill.
		cmd.WaitDelay = time.Second
		b, err := cmd.Output()
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				return nil, fmt.Errorf("while running gh in %s: %w: %s", repoDir, err, strings.TrimSpace(string(exit.Stderr)))
			}
			return nil, fmt.Errorf("while running gh in %s: %w", repoDir, err)
		}
		return b, nil
	})
}

// Merge names the PR by URL, so gh needs no remote to resolve, and passes the
// head so gh refuses a PR that was pushed to since.
func (c *CLI) Merge(ctx context.Context, prURL, head string) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.bin, "pr", "merge", prURL, "--merge", "--match-head-commit", head)
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("while running gh pr merge %s: %w: %s", prURL, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RepoName returns the owner/name that gh resolves for the repo cloned at
// repoDir, which is the repo Read looks for PRs in.
func (c *CLI) RepoName(ctx context.Context, repoDir string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.bin, "repo", "view", "--json", "nameWithOwner", "-q", ".nameWithOwner")
	cmd.Dir = repoDir
	cmd.WaitDelay = time.Second
	b, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", fmt.Errorf("while running gh repo view in %s: %w: %s", repoDir, err, strings.TrimSpace(string(exit.Stderr)))
		}
		return "", fmt.Errorf("while running gh repo view in %s: %w", repoDir, err)
	}
	return strings.TrimSpace(string(b)), nil
}

var _ Repository = (*CLI)(nil)
