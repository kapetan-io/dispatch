//go:build drift

package harness_test

// Package exception: drift test that runs the installed claude, whose transcript no fake stands in for.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/harness"
)

// TestRealClaudesTranscriptShowsAMessageTakenAfterItsStop fails when the Stop hook stops carrying
// last_assistant_message or transcript_path, or when a later message stops adding a user entry,
// stamped after the Stop, to the transcript that path names. It runs two short Haiku turns.
// Run with: go test -tags drift ./internal/harness/
func TestRealClaudesTranscriptShowsAMessageTakenAfterItsStop(t *testing.T) {
	var claude string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if strings.HasSuffix(filepath.ToSlash(dir), "scripts/shims") {
			continue
		}
		if path, err := exec.LookPath(filepath.Join(dir, "claude")); err == nil {
			claude = path
			break
		}
	}
	require.NotEmpty(t, claude)
	work := t.TempDir()
	payload := filepath.Join(work, "stop.json")
	settings, err := json.Marshal(map[string]any{"hooks": map[string]any{"Stop": []any{map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": "cat > " + payload}},
	}}}})
	require.NoError(t, err)
	settingsFile := filepath.Join(work, "settings.json")
	require.NoError(t, os.WriteFile(settingsFile, settings, 0o600))
	const session = "6f3c1c2e-8a52-4f0e-9d63-0c5b7a1d2e44"
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(claude, append([]string{"-p", "--model", "haiku", "--settings", settingsFile}, args...)...)
		cmd.Dir = work
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}

	run("--session-id", session, "Reply with exactly: ok")

	raw, err := os.ReadFile(payload)
	require.NoError(t, err)
	stop, _, ok := harness.ClaudeCode{}.Stop(raw)
	require.True(t, ok)
	assert.Contains(t, strings.ToLower(stop.Message), "ok")
	require.NotEmpty(t, stop.Transcript)
	// Claude keeps the transcript under ~/.claude/projects, outside the temp dir.
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(stop.Transcript)) })
	ended := time.Now().UnixMilli()
	took, err := harness.ClaudeCode{}.TookMessageAfter(stop.Transcript, ended)
	require.NoError(t, err)
	assert.False(t, took)

	time.Sleep(50 * time.Millisecond)
	run("--resume", session, "Reply with exactly: two")

	took, err = harness.ClaudeCode{}.TookMessageAfter(stop.Transcript, ended)
	require.NoError(t, err)
	assert.True(t, took)
}
