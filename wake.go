package dispatch

import (
	"context"
	"strings"

	"github.com/kapetan-io/dispatch/internal/clock"
	"github.com/kapetan-io/dispatch/internal/loop"
)

// wakeLoop runs as a Notification or UserPromptSubmit hook of any registered harness and asks the running loop for
// a cycle, so the workspace label follows the worker within seconds. A prompt with any text
// is reported instead: the loop stores a slash command with who typed it, and any other message
// with its time, then wakes itself. A Notification carries no text and only wakes. It prints nothing and always succeeds, because UserPromptSubmit adds stdout to
// the prompt and the hook runs in every session on the machine.
func wakeLoop(ctx context.Context, opts Options) error {
	input, ok := hookInput(opts)
	if !ok {
		return nil
	}
	herdrCtx, cancel := clock.WithTimeout(ctx, opts.Clock, "wake", stopTimeout)
	defer cancel()
	harness, ok := reportingHarness(herdrCtx, opts)
	if !ok {
		return nil
	}
	report, dir, ok := harness.Prompt(input)
	if !ok {
		return nil
	}
	tree, ok := workerTree(ctx, opts, dir)
	if !ok {
		return nil
	}
	if strings.TrimSpace(report.Text) != "" && report.Session != "" {
		report.Tree, report.At = tree, opts.Clock.Now().UnixMilli()
		_ = loop.Send(ctx, opts.StateDir, loop.Request{Op: loop.OpPrompt, Prompt: &report})
		return nil
	}
	_ = loop.Wake(ctx, opts.StateDir)
	return nil
}
