//go:build drift

package harness_test

// Package exception: drift test that runs a real Claude in a herdr pane, which no fake stands in for.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/harness"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
)

// TestRealClaudesClearGivesANewSessionIDInTheSameProcess fails when herdr or claude is missing or
// logged out. follow-clear moves what dispatch keyed by the old session id when Claude's SessionStart
// hook reports source "clear", and ties followers by the messaging socket a clear keeps, so a Claude
// that renamed either, or started reporting /compact as a clear, would break it. It spends three
// short Claude turns. Run with: go test -tags drift ./internal/harness/
func TestRealClaudesClearGivesANewSessionIDInTheSameProcess(t *testing.T) {
	t.Cleanup(func() {
		left, err := herdrtest.Leftovers(os.Getpid())
		require.NoError(t, err)
		assert.Empty(t, left)
	})
	session := herdrtest.StartSession(t)
	ctx := context.Background()
	cwd, log := t.TempDir(), filepath.Join(t.TempDir(), "starts.log")
	settings, err := json.Marshal(map[string]any{"hooks": map[string]any{"SessionStart": []any{map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": fmt.Sprintf(`echo "$CLAUDE_CODE_MESSAGING_SOCKET $(cat | tr -d '\n')" >> %q`, log)}},
	}}}})
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(cwd, ".claude"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cwd, ".claude", "settings.json"), settings, 0o644))
	out, err := session.Output(ctx, "workspace", "create", "--cwd", cwd, "--label", "clear", "--no-focus")
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
	require.Eventually(t, func() bool {
		o, err := session.Output(ctx, "pane", "read", pane)
		return err == nil && len(o) > 1
	}, 60*time.Second, 50*time.Millisecond)
	const first = "9c4f5d1b-7e0a-4f4b-ad6c-5a8b1e0f4d23"
	program := harness.ClaudeCode{}.Program(harness.Start{Model: "haiku", Session: first})
	args := append([]string{"--session-id", first}, program.Argv...)
	require.NoError(t, session.Run("pane", "run", pane, "claude "+strings.Join(shellQuote(args), " ")))
	require.NoError(t, session.CLI().Kickoff(ctx, pane, "Reply with only the word ready.", program.Startup))
	require.NoError(t, session.Run("agent", "wait", pane, "--until", "idle", "--timeout", "120000"))

	require.NoError(t, session.Run("pane", "run", pane, "/clear"))
	starts := func(want int) []string {
		var lines []string
		require.Eventually(t, func() bool {
			b, _ := os.ReadFile(log)
			lines = strings.Split(strings.TrimSpace(string(b)), "\n")
			return len(lines) >= want
		}, 30*time.Second, 100*time.Millisecond)
		return lines
	}
	lines := starts(2)
	startup, cleared := strings.SplitN(lines[0], " ", 2), strings.SplitN(lines[1], " ", 2)
	assert.NotEmpty(t, startup[0])
	assert.Equal(t, startup[0], cleared[0])
	_, _, ok := harness.ClaudeCode{}.Cleared([]byte(startup[1]))
	assert.False(t, ok)
	id, dir, ok := harness.ClaudeCode{}.Cleared([]byte(cleared[1]))
	require.True(t, ok)
	assert.NotEqual(t, first, id)
	assert.NotEmpty(t, dir)
	assert.Equal(t, startup[0], harness.ClaudeCode{}.Process(environment.Environment{"CLAUDE_CODE_MESSAGING_SOCKET=" + startup[0]}))

	require.NoError(t, session.Run("pane", "run", pane, "Reply with only the word two."))
	require.NoError(t, session.Run("agent", "wait", pane, "--until", "idle", "--timeout", "120000"))
	require.NoError(t, session.Run("pane", "run", pane, "/compact"))
	lines = starts(3)
	compacted := strings.SplitN(lines[2], " ", 2)
	assert.Equal(t, startup[0], compacted[0])
	_, _, ok = harness.ClaudeCode{}.Cleared([]byte(compacted[1]))
	assert.False(t, ok)
	assert.Contains(t, compacted[1], `"session_id":"`+id+`"`)
}
