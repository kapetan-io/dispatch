//go:build drift

package harness_test

// Package exception: drift test that runs a real Claude in a herdr pane, which no fake stands in for.

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
)

type listed struct {
	SessionID string `json:"sessionId"`
	Socket    string `json:"messagingSocketPath"`
	StartedAt int64  `json:"startedAt"`
}

// listedFor returns the session list entries of the Claude Code session with this id.
func listedFor(t *testing.T, id string) []listed {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(environment.Process().ExpandHome("~/.claude/sessions"), "*.json"))
	require.NoError(t, err)
	var found []listed
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var entry listed
		if json.Unmarshal(raw, &entry) == nil && entry.SessionID == id {
			found = append(found, entry)
		}
	}
	return found
}

func startedClaude(t *testing.T) realClaude {
	t.Helper()
	t.Cleanup(func() {
		left, err := herdrtest.Leftovers(os.Getpid())
		require.NoError(t, err)
		assert.Empty(t, left)
	})
	return startRealClaude(t, herdrtest.StartSession(t))
}

// TestRealClaudeListsItsSessionWithASocket fails when Claude Code stops writing
// ~/.claude/sessions/<pid>.json with sessionId and messagingSocketPath, which Deliver reads.
// Run with: go test -tags drift ./internal/harness/
func TestRealClaudeListsItsSessionWithASocket(t *testing.T) {
	claude := startedClaude(t)

	entries := listedFor(t, claude.id)

	require.Len(t, entries, 1)
	assert.True(t, filepath.IsAbs(entries[0].Socket))
	assert.NotZero(t, entries[0].StartedAt)
}

// TestRealClaudeTakesALongLineAndAMultiLineMessageWhole fails when a message of 1,125 characters
// on one line, or one with several lines, reaches the session cut down or merged. The model
// retypes a command it runs, so arrival is read from the peer message Claude shows, which is
// the text as received.
// Run with: go test -tags drift ./internal/harness/
func TestRealClaudeTakesALongLineAndAMultiLineMessageWhole(t *testing.T) {
	claude := startedClaude(t)
	multi := filepath.Join(t.TempDir(), "multi")
	// 1,125 characters on one line, with a word at each end that a cut would lose.
	line := "HEADWORD " + strings.Repeat("0123456789", 110) + " TAILWORD."
	for len(line) < 1125 {
		line += "."
	}

	require.NoError(t, realSessions.Deliver(context.Background(), claude.id,
		"dispatch event: read this and reply with the word done: "+line))
	require.Eventually(t, func() bool {
		screen := claude.screen(t)
		return strings.Contains(screen, "HEADWORD") && strings.Contains(screen, "TAILWORD")
	}, 2*time.Minute, 500*time.Millisecond)

	require.NoError(t, realSessions.Deliver(context.Background(), claude.id,
		"dispatch event: run this shell command, then reply with the word done:\nprintf '%s\\n' first second > "+multi))
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(multi)
		return err == nil && string(b) == "first\nsecond\n"
	}, 2*time.Minute, 500*time.Millisecond)
}

// TestRealClaudeDropsAFrameCutOffBeforeItsNewline fails when Claude runs half a message. Deliver
// relies on it to keep a failed write from leaving part of a message behind.
// Run with: go test -tags drift ./internal/harness/
func TestRealClaudeDropsAFrameCutOffBeforeItsNewline(t *testing.T) {
	claude := startedClaude(t)
	marker := filepath.Join(t.TempDir(), "marker")
	entries := listedFor(t, claude.id)
	require.Len(t, entries, 1)
	conn, err := net.Dial("unix", entries[0].Socket)
	require.NoError(t, err)
	frame, err := json.Marshal(map[string]any{
		"type":    "user",
		"message": map[string]string{"role": "user", "content": "dispatch event: run this shell command, then reply with the word done: touch " + marker},
	})
	require.NoError(t, err)

	_, err = conn.Write(frame[:len(frame)/2])
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	// Proving a negative takes a wait. Claude takes a message within seconds once it is free.
	time.Sleep(15 * time.Second)

	_, statErr := os.Stat(marker)
	assert.True(t, os.IsNotExist(statErr))
}

// TestRealClaudeKeepsADraftInItsInputBoxWhenAMessageArrives fails when a delivered message
// sends, clears or merges with text a person left typed in the input box.
// Run with: go test -tags drift ./internal/harness/
func TestRealClaudeKeepsADraftInItsInputBoxWhenAMessageArrives(t *testing.T) {
	claude := startedClaude(t)
	marker := filepath.Join(t.TempDir(), "marker")
	const draft = "half-typed-draft-zzz"
	require.NoError(t, claude.session.Run("pane", "send-text", claude.pane, draft))
	require.Eventually(t, func() bool { return strings.Contains(claude.screen(t), draft) }, 30*time.Second, 200*time.Millisecond)

	require.NoError(t, realSessions.Deliver(context.Background(), claude.id,
		"dispatch event: run this shell command, then reply with the word done: touch "+marker))

	require.Eventually(t, func() bool { _, err := os.Stat(marker); return err == nil }, 2*time.Minute, 500*time.Millisecond)
	assert.Contains(t, claude.screen(t), draft)
}

// TestRealClaudeListsAResumedSessionUnderTheSameIDWithANewerStart fails when a resume stops
// keeping the session id or stops writing a newer startedAt, which Deliver relies on to pick the
// live entry. A message sent after the resume must reach the new process.
// Run with: go test -tags drift ./internal/harness/
func TestRealClaudeListsAResumedSessionUnderTheSameIDWithANewerStart(t *testing.T) {
	claude := startedClaude(t)
	before := listedFor(t, claude.id)
	require.Len(t, before, 1)
	claude.session.Type(t, claude.pane, "/exit")
	require.Eventually(t, func() bool { return len(listedFor(t, claude.id)) == 0 }, time.Minute, 200*time.Millisecond)

	// A command typed before the shell shows its prompt again is lost.
	require.Eventually(t, func() bool { return strings.Contains(claude.screen(t), "Resume this session with") }, time.Minute, 200*time.Millisecond)
	time.Sleep(time.Second)
	require.NoError(t, claude.session.Run("pane", "run", claude.pane, "claude --resume "+claude.id+" --model haiku --dangerously-skip-permissions"))

	if !assert.Eventually(t, func() bool { return len(listedFor(t, claude.id)) == 1 }, time.Minute, 200*time.Millisecond) {
		out, _ := claude.session.Output(context.Background(), "pane", "read", claude.pane)
		t.Log(string(out))
		t.FailNow()
	}
	after := listedFor(t, claude.id)
	assert.Greater(t, after[0].StartedAt, before[0].StartedAt)
	assert.NotEqual(t, before[0].Socket, after[0].Socket)
	marker := filepath.Join(t.TempDir(), "marker")
	require.NoError(t, realSessions.Deliver(context.Background(), claude.id,
		"dispatch event: run this shell command, then reply with the word done: touch "+marker))
	require.Eventually(t, func() bool { _, err := os.Stat(marker); return err == nil }, 2*time.Minute, 500*time.Millisecond)
}
