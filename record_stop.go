package dispatch

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/clock"
	"github.com/kapetan-io/dispatch/internal/loop"
)

// stopTimeout caps what record-stop waits on herdr and dispatch together, and what wake waits on herdr, because the
// hook runs after every turn. A report lost to it costs little, since a worker with no record keeps its busy label until the next Stop.
const stopTimeout = time.Second

// recordStop runs as a Stop hook. It tells the running dispatch when the session ended its
// turn and how many background tasks were still running, for the workspace label and the
// rules' turn log to read. It succeeds silently everywhere else, because the hook runs in
// every session on the machine and a failure would show in all of them.
func recordStop(ctx context.Context, opts Options) error {
	input, ok := hookInput(opts)
	if !ok {
		return nil
	}
	// One budget covers the herdr lookup and the report, so the hook never holds a turn longer than stopTimeout on them.
	askCtx, cancel := clock.WithTimeout(ctx, opts.Clock, "record-stop", stopTimeout)
	defer cancel()
	harness, ok := reportingHarness(askCtx, opts)
	if !ok {
		return nil
	}
	report, dir, ok := harness.Stop(input)
	if !ok {
		return nil
	}
	tree, ok := workerTree(ctx, opts, dir)
	if !ok {
		return nil
	}
	report.Tree, report.At = tree, opts.Clock.Now().UnixMilli()
	// The hook runs in every session, so a report dispatch can't take is dropped without a word
	// whether dispatch is not running, stuck or refuses it.
	_, _ = loop.Ask(askCtx, opts.StateDir, loop.Request{Op: loop.OpStop, Stop: &report})
	return nil
}

// worktreeRoot returns the top of the working tree containing dir.
func worktreeRoot(ctx context.Context, opts Options, dir string) (string, bool) {
	cmd := opts.Env.Command(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	root, err := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	return root, err == nil
}
