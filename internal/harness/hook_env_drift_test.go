//go:build drift

package harness_test

// Package exception: drift test that runs a real Claude in a herdr pane, which no fake stands in for.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdrtest"
)

// TestRealClaudesStopHookSeesTheIdOfThePaneItRunsIn fails when herdr or claude is missing or
// logged out. record-stop and wake find a session's harness from HERDR_PANE_ID, so a Claude
// whose hooks lose it would make every report vanish. It spends one short Claude turn.
// Run with: go test -tags drift ./internal/harness/
func TestRealClaudesStopHookSeesTheIdOfThePaneItRunsIn(t *testing.T) {
	t.Cleanup(func() {
		left, err := herdrtest.Leftovers(os.Getpid())
		require.NoError(t, err)
		assert.Empty(t, left)
	})
	session := herdrtest.StartSession(t)
	ctx := context.Background()
	seen := filepath.Join(t.TempDir(), "pane")
	settings, err := json.Marshal(map[string]any{"hooks": map[string]any{"Stop": []any{map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": fmt.Sprintf("printenv HERDR_PANE_ID > %q", seen)}},
	}}}})
	require.NoError(t, err)

	out, err := session.Output(ctx, "workspace", "create", "--cwd", t.TempDir(), "--label", "hook-env", "--no-focus")
	require.NoError(t, err)
	var created struct {
		Result struct {
			Pane struct {
				ID string `json:"pane_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(out, &created))
	pane := created.Result.Pane.ID

	// A command typed before the shell shows its prompt can be lost.
	require.Eventually(t, func() bool {
		out, err := session.Output(ctx, "pane", "read", pane)
		return err == nil && len(out) > 1
	}, 60*time.Second, 50*time.Millisecond)
	require.NoError(t, session.Run("pane", "run", pane, fmt.Sprintf("claude -p 'reply with the word ok' --settings '%s'", settings)))

	require.Eventually(t, func() bool {
		b, err := os.ReadFile(seen)
		return err == nil && len(b) > 0
	}, 2*time.Minute, 500*time.Millisecond)
	b, err := os.ReadFile(seen)
	require.NoError(t, err)
	assert.Equal(t, pane+"\n", string(b))
}
