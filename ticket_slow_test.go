//go:build tests_slow

package dispatch_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"

	"github.com/kapetan-io/dispatch"
)

// A start waits for the file's lock for the default timeout, which a test cannot shorten
// without hiding that the default is within 10 seconds.
func TestRunStopsWithinTenSecondsWhenAnotherProcessHoldsTheDatabase(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.Stop()
	// Another program has the file open, which dispatch.lock does not stop.
	held, err := bolt.Open(d.databasePath(), 0o600, nil)
	require.NoError(t, err)
	defer func() { _ = held.Close() }()

	began := time.Now()
	err = d.startLoop(dispatch.Options{})

	require.ErrorContains(t, err, "dispatch.db")
	assert.Less(t, time.Since(began), 10*time.Second)
}
