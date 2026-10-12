package treehousetest

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/treehouse"
)

// Subject is a pool under test and the repo whose pool it is.
type Subject struct {
	Pool treehouse.Pool
	Repo string
	// Provision makes the pool able to lease n trees at once. The fake gets n
	// available trees, and treehouse gets max_trees = n in its config.
	Provision func(n int)
}

// Scenario is one situation with the outcome both the fake and the real
// treehouse must give.
type Scenario struct {
	Name string
	Run  func(t *testing.T, s Subject)
}

// Scenarios is the contract for treehouse.Pool. The fake runs it in every test
// run, and the real adapter runs it under the drift build tag. The error text
// checked is treehouse's own wording, which the fake copies.
var Scenarios = []Scenario{
	{Name: "EmptyPoolHasNoTrees", Run: func(t *testing.T, s Subject) {
		trees, err := s.Pool.Trees(context.Background(), s.Repo)

		require.NoError(t, err)
		assert.Empty(t, trees)
	}},
	{Name: "TreesFailsInAMissingDirectory", Run: func(t *testing.T, s Subject) {
		_, err := s.Pool.Trees(context.Background(), filepath.Join(s.Repo, "moved"))

		require.Error(t, err)
	}},
	{Name: "LeaseTakesATreeForTheTicket", Run: func(t *testing.T, s Subject) {
		ctx := context.Background()
		s.Provision(1)

		leased, err := s.Pool.Lease(ctx, s.Repo, "ENG-1")

		require.NoError(t, err)
		assert.NotEmpty(t, leased.Path)
		assert.Equal(t, "ENG-1", leased.LeaseHolder)
		trees, err := s.Pool.Trees(ctx, s.Repo)
		require.NoError(t, err)
		require.Len(t, trees, 1)
		assert.Equal(t, leased.Path, trees[0].Path)
		assert.Equal(t, "leased", trees[0].Status)
		assert.Equal(t, "ENG-1", trees[0].LeaseHolder)
	}},
	{Name: "LeaseFailsWhenEveryTreeIsTaken", Run: func(t *testing.T, s Subject) {
		ctx := context.Background()
		s.Provision(1)
		taken, err := s.Pool.Lease(ctx, s.Repo, "ENG-1")
		require.NoError(t, err)

		_, err = s.Pool.Lease(ctx, s.Repo, "ENG-2")

		require.ErrorContains(t, err, "worktrees are in use or dirty")
		trees, err := s.Pool.Trees(ctx, s.Repo)
		require.NoError(t, err)
		require.Len(t, trees, 1)
		assert.Equal(t, taken.Path, trees[0].Path)
		assert.Equal(t, "ENG-1", trees[0].LeaseHolder)
	}},
	{Name: "ReturnFreesTheTreeForItsHolder", Run: func(t *testing.T, s Subject) {
		ctx := context.Background()
		s.Provision(1)
		leased, err := s.Pool.Lease(ctx, s.Repo, "ENG-1")
		require.NoError(t, err)

		require.NoError(t, s.Pool.Return(ctx, s.Repo, leased.Path, "ENG-1"))

		trees, err := s.Pool.Trees(ctx, s.Repo)
		require.NoError(t, err)
		require.Len(t, trees, 1)
		assert.Equal(t, "available", trees[0].Status)
		assert.Empty(t, trees[0].LeaseHolder)
	}},
	{Name: "ReturnRefusesAnotherHoldersTree", Run: func(t *testing.T, s Subject) {
		ctx := context.Background()
		s.Provision(1)
		leased, err := s.Pool.Lease(ctx, s.Repo, "ENG-1")
		require.NoError(t, err)

		err = s.Pool.Return(ctx, s.Repo, leased.Path, "ENG-2")

		require.ErrorContains(t, err, "lease holder does not match worktree "+leased.Path)
		trees, err := s.Pool.Trees(ctx, s.Repo)
		require.NoError(t, err)
		require.Len(t, trees, 1)
		assert.Equal(t, "leased", trees[0].Status)
		assert.Equal(t, "ENG-1", trees[0].LeaseHolder)
	}},
	{Name: "ReturnRefusesATreeThatIsNotLeased", Run: func(t *testing.T, s Subject) {
		ctx := context.Background()
		s.Provision(1)
		leased, err := s.Pool.Lease(ctx, s.Repo, "ENG-1")
		require.NoError(t, err)
		require.NoError(t, s.Pool.Return(ctx, s.Repo, leased.Path, "ENG-1"))

		err = s.Pool.Return(ctx, s.Repo, leased.Path, "ENG-1")

		require.ErrorContains(t, err, leased.Path+" is not leased")
	}},
	{Name: "ReturnRefusesAPathOutsideThePool", Run: func(t *testing.T, s Subject) {
		s.Provision(1)

		err := s.Pool.Return(context.Background(), s.Repo, "/nowhere/tree", "ENG-1")

		require.ErrorContains(t, err, "/nowhere/tree is not managed by treehouse")
	}},
	{Name: "ReturnedTreeIsLeasedAgain", Run: func(t *testing.T, s Subject) {
		ctx := context.Background()
		s.Provision(1)
		first, err := s.Pool.Lease(ctx, s.Repo, "ENG-1")
		require.NoError(t, err)
		require.NoError(t, s.Pool.Return(ctx, s.Repo, first.Path, "ENG-1"))

		second, err := s.Pool.Lease(ctx, s.Repo, "ENG-2")

		require.NoError(t, err)
		assert.Equal(t, first.Path, second.Path)
		assert.Equal(t, "ENG-2", second.LeaseHolder)
	}},
}
