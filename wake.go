package dispatch

import (
	"context"
	"encoding/json"
	"time"

	"github.com/kapetan-io/dispatch/internal/loop"
	"github.com/kapetan-io/dispatch/internal/sessionlog"
)

// promptTimeout caps the wait on dispatch for a command's report, because the hook runs while a
// person waits to send a message. A report lost to it reads the command as missing.
const promptTimeout = time.Second

// wakeLoop runs as a Notification or UserPromptSubmit hook and asks the running loop for
// a cycle, so the workspace label follows the worker within seconds. A prompt that starts
// with a slash command is reported to the loop as the wake, which stores the command and who
// typed it. It prints nothing and always succeeds, because UserPromptSubmit adds stdout to
// the prompt and the hook runs in every Claude session on the machine.
func wakeLoop(ctx context.Context, opts Options) error {
	var in struct {
		SessionID string `json:"session_id"`
		CWD       string `json:"cwd"`
		Prompt    string `json:"prompt"`
	}
	if json.NewDecoder(opts.Stdin).Decode(&in) != nil {
		return nil
	}
	tree, ok := workerTree(ctx, opts, in.CWD)
	if !ok {
		return nil
	}
	if _, isCommand := sessionlog.ParseCommand(in.Prompt); isCommand && in.SessionID != "" {
		askCtx, cancel := context.WithTimeout(ctx, promptTimeout)
		defer cancel()
		_, _ = loop.Ask(askCtx, opts.StateDir, loop.Request{Op: loop.OpPrompt, Prompt: &loop.Prompt{
			Tree: tree, Session: in.SessionID, At: opts.Clock.Now().UnixMilli(), Text: in.Prompt,
		}})
		return nil
	}
	_ = loop.Wake(ctx, opts.StateDir)
	return nil
}
