package dispatch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/dispatchdb"
	"github.com/kapetan-io/dispatch/internal/localticket"
	"github.com/kapetan-io/dispatch/internal/lock"
	"github.com/kapetan-io/dispatch/internal/loop"
	"github.com/kapetan-io/dispatch/internal/ticket"
	"github.com/kapetan-io/dispatch/internal/workers"
	"github.com/kapetan-io/dispatch/internal/world"
)

// localOptions are the options that open the local tickets in dispatch.db, nil while local tickets are off.
func localOptions(opts Options, cfg config.Config, file *dispatchdb.File) *localticket.Options {
	if !cfg.Plugins.Local.On {
		return nil
	}
	return &localticket.Options{File: file, Repos: cfg.Repos.Labels(), LockTimeout: opts.LocalLockTimeout}
}

// database is what a command other than dispatch run reads of dispatch.db.
type database struct {
	// Local is every local ticket, nil while local tickets are off.
	Local *localticket.Listing
	// Rows are the worker rows, and Recorded is false until the workers of every configured repo
	// are imported, so a worker is still found by its lease.
	Rows     workers.Snapshot
	Recorded bool
}

// into gives a world read the tickets and worker rows d holds.
func (d database) into(src *world.Sources) {
	// A nil *Listing assigned directly would make src.Local a non-nil interface.
	if d.Local != nil {
		src.Local = d.Local
	}
	if d.Recorded {
		src.Workers = d.Rows
	}
}

// readOnlyDatabase reads dispatch.db without changing the file or creating one. The caller holds
// dispatch.lock, so no dispatch run has the file. With no file to read, the local tickets are an
// empty listing with the default statuses a start would seed.
func readOnlyDatabase(cfg config.Config, opts Options) (database, error) {
	var found database
	file, err := dispatchdb.OpenReadOnly(dispatchdb.Options{StateDir: opts.StateDir, LockTimeout: opts.LocalLockTimeout})
	if err != nil {
		return found, err
	}
	if file == nil {
		if cfg.Plugins.Local.On {
			found.Local = &localticket.Listing{Table: ticket.DefaultStatuses()}
		}
		return found, nil
	}
	defer func() { _ = file.Close() }()
	// The tickets.db of an earlier dispatch holds no worker rows, and its workers are imported at the next start.
	if filepath.Base(file.Path()) == dispatchdb.Name {
		if found.Rows, err = workers.New(file).Snapshot(); err != nil {
			return found, err
		}
		// A start that renamed the file and died before its first read imported nothing, and
		// until the import the leases say where the workers are.
		found.Recorded = true
		for _, label := range cfg.Repos.Labels() {
			done, err := file.WorkersImported(label)
			if err != nil {
				return found, err
			}
			found.Recorded = found.Recorded && done
		}
	}
	if !cfg.Plugins.Local.On {
		return found, nil
	}
	store, err := localticket.OpenReadOnly(*localOptions(opts, cfg, file))
	if err != nil {
		return found, err
	}
	listing, err := store.Listing()
	if err != nil {
		return found, err
	}
	found.Local = &listing
	return found, nil
}

// trackedDatabase is what `dispatch rules check` reads of dispatch.db. With dispatch run stopped it
// opens the file read-only under dispatch.lock, and while dispatch run holds the file it asks that
// dispatch, so neither path writes the file.
func trackedDatabase(ctx context.Context, opts Options, cfg config.Config) (database, error) {
	if err := os.MkdirAll(opts.StateDir, 0o700); err != nil {
		return database{}, fmt.Errorf("while creating state dir: %w", err)
	}
	l, err := lock.Acquire(filepath.Join(opts.StateDir, lockName))
	if errors.Is(err, lock.ErrHeld) {
		resp, err := loop.Ask(ctx, opts.StateDir, loop.Request{Op: loop.OpTicket, Call: &loop.TicketCall{Verb: loop.VerbTracked}})
		if errors.Is(err, loop.ErrNotRunning) {
			return database{}, fmt.Errorf("dispatch run holds the lock on %s but does not answer yet; try again once it has started", opts.StateDir)
		}
		if err != nil {
			return database{}, err
		}
		if resp.Error != "" {
			return database{}, errors.New(resp.Error)
		}
		// The loop reads config.toml only at start, so it may run without a table the file now has.
		if cfg.Plugins.Local.On && resp.Tracked == nil {
			return database{}, errors.New("the running dispatch started with local tickets off; restart it to read local tickets")
		}
		found := database{Rows: resp.Rows, Recorded: true}
		if cfg.Plugins.Local.On {
			found.Local = resp.Tracked
		}
		return found, nil
	}
	if err != nil {
		return database{}, err
	}
	defer func() { _ = l.Release() }()
	return readOnlyDatabase(cfg, opts)
}
