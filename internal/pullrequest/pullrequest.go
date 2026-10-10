// Package pullrequest reads the state of a ticket's open PR from GitHub, in
// the terms the dispatcher needs to decide whether the PR needs work.
package pullrequest

import (
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
	// Approved means a reviewer's latest review approves the head commit. An
	// approval of an earlier commit does not count, so a push clears it.
	Approved bool
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

const query = `query PullRequest($owner: String!, $repo: String!, $branch: String!, $threads: Int!) {
  repository(owner: $owner, name: $repo) {
    pullRequests(headRefName: $branch, states: OPEN, first: 1, orderBy: {field: UPDATED_AT, direction: DESC}) {
      nodes {
        number url headRefOid mergeable reviewDecision
        commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
        reviewThreads(first: $threads) { nodes { isResolved } pageInfo { hasNextPage } }
        latestOpinionatedReviews(first: 100) { nodes { state commit { oid } } }
      }
    }
  }
}`

// Read finds the open PR whose head branch is branch in the repo cloned at
// repoDir, with one `gh api graphql` call. It returns false when the branch
// has no open PR, and an error when the PR has more than maxThreads review threads. Each call is cut off after timeout.
func Read(ctx context.Context, bin, repoDir, branch string, timeout time.Duration) (PR, bool, error) {
	// The tick holds its lock while it waits, and gh sets no timeout of its own.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "api", "graphql",
		"-F", "owner={owner}", "-F", "repo={repo}", "-f", "branch="+branch,
		"-F", fmt.Sprintf("threads=%d", maxThreads), "-f", "query="+query)
	cmd.Dir = repoDir
	// A child that inherits gh's pipes must not keep Output waiting after the kill.
	cmd.WaitDelay = time.Second
	b, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return PR{}, false, fmt.Errorf("while running gh in %s: %w: %s", repoDir, err, strings.TrimSpace(string(exit.Stderr)))
		}
		return PR{}, false, fmt.Errorf("while running gh in %s: %w", repoDir, err)
	}
	var resp struct {
		Data struct {
			Repository struct {
				PullRequests struct {
					Nodes []struct {
						Number         int    `json:"number"`
						URL            string `json:"url"`
						HeadRefOid     string `json:"headRefOid"`
						Mergeable      string `json:"mergeable"`
						ReviewDecision string `json:"reviewDecision"`
						Commits        struct {
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
						ReviewThreads struct {
							Nodes []struct {
								IsResolved bool `json:"isResolved"`
							} `json:"nodes"`
							PageInfo struct {
								HasNextPage bool `json:"hasNextPage"`
							} `json:"pageInfo"`
						} `json:"reviewThreads"`
					} `json:"nodes"`
				} `json:"pullRequests"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		return PR{}, false, fmt.Errorf("while reading gh output for %s: %w", branch, err)
	}
	nodes := resp.Data.Repository.PullRequests.Nodes
	if len(nodes) == 0 {
		return PR{}, false, nil
	}
	n := nodes[0]
	if n.ReviewThreads.PageInfo.HasNextPage {
		return PR{}, false, fmt.Errorf("PR %s has more than %d review threads; dispatch reads one page and would miss unresolved ones", n.URL, maxThreads)
	}
	checksPassed := true
	pr := PR{Number: n.Number, URL: n.URL, Head: n.HeadRefOid, Repo: RepoOf(n.URL)}
	for _, r := range n.LatestOpinionatedReviews.Nodes {
		if r.State == "APPROVED" && r.Commit.Oid == n.HeadRefOid {
			pr.Approved = true
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
