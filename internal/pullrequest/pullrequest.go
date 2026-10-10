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

// PR is an open pull request and what is wrong with it.
type PR struct {
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
	// ActivityAt is the RFC 3339 time of the newest review or conversation
	// comment, "" when there is none. It skips approvals without a body,
	// dismissals and drafts, which ask the worker for nothing. A review with
	// only a body opens no thread and sets no reviewDecision, so this is the
	// only way the dispatcher sees it.
	ActivityAt string
	// ActivitySummary describes the item at ActivityAt, such as "a review by alice".
	ActivitySummary string
	// Problems are the reasons the PR needs work, phrased to follow "has".
	Problems []string
	// Ready means the PR needs no work and nothing is still running: every check
	// has passed (a PR with no checks counts as passed) and GitHub says it
	// merges cleanly. A running check, such as a bot review still in progress,
	// or a merge state GitHub has not computed yet, is not ready.
	Ready bool
}

// NeedsWork reports whether the PR has a failed check, changes requested, an
// unresolved review thread or a merge conflict.
func (p PR) NeedsWork() bool {
	return len(p.Problems) > 0
}

// Problem joins the problems into one phrase, such as "a failed check and a
// merge conflict".
func (p PR) Problem() string {
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

// prPages bounds how many pages of same-named PRs Read walks looking for one
// from this repo. A branch with 20 fork PRs ahead of the worker's is the case
// this exists for, and more than prPages of them is an error rather than a miss.
const prPages = 5

const query = `query PullRequest($owner: String!, $repo: String!, $branch: String!, $threads: Int!, $after: String) {
  repository(owner: $owner, name: $repo) {
    pullRequests(headRefName: $branch, states: OPEN, first: 20, after: $after, orderBy: {field: UPDATED_AT, direction: DESC}) {
      pageInfo { hasNextPage endCursor }
      nodes {
        number url isCrossRepository headRefOid mergeable reviewDecision
        commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
        reviewThreads(first: $threads) { nodes { isResolved } pageInfo { hasNextPage } }
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
	ReviewDecision    string `json:"reviewDecision"`
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

// Read finds the open PR whose head branch is branch in the repo cloned at
// repoDir, with one `gh api graphql` call per page of same-named PRs. It
// returns false when the branch has no open PR from this repo, and an error
// when the PR has more than maxThreads review threads. Each call is cut off after timeout.
func Read(ctx context.Context, bin, repoDir, branch string, timeout time.Duration) (PR, bool, error) {
	// headRefName matches fork PRs too, and anyone can open one on a branch
	// name Linear makes predictable. Only a PR from this repo is the worker's.
	var n prNode
	after := ""
	for page := 0; ; page++ {
		if page == prPages {
			return PR{}, false, fmt.Errorf("branch %s has more than %d PRs and none from this repo in the first %d pages", branch, prPages*20, prPages)
		}
		resp, err := readPage(ctx, bin, repoDir, branch, after, timeout)
		if err != nil {
			return PR{}, false, err
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
			return PR{}, false, nil
		}
		after = prs.PageInfo.EndCursor
	}
	if n.ReviewThreads.PageInfo.HasNextPage {
		return PR{}, false, fmt.Errorf("PR %s has more than %d review threads; dispatch reads one page and would miss unresolved ones", n.URL, maxThreads)
	}
	checksPassed := true
	pr := PR{Number: n.Number, URL: n.URL, Head: n.HeadRefOid, Repo: RepoOf(n.URL)}
	// reviewDecision is empty on an unprotected branch, so one reviewer's
	// standing request for changes has to veto another's approval here.
	for _, r := range n.LatestOpinionatedReviews.Nodes {
		if r.State == "APPROVED" && r.Commit.Oid == n.HeadRefOid {
			pr.Approved = true
		}
	}
	for _, r := range n.LatestOpinionatedReviews.Nodes {
		if r.State == "CHANGES_REQUESTED" {
			pr.Approved = false
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
	if n.ReviewDecision == "CHANGES_REQUESTED" {
		pr.Problems = append(pr.Problems, "changes requested")
	}
	for _, t := range n.ReviewThreads.Nodes {
		if !t.IsResolved {
			pr.Problems = append(pr.Problems, "an unresolved review thread")
			break
		}
	}
	if n.Mergeable == "CONFLICTING" {
		pr.Problems = append(pr.Problems, "a merge conflict")
	}
	pr.Ready = !pr.NeedsWork() && checksPassed && n.Mergeable == "MERGEABLE"
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

// Merge merges the PR with a merge commit. It names the PR by URL, so gh needs
// no remote to resolve, and it passes the head so a push after the approval
// was read makes gh refuse instead of merging unreviewed code.
func Merge(ctx context.Context, bin, prURL, head string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "pr", "merge", prURL, "--merge", "--match-head-commit", head)
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("while running gh pr merge %s: %w: %s", prURL, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RepoName returns the owner/name that gh resolves for the repo cloned at
// repoDir, which is the repo Read looks for PRs in.
func RepoName(ctx context.Context, bin, repoDir string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "repo", "view", "--json", "nameWithOwner", "-q", ".nameWithOwner")
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

func readPage(ctx context.Context, bin, repoDir, branch, after string, timeout time.Duration) (prPage, error) {
	// The tick holds its lock while it waits, and gh sets no timeout of its own.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := []string{"api", "graphql",
		"-F", "owner={owner}", "-F", "repo={repo}", "-f", "branch=" + branch,
		"-F", fmt.Sprintf("threads=%d", maxThreads), "-f", "query=" + query}
	if after != "" {
		args = append(args, "-f", "after="+after)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = repoDir
	// A child that inherits gh's pipes must not keep Output waiting after the kill.
	cmd.WaitDelay = time.Second
	b, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return prPage{}, fmt.Errorf("while running gh in %s: %w: %s", repoDir, err, strings.TrimSpace(string(exit.Stderr)))
		}
		return prPage{}, fmt.Errorf("while running gh in %s: %w", repoDir, err)
	}
	var resp prPage
	if err := json.Unmarshal(b, &resp); err != nil {
		return prPage{}, fmt.Errorf("while reading gh output for %s: %w", branch, err)
	}
	return resp, nil
}
