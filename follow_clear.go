package dispatch

import (
	"context"
	"slices"

	"github.com/kapetan-io/dispatch/internal/clock"
	"github.com/kapetan-io/dispatch/internal/harness"
	"github.com/kapetan-io/dispatch/internal/loop"
	"github.com/kapetan-io/dispatch/internal/sessionlog"
	"github.com/kapetan-io/dispatch/internal/stops"
)

// followClear runs as a SessionStart hook. When the session's program began a new conversation in
// the same process (Claude Code's /clear), it moves what dispatch keyed by the old session id to
// the new one: the worker's records and host label, and the followers in that process. It
// succeeds silently everywhere else, because the hook runs in every session on the machine.
func followClear(ctx context.Context, opts Options) error {
	input, ok := hookInput(opts)
	if !ok {
		return nil
	}
	kinds := opts.Harnesses.Kinds()
	slices.Sort(kinds)
	for _, kind := range kinds {
		h := opts.Harnesses[kind]
		if session, dir, ok := h.Cleared(input); ok {
			moveCleared(ctx, opts, kind, h, session, dir)
			return nil
		}
	}
	return nil
}

func moveCleared(ctx context.Context, opts Options, kind string, h harness.Harness, session, dir string) {
	ctx, cancel := clock.WithTimeout(ctx, opts.Clock, "follow-clear", stopTimeout)
	defer cancel()
	cleared := loop.Cleared{New: session, Kind: kind, Process: h.Process(opts.Env)}
	moved := moveWorker(ctx, opts, h, session, dir)
	if moved || cleared.Process != "" {
		_, _ = loop.Ask(ctx, opts.StateDir, loop.Request{Op: loop.OpCleared, Cleared: &cleared})
	}
}

// moveWorker moves the records and then the host label of the worker whose session cleared, and
// reports whether both moved. The label is the only place that remembers the old id, so a move that
// fails part way puts the records back under it, and the next clear starts from the same old id.
func moveWorker(ctx context.Context, opts Options, h harness.Harness, session, dir string) bool {
	agent, found := reportingAgent(ctx, opts)
	if !found || agent.Kind != h.AgentKind() || agent.Session == "" || agent.Session == session {
		return false
	}
	tree, ok := workerTree(ctx, opts, dir)
	if !ok {
		return false
	}
	old := agent.Session
	if stops.Rekey(opts.StateDir, tree, old, session) != nil {
		return false
	}
	if sessionlog.Rekey(opts.StateDir, tree, old, session) != nil || agent.Host.FollowSession(ctx, agent.Place, session) != nil {
		_ = stops.Rekey(opts.StateDir, tree, session, old)
		_ = sessionlog.Rekey(opts.StateDir, tree, session, old)
		return false
	}
	return true
}
