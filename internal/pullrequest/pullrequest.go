// Package pullrequest reads the state of a ticket's open PR from GitHub, in
// the terms the dispatcher needs to decide whether the PR needs work.
package pullrequest

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
)

// Repository is the GitHub repo a clone belongs to, where a ticket's branch has
// at most one open pull request.
type Repository interface {
	// Read finds the open PR whose head branch is branch in the repo cloned at
	// repoDir, as judged by rules. It returns false when the branch has no open
	// PR from this repo.
	Read(ctx context.Context, repoDir, branch string, rules Rules) (PullRequest, bool, error)
	// Merge merges the PR at prURL with a merge commit. It refuses, and merges
	// nothing, when the PR's head is no longer head, so a push after the
	// approval was read is never merged unreviewed.
	Merge(ctx context.Context, prURL, head string) error
}

// Rules say which of a PR's items count. The zero value judges everything as
// GitHub reports it.
type Rules struct {
	// IgnoreAuthors are GitHub logins, matched without regard to case, whose
	// reviews, comments, approvals and change requests count for nothing.
	IgnoreAuthors []string
	// RequiredChecksOnly makes only the checks GitHub requires for merging the
	// PR count. A branch that requires none has no check that counts.
	RequiredChecksOnly bool
	// ApprovalChecks are names of checks that pass only after a human approves
	// the PR, so they can't gate a PR that is waiting for that review.
	ApprovalChecks []string
}

func (r Rules) isApprovalCheck(c checkContext) bool {
	name := cmp.Or(c.Name, c.Context)
	return slices.ContainsFunc(r.ApprovalChecks, func(n string) bool { return strings.EqualFold(n, name) })
}

func (r Rules) ignores(a *author) bool {
	return a != nil && slices.ContainsFunc(r.IgnoreAuthors, func(login string) bool { return strings.EqualFold(login, a.Login) })
}

// PullRequest is an open pull request and what is wrong with it.
type PullRequest struct {
	Number int
	URL    string
	// Head is the SHA of the PR's head commit. A push moves it.
	Head string
	// Draft means the PR has not been opened for review. GitHub refuses to
	// merge one.
	Draft bool
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
	// and nudging every cycle tells the worker about work it already did. The
	// thread's newest comment reaches the worker through ActivityAt instead.
	UnresolvedThreads bool
	// Problems are the reasons the PR needs work, phrased to follow "has".
	Problems []string
	// Ready means the PR needs no work, has no open review thread and nothing is still running: every check
	// (every required one, under Rules.RequiredChecksOnly, and none named in Rules.ApprovalChecks) has passed (a PR with no checks counts as passed) and GitHub says it
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

// Kinds of PR item Source.Older pages through.
const (
	kindReviews  = "reviews"
	kindComments = "comments"
)

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
        number url isCrossRepository isDraft headRefOid mergeable
        commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
        reviewThreads(first: $threads) { nodes { isResolved comments(last: 20) { nodes { createdAt author { login } } } } pageInfo { hasNextPage } }
        latestOpinionatedReviews(first: 100) { nodes { state commit { oid } author { login } } }
        reviews(last: 100) { nodes { state body createdAt submittedAt author { login } } pageInfo { hasPreviousPage startCursor } }
        comments(last: 100) { nodes { createdAt author { login } } pageInfo { hasPreviousPage startCursor } }
      }
    }
  }
}`

const olderReviewsQuery = `query OlderReviews($owner: String!, $repo: String!, $number: Int!, $before: String!) {
  repository(owner: $owner, name: $repo) {
    pullRequest(number: $number) {
      reviews(last: 100, before: $before) { nodes { state body createdAt submittedAt author { login } } pageInfo { hasPreviousPage startCursor } }
    }
  }
}`

const olderCommentsQuery = `query OlderComments($owner: String!, $repo: String!, $number: Int!, $before: String!) {
  repository(owner: $owner, name: $repo) {
    pullRequest(number: $number) {
      comments(last: 100, before: $before) { nodes { createdAt author { login } } pageInfo { hasPreviousPage startCursor } }
    }
  }
}`

const checksQuery = `query Checks($owner: String!, $repo: String!, $number: Int!) {
  repository(owner: $owner, name: $repo) {
    pullRequest(number: $number) {
      commits(last: 1) { nodes { commit { oid statusCheckRollup { contexts(first: 100) {
        pageInfo { hasNextPage }
        nodes {
          __typename
          ... on CheckRun { name status conclusion isRequired(pullRequestNumber: $number) }
          ... on StatusContext { context state isRequired(pullRequestNumber: $number) }
        }
      } } } } }
    }
  }
}`

// checkContext is a CheckRun or a StatusContext. A CheckRun has a status and,
// once COMPLETED, a conclusion. A StatusContext has only a state.
type checkContext struct {
	Typename string `json:"__typename"`
	// Name fills for a CheckRun and Context for a StatusContext.
	Name       string `json:"name"`
	Context    string `json:"context"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
	IsRequired bool   `json:"isRequired"`
}

