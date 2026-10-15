// Package world reads what Linear, herdr, treehouse, GitHub and the Stop
// records say right now, and joins it into the facts the dispatcher decides
// from. A read that fails or comes back cut short is recorded as unread, never
// as empty, so nothing downstream mistakes "couldn't look" for "nothing there".
package world

import (
	"context"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/pullrequest"
	"github.com/kapetan-io/dispatch/internal/stops"
	"github.com/kapetan-io/dispatch/internal/treehouse"
)

// Sources are the tools one read goes through.
type Sources struct {
	Linear       linear.Workspace
	Herdr        herdr.Herdr
	Treehouse    treehouse.Pool
	PullRequests pullrequest.Repository
	// StateDir holds the Stop records.
	StateDir string
	// Stderr receives one line for each read that failed without stopping the read.
	Stderr io.Writer
	// Tolerant keeps going when herdr, or every pool, can't be read, and
	// records them as unread. Without it Read returns that failure, which is
	// what a tick that acts must do.
	Tolerant bool
	// Resilient records a failed Linear read in World.LinearErr and goes on,
	// where without it Read returns that failure.
	Resilient bool
}

// Scope is the part of the config a read depends on.
type Scope struct {
	Statuses       config.Statuses
	AILabel        string
	Teams          []string
	RepoLabelGroup string
	Repos          map[string]config.Repo
}

// ScopeOf is the scope a config sets.
func ScopeOf(cfg config.Config) Scope {
	return Scope{
		Statuses:       cfg.Plugins.Linear.Statuses,
		AILabel:        cfg.Plugins.Linear.AILabel,
		Teams:          cfg.Plugins.Linear.Teams,
		RepoLabelGroup: cfg.Plugins.Linear.RepoLabelGroup,
		Repos:          cfg.Repos,
	}
}

// World is one read of everything, joined. Every field that can fail to be read
// has a sibling that says so.
type World struct {
	// At is when the read started.
	At time.Time
	// Tickets are the managed tickets: started, assigned to you and carrying the opt-in label.
	// When LinearErr is set they are the last ones read, and no longer known.
	Tickets   []linear.Ticket
	LinearErr error
	// Snapshot is herdr's view. It is empty when HerdrErr is set.
	Snapshot herdr.Snapshot
	HerdrErr error
	// Trees are the trees of every pool that was read. PoolErrs holds the repo
	// labels whose pool wasn't, and so whose tickets' workers are unknown.
	Trees    []treehouse.Tree
	PoolErrs map[string]error
	// Workers maps a ticket identifier to what the lookup found.
	Workers map[string]Worker
	// Stops maps an In Progress ticket identifier to its idle worker's latest Stop.
	// Tickets without a record are absent.
	Stops map[string]stops.Record
	// LeaseHolders holds the tickets that hold a lease and aren't in Tickets,
	// which is how finished and paused tickets are found. HoldersErr is set when
	// the lookup failed, so who holds a tree is unknown.
	LeaseHolders []linear.Ticket
	HoldersErr   error
	// Leases maps a lease holder to a worker for each tree it holds.
	Leases map[string][]Worker
	// Leased holds every ticket identifier that holds a treehouse lease.
	Leased map[string]bool
	// Children maps an In Progress parent's identifier to all its children, in
	// every status. A parent absent from the map had its children unread, and
	// ChildrenErr says why.
	Children    map[string][]linear.Ticket
	ChildrenErr error
	// Unfinished maps a tree path to what returning it would destroy, empty for
	// a pushed tree with no tracked changes. A path in neither map was not
	// inspected. CheckErrors holds the trees that could not be read, and
	// Untracked the untracked files in each tree.
	Unfinished  map[string]string
	Work        map[string]treehouse.Work
	CheckErrors map[string]error
	Untracked   map[string][]string
	// PRs maps an In Review ticket identifier to its open PR. Tickets whose PR
	// was not read, or that have none, are absent. PRReadFailed holds the ones
	// whose read errored, as opposed to finding no open PR.
	PRs          map[string]pullrequest.PullRequest
	PRReadFailed map[string]bool
	// UnreadRepos are the repo labels whose pool could not be read.
	UnreadRepos map[string]bool
	// Nudged holds the tickets whose worker was typed into since the last full
	// read. They show as working until the next one, so nothing that waits for an
	// idle worker acts on a worker that is already busy with the nudge.
	Nudged map[string]bool
}

// Part names the sources one read goes through.
type Part uint

const (
	// FromLinear reads the tracked tickets, who holds the trees and the children of open parents.
	FromLinear Part = 1 << iota
	// FromLocal reads herdr, every pool and the Stop records.
	FromLocal
	// FromGitHub reads the PR of each ticket that has one to read.
	FromGitHub
	// Everything reads every source.
	Everything = FromLinear | FromLocal | FromGitHub
)

