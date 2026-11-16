package loop

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/history"
	"github.com/kapetan-io/dispatch/internal/rules"
	"github.com/kapetan-io/dispatch/internal/ticket"
	"github.com/kapetan-io/dispatch/internal/treekey"
	"github.com/kapetan-io/dispatch/internal/workers"
	"github.com/kapetan-io/dispatch/internal/world"
)

// leaseKey names the lease an action takes or gives back: a launch's ticket or a return's tree.
// Any other action touches no lease and has none.
func leaseKey(w rules.Want) string {
	switch w.Receipt {
	case "launch":
		return "ticket/" + strings.ToUpper(w.Subject)
	case "return":
		return "tree/" + treekey.Of(w.Target["path"])
	}
	return ""
}

// leaseBusy reports whether a launch of the ticket or a return of the tree is running, or ended
// while the last read was out. That read's pool may predate what the action did to the lease, so
// the resync leaves the row and the lease to the read that follows the action.
func (l *Loop) leaseBusy(ticket, tree string) bool {
	keys := []string{"ticket/" + strings.ToUpper(ticket)}
	if tree != "" {
		keys = append(keys, "tree/"+treekey.Of(tree))
	}
	for _, key := range keys {
		if l.settledDuring[key] {
			return true
		}
	}
	for _, f := range l.flights {
		if slices.Contains(keys, leaseKey(f.want)) {
			return true
		}
	}
	return false
}

// untracked reports whether the ticket is known to be untracked: its ticket system was read and
// did not list it. A ticket whose system could not be read is not known to be finished.
func (l *Loop) untracked(id string) bool {
	failed := l.wd.LinearErr
	if ticket.SystemOf(id) == ticket.SystemLocal {
		failed = l.wd.LocalErr
	}
	return failed == nil && !slices.ContainsFunc(l.wd.Tickets, func(t ticket.Ticket) bool {
		return strings.EqualFold(t.Identifier, id)
	})
}

// resyncWorkers makes the worker rows agree with the pools the read that just landed showed, and
// rebuilds the world's workers when a row changed, so the cycle that follows decides from the
// rows as they are. A repo whose pool was not read is left out, so no row changes because of a
// failed read. warn is true for a read of every source, which is when a stray lease is named.
func (l *Loop) resyncWorkers(warn bool) error {
	var pools []workers.Pool
	changed := false
	for _, label := range l.o.Config.Repos.Labels() {
		if l.wd.UnreadRepos[label] {
			continue
		}
		var leases []workers.Lease
		for _, t := range l.wd.Trees {
			if t.RepoDir == l.o.Config.Repos[label].Path {
				leases = append(leases, workers.Lease{Tree: t.Path, Holder: t.LeaseHolder})
			}
		}
		pool := workers.ReadPool(label, leases)
		imported, err := l.importWorkers(pool)
		if err != nil {
			return err
		}
		changed = changed || imported
		pools = append(pools, pool)
	}
	found, err := l.o.Workers.Resync(pools, l.leaseBusy, l.untracked, l.o.Config.Repos.Labels())
	if err != nil {
		// The rows stay as they were, and the next read tries again.
		_, _ = fmt.Fprintf(l.o.Err, "%s dispatch: while resyncing the worker rows: %v\n", l.clock.Now().Format(time.TimeOnly), err)
		return nil
	}
	for _, gone := range found.Gone {
		data, _ := json.Marshal(gone)
		event := history.Event{
			Line: history.LineEvent, Ingest: l.ingest(), Source: "dispatch", Type: history.EventWorkerGone, Subject: gone.Ticket, Data: data,
		}
		if err := l.write(event); err != nil {
			return err
		}
	}
	if warn {
		for _, stray := range found.Strays {
			_, _ = fmt.Fprintf(l.o.Err, "dispatch: %s holds tree %s in %s with no worker record; dispatch will not return it. A launch of %s uses it, or return it with treehouse.\n",
				stray.Ticket, stray.Tree, stray.Repo, stray.Ticket)
		}
	}
	if changed || found.Changed {
		l.wd = world.Rejoin(l.o.Sources, l.scope, l.wd)
	}
	return nil
}

// importWorkers gives a repo's running workers their rows the first time its pool is read, from
// the files an earlier dispatch kept, and then deletes those files. A failure leaves the repo
// not imported, which holds its launches, and the next read tries again.
func (l *Loop) importWorkers(pool workers.Pool) (bool, error) {
	done, err := l.o.Workers.Imported(pool.Repo())
	if err == nil && !done {
		err = l.o.Workers.Import(pool, l.o.StateDir, l.clock.Now())
	}
	if err != nil {
		_, _ = fmt.Fprintf(l.o.Err, "%s dispatch: while importing the workers of %s: %v\n", l.clock.Now().Format(time.TimeOnly), pool.Repo(), err)
		return false, nil
	}
	// The files go at every read until none is left, so a start killed after the import still removes them.
	workers.RemoveOldFiles(l.o.StateDir, pool)
	return !done, nil
}
