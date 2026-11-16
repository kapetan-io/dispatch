package loop

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/launcher"
	"github.com/kapetan-io/dispatch/internal/stackrecord"
	"github.com/kapetan-io/dispatch/internal/ticket"
	"github.com/kapetan-io/dispatch/internal/ticketbranch"
	"github.com/kapetan-io/dispatch/internal/world"
)

// resyncBranches makes the branch rows agree with the branches the read that just landed listed,
// after it imports any repo whose branches an earlier dispatch kept in git config and unsets the
// keys an import listed. A repo whose clone was not listed is left out, so no row changes because
// of a failed read. warn is true for a read of every source, which is when a gone branch and a
// repo that cannot be imported are named. It reports whether an import named a branch other than
// the one the ticket system gives, so the caller reads the PRs again on the branches the rows name.
func (l *Loop) resyncBranches(warn bool) bool {
	changed, renamed := false, false
	var listings []ticketbranch.Listing
	for _, label := range l.o.Config.Repos.Labels() {
		if l.wd.ListingErrs[label] != nil {
			continue
		}
		imported, named := l.importBranches(label, warn)
		changed, renamed = changed || imported, renamed || named
		l.unsetKeys(label)
		listings = append(listings, ticketbranch.NewListing(label, l.wd.Listings[label]))
	}
	busy := func(id string) bool { return l.leaseBusy(id, "") }
	found, err := l.o.Branches.Resync(listings, busy, l.untracked)
	if err != nil {
		// The rows stay as they were, and the next read tries again.
		_, _ = fmt.Fprintf(l.o.Err, "%s dispatch: while resyncing the branch rows: %v\n", l.clock.Now().Format(time.TimeOnly), err)
		return renamed
	}
	if warn {
		for _, gone := range found.Missing {
			_, _ = fmt.Fprintf(l.o.Err, "dispatch: %s's branch %s in %s is gone; dispatch will not launch %s until `dispatch ticket branch %s <name>` names its branch.\n",
				gone.Ticket, gone.Name, gone.Repo, gone.Ticket, gone.Ticket)
		}
	}
	if changed || found.Changed {
		l.wd = world.RereadBranches(l.runCtx, l.o.Sources, l.scope, l.wd)
	}
	return renamed
}

// importBranches copies a repo's ticket branch keys and stack records from its clone's git config
// into branch rows the first time every read the copy depends on succeeded. It reports whether it
// did, and whether a row it added names a branch other than the ticket system's. The name match
// of a ticket with no key reads the tracked tickets and the pool, so a failed read of either waits
// for the next one. A failure leaves the repo not imported, which
// holds its launches and its stack records.
func (l *Loop) importBranches(label string, warn bool) (imported, named bool) {
	done, err := l.o.Branches.Imported(label)
	if err != nil {
		_, _ = fmt.Fprintf(l.o.Err, "%s dispatch: while importing the branches of %s: %v\n", l.clock.Now().Format(time.TimeOnly), label, err)
		return false, false
	}
	if done {
		return false, false
	}
	if l.wd.UnreadRepos[label] || l.wd.LinearErr != nil || l.wd.LocalErr != nil {
		return false, false
	}
	dir := l.o.Config.Repos[label].Path
	ctx, cancel := context.WithTimeout(l.runCtx, world.ReadDeadline)
	defer cancel()
	keys, err := stackrecord.Read(ctx, l.o.Sources.Env, dir)
	var local map[string]string
	if err == nil {
		local, err = stackrecord.LocalBranches(ctx, l.o.Sources.Env, dir)
	}
	if err != nil {
		_, _ = fmt.Fprintf(l.o.Err, "%s dispatch: while importing the branches of %s: %v\n", l.clock.Now().Format(time.TimeOnly), label, err)
		return false, false
	}
	if len(keys.Half) > 0 {
		if warn {
			for _, branch := range slices.Sorted(maps.Keys(keys.Half)) {
				key := keys.Half[branch]
				_, _ = fmt.Fprintf(l.o.Err, "dispatch: %s is not imported: branch %s has half a stack record (only %s). Run `git -C %s config --unset branch.%s.%s`, or set the other key.\n",
					label, branch, key, dir, branch, key)
			}
		}
		return false, false
	}
	rows := l.namedBranches(label, keys, local)
	if err := l.o.Branches.Import(label, rows, keys.Branches, l.clock.Now()); err != nil {
		_, _ = fmt.Fprintf(l.o.Err, "%s dispatch: while importing the branches of %s: %v\n", l.clock.Now().Format(time.TimeOnly), label, err)
		return false, false
	}
	return true, slices.ContainsFunc(rows, l.namesAnotherBranch)
}