// Read takes one reading of the world. The failures that leave nothing to
// decide from, which are Linear's tickets and, unless Tolerant, herdr and
// every pool, are returned. The others are recorded in the World.
func Read(ctx context.Context, src Sources, scope Scope, now time.Time) (World, error) {
	return Refresh(ctx, src, scope, World{}, Everything, now)
}

// Refresh reads the sources in parts again and keeps what prev says about the
// rest, so a launch can be followed by a look at herdr and treehouse that makes
// no Linear call. A failed read of one source never empties what another source
// said. With Resilient, a failed Linear read is recorded in LinearErr and the
// tickets prev held are kept, so nothing downstream mistakes the failure for
// "no tickets".
func Refresh(ctx context.Context, src Sources, scope Scope, prev World, parts Part, now time.Time) (World, error) {
	w := prev
	w.At = now
	if parts&FromLocal != 0 || prev.At.IsZero() {
		parts |= FromLocal
		w.HerdrErr, w.PoolErrs, w.UnreadRepos, w.Trees = nil, map[string]error{}, map[string]bool{}, nil
		snap, err := src.Herdr.Read(ctx)
		if err != nil {
			if !src.Tolerant {
				return w, err
			}
			w.HerdrErr = err
		}
		w.Snapshot = snap
		readErrs := map[string]error{}
		for label, repo := range scope.Repos {
			trees, err := src.Treehouse.Trees(ctx, repo.Path)
			if err != nil {
				readErrs[label] = treehouse.StatusError(repo.Path, err)
				continue
			}
			w.Trees = append(w.Trees, trees...)
		}
		// A repo whose pool can't be read holds back only its own tickets, so one
		// moved clone doesn't stop launches in every other repo.
		for label, err := range readErrs {
			if len(readErrs) == len(scope.Repos) && !src.Tolerant {
				return w, err
			}
			if len(readErrs) != len(scope.Repos) {
				_, _ = fmt.Fprintf(src.Stderr, "dispatch: while reading the %s pool: %v\n", label, err)
			}
			w.PoolErrs[label] = err
			w.UnreadRepos[label] = true
		}
	}
	if parts&FromLinear != 0 || prev.At.IsZero() {
		parts |= FromLinear
		tickets, err := src.Linear.ManagedTickets(ctx, scope.AILabel, scope.Teams)
		switch {
		case err == nil:
			w.Tickets, w.LinearErr = tickets, nil
		case src.Resilient:
			w.LinearErr = err
			_, _ = fmt.Fprintf(src.Stderr, "dispatch: while reading the tickets: %v\n", err)
		default:
			return w, err
		}
	}
	if parts&(FromLinear|FromLocal) != 0 {
		// A failed lease lookup skips teardown only; launches and nudges still run.
		if parts&FromLinear != 0 && w.LinearErr == nil {
			w.LeaseHolders, w.HoldersErr = leaseHolders(ctx, src.Linear, scope, w.Trees, w.Tickets)
			if w.HoldersErr != nil {
				_, _ = fmt.Fprintf(src.Stderr, "dispatch: while reading the tickets that hold trees: %v\n", w.HoldersErr)
			}
		} else if parts&FromLinear != 0 {
			w.HoldersErr = w.LinearErr
		}
		w.Leased = map[string]bool{}
		for _, tree := range w.Trees {
			if tree.LeaseHolder != "" {
				w.Leased[tree.LeaseHolder] = true
			}
		}
		snap := w.Snapshot
		w.Workers, w.Stops = map[string]Worker{}, map[string]stops.Record{}
		for _, t := range w.Tickets {
			wk := FindWorker(t.Identifier, w.Trees, snap)
			w.Workers[t.Identifier] = wk
			if wk.State == Idle && t.Status == scope.Statuses.InProgress {
				if rec, ok := stops.Read(src.StateDir, wk.TreePath, wk.Session); ok {
					w.Stops[t.Identifier] = rec
				}
			}
		}
		w.Leases = map[string][]Worker{}
		for _, t := range w.LeaseHolders {
			w.Leases[t.Identifier] = FindWorkers(t.Identifier, w.Trees, snap)
		}
	}
	if parts&FromLinear != 0 && w.LinearErr == nil {
		// Only promotion and closing wait on the children, so a failed read holds
		// back those two and nothing else.
		w.Children, w.ChildrenErr = nil, nil
		if parents := scope.Parents(w.Tickets); len(parents) > 0 {
			ids := make([]string, len(parents))
			for i, parent := range parents {
				ids[i] = parent.ID
			}
			if w.Children, w.ChildrenErr = src.Linear.ParentChildren(ctx, ids); w.ChildrenErr != nil {
				_, _ = fmt.Fprintf(src.Stderr, "dispatch: while reading the children of open parents: %v\n", w.ChildrenErr)
			}
		}
	}
	if parts&(FromLinear|FromLocal) != 0 {
		w.inspectTrees(ctx, scope)
	}
	if parts&FromGitHub != 0 || prev.At.IsZero() {
		w.readPRs(ctx, src, scope)
	}
	return w, nil
}

