package dispatch

import (
	"context"
	"fmt"
	"slices"

	"github.com/kapetan-io/dispatch/internal/cache"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/reconcile"
	"github.com/kapetan-io/dispatch/internal/stops"
	"github.com/kapetan-io/dispatch/internal/treehouse"
)

// heldFinished lists the finished tickets that still hold a lease.
func heldFinished(in reconcile.Input) map[string]bool {
	held := map[string]bool{}
	for _, t := range in.LeaseHolders {
		if slices.Contains(in.Done, t.Status) && len(in.Leases[t.Identifier]) > 0 {
			held[t.Identifier] = true
		}
	}
	return held
}

// teardown closes a finished ticket's workspace and returns its tree. The
// tree is read again once the workspace is closed, because the worker could
// have written to it since the plan was made. Work found then keeps the tree,
// and the next tick's plan comments on it. Untracked files are deleted just
// before the return, so the log names what was actually removed.
func teardown(ctx context.Context, opts Options, a reconcile.Action) error {
	if a.WorkspaceID != "" {
		if err := opts.Herdr.CloseWorkspace(ctx, a.WorkspaceID); err != nil {
			return fmt.Errorf("while closing herdr workspace %s: %w", a.WorkspaceID, err)
		}
	}
	why, err := treehouse.Unfinished(ctx, a.TreePath)
	if err != nil {
		return err
	}
	if why != "" {
		_, _ = fmt.Fprintln(opts.Stdout, reconcile.Action{Kind: reconcile.Keep, Ticket: a.Ticket, Reason: "the worker left " + why + " in " + a.TreePath})
		return nil
	}
	files, err := treehouse.Untracked(ctx, a.TreePath)
	if err != nil {
		return err
	}
	if len(files) > 0 {
		if err := treehouse.RemoveUntracked(ctx, a.TreePath); err != nil {
			return err
		}
		left, err := treehouse.Untracked(ctx, a.TreePath)
		if err != nil {
			return err
		}
		// git clean skips a nested repository, and returning the tree would discard it.
		if len(left) > 0 {
			_, _ = fmt.Fprintln(opts.Stdout, reconcile.Action{Kind: reconcile.Keep, Ticket: a.Ticket, Reason: "untracked " + reconcile.NameList(left) + " in " + a.TreePath + " survived git clean"})
			return nil
		}
		a.Reason = a.Ticket.Status + ", removed untracked " + reconcile.NameList(files)
	}
	if err := opts.Treehouse.Return(ctx, a.RepoDir, a.TreePath, a.Ticket.Identifier); err != nil {
		return fmt.Errorf("while returning %s to the pool: %w", a.TreePath, err)
	}
	stops.Remove(opts.StateDir, a.TreePath)
	_, _ = fmt.Fprintln(opts.Stdout, a)
	return nil
}

// stop closes the workspace of a paused ticket's worker. The tree stays leased.
func stop(ctx context.Context, opts Options, a reconcile.Action) error {
	if err := opts.Herdr.CloseWorkspace(ctx, a.WorkspaceID); err != nil {
		return fmt.Errorf("while closing herdr workspace %s: %w", a.WorkspaceID, err)
	}
	_, _ = fmt.Fprintln(opts.Stdout, a)
	return nil
}

// keep stops the worker on a tree that holds work, then posts the one comment
// that says so. The comment is marked done only once Linear accepts it, so a
// failed one is retried and a posted one never repeats.
func keep(ctx context.Context, opts Options, lin linear.Workspace, c cache.Cache, a reconcile.Action) error {
	if a.WorkspaceID != "" {
		if err := opts.Herdr.CloseWorkspace(ctx, a.WorkspaceID); err != nil {
			return fmt.Errorf("while closing herdr workspace %s: %w", a.WorkspaceID, err)
		}
	}
	if a.Text != "" {
		if err := lin.Comment(ctx, a.Ticket.ID, a.Text); err != nil {
			return fmt.Errorf("while commenting on %s: %w", a.Ticket.Identifier, err)
		}
		e := c[a.Ticket.Identifier]
		e.TeardownWarned = true
		c[a.Ticket.Identifier] = e
	}
	_, _ = fmt.Fprintln(opts.Stdout, a)
	return nil
}
