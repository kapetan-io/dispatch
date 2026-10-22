//go:build tests_slow

package dispatch_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
)

func TestRulesInitRunsTheRulesItWrote(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := filepath.Join(t.TempDir(), "rules")
	require.NoError(t, d.Run("rules", "init", dir).Err)

	check := d.RunWith(dispatch.Options{RulesDir: dir}, "rules", "check")
	require.NoError(t, check.Err)
	assert.Regexp(t, `launch-worker +whenever +ticket +wants launch now for ENG-1\n`, check.Stdout)

	test := d.RunWith(dispatch.Options{RulesDir: dir}, "rules", "test")
	require.NoError(t, test.Err)
	assert.Contains(t, test.Stdout, "0 failed")
}

func TestRulesTestRunsTheShippedScenarios(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})

	res := d.Run("rules", "test")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "0 failed")
	assert.NotContains(t, res.Stdout, "FAIL")
}