// leaseHolders reads the tickets that hold a tree but are not among the
// managed ones. It makes no call when every lease belongs to a managed ticket.
func leaseHolders(ctx context.Context, lin linear.Workspace, scope Scope, trees []treehouse.Tree, managed []linear.Ticket) ([]linear.Ticket, error) {
	var holders []string
	for _, tree := range trees {
		if tree.LeaseHolder == "" || slices.Contains(holders, tree.LeaseHolder) {
			continue
		}
		if !slices.ContainsFunc(managed, func(t linear.Ticket) bool { return t.Identifier == tree.LeaseHolder }) {
			holders = append(holders, tree.LeaseHolder)
		}
	}
	if len(holders) == 0 {
		return nil, nil
	}
	return lin.LeasedTickets(ctx, scope.AILabel, scope.Teams, holders)
}

// inspectTrees reads the git state of each tree of a finished ticket. An
// unreadable tree is recorded and kept leased.
func (w *World) inspectTrees(ctx context.Context, scope Scope) {
	w.Unfinished, w.CheckErrors, w.Untracked = map[string]string{}, map[string]error{}, map[string][]string{}
	w.Work = map[string]treehouse.Work{}
	for _, t := range w.LeaseHolders {
		if !scope.Retires(t) {
			continue
		}
		for _, wk := range w.Leases[t.Identifier] {
			work, err := treehouse.Inspect(ctx, wk.TreePath)
			if err != nil {
				w.CheckErrors[wk.TreePath] = err
				continue
			}
			files, err := treehouse.Untracked(ctx, wk.TreePath)
			if err != nil {
				w.CheckErrors[wk.TreePath] = err
				continue
			}
			w.Unfinished[wk.TreePath], w.Untracked[wk.TreePath], w.Work[wk.TreePath] = work.String(), files, work
		}
	}
}

// readPRs reads the open PR of each ticket for which ReadsPR is true. A PR that
// can't be read is reported and left out, so it holds back only its own ticket.
func (w *World) readPRs(ctx context.Context, src Sources, scope Scope) {
	w.PRs, w.PRReadFailed = map[string]pullrequest.PullRequest{}, map[string]bool{}
	for _, t := range w.Tickets {
		if !scope.ReadsPR(t, w.Workers[t.Identifier].State) {
			continue
		}
		repo, ok := t.LabelIn(scope.RepoLabelGroup)
		dir := scope.Repos[repo].Path
		if !ok || dir == "" {
			continue
		}
		settings := scope.Repos[repo]
		pr, found, err := src.PullRequests.Read(ctx, dir, t.Branch, pullrequest.Rules{IgnoreAuthors: settings.IgnoreCommentAuthors, RequiredChecksOnly: settings.IgnoreOptionalChecks})
		if err != nil {
			_, _ = fmt.Fprintf(src.Stderr, "dispatch: while reading the PR for %s: %v\n", t.Identifier, err)
			w.PRReadFailed[t.Identifier] = true
			continue
		}
		if found {
			w.PRs[t.Identifier] = pr
		}
	}
}

// DesignLabels mark tickets whose sessions the dispatcher never nudges.
var DesignLabels = []string{"design", "design-review"}

// NotDesign reports whether the ticket is known not to be a design or review
// ticket. A ticket whose labels were cut short might be one.
func NotDesign(t linear.Ticket) bool {
	return !t.LabelsTruncated && !slices.ContainsFunc(DesignLabels, t.HasLabel)
}

// Retires reports whether the dispatcher tears down this lease holder's trees:
// its status is a finished one.
func (s Scope) Retires(t linear.Ticket) bool {
	return slices.Contains(s.Statuses.Finished, t.Status)
}

// Parents returns the tickets whose children the dispatcher acts on: In
// Progress parents that are known not to be design tickets, since the design
// worker and the review own the children and close of those.
func (s Scope) Parents(tickets []linear.Ticket) []linear.Ticket {
	var parents []linear.Ticket
	for _, t := range tickets {
		if t.Status == s.Statuses.InProgress && t.HasChildren && NotDesign(t) {
			parents = append(parents, t)
		}
	}
	return parents
}

// ReadsPR reports whether the dispatcher uses this In Review ticket's PR.
// Working and blocked workers are skipped so their PRs cost no call, and so are
// tickets whose labels or children make it unsure they are build tickets.
func (s Scope) ReadsPR(t linear.Ticket, state WorkerState) bool {
	if t.Status != s.Statuses.InReview || t.LabelsTruncated || t.HasChildren || !NotDesign(t) {
		return false
	}
	return state == NoWorker || state == Idle
}
