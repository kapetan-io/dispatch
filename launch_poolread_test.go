package dispatch_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLaunchReportsBothFailuresWhenTreehouseLeaseFailsAndThePoolCannotBeRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher})
	d.Trees.Pooled()
	d.fakes().Treehouse.FailLease(errors.New("exit status 1: no worktree"))
	boom := errors.New("exit status 1 (db locked)")
	// Each attempt reads the pool once before leasing and once after the lease fails.
	d.fakes().Treehouse.FailTrees(nil, boom, nil, boom, nil, boom)

	out := d.launch("ENG-1", "thrawn01/eng-1")

	assert.Equal(t, "pool_error", out.result["status"])
	assert.Contains(t, out.result["detail"], "the pool could not be read")
	assert.Contains(t, out.result["detail"], "db locked")
	assert.Contains(t, out.result["detail"], "exit status 1: no worktree")
	assert.NotContains(t, out.result["detail"], "0 of 0")
	assert.Equal(t, 3, d.calls("treehouse", "lease"))
}
