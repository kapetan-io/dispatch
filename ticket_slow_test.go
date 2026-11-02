//go:build tests_slow

package dispatch_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
)

// A second run waits for the file's lock for the default timeout, which a test cannot shorten
// without hiding that the default is within 10 seconds.
func TestRunStopsWithinTenSecondsWhenAnotherRunHoldsTheDatabase(t *testing.T) {
	t.Parallel()
	shared := "[plugins.local]\npath = \"" + t.TempDir() + "/shared.db\"\n"
	first := newDispatch(t, dispatchOptions{Config: shared})
	first.StartBeforeTheEvents()
	second := newDispatch(t, dispatchOptions{Config: shared})

	began := time.Now()
	err := second.startLoop(dispatch.Options{})

	require.ErrorContains(t, err, "shared.db")
	assert.Less(t, time.Since(began), 10*time.Second)
}
