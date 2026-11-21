package pullrequest

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

// MergedChange is a merged PR with the change it made: the base commit its diff starts from, its
// head branch, and the title and body its author wrote.
type MergedChange struct {
	MergedPR
	// Base is the commit of the base branch the PR was compared against.
	Base   string
	Branch string
	Title  string
	Body   string
}

// CommitPullsLimit is the most PRs GitHub lists for a commit in one page. A commit held by that
// many is refused, since the PR that merged it could be on a later page.
const CommitPullsLimit = 100

// MergedListLimit is the most PRs one OwnMerged read lists. A window with more is refused rather
// than cut short, since a short list would hide fix chains.
const MergedListLimit = 1000

// MergedChangeSource answers the reads of merged changes with GitHub's raw JSON.
type MergedChangeSource interface {
	// MergedList is gh pr list's JSON for the PRs the gh user wrote that merged into repo, an
	// owner/name, on the days from through to in UTC. It may hold PRs that merged outside the
	// window, which ListOwnMerged drops.
	MergedList(ctx context.Context, repo string, from, to time.Time) ([]byte, error)
	// CommitPulls is GitHub's REST answer listing the pull requests that hold commit sha.
	CommitPulls(ctx context.Context, repo, sha string) ([]byte, error)
}

type listedNode struct {
	Number      int       `json:"number"`
	URL         string    `json:"url"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	HeadRefName string    `json:"headRefName"`
	HeadRefOid  string    `json:"headRefOid"`
	BaseRefOid  string    `json:"baseRefOid"`
	MergedAt    time.Time `json:"mergedAt"`
}

// ListOwnMerged reads the PRs the gh user wrote that merged into repo between from and to, by number.
func ListOwnMerged(ctx context.Context, src MergedChangeSource, repo string, from, to time.Time) ([]MergedChange, error) {
	body, err := src.MergedList(ctx, repo, from, to)
	if err != nil {
		return nil, err
	}
	var nodes []listedNode
	if err := json.Unmarshal(body, &nodes); err != nil {
		return nil, fmt.Errorf("while reading gh output for the merged PRs of %s: %w", repo, err)
	}
	if len(nodes) >= MergedListLimit {
		return nil, fmt.Errorf("%s has %d or more merged PRs in the window, more than one read lists; shorten the window", repo, MergedListLimit)
	}
	var out []MergedChange
	for _, n := range nodes {
		if n.MergedAt.IsZero() || n.MergedAt.Before(from) || n.MergedAt.After(to) {
			continue
		}
		out = append(out, MergedChange{MergedPR: MergedPR{Number: n.Number, URL: n.URL, Head: n.HeadRefOid, MergedAt: n.MergedAt},
			Base: n.BaseRefOid, Branch: n.HeadRefName, Title: n.Title, Body: n.Body})
	}
	slices.SortFunc(out, func(a, b MergedChange) int { return cmp.Compare(a.Number, b.Number) })
	return out, nil
}

type commitPull struct {
	Number         int     `json:"number"`
	HTMLURL        string  `json:"html_url"`
	Title          string  `json:"title"`
	Body           *string `json:"body"`
	MergedAt       *string `json:"merged_at"`
	MergeCommitSHA string  `json:"merge_commit_sha"`
	Head           struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		SHA string `json:"sha"`
	} `json:"base"`
}

// FindMergedHolding reads which merged PR brought commit sha into repo: the one whose merge commit it
// is, or else the first of the merged PRs that hold it to merge. It returns false when no merged
// PR holds it.
func FindMergedHolding(ctx context.Context, src MergedChangeSource, repo, sha string) (MergedChange, bool, error) {
	body, err := src.CommitPulls(ctx, repo, sha)
	if err != nil {
		return MergedChange{}, false, err
	}
	var pulls []commitPull
	if err := json.Unmarshal(body, &pulls); err != nil {
		return MergedChange{}, false, fmt.Errorf("while reading gh output for the PRs of %s commit %s: %w", repo, sha, err)
	}
	if len(pulls) >= CommitPullsLimit {
		return MergedChange{}, false, fmt.Errorf("%d or more PRs hold %s commit %s, so the one that merged it may be past the first page", CommitPullsLimit, repo, sha)
	}
	var merged []MergedChange
	exact := -1
	for _, p := range pulls {
		if p.MergedAt == nil || *p.MergedAt == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, *p.MergedAt)
		if err != nil {
			return MergedChange{}, false, fmt.Errorf("while reading gh output for the PRs of %s commit %s: merged_at %q: %w", repo, sha, *p.MergedAt, err)
		}
		pr := MergedChange{MergedPR: MergedPR{Number: p.Number, URL: p.HTMLURL, Head: p.Head.SHA, MergedAt: at},
			Base: p.Base.SHA, Branch: p.Head.Ref, Title: p.Title}
		if p.Body != nil {
			pr.Body = *p.Body
		}
		if p.MergeCommitSHA == sha && exact < 0 {
			exact = len(merged)
		}
		merged = append(merged, pr)
	}
	if len(merged) == 0 {
		return MergedChange{}, false, nil
	}
	if exact >= 0 {
		return merged[exact], true, nil
	}
	return slices.MinFunc(merged, func(a, b MergedChange) int { return a.MergedAt.Compare(b.MergedAt) }), true, nil
}

// OwnMerged runs gh pr list for the PRs the gh user wrote that merged into repo between from and to.
func (c *CLI) OwnMerged(ctx context.Context, repo string, from, to time.Time) ([]MergedChange, error) {
	return ListOwnMerged(ctx, cliMergedChanges{c}, repo, from, to)
}

// MergedHolding runs gh api on the REST endpoint that lists the pull requests holding a commit.
func (c *CLI) MergedHolding(ctx context.Context, repo, sha string) (MergedChange, bool, error) {
	return FindMergedHolding(ctx, cliMergedChanges{c}, repo, sha)
}

type cliMergedChanges struct{ cli *CLI }

// MergedList searches a day wider on each side than the window, since whether GitHub reads the
// days of a merged: search in UTC is unconfirmed, and ListOwnMerged keeps only the window.
func (s cliMergedChanges) MergedList(ctx context.Context, repo string, from, to time.Time) ([]byte, error) {
	return s.cli.gh(ctx, "", []string{"pr", "list", "--repo", repo, "--state", "merged", "--author", "@me",
		"--search", fmt.Sprintf("merged:%s..%s", from.UTC().AddDate(0, 0, -1).Format(time.DateOnly), to.UTC().AddDate(0, 0, 1).Format(time.DateOnly)),
		"--limit", fmt.Sprint(MergedListLimit),
		"--json", "number,url,title,body,headRefName,headRefOid,baseRefOid,mergedAt"})
}

func (s cliMergedChanges) CommitPulls(ctx context.Context, repo, sha string) ([]byte, error) {
	return s.cli.gh(ctx, "", []string{"api", fmt.Sprintf("repos/%s/commits/%s/pulls?per_page=%d", repo, sha, CommitPullsLimit)})
}
