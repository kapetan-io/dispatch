//go:build drift

package pullrequest_test

// Package exception: this package wraps the gh tool, so every test here is an adapter or contract test of it.

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInstalledGHStillHasTheFlagsDispatchUses checks each flag the adapter
// passes against the installed gh's --help. GitHub itself is never called, so
// whether gh refuses a moved head is not checked, only that the flag that asks
// it to still exists. A missing gh fails the test.
// Run with: go test -tags drift ./internal/pullrequest/
func TestInstalledGHStillHasTheFlagsDispatchUses(t *testing.T) {
	bin, err := exec.LookPath("gh")
	require.NoError(t, err)

	for _, test := range []struct {
		command []string
		flags   []string
	}{
		{command: []string{"pr", "merge"}, flags: []string{"--merge", "--match-head-commit"}},
		{command: []string{"api"}, flags: []string{"--field", "--raw-field"}},
		{command: []string{"repo", "view"}, flags: []string{"--json", "--jq"}},
	} {
		t.Run(test.command[0], func(t *testing.T) {
			out, err := exec.Command(bin, append(test.command, "--help")...).CombinedOutput()
			require.NoError(t, err, string(out))

			for _, flag := range test.flags {
				require.Contains(t, string(out), flag)
			}
		})
	}
}
