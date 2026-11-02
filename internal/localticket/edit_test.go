package localticket_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/localticket"
	"github.com/kapetan-io/dispatch/internal/ticket"
)

// Two edits at once are concurrent callers, which only the store's API can drive.
func TestEditTwoCycleMakingEditsAtOnceLeavesTheOtherOneApplied(t *testing.T) {
	store := open(t, options(t))
	a, err := store.Create(draft("a"))
	require.NoError(t, err)
	b, err := store.Create(draft("b"))
	require.NoError(t, err)

	var wg sync.WaitGroup
	for _, edit := range []struct{ id, parent string }{{a, b}, {b, a}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = store.Edit(edit.id, localticket.Edit{Parent: &edit.parent})
		}()
	}
	wg.Wait()

	first, err := store.Show(a)
	require.NoError(t, err)
	second, err := store.Show(b)
	require.NoError(t, err)
	assert.Equal(t, 1, countNonEmpty(first.Parent, second.Parent))
}

func countNonEmpty(values ...string) int {
	n := 0
	for _, v := range values {
		if v != "" {
			n++
		}
	}
	return n
}

func TestEditRefusalChangesNothingItAlsoNamed(t *testing.T) {
	store := open(t, options(t))
	a, err := store.Create(draft("a"))
	require.NoError(t, err)
	before, err := store.Show(a)
	require.NoError(t, err)

	err = store.Edit(a, localticket.Edit{Title: new("renamed"), AddLabels: []string{"urgent"}, Parent: new("LOC-12")})

	require.ErrorIs(t, err, localticket.ErrRefused)
	after, err := store.Show(a)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestOpenSeedsExactlyTheDefaultStatusesIntoAFileKilledBeforeSeeding(t *testing.T) {
	o := options(t)
	writeStatuses(t, o.Path, nil)

	store := open(t, o)
	assert.Equal(t, ticket.DefaultStatuses(), store.Statuses())
}
