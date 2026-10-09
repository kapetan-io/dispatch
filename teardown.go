package dispatch

import (
	"context"
	"fmt"
	"slices"

	"github.com/kapetan-io/dispatch/internal/cache"
	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/reconcile"
	"github.com/kapetan-io/dispatch/internal/treehouse"
	"github.com/kapetan-io/dispatch/internal/worker"
)

// leasedTickets reads the tickets that hold a tree but are not among the
// managed ones, which is how finished and paused tickets are found. It makes
// no call when every lease belongs to a managed ticket.
func leasedTickets(ctx context.Context, lin linear.Client, cfg config.Config, trees []treehouse.Tree, managed []linear.Ticket) ([]linear.Ticket, error) {
	var holders []string
	for _, tr := range trees {
		if tr.LeaseHolder == "" || slices.Contains(holders, tr.LeaseHolder) {
			continue
		}
		if !slices.ContainsFunc(managed, func(t linear.Ticket) bool { return t.Identifier == tr.LeaseHolder }) {
			holders = append(holders, tr.LeaseHolder)
		}
	}
	if len(holders) == 0 {
		return nil, nil
	}
	return lin.LeasedTickets(ctx, cfg.Linear.AILabel, cfg.Linear.Teams, holders)
}

// inspectTrees reads the git state of each tree Plan may return. An unreadable
// tree is reported and kept leased.
func inspectTrees(ctx context.Context, in reconcile.Input) (unfinished map[string]string, failed map[string]error) {
	unfinished, failed = map[string]string{}, map[string]error{}
	for _, t := range in.Leased {
		if !in.Retires(t) {
			continue
		}
		for _, w := range in.Leases[t.Identifier] {
			why, err := treehouse.Unfinished(ctx, w.TreePath)
			if err != nil {
				failed[w.TreePath] = err
				continue
			}
			unfinished[w.TreePath] = why
		}
	}
	return unfinished, failed
}

// heldFinished lists the finished tickets that still hold a lease.
func heldFinished(in reconcile.Input) map[string]bool {
	held := map[string]bool{}
	for _, t := range in.Leased {
		if slices.Contains(in.Done, t.Status) && len(in.Leases[t.Identifier]) > 0 {
			held[t.Identifier] = true
		}
	}
	return held
}

// teardown closes a finished ticket's workspace and returns its tree. The
// tree is read again once the workspace is closed, because the worker could
// have written to it since the plan was made. Work found then keeps the tree,
// and the next tick's plan comments on it.
func teardown(ctx context.Context, opts Options, a reconcile.Action) error {
	if a.WorkspaceID != "" {
		if err := herdr.CloseWorkspace(ctx, opts.Herdr, a.WorkspaceID); err != nil {
			return err
		}
	}
	why, err := treehouse.Unfinished(ctx, a.TreePath)
	if err != nil {
		return err
	}
	if why != "" {
		_, _ = fmt.Fprintf(opts.Stdout, "keep %s: the worker left %s in %s\n", a.Ticket.Identifier, why, a.TreePath)
		return nil
	}
	if err := treehouse.Return(ctx, opts.Treehouse, a.RepoDir, a.TreePath, a.Ticket.Identifier); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(opts.Stdout, a)
	return nil
}

// keep stops the worker on a tree that holds work, then posts the one comment
// that says so. The comment is marked done only once Linear accepts it, so a
// failed one is retried and a posted one never repeats.
func keep(ctx context.Context, opts Options, lin linear.Client, c cache.Cache, a reconcile.Action) error {
	if a.WorkspaceID != "" {
		if err := herdr.CloseWorkspace(ctx, opts.Herdr, a.WorkspaceID); err != nil {
			return err
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

// leaseWorkers finds a worker for each tree each leased ticket holds.
func leaseWorkers(leased []linear.Ticket, trees []treehouse.Tree, snap herdr.Snapshot) map[string][]worker.Worker {
	leases := map[string][]worker.Worker{}
	for _, t := range leased {
		leases[t.Identifier] = worker.FindAll(t.Identifier, trees, snap)
	}
	return leases
}
