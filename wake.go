package dispatch

import (
	"context"
	"encoding/json"

	"github.com/kapetan-io/dispatch/internal/loop"
	"github.com/kapetan-io/dispatch/internal/sessionlog"
)

// wakeLoop runs as a Notification or UserPromptSubmit hook and asks the running loop for
// a cycle, so the workspace label follows the worker within seconds. A prompt that starts
// with a slash command is reported instead of the plain wake: the loop stores the command and
// who typed it, then wakes itself. It prints nothing and always succeeds, because
// UserPromptSubmit adds stdout to the prompt and the hook runs in every Claude session on the machine.
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
		_ = loop.Send(ctx, opts.StateDir, loop.Request{Op: loop.OpPrompt, Prompt: &loop.Prompt{
			Tree: tree, Session: in.SessionID, At: opts.Clock.Now().UnixMilli(), Text: in.Prompt,
		}})
		return nil
	}
	_ = loop.Wake(ctx, opts.StateDir)
	return nil
}
