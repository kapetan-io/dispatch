package dispatch

import (
	"context"
	"encoding/json"

	"github.com/kapetan-io/dispatch/internal/harness"
)

// reportingHarness finds the harness of the session whose hook is running, from the agent kind
// herdr reports for the pane in HERDR_PANE_ID. A session outside herdr has no pane id and is
// dropped before any herdr call, because the hook runs in every session on the machine.
func reportingHarness(ctx context.Context, opts Options) (harness.Harness, bool) {
	pane := opts.Env.Get("HERDR_PANE_ID")
	if pane == "" {
		return nil, false
	}
	agent, found, err := opts.Herdr.AgentAt(ctx, pane)
	if err != nil || !found {
		return nil, false
	}
	return opts.Harnesses.ForAgent(agent.Kind)
}

// hookInput reads the first JSON value on stdin, as a hook that never closes stdin would need.
func hookInput(opts Options) ([]byte, bool) {
	var input json.RawMessage
	err := json.NewDecoder(opts.Stdin).Decode(&input)
	return input, err == nil
}