type checksPage struct {
	Data struct {
		Repository struct {
			PullRequest struct {
				Commits struct {
					Nodes []struct {
						Commit struct {
							Oid               string `json:"oid"`
							StatusCheckRollup *struct {
								Contexts struct {
									Nodes    []checkContext `json:"nodes"`
									PageInfo struct {
										HasNextPage bool `json:"hasNextPage"`
									} `json:"pageInfo"`
								} `json:"contexts"`
							} `json:"statusCheckRollup"`
						} `json:"commit"`
					} `json:"nodes"`
				} `json:"commits"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
}

// rollupState is the state GitHub's rollup would have if only the checks that
// count did: SUCCESS when none does, FAILURE when one failed, PENDING while one
// has not finished. A check counts unless it is an approval check, or optional
// while rules want only the required ones.
func rollupState(contexts []checkContext, rules Rules) string {
	running := false
	for _, c := range contexts {
		if rules.isApprovalCheck(c) || (rules.RequiredChecksOnly && !c.IsRequired) {
			continue
		}
		switch {
		case c.Typename == "StatusContext" && (c.State == "FAILURE" || c.State == "ERROR"):
			return "FAILURE"
		case c.Typename == "StatusContext" && c.State != "SUCCESS":
			running = true
		case c.Typename != "StatusContext" && c.Status != "COMPLETED":
			running = true
		case c.Typename != "StatusContext" && !slices.Contains([]string{"SUCCESS", "NEUTRAL", "SKIPPED"}, c.Conclusion):
			return "FAILURE"
		}
	}
	if running {
		return "PENDING"
	}
	return "SUCCESS"
}

// countedRollupState reads the checks behind PR number and returns the rollup
// state of the ones that count under rules. A second page of checks is an
// error, because a failure beyond the first page would go unnoticed. Checks of any
// commit but head, which a push between the two reads causes, are PENDING, so
// the PR is not ready until the next read agrees with itself.
func countedRollupState(ctx context.Context, src Source, number int, head string, rules Rules) (string, error) {
	b, err := src.Checks(ctx, number)
	if err != nil {
		return "", err
	}
	var resp checksPage
	if err := json.Unmarshal(b, &resp); err != nil {
		return "", fmt.Errorf("while reading gh output for the checks of PR %d: %w", number, err)
	}
	for _, c := range resp.Data.Repository.PullRequest.Commits.Nodes {
		if c.Commit.Oid != head {
			return "PENDING", nil
		}
		if r := c.Commit.StatusCheckRollup; r != nil {
			if r.Contexts.PageInfo.HasNextPage {
				return "", fmt.Errorf("PR %d has more than 100 checks; dispatch reads one page and would miss required ones", number)
			}
			return rollupState(r.Contexts.Nodes, rules), nil
		}
	}
	// The rollup the branch query saw has vanished, so this read can't vouch for it.
	return "PENDING", nil
}

type reviewNode struct {
	State       string  `json:"state"`
	Body        string  `json:"body"`
	CreatedAt   string  `json:"createdAt"`
	SubmittedAt string  `json:"submittedAt"`
	Author      *author `json:"author"`
}

type opinionNode struct {
	State  string `json:"state"`
	Commit struct {
		Oid string `json:"oid"`
	} `json:"commit"`
	Author *author `json:"author"`
}

// asksForNothing is true of a review that gives the worker nothing to read. A
// draft review is the token owner's own, and createdAt is when it was started,
// not when it was submitted.
func (r reviewNode) asksForNothing() bool {
	return r.State == "DISMISSED" || r.State == "PENDING" || (r.State == "APPROVED" && strings.TrimSpace(r.Body) == "")
}

type commentNode struct {
	CreatedAt string  `json:"createdAt"`
	Author    *author `json:"author"`
}

// page is a newest-last window of items. PageInfo says whether older ones exist.
type page[T any] struct {
	Nodes    []T `json:"nodes"`
	PageInfo struct {
		HasPreviousPage bool   `json:"hasPreviousPage"`
		StartCursor     string `json:"startCursor"`
	} `json:"pageInfo"`
}

type prNode struct {
	Number            int    `json:"number"`
	IsCrossRepository bool   `json:"isCrossRepository"`
	IsDraft           bool   `json:"isDraft"`
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
		Nodes []opinionNode `json:"nodes"`
	} `json:"latestOpinionatedReviews"`
	Reviews       page[reviewNode]  `json:"reviews"`
	Comments      page[commentNode] `json:"comments"`
	ReviewThreads struct {
		Nodes []struct {
			IsResolved bool `json:"isResolved"`
			Comments   struct {
				Nodes []commentNode `json:"nodes"`
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

type olderPage struct {
	Data struct {
		Repository struct {
			PullRequest struct {
				Reviews  page[reviewNode]  `json:"reviews"`
				Comments page[commentNode] `json:"comments"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
}

// Source is where Find gets GitHub's answers, as the JSON gh prints.
type Source interface {
	// PRPage returns one page of the answer to the PR query for a branch, the
	// page after cursor when cursor is not empty.
	PRPage(ctx context.Context, branch, cursor string) ([]byte, error)
	// Older returns the page of PR number's reviews or comments (kind is
	// kindReviews or kindComments) that ends before the cursor.
	Older(ctx context.Context, number int, kind, before string) ([]byte, error)
	// Checks returns the answer to the query for the checks behind PR number's
	// head, each with whether the PR requires it. GitHub answers that only for
	// a PR number, so the branch query can't carry it.
	Checks(ctx context.Context, number int) ([]byte, error)
}

// widen returns the items keep accepts, reading older pages until the window
// holds one or reaches the oldest, so the number of ignored items never decides
// anything.
func widen[T any](ctx context.Context, src Source, number int, kind string, newest page[T], keep func(T) bool, pick func(olderPage) page[T]) ([]T, error) {
	items, prev := newest.Nodes, newest.PageInfo
	for prev.HasPreviousPage && !slices.ContainsFunc(items, keep) {
		if prev.StartCursor == "" {
			return nil, fmt.Errorf("GitHub reports older %s on PR %d but gave no cursor", kind, number)
		}
		b, err := src.Older(ctx, number, kind, prev.StartCursor)
		if err != nil {
			return nil, err
		}
		var resp olderPage
		if err := json.Unmarshal(b, &resp); err != nil {
			return nil, fmt.Errorf("while reading gh output for older %s of PR %d: %w", kind, number, err)
		}
		got := pick(resp)
		if got.PageInfo.HasPreviousPage && got.PageInfo.StartCursor == prev.StartCursor {
			return nil, fmt.Errorf("older %s of PR %d did not advance past cursor %s", kind, number, prev.StartCursor)
		}
		items, prev = append(slices.Clone(got.Nodes), items...), got.PageInfo
	}
	return slices.DeleteFunc(slices.Clone(items), func(item T) bool { return !keep(item) }), nil
}

// Find walks the pages src returns for branch and picks the open PR from this
// repo, since a fork PR can share the branch name. It returns false when the
// branch has no such PR, and an error when the PR has more than maxThreads
// review threads. Rules filter every item once, before anything is derived
// from them, so a field added later is filtered too.
func Find(ctx context.Context, branch string, src Source, rules Rules) (PullRequest, bool, error) {
	// headRefName matches fork PRs too, and anyone can open one on a branch
	// name Linear makes predictable. Only a PR from this repo is the worker's.
	var n prNode
	after := ""
	for page := 0; ; page++ {
		if page == prPages {
			return PullRequest{}, false, fmt.Errorf("branch %s has more than %d PRs and none from this repo in the first %d pages", branch, prPages*20, prPages)
		}
		b, err := src.PRPage(ctx, branch, after)
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
	reviews, err := widen(ctx, src, n.Number, kindReviews, n.Reviews, func(r reviewNode) bool { return !rules.ignores(r.Author) && !r.asksForNothing() }, func(o olderPage) page[reviewNode] { return o.Data.Repository.PullRequest.Reviews })
	if err != nil {
		return PullRequest{}, false, err
	}
	comments, err := widen(ctx, src, n.Number, kindComments, n.Comments, func(c commentNode) bool { return !rules.ignores(c.Author) }, func(o olderPage) page[commentNode] { return o.Data.Repository.PullRequest.Comments })
	if err != nil {
		return PullRequest{}, false, err
	}
	opinions := slices.DeleteFunc(slices.Clone(n.LatestOpinionatedReviews.Nodes), func(r opinionNode) bool { return rules.ignores(r.Author) })
	checksPassed, checksRunning := true, false
	pr := PullRequest{Number: n.Number, URL: n.URL, Head: n.HeadRefOid, Draft: n.IsDraft}
	// GitHub's reviewDecision is empty on an unprotected branch and can't say
	// which commit a request is on, so the reviews are read directly. One
	// reviewer's standing request vetoes another's approval.
	for _, r := range opinions {
		if r.State == "APPROVED" && r.Commit.Oid == n.HeadRefOid {
			pr.Approved = true
		}
	}
	// A request on an older commit is the worker's to have answered by pushing,
	// so only one on the head counts.
	changesRequested := false
	for _, r := range opinions {
		if r.State == "CHANGES_REQUESTED" {
			pr.Approved = false
			changesRequested = changesRequested || r.Commit.Oid == n.HeadRefOid
		}
	}
	// PENDING and EXPECTED are not failures, because the worker has nothing to
	// fix yet, but they are not passed either, so the PR is not ready.
	for _, c := range n.Commits.Nodes {
		r := c.Commit.StatusCheckRollup
		if r == nil {
			continue
		}
		state := r.State
		// An EXPECTED rollup is a required check that has not started, which
		// stays not ready unless that check is an approval check, so only a
		// repo with approval checks looks at which checks are behind it.
		approvals := len(rules.ApprovalChecks) > 0
		unsettled := state == "FAILURE" || state == "ERROR" || state == "PENDING" || (state == "EXPECTED" && approvals)
		if (rules.RequiredChecksOnly || approvals) && unsettled {
			if state, err = countedRollupState(ctx, src, n.Number, n.HeadRefOid, rules); err != nil {
				return PullRequest{}, false, err
			}
		}
		if state == "FAILURE" || state == "ERROR" {
			pr.Problems = append(pr.Problems, "a failed check")
		}
		if state != "SUCCESS" {
			checksPassed = false
			checksRunning = checksRunning || state == "PENDING" || state == "EXPECTED"
		}
	}
	// RFC 3339 UTC times sort as strings, and only the newest item matters, so
	// widen reads far enough back to find the newest unlisted one.
	note := func(at, summary string) {
		if at > pr.ActivityAt {
			pr.ActivityAt, pr.ActivitySummary = at, summary
		}
	}
	for _, r := range reviews {
		note(cmp.Or(r.SubmittedAt, r.CreatedAt), "a review by "+r.Author.name())
	}
	for _, c := range comments {
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
		// A thread whose answer carries no comments still counts.
		kept := slices.DeleteFunc(slices.Clone(t.Comments.Nodes), func(c commentNode) bool { return rules.ignores(c.Author) })
		if len(kept) == 0 && len(t.Comments.Nodes) > 0 {
			continue
		}
		pr.UnresolvedThreads = true
		for _, c := range kept {
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

// CLI is the Repository that runs the installed gh command.
type CLI struct {
	bin     string
	timeout time.Duration
}

// Option sets how a CLI runs gh.
type Option func(*CLI)

// WithTimeout cuts each gh call off after d, so a stalled network can't hold
// the read that is waiting on it.
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

// Read makes one `gh api graphql` call per page of same-named PRs, and more
// only to reach older reviews or comments when rules ignore every newer one.
func (c *CLI) Read(ctx context.Context, repoDir, branch string, rules Rules) (PullRequest, bool, error) {
	return Find(ctx, branch, cliSource{c, repoDir}, rules)
}

type cliSource struct {
	cli *CLI
	dir string
}

func (s cliSource) PRPage(ctx context.Context, branch, after string) ([]byte, error) {
	args := []string{"api", "graphql",
		"-F", "owner={owner}", "-F", "repo={repo}", "-f", "branch=" + branch,
		"-F", fmt.Sprintf("threads=%d", maxThreads), "-f", "query=" + query}
	if after != "" {
		args = append(args, "-f", "after="+after)
	}
	return s.cli.graphql(ctx, s.dir, args)
}

func (s cliSource) Older(ctx context.Context, number int, kind, before string) ([]byte, error) {
	q := olderCommentsQuery
	if kind == kindReviews {
		q = olderReviewsQuery
	}
	return s.cli.graphql(ctx, s.dir, []string{"api", "graphql",
		"-F", "owner={owner}", "-F", "repo={repo}", "-F", fmt.Sprintf("number=%d", number),
		"-f", "before=" + before, "-f", "query=" + q})
}

func (s cliSource) Checks(ctx context.Context, number int) ([]byte, error) {
	return s.cli.graphql(ctx, s.dir, []string{"api", "graphql",
		"-F", "owner={owner}", "-F", "repo={repo}", "-F", fmt.Sprintf("number=%d", number),
		"-f", "query=" + checksQuery})
}

func (c *CLI) graphql(ctx context.Context, dir string, args []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Dir = dir
	// A child that inherits gh's pipes must not keep Output waiting after the kill.
	cmd.WaitDelay = time.Second
	b, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, fmt.Errorf("while running gh in %s: %w: %s", dir, err, strings.TrimSpace(string(exit.Stderr)))
		}
		return nil, fmt.Errorf("while running gh in %s: %w", dir, err)
	}
	return b, nil
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

var _ Repository = (*CLI)(nil)
