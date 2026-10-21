package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kapetan-io/dispatch/internal/sends"
	"github.com/kapetan-io/dispatch/internal/sessionlog"
)

// hookDeadline bounds the git calls of record-command, because it runs while a person waits to type. The Stop hook runs after the turn, so it has none.
const hookDeadline = 3 * time.Second

// promptInput is the part of Claude Code's UserPromptSubmit hook input that dispatch reads.
type promptInput struct {
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
	Prompt    string `json:"prompt"`
}

// recordCommand runs as a UserPromptSubmit hook. It adds a slash command to the session's log
// with who typed it, for the rules to read. It stores nothing of a message that is not a command.
// It succeeds silently everywhere, and the plugin's hook entry discards its output, because
// text on stdout is added to the person's prompt and a failure would show in every Claude session.
func recordCommand(ctx context.Context, opts Options) error {
	var in promptInput
	if json.NewDecoder(opts.Stdin).Decode(&in) != nil {
		return nil
	}
	cmd, ok := sessionlog.ParseCommand(in.Prompt)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, hookDeadline)
	defer cancel()
	tree, ok := workerTree(ctx, opts, in.CWD)
	if !ok {
		return nil
	}
	cmd.At = opts.Clock.Now().UnixMilli()
	cmd.By = sessionlog.ByPerson
	if sends.Claim(opts.StateDir, tree, in.SessionID, in.Prompt) {
		cmd.By = sessionlog.ByDispatch
	}
	if err := sessionlog.AppendCommand(opts.StateDir, tree, in.SessionID, cmd); err != nil {
		_, _ = fmt.Fprintln(opts.Stderr, "dispatch:", err)
	}
	return nil
}
