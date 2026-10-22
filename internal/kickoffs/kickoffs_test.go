package kickoffs_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/kickoffs"
)

// Package exception: needs concurrent callers, which the CLI cannot arrange.
func TestAReleaseRacingANewerLaunchNeverErasesTheNewerMark(t *testing.T) {
	state, tree := t.TempDir(), t.TempDir()

	for range 500 {
		first, err := kickoffs.Mark(state, tree, time.UnixMilli(1000))
		require.NoError(t, err)
		var done sync.WaitGroup
		done.Add(2)
		go func() { defer done.Done(); kickoffs.Release(state, tree, first) }()
		go func() {
			defer done.Done()
			_, err := kickoffs.Mark(state, tree, time.UnixMilli(2000))
			assert.NoError(t, err)
		}()
		done.Wait()

		at, held := kickoffs.Since(state, tree)
		require.True(t, held)
		require.Equal(t, time.UnixMilli(2000), at)
		kickoffs.Clear(state, tree)
	}
}

// Package exception: needs a launch replaced between two steps, which the CLI cannot arrange.
func TestAReleaseByALaunchThatWasReplacedLeavesTheNewerMark(t *testing.T) {
	state, tree := t.TempDir(), t.TempDir()
	first, err := kickoffs.Mark(state, tree, time.UnixMilli(1000))
	require.NoError(t, err)
	_, err = kickoffs.Mark(state, tree, time.UnixMilli(2000))
	require.NoError(t, err)

	kickoffs.Release(state, tree, first)

	at, held := kickoffs.Since(state, tree)
	assert.True(t, held)
	assert.Equal(t, time.UnixMilli(2000), at)
}
