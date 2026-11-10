// Package world reads what Linear, herdr, treehouse, GitHub and the Stop
// records say right now, and joins it into the facts the dispatcher decides
// from. A read that fails or comes back cut short is recorded as unread, never
// as empty, so nothing downstream mistakes "couldn't look" for "nothing there".
package world

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"time"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/gitremote"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/inbox"
	"github.com/kapetan-io/dispatch/internal/kickoffs"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/localticket"
	"github.com/kapetan-io/dispatch/internal/pullrequest"
	"github.com/kapetan-io/dispatch/internal/sessionlog"
	"github.com/kapetan-io/dispatch/internal/stackrecord"
	"github.com/kapetan-io/dispatch/internal/stops"
	"github.com/kapetan-io/dispatch/internal/ticket"
	"github.com/kapetan-io/dispatch/internal/treehouse"
)

// Sources are the tools one read goes through.
type Sources struct {
	Linear       linear.Workspace
	Herdr        herdr.Herdr
	Treehouse    treehouse.Pool
	PullRequests pullrequest.Repository
	// Inbox is the owner's GitHub inbox. Nil means no read is made.
	Inbox inbox.Inbox
	// Env is what the git commands that inspect a tree run with.
	Env environment.Environment
	// StateDir holds the Stop records and session logs.
	StateDir string
	// Stderr receives one line for each read that failed without stopping the read.
	Stderr io.Writer
	// Tolerant keeps going when herdr, or every pool, can't be read, and
	// records them as unread. Without it the read returns that failure.
	Tolerant bool
	// Resilient records a failed Linear read in World.LinearErr and goes on,
	// where without it Read returns that failure.
	Resilient bool
	// LocalOn is true when local tickets are on, so a Linear read that returns a LOC- id fails.
	LocalOn bool
	// Local is where the local tickets are read from, nil when no database is open or read.
	Local LocalTickets
}

// LocalTickets is the local ticket database, as the open file or as a Snapshot of it.
type LocalTickets interface {
	Statuses() ticket.Statuses
	Tracked(holders []string, wantChildren func(ticket.Ticket) bool) (localticket.Tracked, error)
}

// Scope is the part of the config a read depends on.
type Scope struct {
	// Linear and Local are the statuses of each ticket system. Local is empty while local tickets are off.
	Linear         ticket.Statuses
	Local          ticket.Statuses
	AILabel        string
	Teams          []string
	RepoLabelGroup string
	Repos          config.Repos
}

// ScopeOf is the scope a config sets. Its Local statuses are empty, so ask World.Scope for any
// question about a ticket's status.
func ScopeOf(cfg config.Config) Scope {
	return Scope{
		Linear:         cfg.Plugins.Linear.Statuses.Table(),
		AILabel:        cfg.Plugins.Linear.AILabel,
		Teams:          cfg.Plugins.Linear.Teams,
		RepoLabelGroup: cfg.Plugins.Linear.RepoLabelGroup,
		Repos:          cfg.Repos,
	}
}

// Scope is the scope a config sets, with the local statuses this read found.
func (w World) Scope(cfg config.Config) Scope {
	s := ScopeOf(cfg)
	s.Local = w.LocalStatuses
	return s
}

// StatusesOf is the statuses of the ticket system t belongs to.
func (s Scope) StatusesOf(t ticket.Ticket) ticket.Statuses {
	return s.StatusesOfID(t.Identifier)
}

// StatusesOfID is the statuses of the ticket system the id belongs to.
func (s Scope) StatusesOfID(id string) ticket.Statuses {
	if ticket.SystemOf(id) == ticket.SystemLocal {
		return s.Local
	}
	return s.Linear
}

// Is is true when t's status does job in t's own ticket system.
func (s Scope) Is(t ticket.Ticket, job ticket.Job) bool {
	return s.StatusesOf(t).Has(t.Status, job)
}

