//go:build drift

package linear_test

// Package exception: this package wraps the Linear API, so every test here is an adapter or contract test of it.

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/lineartest"
)

// TestRealLinearKeepsTheLinearContract runs the fake's unseeded scenarios
// against the Linear workspace LINEAR_API_KEY belongs to. It only reads: moves
// and comments are aimed at a ticket that doesn't exist, which Linear refuses
// before it changes anything. A missing key fails the test.
// DISPATCH_DRIFT_LINEAR_TEAM names the team to read, and defaults to ENG.
// Run with: LINEAR_API_KEY=... go test -tags drift ./internal/linear/
func TestRealLinearKeepsTheLinearContract(t *testing.T) {
	key := os.Getenv("LINEAR_API_KEY")
	require.NotEmpty(t, key, "LINEAR_API_KEY is not set")
	team := os.Getenv("DISPATCH_DRIFT_LINEAR_TEAM")
	if team == "" {
		team = "ENG"
	}
	connect := func(key string) linear.Workspace { return linear.NewClient(linear.DefaultURL, key) }
	viewer, err := connect(key).Viewer(context.Background())
	require.NoError(t, err)

	for _, scenario := range lineartest.Scenarios {
		if scenario.Seeded {
			continue
		}
		t.Run(scenario.Name, func(t *testing.T) {
			scenario.Run(t, lineartest.Subject{Connect: connect, Key: key, Team: team, Viewer: viewer})
		})
	}
}
