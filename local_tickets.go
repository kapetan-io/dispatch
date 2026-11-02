package dispatch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/localticket"
	"github.com/kapetan-io/dispatch/internal/lock"
	"github.com/kapetan-io/dispatch/internal/loop"
	"github.com/kapetan-io/dispatch/internal/ticket"
)

// localOptions are the options that open the local ticket database, nil while local tickets are off.
func localOptions(opts Options, cfg config.Config) *localticket.Options {
	if !cfg.Plugins.Local.On {
		return nil
	}
	local := &localticket.Options{Path: cfg.Plugins.Local.Path, Repos: cfg.Repos.Labels(), LockTimeout: opts.LocalLockTimeout}
	if local.Path == "" {
		local.Path = filepath.Join(opts.StateDir, "tickets.db")
	}
	return local
}

// readOnlyLocal reads every local ticket without changing the file or creating one. The caller holds
// dispatch.lock, so no dispatch run has the file. With no file to read it returns an empty listing
// with the default statuses a start would seed. It returns nil while local tickets are off.
func readOnlyLocal(cfg config.Config, opts Options) (*localticket.Listing, error) {
	local := localOptions(opts, cfg)
	if local == nil {
		return nil, nil
	}
	store, err := localticket.OpenReadOnly(*local)
	if err != nil {
		return nil, err
	}
	if store == nil {
		return &localticket.Listing{Table: ticket.DefaultStatuses()}, nil
	}
	defer func() { _ = store.Close() }()
	snap, err := store.Listing()
	if err != nil {
		return nil, err
	}
	return &snap, nil
}

// trackedLocal is what `dispatch rules check` reads of the local tickets. With dispatch run stopped it
// opens the file read-only under dispatch.lock, and while dispatch run holds the file it asks that
// dispatch, so neither path writes the file. It returns nil while local tickets are off or no file exists.
func trackedLocal(ctx context.Context, opts Options, cfg config.Config) (*localticket.Listing, error) {
	if !cfg.Plugins.Local.On {
		return nil, nil
	}
	if err := os.MkdirAll(opts.StateDir, 0o700); err != nil {
		return nil, fmt.Errorf("while creating state dir: %w", err)
	}
	l, err := lock.Acquire(filepath.Join(opts.StateDir, lockName))
	if errors.Is(err, lock.ErrHeld) {
		resp, err := loop.Ask(ctx, opts.StateDir, loop.Request{Op: loop.OpTicket, Call: &loop.TicketCall{Verb: loop.VerbTracked}})
		if errors.Is(err, loop.ErrNotRunning) {
			return nil, fmt.Errorf("dispatch run holds the lock on %s but does not answer yet; try again once it has started", opts.StateDir)
		}
		if err != nil {
			return nil, err
		}
		// The loop reads config.toml only at start, so its message may name a table the file now has.
		if resp.Error == loop.LocalOff {
			return nil, errors.New("the running dispatch started with local tickets off; restart it to read local tickets")
		}
		if resp.Error != "" {
			return nil, errors.New(resp.Error)
		}
		return resp.Tracked, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = l.Release() }()
	return readOnlyLocal(cfg, opts)
}
