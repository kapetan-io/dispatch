package pullrequest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// MergedBatch is the most branches one ReadMerged call asks about, the aliases one GraphQL query
// carries.
const MergedBatch = 50

// mergedPerBranch is how many of a branch's most recently updated merged PRs one read looks at. A branch with more merged
// PRs than this, none from this repo into the default branch among the most recently updated, reads as unmerged,
// which leaves a ticket open and never closes one by mistake.
const mergedPerBranch = 20

// MergedPR is the newest PR from this repo that merged into the repo's default branch from a branch.
// It is not a PullRequest, which is an open PR and what is wrong with it.
type MergedPR struct {
	Number   int
	URL      string
	Head     string
	MergedAt time.Time
}

// MergedSource answers the merged query with GitHub's raw JSON.
type MergedSource interface {
	// MergedPage returns the answer for a query that aliases one pullRequests field per branch,
	// the field for branches[i] named b<i>, beside the repo's defaultBranchRef.
	MergedPage(ctx context.Context, branches []string) ([]byte, error)
}

type mergedNode struct {
	Number            int       `json:"number"`
	URL               string    `json:"url"`
	HeadRefOid        string    `json:"headRefOid"`
	BaseRefName       string    `json:"baseRefName"`
	IsCrossRepository bool      `json:"isCrossRepository"`
	MergedAt          time.Time `json:"mergedAt"`
}

// FindMerged reads, for each branch, its newest PR that merged into the default branch. A PR from a
// fork that shares the branch's name, and one merged into any other branch, are not the branch's.
// A branch with no such PR is left out of the answer.
func FindMerged(ctx context.Context, branches []string, src MergedSource) (map[string]MergedPR, error) {
	if len(branches) > MergedBatch {
		return nil, fmt.Errorf("a merged read takes at most %d branches, not %d", MergedBatch, len(branches))
	}
	body, err := src.MergedPage(ctx, branches)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data struct {
			Repository map[string]json.RawMessage `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("while reading gh output for merged PRs: %w", err)
	}
	var base struct {
		Name string `json:"name"`
	}
	if raw := resp.Data.Repository["defaultBranchRef"]; len(raw) == 0 || json.Unmarshal(raw, &base) != nil || base.Name == "" {
		return nil, fmt.Errorf("gh reported no default branch for the repo")
	}
	found := map[string]MergedPR{}
	for i, branch := range branches {
		var field struct {
			Nodes []mergedNode `json:"nodes"`
		}
		if raw := resp.Data.Repository[fmt.Sprintf("b%d", i)]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &field); err != nil {
				return nil, fmt.Errorf("while reading gh output for merged PRs of %s: %w", branch, err)
			}
		}
		var newest mergedNode
		for _, n := range field.Nodes {
			if n.IsCrossRepository || n.BaseRefName != base.Name || n.MergedAt.IsZero() || !n.MergedAt.After(newest.MergedAt) {
				continue
			}
			newest = n
		}
		if !newest.MergedAt.IsZero() {
			found[branch] = MergedPR{Number: newest.Number, URL: newest.URL, Head: newest.HeadRefOid, MergedAt: newest.MergedAt}
		}
	}
	return found, nil
}

// ReadMerged makes one `gh api graphql` call for up to MergedBatch branches.
func (c *CLI) ReadMerged(ctx context.Context, repoDir string, branches []string) (map[string]MergedPR, error) {
	return FindMerged(ctx, branches, cliMergedSource{c, repoDir})
}

type cliMergedSource struct {
	cli *CLI
	dir string
}

func (s cliMergedSource) MergedPage(ctx context.Context, branches []string) ([]byte, error) {
	args := []string{"api", "graphql", "-F", "owner={owner}", "-F", "repo={repo}"}
	var decls, fields strings.Builder
	for i, branch := range branches {
		args = append(args, "-f", fmt.Sprintf("b%d=%s", i, branch))
		fmt.Fprintf(&decls, ",$b%d:String!", i)
		fmt.Fprintf(&fields, "b%[1]d:pullRequests(headRefName:$b%[1]d,states:MERGED,first:%d,orderBy:{field:UPDATED_AT,direction:DESC}){nodes{number url headRefOid baseRefName isCrossRepository mergedAt}}\n", i, mergedPerBranch)
	}
	query := fmt.Sprintf("query($owner:String!,$repo:String!%s){repository(owner:$owner,name:$repo){defaultBranchRef{name}\n%s}}", decls.String(), fields.String())
	return s.cli.graphql(ctx, s.dir, append(args, "-f", "query="+query))
}
