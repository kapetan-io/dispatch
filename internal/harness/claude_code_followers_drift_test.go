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

	"github.com/kapetan-io/dispatch/internal/harness"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
)

// realClaude is an interactive Claude Code session in a herdr pane, started the way a worker
// is, whose inbox socket it reported by running a command.
type realClaude struct {
	session *herdrtest.Session
	pane    string
	socket  string
}

func startRealClaude(t *testing.T, session *herdrtest.Session) realClaude {
	t.Helper()
	ctx := context.Background()
	out, err := session.Output(ctx, "workspace", "create", "--cwd", t.TempDir(), "--label", "follower", "--no-focus")
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
	program := harness.ClaudeCode{}.Program(harness.Start{Model: "haiku"})
	require.NoError(t, session.Run("pane", "run", pane, "claude "+strings.Join(shellQuote(program.Argv), " ")))

	seen := filepath.Join(t.TempDir(), "socket")
	kickoff := fmt.Sprintf("Run this shell command, then reply with the word done: printenv CLAUDE_CODE_MESSAGING_SOCKET > %s", seen)
	require.NoError(t, session.CLI().Kickoff(ctx, pane, kickoff, program.Startup))
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(seen)
		return err == nil && len(b) > 0
	}, 2*time.Minute, 500*time.Millisecond)
	b, err := os.ReadFile(seen)
	require.NoError(t, err)
	return realClaude{session: session, pane: pane, socket: strings.TrimSpace(string(b))}
}

func shellQuote(args []string) []string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
	}
	return quoted
}

func (c realClaude) screen(t *testing.T) string {
	t.Helper()
	out, err := c.session.Output(context.Background(), "agent", "read", c.pane, "--source", "visible", "--lines", "40")
	require.NoError(t, err)
	return string(out)
}

// TestRealClaudeReadsAMessageDeliveredToItsInbox fails when herdr or claude is missing or
// logged out. It spends two short Claude turns.
// Run with: go test -tags drift ./internal/harness/
func TestRealClaudeReadsAMessageDeliveredToItsInbox(t *testing.T) {
	t.Cleanup(func() {
		left, err := herdrtest.Leftovers(os.Getpid())
		require.NoError(t, err)
		assert.Empty(t, left)
	})
	claude := startRealClaude(t, herdrtest.StartSession(t))
	marker := filepath.Join(t.TempDir(), "marker")

	require.NoError(t, harness.ClaudeCode{}.Deliver(context.Background(), claude.socket,
		"dispatch event: run this shell command, then reply with the word done: touch "+marker))

	require.Eventually(t, func() bool { _, err := os.Stat(marker); return err == nil }, 2*time.Minute, 500*time.Millisecond)
}

// TestRealClaudeKeepsAMessageOutOfAnOpenQuestion fails when herdr or claude is missing or
// logged out. Deliver's contract is that a message never answers an open question, so a
// Claude that took the message as the answer fails it. It spends three short Claude turns.
// Run with: go test -tags drift ./internal/harness/
func TestRealClaudeKeepsAMessageOutOfAnOpenQuestion(t *testing.T) {
	t.Cleanup(func() {
		left, err := herdrtest.Leftovers(os.Getpid())
		require.NoError(t, err)
		assert.Empty(t, left)
	})
	ctx := context.Background()
	claude := startRealClaude(t, herdrtest.StartSession(t))
	marker := filepath.Join(t.TempDir(), "marker")
	const question = "Use the AskUserQuestion tool to ask me one question, which color I want, with the options Crimson and Cobalt. Do nothing else until I answer."
	require.NoError(t, claude.session.CLI().Prompt(ctx, claude.pane, question))
	require.NoError(t, claude.session.Run("agent", "wait", claude.pane, "--until", "blocked", "--timeout", "120000"))
	require.Contains(t, claude.screen(t), "Cobalt")

	require.NoError(t, harness.ClaudeCode{}.Deliver(ctx, claude.socket,
		"dispatch event: run this shell command, then reply with the word done: touch "+marker))
	// Proving a negative takes a wait. Claude takes a queued message within seconds once it is free.
	time.Sleep(15 * time.Second)

	assert.Contains(t, claude.screen(t), "Cobalt")
	_, err := os.Stat(marker)
	assert.True(t, os.IsNotExist(err))

	// The queued message runs once the question is answered.
	require.NoError(t, claude.session.Run("pane", "send-keys", claude.pane, "Enter"))
	require.Eventually(t, func() bool { _, err := os.Stat(marker); return err == nil }, 2*time.Minute, 500*time.Millisecond)
}
