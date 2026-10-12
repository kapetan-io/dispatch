package pullrequesttest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/journal"
	"github.com/kapetan-io/dispatch/internal/pullrequesttest"
)

const prURL = "https://github.com/o/r/pull/7"

func page(head string, cross bool) map[string]any {
	return map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequests": map[string]any{"nodes": []any{map[string]any{
		"number":            7,
		"url":               prURL,
		"isCrossRepository": cross,
		"headRefOid":        head,
		"mergeable":         "MERGEABLE",
		"reviewThreads":     map[string]any{"nodes": []any{}, "pageInfo": map[string]bool{"hasNextPage": false}},
	}}}}}}
}

func TestRepositoryMergesThePRWhoseHeadMatches(t *testing.T) {
	ctx := context.Background()
	repo := pullrequesttest.New(&journal.Journal{})
	repo.Respond("b", "", page("a1", false))

	require.NoError(t, repo.Merge(ctx, prURL, "a1"))

	_, found, err := repo.Read(ctx, "/repo", "b")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Equal(t, []string{prURL + "|a1"}, repo.Merged())
}

func TestRepositoryRefusesToMergeAPRWhoseHeadMoved(t *testing.T) {
	ctx := context.Background()
	repo := pullrequesttest.New(&journal.Journal{})
	repo.Respond("b", "", page("a1", false))
	pr, found, err := repo.Read(ctx, "/repo", "b")
	require.NoError(t, err)
	require.True(t, found)
	repo.PushAfterRead("b", "a2")

	_, _, err = repo.Read(ctx, "/repo", "b")
	require.NoError(t, err)
	err = repo.Merge(ctx, prURL, pr.Head)

	require.ErrorContains(t, err, "Head branch was modified")
	assert.Empty(t, repo.Merged())
	pr, found, err = repo.Read(ctx, "/repo", "b")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "a2", pr.Head)
}

func TestRepositoryRefusesAPRThatIsNotOpen(t *testing.T) {
	repo := pullrequesttest.New(&journal.Journal{})

	err := repo.Merge(context.Background(), prURL, "a1")

	require.ErrorContains(t, err, "Could not resolve to a PullRequest")
}

func TestRepositoryNeverMergesAForkPR(t *testing.T) {
	repo := pullrequesttest.New(&journal.Journal{})
	repo.Respond("b", "", page("a1", true))

	err := repo.Merge(context.Background(), prURL, "a1")

	require.Error(t, err)
	assert.Empty(t, repo.Merged())
}

func TestRepositoryInjectedFailuresDoNotChangeWhatItAccepts(t *testing.T) {
	ctx := context.Background()
	repo := pullrequesttest.New(&journal.Journal{})
	repo.Respond("b", "", page("a1", false))

	repo.FailMerge(errors.New("outage"))
	require.ErrorContains(t, repo.Merge(ctx, prURL, "a1"), "outage")
	repo.FailMerge(nil)
	repo.PushAfterRead("b", "a2")
	_, _, err := repo.Read(ctx, "/repo", "b")
	require.NoError(t, err)

	require.ErrorContains(t, repo.Merge(ctx, prURL, "a1"), "Head branch was modified")
}
