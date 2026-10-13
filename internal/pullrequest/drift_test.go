//go:build drift

package pullrequest_test

import (
	"context"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/pullrequest"
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

// TestGitHubStillAcceptsThePRQuery sends the PR query to GitHub for a branch
// that has no PR, which is read-only and still makes GitHub validate the whole
// query against its schema.
func TestGitHubStillAcceptsThePRQuery(t *testing.T) {
	_, found, err := pullrequest.NewCLI("gh").Read(context.Background(), ".", "dispatch-drift-no-such-branch", pullrequest.Rules{IgnoreAuthors: []string{"bot"}})

	require.NoError(t, err)
	require.False(t, found)
}