// RepoOf is the repo label of t: a local ticket's own repo, and for a Linear ticket the label in the
// repo label group, resolved to the spelling of its [repos.<label>] table when it has one.
func (s Scope) RepoOf(t ticket.Ticket) (string, bool) {
	label, ok := t.Repo, t.Repo != ""
	if ticket.SystemOf(t.Identifier) != ticket.SystemLocal {
		label, ok = t.LabelIn(s.RepoLabelGroup)
	}
	if name, _, found := s.Repos.Find(label); found {
		label = name
	}
	return label, ok
}

// World is one read of everything, joined. Every field that can fail to be read
// has a sibling that says so. A new field must also be added to worldRecord in json.go,
// which is the form history writes.
type World struct {
	// At is when the read started.
	At time.Time
	// Tickets are the tracked tickets of both ticket systems: Linear tickets that are started,
	// assigned to you and carrying the opt-in label, and every local ticket outside a finished
	// status. When a system's own error (LinearErr or LocalErr) is set, its tickets are the last
	// ones read, and no longer known.
	Tickets   []ticket.Ticket
	LinearErr error
	// LocalStatuses are the statuses of the local ticket database, empty while local tickets are off.
	// LocalErr is set when the last read of the local tickets failed, so those records are unknown.
	LocalStatuses ticket.Statuses
	LocalErr      error
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
	// Sessions maps an In Progress or In Review ticket identifier to the log of its
	// worker's current session. SessionErrs holds the tickets whose log could not
	// be read, which is unknown, not empty. A ticket in neither map has no worker or is
	// not In Progress or In Review.
	Sessions    map[string]sessionlog.Log
	SessionErrs map[string]error
	// LeaseHolders holds the tickets that hold a lease and aren't in Tickets,
	// which is how finished and paused tickets are found. HoldersErr is set when
	// the lookup failed, so who holds a tree is unknown.
	LeaseHolders []ticket.Ticket
	HoldersErr   error
	// Leases maps a lease holder to a worker for each tree it holds.
	Leases map[string][]Worker
	// Leased holds every ticket identifier that holds a treehouse lease.
	Leased map[string]bool
	// Children maps an In Progress parent's identifier to all its children, in
	// every status. A parent absent from the map had its children unread, and
	// ChildrenErr says why.
	Children    map[string][]ticket.Ticket
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
	// PRRateLimited holds the tickets in PRReadFailed whose read GitHub refused because the
	// account's budget is spent, with the time GitHub says it refills, zero when it gives none.
	PRRateLimited map[string]time.Time
	// PRNotFound maps a ticket whose last PR read found no open PR to the branch it asked for.
	PRNotFound map[string]string
	// PRReadsAnswered counts the PR reads GitHub answered, so a read that asked it nothing is not
	// taken for proof the budget is back.
	PRReadsAnswered int
	// MergedPRs maps a local ticket's identifier to the newest PR from its repo that merged
	// into the default branch from its branch. A ticket the merged read covers with no such
	// PR is absent. A failed merged read puts the tickets it covered in PRReadFailed, and in PRRateLimited when GitHub refused it for a spent budget.
	MergedPRs map[string]pullrequest.MergedPR
	// UnreadRepos are the repo labels whose pool could not be read.
	UnreadRepos map[string]bool
	// StackRecords maps a repo label to the stack records of its clone. StackErrs holds the
	// labels whose records could not be read, which is unknown, not empty. A refresh that skips
	// the local read keeps the earlier ones.
	StackRecords map[string]stackrecord.Records
	StackErrs    map[string]error
	// Nudged holds the tickets whose worker was sent a nudge. They show as working until a read
	// sees the worker working or blocked, or the next resync, so nothing that waits for an idle
	// worker acts on a worker that is already busy with the nudge.
	Nudged map[string]bool
	// Inbox holds the items of the last good read of the owner's GitHub inbox, newest 50 by
	// UpdatedAt. When InboxErr is set the read failed and these are still the last good ones,
	// as is InboxReadAt, because nothing else points at an inbox item. InboxReadAt is zero
	// until a read has succeeded.
	Inbox       []inbox.Item
	InboxErr    error
	InboxReadAt time.Time
	// Remotes maps a repo label to the owner/name its clone's chosen remote points at. A label is
	// absent when the remote is missing or not on GitHub. A failed read of one clone leaves its last
	// good value and sets RemoteErrs[label]. Read only while Sources.Inbox is set.
	Remotes    map[string]string
	RemoteErrs map[string]error
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
	// FromLocalTickets reads the local tickets, who holds the trees among them and the children of open
	// local parents. It runs whenever FromLocal does, because the lease holders come from the trees.
	FromLocalTickets
	// FromInbox reads the owner's GitHub inbox. It is its own part so the Refresh after a worker goes
	// idle or an action finishes makes no gh call. Only Everything, which start and resync read, includes it.
	FromInbox
	// Everything reads every source.
	Everything = FromLinear | FromLocal | FromLocalTickets | FromGitHub | FromInbox
)

