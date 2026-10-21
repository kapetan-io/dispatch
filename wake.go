package dispatch

import (
	"context"
	"encoding/json"

	"github.com/kapetan-io/dispatch/internal/loop"
)

// wakeLoop runs as a Notification or UserPromptSubmit hook. A worker that went idle or
// got a prompt changes its workspace label only when a cycle runs, so the hook asks the
// running loop for one instead of waiting for the next resync. It prints nothing and
// always succeeds, because UserPromptSubmit adds stdout to the prompt and the hook runs
// in every Claude session on the machine.
func wakeLoop(ctx context.Context, opts Options) error {
	var in struct {
		CWD string `json:"cwd"`
	}
	if json.NewDecoder(opts.Stdin).Decode(&in) != nil {
		return nil
	}
	if _, ok := workerTree(ctx, opts, in.CWD); !ok {
		return nil
	}
	_ = loop.Wake(ctx, opts.StateDir)
	return nil
}
