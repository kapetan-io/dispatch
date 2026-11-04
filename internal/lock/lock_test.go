package lock_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/lock"
)

func TestWaitTakesAFreeLock(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "a.lock")

	held, err := lock.Wait(t.Context(), path)

	require.NoError(t, err)
	require.NoError(t, held.Release())
}

func TestWaitReturnsOnceTheHolderReleases(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "a.lock")
	first, err := lock.Wait(t.Context(), path)
	require.NoError(t, err)

	got := make(chan error, 1)
	go func() {
		second, err := lock.Wait(t.Context(), path)
		if err == nil {
			err = second.Release()
		}
		got <- err
	}()

	select {
	case <-got:
		t.Fatal("the second Wait returned while the lock was held")
	case <-time.After(50 * time.Millisecond):
	}
	require.NoError(t, first.Release())
	select {
	case err := <-got:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the second Wait never returned after the release")
	}
}

func TestWaitGivesUpWhenTheContextEnds(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "a.lock")
	first, err := lock.Wait(t.Context(), path)
	require.NoError(t, err)
	defer func() { _ = first.Release() }()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	_, err = lock.Wait(ctx, path)

	require.ErrorIs(t, err, context.DeadlineExceeded)
}