// Read takes one reading of the world. The failures that leave nothing to
// decide from, which are Linear's tickets and, unless Tolerant, herdr and
// every pool, are returned. The others are recorded in the World.
func Read(ctx context.Context, src Sources, scope Scope, now time.Time) (World, error) {
	return Refresh(ctx, src, scope, World{}, Everything, now)
}

// ReadDeadline bounds each read of one source, so a slow read cannot spend the time of the
// reads after it.
const ReadDeadline = time.Minute

func bound(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, ReadDeadline)
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
	if src.Local != nil {
		scope.Local = src.Local.Statuses()
		w.LocalStatuses = scope.Local
	}
	if parts&FromLocal != 0 || prev.At.IsZero() {
		parts |= FromLocal | FromLocalTickets
		w.HerdrErr, w.PoolErrs, w.UnreadRepos, w.Trees = nil, map[string]error{}, map[string]bool{}, nil
		w.StackRecords, w.StackErrs = map[string]stackrecord.Records{}, map[string]error{}
		readCtx, done := bound(ctx)
		snap, err := src.Herdr.Read(readCtx)
		done()
		if err != nil {
			if !src.Tolerant {
				return w, err
			}
			w.HerdrErr = err
		}
		w.Snapshot = snap
		readErrs := map[string]error{}
		for label, repo := range scope.Repos {
			readCtx, done := bound(ctx)
			trees, err := src.Treehouse.Trees(readCtx, repo.Path)
			done()
			if err != nil {
				readErrs[label] = treehouse.StatusError(repo.Path, err)
				continue
			}
			w.Trees = append(w.Trees, trees...)
		}
		for label, repo := range scope.Repos {
			readCtx, done := bound(ctx)
			recs, err := stackrecord.Read(readCtx, src.Env, repo.Path)
			done()
			if err != nil {
				w.StackErrs[label] = err
				continue
			}
			w.StackRecords[label] = recs
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
		readCtx, done := bound(ctx)
		tickets, err := src.Linear.ManagedTickets(readCtx, scope.AILabel, scope.Teams)
		done()
		if err == nil && src.LocalOn {
			err = clash(tickets...)
		}
		switch {
		case err == nil:
			w.Tickets, w.LinearErr = replaceLinear(src, w.Tickets, tickets), nil
		case src.Resilient:
			w.LinearErr = err
			_, _ = fmt.Fprintf(src.Stderr, "dispatch: while reading the tickets: %v\n", err)
		default:
			return w, err
		}
	}
	if parts&FromLocalTickets != 0 || prev.At.IsZero() {
		parts |= FromLocalTickets
		w.readLocal(src, scope)
	}
	if parts&(FromLinear|FromLocal|FromLocalTickets) != 0 {
		w.onTicketBranches(scope)
		// A failed lease lookup skips teardown only; launches and nudges still run.
		if parts&FromLinear != 0 && w.LinearErr == nil {
			readCtx, done := bound(ctx)
			var holders []ticket.Ticket
			holders, w.HoldersErr = leaseHolders(readCtx, src.Linear, scope, w.Trees, w.Tickets)
			done()
			if w.HoldersErr == nil && src.LocalOn {
				w.HoldersErr = clash(holders...)
			}
			// A failed lookup leaves no Linear holders, so a tree reads unread rather than deciding
			// from holders an earlier lookup found. Local holders come from the local read.
			w.LeaseHolders = replaceLinear(src, w.LeaseHolders, holders)
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
		w.Workers, w.Stops = map[string]Worker{}, map[string]stops.Record{}
		w.Sessions, w.SessionErrs = map[string]sessionlog.Log{}, map[string]error{}
		for _, t := range w.Tickets {
			w.joinWorker(src, scope, t)
		}
		w.joinLeases()
	}
	if parts&FromLinear != 0 && w.LinearErr == nil {
		// Only promotion and closing wait on the children, so a failed read holds
		// back those two and nothing else.
		w.Children, w.ChildrenErr = localChildren(w.Children), nil
		if parents := scope.Parents(linearOnly(w.Tickets)); len(parents) > 0 {
			ids := make([]string, len(parents))
			for i, parent := range parents {
				ids[i] = parent.ID
			}
			readCtx, done := bound(ctx)
			var kids map[string][]ticket.Ticket
			kids, w.ChildrenErr = src.Linear.ParentChildren(readCtx, ids)
			done()
			if w.ChildrenErr == nil && src.LocalOn {
				for _, list := range kids {
					if w.ChildrenErr = clash(list...); w.ChildrenErr != nil {
						break
					}
				}
			}
			if w.ChildrenErr == nil {
				if w.Children == nil {
					w.Children = map[string][]ticket.Ticket{}
				}
				maps.Copy(w.Children, kids)
			}
			if w.ChildrenErr != nil {
				_, _ = fmt.Fprintf(src.Stderr, "dispatch: while reading the children of open parents: %v\n", w.ChildrenErr)
			}
		}
	}
	if parts&(FromLinear|FromLocal|FromLocalTickets) != 0 {
		w.inspectTrees(ctx, src.Env, scope)
	}
	if parts&FromGitHub != 0 || prev.At.IsZero() {
		w.readPRs(ctx, src, scope)
		w.readMergedPRs(ctx, src, scope)
	}
	if parts&FromInbox != 0 || prev.At.IsZero() {
		w.readInbox(ctx, src, scope, now)
	}
	return w, nil
}

// joinWorker builds the ticket's worker from the trees and the snapshot, with its Stop record and
// session log, so a full read and a pane read build a worker one way.
func (w *World) joinWorker(src Sources, scope Scope, t ticket.Ticket) {
	wk := FindWorker(t.Identifier, w.Trees, w.Snapshot)
	if at, ok := kickoffs.Since(src.StateDir, wk.TreePath); ok && wk.State != NoWorker && !kickoffRan(src.StateDir, wk, at) {
		wk.UnconfirmedSince = at.UnixMilli()
		// A Claude stuck at a startup dialog after its launcher died reports no status.
		// Reading it idle lets close-unconfirmed-worker recover it.
		if wk.noStatus {
			wk.State = Idle
		}
	}
	w.Workers[t.Identifier] = wk
	if wk.State == Idle && scope.Is(t, ticket.InProgress) {
		if rec, ok := stops.Read(src.StateDir, wk.TreePath, wk.Session); ok {
			w.Stops[t.Identifier] = rec
		}
	}
	if wk.State != NoWorker && (scope.Is(t, ticket.InProgress) || scope.Is(t, ticket.InReview)) {
		w.readSession(src.StateDir, t.Identifier, wk)
	}
}

// copied is a new map holding m's entries, empty and not nil for a nil m, as a full read leaves them.
func copied[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	maps.Copy(out, m)
	return out
}

// joinLeases builds the workers of every lease holder that is not tracked.
func (w *World) joinLeases() {
	w.Leases = map[string][]Worker{}
	for _, t := range w.LeaseHolders {
		w.Leases[t.Identifier] = FindWorkers(t.Identifier, w.Trees, w.Snapshot)
	}
}

// ReadPanes reads the agents in the named panes alone and rebuilds only the workers on them. It
// reports false when it can't, which is any pane whose answer is not the same agent it held before:
// the agent came or went, changed kind or session, or the multiplexer failed. It then changes
// nothing, and the caller reads the multiplexer in full, which settles what happened.
func ReadPanes(ctx context.Context, src Sources, scope Scope, prev World, panes []string, at time.Time) (World, bool) {
	held := map[string]herdr.Agent{}
	for _, a := range prev.Snapshot.Agents {
		held[a.PaneID] = a
	}
	status := map[string]string{}
	for _, pane := range panes {
		was, ok := held[pane]
		if !ok {
			return prev, false
		}
		readCtx, done := bound(ctx)
		agent, found, err := src.Herdr.AgentAt(readCtx, pane)
		done()
		if err != nil || !found || agent.Kind != was.Kind || agent.Session != was.Session {
			return prev, false
		}
		status[pane] = agent.Status
	}
	w := prev
	w.At = at
	// The previous world may still be in use, so nothing it holds is written to.
	w.Snapshot.Agents = slices.Clone(prev.Snapshot.Agents)
	for i, a := range w.Snapshot.Agents {
		if s, ok := status[a.PaneID]; ok {
			w.Snapshot.Agents[i].Status = s
		}
	}
	w.Workers, w.Stops = copied(prev.Workers), copied(prev.Stops)
	w.Sessions, w.SessionErrs = copied(prev.Sessions), copied(prev.SessionErrs)
	for _, t := range w.Tickets {
		onPanes := slices.ContainsFunc(FindWorkers(t.Identifier, w.Trees, prev.Snapshot), func(wk Worker) bool {
			_, named := status[wk.PaneID]
			return named
		})
		if !onPanes {
			continue
		}
		delete(w.Stops, t.Identifier)
		delete(w.Sessions, t.Identifier)
		delete(w.SessionErrs, t.Identifier)
		w.joinWorker(src, scope, t)
	}
	w.joinLeases()
	return w, true
}

// readSession reads the log of the worker's current session. With no session from herdr
// the log is unknown, never another session's, because a tree holds the logs of every
// session that ran in it.
func (w *World) readSession(stateDir, ticket string, wk Worker) {
	if wk.Session == "" {
		w.SessionErrs[ticket] = errors.New("herdr reports no session for the worker")
		return
	}
	log, err := sessionlog.Read(stateDir, wk.TreePath, wk.Session)
	if err != nil {
		w.SessionErrs[ticket] = err
		return
	}
	w.Sessions[ticket] = log
}

// leaseHolders reads the tickets that hold a tree but are not among the
// managed ones. It makes no call when every lease belongs to a managed ticket.
func leaseHolders(ctx context.Context, lin linear.Workspace, scope Scope, trees []treehouse.Tree, managed []ticket.Ticket) ([]ticket.Ticket, error) {
	var holders []string
	for _, tree := range trees {
		if tree.LeaseHolder == "" || slices.Contains(holders, tree.LeaseHolder) || ticket.SystemOf(tree.LeaseHolder) == ticket.SystemLocal {
			continue
		}
		if !slices.ContainsFunc(managed, func(t ticket.Ticket) bool { return t.Identifier == tree.LeaseHolder }) {
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
func (w *World) inspectTrees(ctx context.Context, env environment.Environment, scope Scope) {
	w.Unfinished, w.CheckErrors, w.Untracked = map[string]string{}, map[string]error{}, map[string][]string{}
	w.Work = map[string]treehouse.Work{}
	for _, t := range w.LeaseHolders {
		if !scope.Retires(t) {
			continue
		}
		for _, wk := range w.Leases[t.Identifier] {
			readCtx, done := bound(ctx)
			work, err := treehouse.Inspect(readCtx, env, wk.TreePath)
			done()
			if err != nil {
				w.CheckErrors[wk.TreePath] = err
				continue
			}
			readCtx, done = bound(ctx)
			files, err := treehouse.Untracked(readCtx, env, wk.TreePath)
			done()
			if err != nil {
				w.CheckErrors[wk.TreePath] = err
				continue
			}
			w.Unfinished[wk.TreePath], w.Untracked[wk.TreePath], w.Work[wk.TreePath] = work.String(), files, work
		}
	}
}

// onTicketBranches is the tickets each on the branch whose ticket branch key names it, since a
// ticket system's read gives every ticket that system's name for the branch, which drifts from the
// branch the worker pushed. A ticket whose repo's keys were not read, or that has no key, keeps the
// ticket system's name.
func (w *World) onTicketBranches(scope Scope) {
	// The previous world may still be in use, so a ticket's Branch changes on a copy.
	w.Tickets = slices.Clone(w.Tickets)
	for i, t := range w.Tickets {
		label, ok := scope.RepoOf(t)
		if !ok {
			continue
		}
		if branch, found := w.StackRecords[label].BranchOf(t.Identifier); found {
			w.Tickets[i].Branch = branch
		}
	}
}

// KeyBranches writes the ticket branch key on the local branch of each tracked ticket that has
// none, which covers tickets launched before the launcher wrote keys. A ticket's branch is the one
// local branch that starts with its id, and with several the one a tree the ticket holds has
// checked out. A branch is never keyed from the tree alone, since a tree can hold another ticket's
// branch. It returns how many keys it wrote, and the caller reads again when that is not zero.
func KeyBranches(ctx context.Context, src Sources, scope Scope, w World) int {
	branches := map[string]map[string]string{}
	var written int
	for _, t := range w.Tickets {
		label, ok := scope.RepoOf(t)
		repo, known := scope.Repos[label]
		recs, read := w.StackRecords[label]
		if !ok || !known || !read {
			continue
		}
		if _, keyed := recs.BranchOf(t.Identifier); keyed {
			continue
		}
		local, listed := branches[label]
		if !listed {
			readCtx, done := bound(ctx)
			var err error
			local, err = stackrecord.LocalBranches(readCtx, src.Env, repo.Path)
			done()
			if err != nil {
				_, _ = fmt.Fprintf(src.Stderr, "dispatch: %v\n", err)
			}
			branches[label] = local
		}
		var named, held []string
		for branch, tree := range local {
			if _, taken := recs.Tickets[branch]; taken || !ticket.OnBranch(t.Identifier, branch) {
				continue
			}
			named = append(named, branch)
			if tree != "" && w.holder(tree) == t.Identifier {
				held = append(held, branch)
			}
		}
		if len(named) > 1 {
			named = held
		}
		if len(named) != 1 {
			continue
		}
		readCtx, done := bound(ctx)
		err := stackrecord.WriteTicket(readCtx, src.Env, repo.Path, named[0], t.Identifier)
		done()
		if err != nil {
			_, _ = fmt.Fprintf(src.Stderr, "dispatch: %v\n", err)
			continue
		}
		// The branch is claimed, so a second ticket whose id this one's extends does not take it.
		recs.Tickets[named[0]] = t.Identifier
		written++
	}
	return written
}

// holder is the ticket that holds the lease on the tree at path, empty when none does.
func (w World) holder(path string) string {
	for _, tree := range w.Trees {
		if tree.Path == path {
			return tree.LeaseHolder
		}
	}
	return ""
}

// readPRs reads the open PR of each ticket for which ReadsPR is true. A PR that
// can't be read is reported and left out, so it holds back only its own ticket.
func (w *World) readPRs(ctx context.Context, src Sources, scope Scope) {
	w.PRReadsAnswered = 0
	w.PRs, w.PRReadFailed, w.PRRateLimited = map[string]pullrequest.PullRequest{}, map[string]bool{}, map[string]time.Time{}
	w.PRNotFound = map[string]string{}
	for _, t := range w.Tickets {
		settings, ok := scope.PRRepo(t, w.Workers[t.Identifier].State)
		if !ok {
			continue
		}
		readCtx, done := bound(ctx)
		pr, found, err := src.PullRequests.Read(readCtx, settings.Path, t.Branch, pullrequest.Rules{IgnoreAuthors: settings.IgnoreCommentAuthors, RequiredChecksOnly: settings.IgnoreOptionalChecks, ApprovalChecks: settings.ApprovalChecks})
		done()
		if err != nil {
			_, _ = fmt.Fprintf(src.Stderr, "dispatch: while reading the PR for %s: %v\n", t.Identifier, err)
			w.failPRRead(t.Identifier, err)
			continue
		}
		w.PRReadsAnswered++
		if found {
			w.PRs[t.Identifier] = pr
		} else {
			w.PRNotFound[t.Identifier] = t.Branch
		}
	}
}

// failPRRead holds the ticket's PR as unreadable, and as rate-limited when GitHub said why.
func (w *World) failPRRead(id string, err error) {
	w.PRReadFailed[id] = true
	var limit *pullrequest.RateLimitError
	if errors.As(err, &limit) {
		w.PRRateLimited[id] = limit.Reset
	}
}

// RateLimit reports whether GitHub refused a PR read for a spent budget, and when it says the
// budget refills, zero when no refused read carried a time.
func (w World) RateLimit() (reset time.Time, limited bool) {
	for _, at := range w.PRRateLimited {
		limited = true
		if at.After(reset) {
			reset = at
		}
	}
	return reset, limited
}

// readMergedPRs reads the merged PR of each local ticket for which MergedRepo is true, one query
// for each batch of a repo's branches. A failed query is reported once and holds back only the
// tickets it covered.
func (w *World) readMergedPRs(ctx context.Context, src Sources, scope Scope) {
	w.MergedPRs = map[string]pullrequest.MergedPR{}
	type repoTickets struct {
		path, label string
		tickets     []ticket.Ticket
	}
	var repos []*repoTickets
	for _, t := range w.Tickets {
		settings, ok := scope.MergedRepo(t)
		if !ok || w.hasPRRead(t.Identifier) {
			continue
		}
		i := slices.IndexFunc(repos, func(r *repoTickets) bool { return r.path == settings.Path })
		if i < 0 {
			label, _ := scope.RepoOf(t)
			repos, i = append(repos, &repoTickets{path: settings.Path, label: label}), len(repos)
		}
		repos[i].tickets = append(repos[i].tickets, t)
	}
	for _, repo := range repos {
		for batch := range slices.Chunk(repo.tickets, pullrequest.MergedBatch) {
			branches := make([]string, len(batch))
			for i, t := range batch {
				branches[i] = t.Branch
			}
			readCtx, done := bound(ctx)
			found, err := src.PullRequests.ReadMerged(readCtx, repo.path, branches)
			done()
			if err != nil {
				_, _ = fmt.Fprintf(src.Stderr, "dispatch: while reading the merged PRs of the %s repo: %v\n", repo.label, err)
				for _, t := range batch {
					w.failPRRead(t.Identifier, err)
				}
				continue
			}
			w.PRReadsAnswered++
			for _, t := range batch {
				if pr, ok := found[t.Branch]; ok {
					w.MergedPRs[t.Identifier] = pr
				}
			}
		}
	}
}

// hasPRRead reports whether a PR read of the ticket found a PR or failed, so the merged read has
// nothing to add.
func (w *World) hasPRRead(id string) bool {
	_, found := w.PRs[id]
	return found || w.PRReadFailed[id]
}

// MergedRepo is the settings of the repo whose merged PRs a read of the local ticket would look in.
// It is false for a Linear ticket and for a ticket in a finished status, which nothing closes again.
func (s Scope) MergedRepo(t ticket.Ticket) (config.Repo, bool) {
	if ticket.SystemOf(t.Identifier) != ticket.SystemLocal || t.Branch == "" || s.Is(t, ticket.Finished) {
		return config.Repo{}, false
	}
	label, ok := s.RepoOf(t)
	_, settings, _ := s.Repos.Find(label)
	return settings, ok && settings.Path != ""
}

// MergedReadCovers reports whether the merged read covered the ticket this cycle, which is when the
// absence of a PR record means the ticket has none.
func (w World) MergedReadCovers(scope Scope, t ticket.Ticket) bool {
	_, ok := scope.MergedRepo(t)
	return ok && !w.hasPRRead(t.Identifier)
}

// readInbox reads the inbox if Sources has one. A failed read assigns neither the items nor the
// time, because no other record points at an inbox item (ADR 0005).
func (w *World) readInbox(ctx context.Context, src Sources, scope Scope, now time.Time) {
	if src.Inbox == nil {
		return
	}
	w.readRemotes(ctx, src, scope)
	readCtx, done := bound(ctx)
	items, err := src.Inbox.Read(readCtx)
	done()
	if err != nil {
		w.InboxErr = err
		_, _ = fmt.Fprintf(src.Stderr, "dispatch: while reading the inbox: %v\n", err)
		return
	}
	w.Inbox, w.InboxErr, w.InboxReadAt = items, nil, now
}

// DesignLabels mark tickets whose sessions the dispatcher never nudges.
var DesignLabels = []string{"design", "design-review"}

// NotDesign reports whether the ticket is known not to be a design or review
// ticket. A ticket whose labels were cut short might be one.
func NotDesign(t ticket.Ticket) bool {
	return !t.LabelsTruncated && !slices.ContainsFunc(DesignLabels, t.HasLabel)
}

// Retires reports whether the dispatcher tears down this lease holder's trees:
// its status is a finished one.
func (s Scope) Retires(t ticket.Ticket) bool {
	return s.Is(t, ticket.Finished)
}

// Parents returns the tickets whose children the dispatcher acts on: In
// Progress parents that are known not to be design tickets, since the design
// worker and the review own the children and close of those.
func (s Scope) Parents(tickets []ticket.Ticket) []ticket.Ticket {
	var parents []ticket.Ticket
	for _, t := range tickets {
		if s.Is(t, ticket.InProgress) && t.HasChildren && NotDesign(t) {
			parents = append(parents, t)
		}
	}
	return parents
}

// PRRepo is the settings of the repo whose PR a read of the ticket would look in. It is false
// when no read would happen, so a ticket without a PR record was either read and has none, or
// was not read.
func (s Scope) PRRepo(t ticket.Ticket, state WorkerState) (config.Repo, bool) {
	if !s.ReadsPR(t, state) {
		return config.Repo{}, false
	}
	label, ok := s.RepoOf(t)
	_, settings, _ := s.Repos.Find(label)
	return settings, ok && settings.Path != ""
}

// ReadsPR reports whether the dispatcher uses this In Review ticket's PR.
// Working and blocked workers are skipped so their PRs cost no call, and so are
// tickets whose labels or children make it unsure they are build tickets.
func (s Scope) ReadsPR(t ticket.Ticket, state WorkerState) bool {
	if !s.Is(t, ticket.InReview) || t.LabelsTruncated || t.HasChildren || !NotDesign(t) {
		return false
	}
	return state == NoWorker || state == Idle
}

// kickoffRan reports whether the worker's session ended a turn after at, which proves its
// kickoff ran even if the launch that marked it was killed before it said so. A Stop from
// another session in the tree proves nothing. With no session known it falls back to the
// newest Stop of any session. A Stop record that exists but can't
// be read is unknown, so it counts as ran and the worker is not closed.
func kickoffRan(stateDir string, wk Worker, at time.Time) bool {
	if stops.Unreadable(stateDir, wk.TreePath, wk.Session) {
		return true
	}
	rec, ok := stops.Read(stateDir, wk.TreePath, wk.Session)
	return ok && rec.At.After(at)
}

// readRemotes reads the chosen remote of every configured clone. A clone whose read fails keeps its
// last good owner/name, so a git hiccup never turns a known repo label into undefined (ADR 0005).
func (w *World) readRemotes(ctx context.Context, src Sources, scope Scope) {
	remotes, errs := maps.Clone(w.Remotes), map[string]error{}
	if remotes == nil {
		remotes = map[string]string{}
	}
	for label, repo := range scope.Repos {
		readCtx, done := bound(ctx)
		name, err := gitremote.Read(readCtx, src.Env, repo.Path, repo.ChosenRemote())
		done()
		if err != nil {
			errs[label] = err
			_, _ = fmt.Fprintf(src.Stderr, "dispatch: while reading the %s remote of the %s clone: %v\n", repo.ChosenRemote(), label, err)
			continue
		}
		if name == "" {
			delete(remotes, label)
			continue
		}
		remotes[label] = name
	}
	w.Remotes, w.RemoteErrs = remotes, errs
}
