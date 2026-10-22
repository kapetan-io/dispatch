package treehousetest_test

// Package exception: this package is a fake of treehouse, so every test here is a contract test of the fake.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/journal"
	"github.com/kapetan-io/dispatch/internal/treehouse"
	"github.com/kapetan-io/dispatch/internal/treehousetest"
)

func TestPoolKeepsTheTreehouseContract(t *testing.T) {
	for _, scenario := range treehousetest.Scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			pool := treehousetest.New(&journal.Journal{})
			repo := t.TempDir()
			scenario.Run(t, treehousetest.Subject{
				Pool:      pool,
				Repo:      repo,
				Elsewhere: func() string { return t.TempDir() },
				Provision: func(n int) {
					for i := range n {
						pool.AddTree(repo, filepath.Join(repo, "pool", fmt.Sprint(i)))
					}
				},
			})
		})
	}
}

func TestPoolInjectedFailuresDoNotChangeWhatItAccepts(t *testing.T) {
	ctx := context.Background()
	pool := treehousetest.New(&journal.Journal{})
	repo := t.TempDir()
	tree := filepath.Join(repo, "pool", "0")
	pool.AddTree(repo, tree)

	// A failed lease leaves the available tree available.
	pool.FailLease(errors.New("cannot build"))
	_, err := pool.Lease(ctx, repo, "ENG-1")
	require.ErrorContains(t, err, "cannot build")
	assert.Equal(t, "available", pool.Status(tree))
	assert.Empty(t, pool.Holder(tree))

	// A failed read fails once and then reads the same pool.
	pool.FailTrees(errors.New("exit status 1"))
	_, err = pool.Trees(ctx, repo)
	require.ErrorContains(t, err, "exit status 1")
	trees, err := pool.Trees(ctx, repo)
	require.NoError(t, err)
	require.Len(t, trees, 1)
	assert.Equal(t, "available", trees[0].Status)

	// An injected lease failure never makes Return accept what leases refuse.
	err = pool.Return(ctx, repo, tree, "ENG-1")
	require.ErrorContains(t, err, "is not leased")
}

func TestPoolMovesALeaseOnlyAfterTheNextRead(t *testing.T) {
	ctx := context.Background()
	pool := treehousetest.New(&journal.Journal{})
	repo := t.TempDir()
	tree := filepath.Join(repo, "pool", "0")
	pool.Seed(repo, treehouse.Tree{Path: tree, Status: "leased", LeaseHolder: "ENG-1"})
	pool.MoveLeaseAfterRead(tree, "ENG-2")

	trees, err := pool.Trees(ctx, repo)

	require.NoError(t, err)
	require.Len(t, trees, 1)
	assert.Equal(t, "ENG-1", trees[0].LeaseHolder)
	assert.Equal(t, "ENG-2", pool.Holder(tree))
	err = pool.Return(ctx, repo, tree, "ENG-1")
	require.ErrorContains(t, err, "lease holder does not match worktree "+tree)
	assert.Equal(t, "ENG-2", pool.Holder(tree))
	assert.Equal(t, "leased", pool.Status(tree))

	again, err := pool.Trees(ctx, repo)
	require.NoError(t, err)
	assert.Equal(t, "ENG-2", again[0].LeaseHolder)
}

func TestPoolRecordsItsCallsInOrder(t *testing.T) {
	ctx := context.Background()
	calls := &journal.Journal{}
	pool := treehousetest.New(calls)
	repo := t.TempDir()
	tree := filepath.Join(repo, "pool", "0")
	pool.AddTree(repo, tree)

	_, err := pool.Trees(ctx, repo)
	require.NoError(t, err)
	_, err = pool.Lease(ctx, repo, "ENG-1")
	require.NoError(t, err)
	require.NoError(t, pool.Return(ctx, repo, tree, "ENG-1"))

	assert.Equal(t, []journal.Call{
		{Tool: "treehouse", Name: "trees", Args: []string{repo}},
		{Tool: "treehouse", Name: "lease", Args: []string{repo, "ENG-1"}},
		{Tool: "treehouse", Name: "return", Args: []string{repo, tree, "ENG-1"}},
	}, calls.Calls())
}