// namesAnotherBranch reports whether the row names a branch other than the one the ticket system
// gives its ticket, which is a branch the PR read before the import did not look on.
func (l *Loop) namesAnotherBranch(b ticketbranch.Branch) bool {
	for _, t := range l.wd.Tickets {
		if strings.EqualFold(t.Identifier, b.Ticket) {
			return t.Branch != b.Row.Name
		}
	}
	return false
}

// namedBranches is the branch rows an import adds to a repo: one per ticket branch key, then one
// for each tracked ticket with no key whose branch the start-time match of an earlier dispatch
// finds. That match takes the one local branch that starts with the ticket's id, and with several
// the one a tree the ticket holds has checked out. Each row carries its branch's stack record.
func (l *Loop) namedBranches(label string, keys stackrecord.Keys, local map[string]string) []ticketbranch.Branch {
	claimed := map[string]bool{}
	var rows []ticketbranch.Branch
	add := func(id, branch string) {
		claimed[branch] = true
		row := ticketbranch.Row{Name: branch}
		if rec, ok := keys.Whole[branch]; ok {
			row.Stack = &rec
		}
		rows = append(rows, ticketbranch.Branch{Ticket: strings.ToUpper(id), Repo: label, Row: row})
	}
	for _, branch := range slices.Sorted(maps.Keys(keys.Tickets)) {
		add(keys.Tickets[branch], branch)
	}
	for _, t := range l.wd.Tickets {
		if repo, ok := l.scope.RepoOf(t); !ok || repo != label {
			continue
		}
		if slices.ContainsFunc(rows, func(b ticketbranch.Branch) bool { return strings.EqualFold(b.Ticket, t.Identifier) }) {
			continue
		}
		var named, held []string
		for branch, tree := range local {
			if claimed[branch] || !ticket.OnBranch(t.Identifier, branch) {
				continue
			}
			named = append(named, branch)
			if tree != "" && l.holder(tree) == t.Identifier {
				held = append(held, branch)
			}
		}
		if len(named) > 1 {
			named = held
		}
		if len(named) == 1 {
			add(t.Identifier, named[0])
		}
	}
	return rows
}

// holder is the ticket that holds the lease on the tree at path, empty when none does.
func (l *Loop) holder(path string) string {
	for _, tree := range l.wd.Trees {
		if tree.Path == path {
			return tree.LeaseHolder
		}
	}
	return ""
}

// unsetKeys removes the git config keys an import found in the repo's clone, and takes each
// branch whose keys are gone off the list. A key that cannot be unset stays listed for the next
// resync.
func (l *Loop) unsetKeys(label string) {
	list, err := l.o.Branches.ToUnset(label)
	if err != nil || len(list) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(l.runCtx, world.ReadDeadline)
	defer cancel()
	var gone []string
	for _, branch := range list {
		if err := stackrecord.Clear(ctx, l.o.Sources.Env, l.o.Config.Repos[label].Path, branch); err != nil {
			_, _ = fmt.Fprintf(l.o.Err, "%s dispatch: while unsetting the dispatch keys of %s in %s: %v\n", l.clock.Now().Format(time.TimeOnly), branch, label, err)
			continue
		}
		gone = append(gone, branch)
	}
	if err := l.o.Branches.KeysUnset(label, gone); err != nil {
		_, _ = fmt.Fprintf(l.o.Err, "%s dispatch: %v\n", l.clock.Now().Format(time.TimeOnly), err)
	}
}

// branchNamed is a `dispatch ticket branch <ticket> <name>` that ended off the loop goroutine.
type branchNamed struct {
	reply chan Response
	lines []string
	err   error
}

