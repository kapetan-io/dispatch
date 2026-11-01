//go:build drift

package inbox_test

// Package exception: this package wraps the gh tool, so every test here is an adapter or contract test of it.

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInstalledGHStillHasTheFlagsDispatchUses checks each flag the adapter passes against the
// installed gh's --help. GitHub is never called, and the notifications endpoint needs an account
// the drift check never touches, so only the flags are checked. A missing gh fails the test.
// Run with: go test -tags drift ./internal/inbox/
func TestInstalledGHStillHasTheFlagsDispatchUses(t *testing.T) {
	bin, err := exec.LookPath("gh")
	require.NoError(t, err)

	out, err := exec.Command(bin, "api", "--help").CombinedOutput()
	require.NoError(t, err, string(out))

	// -f is gh's short form of --raw-field.
	for _, flag := range []string{"--method", "--raw-field"} {
		require.Contains(t, string(out), flag)
	}
}
