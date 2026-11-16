package dispatch

import (
	"context"
	"errors"
	"fmt"

	"github.com/kapetan-io/dispatch/internal/loop"
	"github.com/kapetan-io/dispatch/internal/sessionlog"
)

// errNotAWorker is what `dispatch ask` says outside a worker's tree, or with no harness session.
var errNotAWorker = errors.New("ask runs inside a dispatch worker's session")

// askCommand records that the worker session it runs in needs the user. It names no ticket: the
// tree comes from the working directory and the session from the harness, and the running dispatch
// keeps the ask in that session's log. Only the log of the tree's worker is read, so an ask from
// another session in the tree has no effect.
func askCommand(ctx context.Context, opts Options, args []string) error {
	if len(args) != 0 {
		_, _ = fmt.Fprintln(opts.Stderr, usage)
		return errors.New("ask takes no arguments")
	}
	tree, ok := workerTree(ctx, opts, opts.Dir)
	if !ok {
		return errNotAWorker
	}
	_, session, err := opts.Harnesses.Follower(opts.Env)
	if err != nil {
		return errNotAWorker
	}
	report := sessionlog.AskReport{Tree: tree, Session: session, At: opts.Clock.Now().UnixMilli()}
	resp, err := loop.Ask(ctx, opts.StateDir, loop.Request{Op: loop.OpAsk, Ask: &report})
	if errors.Is(err, loop.ErrNotRunning) {
		return notRunning()
	}
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	_, _ = fmt.Fprintln(opts.Stdout, "asked. Say why in chat and end your turn.")
	return nil
}
