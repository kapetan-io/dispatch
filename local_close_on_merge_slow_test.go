//go:build tests_slow

package dispatch_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Filing 51 tickets through the CLI takes about 10 seconds.
func TestTickReadsMergedPRsInBatchesOfFifty(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	for range 51 {
		d.fileLocal("Backlog")
	}
	d.Tick()
	reads := len(d.Tools().Calls("pullrequest", "merged"))

	d.Advance(5 * time.Minute)

	calls := d.Tools().Calls("pullrequest", "merged")[reads:]
	require.Len(t, calls, 2)
	assert.Equal(t, 50, strings.Count(calls[0], ",")+1)
	assert.Equal(t, 1, strings.Count(calls[1], ",")+1)
}
