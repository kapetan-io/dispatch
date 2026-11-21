package pullrequest_test

// Package exception: this package wraps the gh tool, so every test here is an adapter or contract test of it.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/pullrequest"
)

const commitPulls = `[
 {"number":4,"html_url":"https://github.com/o/r/pull/4","title":"Open","body":null,"merged_at":null,"merge_commit_sha":"m4","head":{"ref":"b4","sha":"h4"},"base":{"sha":"x4"}},
 {"number":5,"html_url":"https://github.com/o/r/pull/5","title":"Five","body":"Fixes ENG-5","merged_at":"2026-10-02T10:00:00Z","merge_commit_sha":"m5","head":{"ref":"b5","sha":"h5"},"base":{"sha":"x5"}},
 {"number":6,"html_url":"https://github.com/o/r/pull/6","title":"Six","body":null,"merged_at":"2026-10-03T10:00:00Z","merge_commit_sha":"c1","head":{"ref":"b6","sha":"h6"},"base":{"sha":"x6"}}
]`

func TestCLIMergedHoldingAsksGitHubForTheCommitsPullRequests(t *testing.T) {
	exe, args := standIn(t, commitPulls, "", "0")

	pr, found, err := pullrequest.NewCLI(exe).MergedHolding(context.Background(), "o/r", "c1")

	require.NoError(t, err)
	assert.Equal(t, []string{"api repos/o/r/commits/c1/pulls"}, recorded(t, args))
	require.True(t, found)
	// The PR whose merge commit it is wins over the earlier merged PR that also holds it.
	assert.Equal(t, pullrequest.MergedChange{MergedPR: pullrequest.MergedPR{Number: 6, URL: "https://github.com/o/r/pull/6", Head: "h6",
		MergedAt: time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)}, Base: "x6", Branch: "b6", Title: "Six"}, pr)
}

func TestCLIMergedHoldingTakesTheFirstMergedPRThatHoldsTheCommit(t *testing.T) {
	exe, _ := standIn(t, commitPulls, "", "0")

	pr, found, err := pullrequest.NewCLI(exe).MergedHolding(context.Background(), "o/r", "h5")

	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, 5, pr.Number)
	assert.Equal(t, "Fixes ENG-5", pr.Body)
}

func TestCLIMergedHoldingFindsNoneWhenNoMergedPRHoldsTheCommit(t *testing.T) {
	exe, _ := standIn(t, `[{"number":4,"merged_at":null}]`, "", "0")

	_, found, err := pullrequest.NewCLI(exe).MergedHolding(context.Background(), "o/r", "c1")

	require.NoError(t, err)
	assert.False(t, found)
}

func TestCLIOwnMergedListsTheUsersMergedPRsOfTheWindow(t *testing.T) {
	exe, args := standIn(t, `[{"number":3,"url":"https://github.com/o/r/pull/3","title":"T","body":"B","headRefName":"b3","headRefOid":"h3","baseRefOid":"x3","mergedAt":"2026-10-04T10:00:00Z"}]`, "", "0")

	prs, err := pullrequest.NewCLI(exe).OwnMerged(context.Background(), "o/r",
		time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC), time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC))

	require.NoError(t, err)
	assert.Equal(t, []string{"pr list --repo o/r --state merged --author @me --search merged:2026-10-01..2026-10-10 --limit 1000 --json number,url,title,body,headRefName,headRefOid,baseRefOid,mergedAt"}, recorded(t, args))
	assert.Equal(t, []pullrequest.MergedChange{{MergedPR: pullrequest.MergedPR{Number: 3, URL: "https://github.com/o/r/pull/3", Head: "h3",
		MergedAt: time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)}, Base: "x3", Branch: "b3", Title: "T", Body: "B"}}, prs)
}