// onBranch answers `dispatch ticket branch`. It prints the ticket's recorded branch, ends its stack
// record, or records a new name. Naming a branch fetches it first, which runs off the loop
// goroutine, and is refused, changing nothing, when the branch is in neither the clone nor origin
// or another ticket's row names it. Naming and ending are refused until the repo is imported.
// None of the three writes to the ticket system.
func (l *Loop) onBranch(call *TicketCall, reply chan Response) {
	id := strings.ToUpper(call.ID)
	repo, err := l.repoOfTicket(id)
	if err != nil {
		reply <- Response{Code: CodeFailed, Error: err.Error()}
		return
	}
	if call.Unstack || call.Branch != "" {
		// The import adds rows only where none exist and unsets every key it found, so a change
		// made before it would be undone or would lose the stack record the keys held.
		switch done, err := l.o.Branches.Imported(repo); {
		case err != nil:
			reply <- Response{Code: CodeFailed, Error: err.Error()}
			return
		case !done:
			reply <- Response{Code: CodeFailed, Error: fmt.Sprintf("the branches of %s are not imported yet, so nothing was changed; dispatch run prints what holds the import", repo)}
			return
		}
	}
	switch {
	case call.Unstack && call.Branch != "":
		reply <- Response{Code: CodeFailed, Error: "--unstack takes no branch name"}
	case call.Unstack:
		if err := l.o.Branches.Unstack(id, repo); err != nil {
			reply <- Response{Code: CodeFailed, Error: err.Error()}
			return
		}
		l.readAgain(world.FromLocal)
		reply <- Response{Lines: []string{fmt.Sprintf("%s: no stack record in %s", id, repo)}}
	case call.Branch == "":
		row, found, err := l.o.Branches.Of(id, repo)
		switch {
		case err != nil:
			reply <- Response{Code: CodeFailed, Error: err.Error()}
		case !found:
			reply <- Response{Lines: []string{fmt.Sprintf("%s: no branch recorded in %s", id, repo)}}
		default:
			reply <- Response{Lines: []string{fmt.Sprintf("%s: branch %s in %s", id, row.Name, repo)}}
		}
	default:
		l.nameBranch(id, repo, call.Branch, reply)
	}
}

// nameBranch fetches the branch and checks it exists before it records it as the ticket's. It
// holds the clone as a launch does, so a launch never checks out a branch other than the one its
// row names when it finishes.
func (l *Loop) nameBranch(id, repo, name string, reply chan Response) {
	dir, env, store := l.o.Config.Repos[repo].Path, l.o.Sources.Env, l.o.Branches
	l.writing++
	l.naming++
	go func() {
		done := branchNamed{reply: reply}
		defer func() {
			select {
			case l.inbox <- done:
			case <-l.stopped:
			}
		}()
		wait, stop := context.WithTimeout(l.runCtx, world.ReadDeadline)
		release, held := launcher.HoldClone(wait, env, dir)
		stop()
		if !held {
			done.err = fmt.Errorf("a launch into %s held its clone too long; nothing was recorded, so run it again", repo)
			return
		}
		defer release()
		// The wait for the clone gets its own deadline, so a long launch cannot cut the fetch short.
		ctx, cancel := context.WithTimeout(l.runCtx, world.ReadDeadline)
		defer cancel()
		_ = env.Command(ctx, "git", "-C", dir, "fetch", "--no-tags", "origin", name).Run()
		local := env.Command(ctx, "git", "-C", dir, "show-ref", "--verify", "--quiet", "refs/heads/"+name).Run()
		remote := env.Command(ctx, "git", "-C", dir, "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+name).Run()
		switch {
		case ctx.Err() != nil:
			done.err = fmt.Errorf("looking for branch %s in %s took too long; nothing was recorded, so run it again", name, dir)
		case local != nil && remote != nil:
			done.err = fmt.Errorf("branch %s is in neither %s nor origin; nothing was recorded", name, dir)
		default:
			if done.err = store.Rename(id, repo, name); done.err == nil {
				done.lines = []string{fmt.Sprintf("%s: branch %s in %s", id, name, repo)}
			}
		}
	}()
}

// onBranchNamed answers the request whose branch was recorded, after asking for the clones and
// the PRs to be read again, since the ticket's PR is read on its branch.
func (l *Loop) onBranchNamed(m branchNamed) {
	l.writing--
	l.naming--
	if m.err != nil {
		m.reply <- Response{Code: CodeFailed, Error: m.err.Error()}
		return
	}
	l.branchEdits++
	l.readAgain(world.FromLocal | world.FromGitHub)
	m.reply <- Response{Lines: m.lines}
}

// repoOfTicket is the label of the repo a ticket's work is in: the repo its ticket system gives it
// while it is tracked, and otherwise the one repo where a branch row records it.
func (l *Loop) repoOfTicket(id string) (string, error) {
	for _, t := range l.wd.Tickets {
		if !strings.EqualFold(t.Identifier, id) {
			continue
		}
		if repo, ok := l.scope.RepoOf(t); ok && repo != "" {
			if _, known := l.o.Config.Repos[repo]; known {
				return repo, nil
			}
		}
		return "", fmt.Errorf("%s has no repo", id)
	}
	snap, err := l.o.Branches.Snapshot()
	if err != nil {
		return "", err
	}
	var repos []string
	for _, b := range snap.Rows {
		if strings.EqualFold(b.Ticket, id) {
			repos = append(repos, b.Repo)
		}
	}
	if len(repos) != 1 {
		return "", fmt.Errorf("%s has no repo", id)
	}
	return repos[0], nil
}
