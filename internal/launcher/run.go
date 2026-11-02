package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/clock"
	"github.com/kapetan-io/dispatch/internal/environment"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/kickoffs"
	"github.com/kapetan-io/dispatch/internal/stackrecord"
	"github.com/kapetan-io/dispatch/internal/treehouse"
	"github.com/kapetan-io/dispatch/internal/typed"
)

// Statuses Run reports besides the ones in launcher.go.
const (
	StatusPoolError            = "pool_error"
	StatusHerdrUnavailable     = "herdr_unavailable"
	StatusUsageError           = "usage_error"
	StatusNotARepo             = "not_a_repo"
	StatusNotPooled            = "not_pooled"
	StatusLeaseFailed          = "lease_failed"
	StatusSwitchFailed         = "switch_failed"
	StatusStackRecordFailed    = "stack_record_failed"
	StatusWorkspaceFailed      = "workspace_failed"
	StatusAgentStartFailed     = "agent_start_failed"
	StatusInjectionUnconfirmed = "injection_unconfirmed"
)

// Typing is where a launch notes what it types into the worker, so the prompt hook can tell
// dispatch's messages from a person's. A note for the session it is about to start replaces
// whatever an earlier session in that tree was sent.
type Typing interface {
	Note(tree, text string)
	Clear(tree string)
}

// Options names the tools a Local launcher drives and the places it runs them.
// Zero values take the defaults.
type Options struct {
	// PluginRoot is the plugin checkout, stamped on the worker as
	// DISPATCH_PLUGIN_ROOT. It has no default.
	PluginRoot string
	// Herdr hosts the worker. Required: the caller builds it for the config's host.
	Herdr herdr.Herdr
	// Treehouse is the pool to lease from. Defaults to the installed treehouse.
	Treehouse treehouse.Pool
	// Stderr receives progress. Defaults to discarding it.
	Stderr io.Writer
	// StateDir is where a launch records that its kickoff is unconfirmed, so a
	// tick can find a session the launch died on. Empty records nothing.
	StateDir string
	// Clock stamps a launch's kickoff record, which the rules compare with the loop's clock,
	// and runs the preflight deadline. Defaults to the wall clock.
	Clock clock.Clock
	// Typed is told the kickoff before it is typed, so the prompt hook labels it as dispatch's.
	// Defaults to noting nothing.
	Typed Typing
	// PreflightTimeout bounds the wait for the herdr server. Defaults to 60s.
	PreflightTimeout time.Duration
	// Pace is the unit of waiting between polls for the herdr server, which waits 5 units.
	// Defaults to 1s.
	Pace time.Duration
	// Env is what the launcher's git commands and the default treehouse run with.
	// Defaults to the process's environment.
	Env environment.Environment
}

func (o Options) withDefaults() Options {
	if o.Treehouse == nil {
		o.Treehouse = treehouse.NewCLI("treehouse").WithEnv(o.Env)
	}
	if o.Stderr == nil {
		o.Stderr = io.Discard
	}
	if o.PreflightTimeout == 0 {
		o.PreflightTimeout = 60 * time.Second
	}
	if o.Pace == 0 {
		o.Pace = time.Second
	}
	if o.Typed == nil {
		// A nil log notes nothing.
		o.Typed = (*typed.Log)(nil)
	}
	if o.Clock == nil {
		o.Clock = clock.Wall{}
	}
	return o
}

// Local is the Launcher that leases a tree from the pool on this machine and
// starts the worker in the herdr that hosts it.
type Local struct {
	opts Options
}

// New returns a Launcher over the tools in opts.
func New(opts Options) *Local { return &Local{opts: opts.withDefaults()} }

// Launch leases a treehouse tree for the ticket, opens a workspace on it and
// starts a worker that is typed the kickoff. The Result says what
// happened, and progress goes to Options.Stderr.
//
// pool_exhausted is proven, never assumed. After a lease failure the pool is
// read again and exhaustion is reported only when no tree is available and the
// pool is at max_trees. Every other lease failure is lease_failed, which counts
// for nothing because the next resync launches again. pool_error is an unreadable
// pool or a lease returned without a path, and counts toward launch_failure_limit.
//
// The herdr handling depends on nested worktree workspaces, an env-stamped tab,
// waiting for the shell before starting the agent, answering the consent and
// folder-trust dialogs, and confirming the kickoff landed. The herdr adapter
// does all of it behind OpenWorkspace, StartWorker and Kickoff. A StartWorker that finds a
// session already running on the tree ends the launch as already_running, and one that finds only
// a terminal with no Claude ends it as terminal_without_agent. Both close and return nothing.
func (r *Local) Launch(ctx context.Context, req Request) Result {
	l := &launch{req: req, opts: r.opts}
	return l.run(ctx)
}

type launch struct {
	req  Request
	opts Options

	repo      string
	tree      string
	workspace string
	// markID is the id this launch holds its kickoffs record under, so it never releases
	// the record of a launch that replaced it.
	markID string
	// markAt is when the launch began, which a claimed record carries.
	markAt time.Time
	// workspaceOpen is set when a failed kickoff could not close the workspace.
	workspaceOpen bool
	// fresh is set when this run took the lease, as opposed to finding one from
	// an earlier run.
	fresh bool
	// stacked is what Result.Stacked reports.
	stacked bool
}

func (l *launch) log(format string, args ...any) {
	_, _ = fmt.Fprintf(l.opts.Stderr, "dispatch launch: "+format+"\n", args...)
}

func (l *launch) run(ctx context.Context) Result {
	req := &l.req
	if req.Ticket == "" || req.Branch == "" {
		l.log("usage: dispatch launch <TICKET> <BRANCH> [--base REF] [--model NAME | --harness harness.NAME] [--kickoff TEXT] [--title TEXT]")
		return Result{Status: StatusUsageError, Detail: "TICKET and BRANCH are required"}
	}
	// Without it the worker starts with no dispatch plugin and no shims.
	if l.opts.PluginRoot == "" {
		return Result{Status: StatusUsageError, Detail: "no plugin root: set DISPATCH_PLUGIN_ROOT or plugin_root in the config"}
	}
	if req.Program.AgentKind == "" {
		return Result{Status: StatusUsageError, Detail: "no program to run: the request names no harness"}
	}
	if req.Kickoff == "" {
		req.Kickoff = "/dispatch:build " + req.Ticket
	}
	// The worker's herdr agent name is the lowercased ticket id, so a relaunch
	// reclaims the same name. Claude itself is never given --name: it draws the
	// name in a rule below an open question, and herdr then misses the question
	// and reports the worker done instead of blocked.
	workerName := strings.ToLower(req.Ticket)

	top, err := l.git(ctx, req.RepoDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return Result{Status: StatusNotARepo, Detail: "run from inside the target repo"}
	}
	l.repo = strings.TrimSpace(top)
	if req.Stack != nil {
		req.Base = req.Stack.Head
	}
	if req.Base == "" {
		req.Base = l.defaultBranch(ctx)
	}
	if _, err := os.Stat(filepath.Join(l.repo, "treehouse.toml")); err != nil {
		return Result{Status: StatusNotPooled, Detail: l.repo + " has no treehouse.toml. Workers only run on leased pool trees, so add one (treehouse init) before approving tickets in this repo."}
	}

	if r, ok := l.preflight(ctx); !ok {
		return r
	}
	if r, ok := l.fetchAndLease(ctx); !ok {
		return r
	}
	if r, ok := l.openWorkspace(ctx); !ok {
		return r
	}
	l.beginKickoff()
	defer l.clearKickoff()
	started, r, ok := l.startWorker(ctx, workerName)
	if !ok {
		return r
	}
	l.claimKickoff()
	l.opts.Typed.Clear(l.tree)
	l.opts.Typed.Note(l.tree, req.Kickoff)

	// An unconfirmed kickoff closes the workspace. Left open, its idle program
	// would look like a live worker, and no cycle would retry or escalate.
	if err := l.opts.Herdr.Kickoff(ctx, started.Pane, req.Kickoff, req.Program.Startup); err != nil {
		if l.opts.Herdr.CloseWorkspace(context.WithoutCancel(ctx), l.workspace) != nil {
			l.workspaceOpen = true
		}
		l.opts.Typed.Clear(l.tree)
		program := req.Program.Name
		detail := program + " started but herdr"
		switch {
		case errors.Is(err, herdr.ErrEnterNotSent):
			detail = program + " never started, because its launch command still showed at the shell prompt and herdr could not send Enter to it, and herdr"
		case errors.Is(err, herdr.ErrNeverStarted):
			detail = program + " never started, because its launch command still showed at the shell prompt after Enter was pressed on it, and herdr"
		}
		return Result{
			Status:    StatusInjectionUnconfirmed,
			Detail:    detail + " never showed the kickoff running, so the workspace was closed and the next resync retries. Record the screens with DISPATCH_LAUNCH_TRACE (`dispatch launch`).",
			Workspace: l.workspace, Pane: started.Pane, Worktree: l.tree,
		}
	}
	return Result{
		Status: StatusOK, Ticket: req.Ticket, Branch: req.Branch, Worktree: l.tree,
		Workspace: l.workspace, Pane: started.Pane, Agent: started.Agent, Kickoff: req.Kickoff, Stacked: l.stacked,
	}
}

// beginKickoff marks the launch unconfirmed until it returns. A launch killed
// before then leaves the mark, which is how a later tick knows the session in
// this tree never got its kickoff. A record already there is shared, not replaced,
// so a launch that herdr refuses cannot erase the winner's.
func (l *launch) beginKickoff() {
	if l.opts.StateDir == "" {
		return
	}
	l.markAt = l.opts.Clock.Now()
	id, err := kickoffs.MarkUnlessHeld(l.opts.StateDir, l.tree, l.markAt)
	if err != nil {
		l.log("%v", err)
		return
	}
	l.markID = id
}

// claimKickoff makes the record this launch's alone. Under the app host herdr starts one session
// per tree, so a launch that got this far won and any other owner lost or died. Under the TUI two
// racing launches both get here, and the last claim replaces the first.
func (l *launch) claimKickoff() {
	if l.opts.StateDir == "" {
		return
	}
	id, err := kickoffs.Mark(l.opts.StateDir, l.tree, l.markAt)
	if err != nil {
		l.log("%v", err)
		return
	}
	l.markID = id
}

// clearKickoff runs on every return, because a launch that reports its own failure has
// already closed its workspace. When that close failed, the idle session is still there, and
// the mark stays so a later cycle closes it.
func (l *launch) clearKickoff() {
	if l.opts.StateDir != "" && !l.workspaceOpen {
		kickoffs.Release(l.opts.StateDir, l.tree, l.markID)
	}
}

func (l *launch) preflight(ctx context.Context) (Result, bool) {
	waitCtx, cancel := clock.WithTimeout(ctx, l.opts.Clock, "launch preflight", l.opts.PreflightTimeout)
	defer cancel()
	unanswered := func(readErr error) Result {
		return Result{Status: StatusHerdrUnavailable, Detail: fmt.Sprintf("herdr server not answering: %v", readErr)}
	}
	for {
		_, err := l.opts.Herdr.Read(ctx)
		if err == nil {
			return Result{}, true
		}
		if waitCtx.Err() != nil && ctx.Err() == nil {
			return unanswered(err), false
		}
		l.log("waiting for herdr server...")
		if !l.sleep(waitCtx, 5*l.opts.Pace) {
			if ctx.Err() == nil {
				return unanswered(err), false
			}
			return Result{Status: StatusHerdrUnavailable, Detail: "interrupted while waiting for the herdr server"}, false
		}
	}
}

// fetchAndLease holds the clone for every fetch a launch makes, treehouse's
// own included, so launches into one repo queue here and then go on in parallel.
func (l *launch) fetchAndLease(ctx context.Context) (Result, bool) {
	clone := l.repo
	if dir, err := l.git(ctx, l.repo, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil {
		clone = strings.TrimSpace(dir)
	}
	resume := func() {}
	if l.req.WhileQueued != nil {
		resume = l.req.WhileQueued()
	}
	queue, stop := ctx, func() {}
	if l.req.QueueLimit > 0 {
		queue, stop = clock.WithTimeout(ctx, l.opts.Clock, "queue "+l.req.Ticket, l.req.QueueLimit)
	}
	done, ok := holdClone(queue, clone)
	stop()
	resume()
	if !ok {
		detail := "interrupted while waiting for another launch into " + l.repo
		if ctx.Err() == nil {
			detail = fmt.Sprintf("gave up waiting for another launch into %s after %s", l.repo, l.req.QueueLimit)
		}
		return Result{Status: StatusPoolError, Detail: detail}, false
	}
	defer done()
	return l.leaseTree(ctx, l.trackBranch(ctx))
}

// trackBranch reports whether the branch already exists, locally or on origin,
// creating a local tracking branch for one that exists only there.
func (l *launch) trackBranch(ctx context.Context) bool {
	b := l.req.Branch
	_, _ = l.git(ctx, l.repo, "fetch", "--no-tags", "origin", b)
	if _, err := l.git(ctx, l.repo, "show-ref", "--verify", "--quiet", "refs/heads/"+b); err == nil {
		return true
	}
	if _, err := l.git(ctx, l.repo, "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+b); err == nil {
		_, _ = l.git(ctx, l.repo, "branch", "--track", b, "origin/"+b)
		return true
	}
	return false
}

// leaseTree reuses the tree already holding this ticket's lease, so a relaunch
// resumes in place. A tree is never adopted just for having the branch checked
// out, since it could be the user's own clone or a pool tree treehouse may hand
// to someone else. If the branch is checked out elsewhere, the switch fails
// loudly. A failure is reported as it is. The next resync launches again.
func (l *launch) leaseTree(ctx context.Context, existed bool) (Result, bool) {
	if res := l.lease(ctx); res.Status != "" {
		return res, false
	}
	return l.checkout(ctx, existed)
}

func (l *launch) lease(ctx context.Context) Result {
	req := l.req
	trees, err := l.opts.Treehouse.Trees(ctx, l.repo)
	if err != nil {
		// An unread pool may hold this ticket's lease already, and leasing again would
		// take a second tree for it.
		return Result{Status: StatusPoolError, Detail: treehouse.StatusError(l.repo, err).Error()}
	}
	for _, t := range trees {
		if t.LeaseHolder == req.Ticket {
			l.tree = t.Path
			return Result{}
		}
	}

	l.log("leasing a treehouse tree for %s", req.Ticket)
	leased, err := l.opts.Treehouse.Lease(ctx, l.repo, req.Ticket)
	if err != nil {
		if errors.Is(err, treehouse.ErrNoPath) {
			l.returnLeaseTaken(ctx)
			return Result{Status: StatusPoolError, Detail: err.Error()}
		}
		if err.Error() != "" {
			l.log("treehouse get: %v", err)
		}
		return l.leaseFailure(ctx, err.Error())
	}
	l.tree = leased.Path
	l.log("leased %s", l.tree)
	l.fresh = true
	return Result{}
}

// checkout puts the leased tree on the ticket's branch. The lease goes back to the pool when
// that fails.
func (l *launch) checkout(ctx context.Context, existed bool) (Result, bool) {
	// A lease from an earlier run is already on its branch.
	if !l.fresh {
		return Result{}, true
	}
	req := l.req
	from := ""
	if !existed {
		from = " from " + req.Base
	}
	// A branch with no record reads as existing to the next launch, which never writes one, so the
	// record goes first.
	if !existed && req.Stack != nil {
		if res, ok := l.writeStackRecord(ctx); !ok {
			return res, false
		}
	}
	// A switch that fails after creating the branch leaves it behind, so its stack record stays with it.
	created := func() bool {
		_, err := l.git(ctx, l.repo, "show-ref", "--verify", "--quiet", "refs/heads/"+req.Branch)
		return err == nil
	}
	var out strings.Builder
	var err error
	switch {
	case existed:
		err = l.gitToStderr(ctx, &out, l.tree, "switch", req.Branch)
	case req.Stack == nil && req.StartPR > 0:
		from = fmt.Sprintf(" from PR %d", req.StartPR)
		// A private ref, so the fetch never collides with a real branch, and + follows a force-pushed PR.
		ref := fmt.Sprintf("origin/dispatch-start/%d", req.StartPR)
		err = l.gitToStderr(ctx, &out, l.repo, "fetch", "--no-tags", "origin", fmt.Sprintf("+refs/pull/%d/head:refs/remotes/%s", req.StartPR, ref))
		if err == nil {
			err = l.gitToStderr(ctx, &out, l.tree, "switch", "--no-track", "-c", req.Branch, ref)
		}
	default:
		_, _ = l.git(ctx, l.repo, "fetch", "--no-tags", "origin", req.Base)
		err = l.gitToStderr(ctx, &out, l.tree, "switch", "--no-track", "-c", req.Branch, "origin/"+req.Base)
		if err != nil {
			err = l.gitToStderr(ctx, &out, l.tree, "switch", "-c", req.Branch, req.Base)
		}
	}
	if err == nil {
		return Result{}, true
	}
	said := strings.Join(strings.Fields(out.String()), " ")
	if said == "" {
		said = err.Error()
	}
	l.log("checkout of %s failed in leased tree, returning the lease", req.Branch)
	if !existed && req.Stack != nil && !created() {
		// A leftover record would attach to whatever branch of this name is created later.
		_ = stackrecord.Clear(context.WithoutCancel(ctx), l.opts.Env, l.repo, req.Branch)
	}
	l.returnLease(ctx)
	return Result{Status: StatusSwitchFailed, Detail: fmt.Sprintf("git switch to %s%s failed in leased tree %s (lease returned): %s", req.Branch, from, l.tree, said)}, false
}

// writeStackRecord fetches the stack parent's branch so the fork point is present locally, then
// records the stack parent and the fork point. It is not retried, since git waits out a lock on the
// clone's config itself. A failed write returns the lease like a failed switch.
func (l *launch) writeStackRecord(ctx context.Context) (Result, bool) {
	req := l.req
	// The head may already be local, and the switch that follows fails loudly when it is not.
	_, _ = l.git(ctx, l.repo, "fetch", "--no-tags", "origin", req.Stack.Branch)
	rec := stackrecord.Record{Parent: req.Stack.Ticket, ForkPoint: req.Stack.Head}
	if err := stackrecord.Write(ctx, l.opts.Env, l.repo, req.Branch, rec); err != nil {
		l.log("%v", err)
		l.returnLease(ctx)
		return Result{Status: StatusStackRecordFailed, Detail: fmt.Sprintf("stack record for %s failed in %s (lease returned): %v", req.Branch, l.repo, err)}, false
	}
	l.stacked = true
	return Result{}, true
}

var maxTrees = regexp.MustCompile(`(?m)^max_trees *= *([0-9]+)`)

// returnLeaseTaken gives back a lease treehouse recorded for this ticket before it reported
// failure, since the next launch would take it for one already on the branch.
func (l *launch) returnLeaseTaken(ctx context.Context) {
	trees, err := l.opts.Treehouse.Trees(ctx, l.repo)
	if err != nil {
		return
	}
	for _, t := range trees {
		if t.LeaseHolder == l.req.Ticket {
			l.tree = t.Path
			l.returnLease(ctx)
		}
	}
}

func (l *launch) leaseFailure(ctx context.Context, leaseErr string) Result {
	trees, err := l.opts.Treehouse.Trees(ctx, l.repo)
	if err != nil {
		return Result{Status: StatusPoolError, Detail: fmt.Sprintf("treehouse lease failed and the pool could not be read: %v. treehouse stderr: %s", treehouse.StatusError(l.repo, err), leaseErr)}
	}
	limit := -1
	if toml, err := os.ReadFile(filepath.Join(l.repo, "treehouse.toml")); err == nil {
		if m := maxTrees.FindSubmatch(toml); m != nil {
			limit, _ = strconv.Atoi(string(m[1]))
		}
	}
	var avail int
	var holders []string
	for _, t := range trees {
		// Treehouse recorded the lease and then failed. The next launch would find it and take it
		// for a lease already on the branch, so it goes back to the pool.
		if t.LeaseHolder == l.req.Ticket {
			l.tree = t.Path
			l.returnLease(ctx)
			avail++
			continue
		}
		if t.Status == "available" {
			avail++
			continue
		}
		holder := t.LeaseHolder
		if holder == "" {
			holder = "?"
		}
		holders = append(holders, fmt.Sprintf("%s (%s)", holder, t.Status))
	}
	list := strings.Join(holders, ", ")
	if limit >= 0 && len(trees) > 0 && avail == 0 && len(trees) >= limit {
		return Result{Status: StatusPoolExhausted, Detail: fmt.Sprintf("all %d/%d pool trees are taken: %s", len(trees), limit, list)}
	}
	return Result{Status: StatusLeaseFailed, Detail: fmt.Sprintf("treehouse lease failed but the pool is not exhausted (%d of %d available, max_trees=%d; holders: %s). treehouse stderr: %s", avail, len(trees), limit, list, leaseErr)}
}

// giveBack closes the workspace and returns the lease when this run took the
// lease, so a failed launch holds no pool slot. Any session in the workspace
// has not been given the kickoff yet, so closing it loses no work. A lease
// from an earlier run may hold a worker's uncommitted work, and the forced
// return would discard it, so that one is kept.
func (l *launch) giveBack(ctx context.Context) {
	if !l.fresh {
		return
	}
	// A canceled launch still cleans up, or the lease and workspace would leak.
	ctx = context.WithoutCancel(ctx)
	// The lease goes back only after the workspace is confirmed closed. A tree
	// returned with its workspace still open would match that workspace by
	// path, so the next ticket to lease it would never start.
	if l.workspace != "" && l.opts.Herdr.CloseWorkspace(ctx, l.workspace) != nil {
		l.log("workspace %s did not close, keeping the lease on %s", l.workspace, l.tree)
		return
	}
	l.log("returning the lease on %s", l.tree)
	l.returnLease(ctx)
}

func (l *launch) returnLease(ctx context.Context) {
	if err := l.opts.Treehouse.Return(context.WithoutCancel(ctx), l.repo, l.tree, l.req.Ticket); err != nil {
		l.log("while returning %s to the pool: %v", l.tree, err)
	}
}

func (l *launch) openWorkspace(ctx context.Context) (Result, bool) {
	snap, err := l.opts.Herdr.Read(ctx)
	if err != nil {
		l.giveBack(ctx)
		return Result{Status: StatusWorkspaceFailed, Detail: fmt.Sprintf("while reading herdr for %s: %v", l.tree, err), Worktree: l.tree}, false
	}
	for _, w := range snap.Workspaces {
		if w.CheckoutPath == l.tree {
			l.workspace = w.ID
			break
		}
	}
	if l.workspace != "" {
		if a, ok := snap.SessionIn(l.workspace); ok {
			return Result{
				Status:    StatusAlreadyRunning,
				Detail:    fmt.Sprintf("workspace %s already has a %s session in pane %s; not typing a second kickoff", l.workspace, a.Kind, a.PaneID),
				Workspace: l.workspace, Pane: a.PaneID, Worktree: l.tree,
			}, false
		}
		return Result{}, true
	}

	// herdr refuses a linked worktree as the source, so name the main
	// checkout even when the launcher runs from inside a pool tree.
	common, _ := l.git(ctx, l.repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	id, err := l.opts.Herdr.OpenWorkspace(ctx, herdr.NewWorkspace{Repo: filepath.Dir(strings.TrimSpace(common)), Path: l.tree, Label: l.req.Ticket})
	if err != nil {
		l.giveBack(ctx)
		return Result{Status: StatusWorkspaceFailed, Detail: err.Error(), Worktree: l.tree}, false
	}
	l.workspace = id
	return Result{}, true
}

func (l *launch) startWorker(ctx context.Context, name string) (herdr.Started, Result, bool) {
	started, err := l.opts.Herdr.StartWorker(ctx, herdr.Worker{
		Workspace:  l.workspace,
		Dir:        l.tree,
		FreshLease: l.fresh,
		Label:      l.tabLabel(),
		Name:       name,
		Program:    l.req.Program,
		PluginRoot: l.opts.PluginRoot,
	})
	if err == nil {
		return started, Result{}, true
	}
	// Whoever owns the session owns the workspace too, so this launch closes and returns nothing.
	if errors.Is(err, herdr.ErrSessionRunning) {
		return started, Result{Status: StatusAlreadyRunning, Detail: fmt.Sprintf("workspace %s already has a session; not typing a second kickoff", l.workspace), Workspace: l.workspace, Worktree: l.tree}, false
	}
	// A racing launch may own this terminal, so leave the workspace and tree alone, as above.
	var held *herdr.TerminalWithoutAgent
	if errors.As(err, &held) {
		return started, Result{Status: StatusTerminalWithoutAgent, Detail: fmt.Sprintf("terminal %s holds %s but runs no Claude; remove it so a worker can start", held.Address, l.tree), Workspace: l.workspace, Worktree: l.tree}, false
	}
	l.giveBack(ctx)
	if started.Pane == "" {
		return started, Result{Status: StatusWorkspaceFailed, Detail: err.Error(), Workspace: l.workspace, Worktree: l.tree}, false
	}
	return started, Result{Status: StatusAgentStartFailed, Detail: err.Error(), Workspace: l.workspace, Pane: started.Pane, Worktree: l.tree}, false
}

func (l *launch) sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

func (l *launch) git(ctx context.Context, dir string, args ...string) (string, error) {
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	out, err := l.opts.Env.Command(ctx, "git", args...).Output()
	return string(out), err
}

// defaultBranch follows treehouse's order for a repo's base branch: the
// remote's HEAD, then the branch checked out in the repo, then
// init.defaultBranch. A checked-out branch that origin doesn't have is someone's
// unpushed work, so it is skipped when the repo has an origin.
func (l *launch) defaultBranch(ctx context.Context) string {
	line := func(args ...string) string {
		out, err := l.git(ctx, l.repo, args...)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(out)
	}
	// A rename on origin leaves the cached origin/HEAD stale. If origin can't be
	// reached the cached value stands.
	_, _ = l.git(ctx, l.repo, "remote", "set-head", "origin", "--auto")
	if name := strings.TrimPrefix(line("symbolic-ref", "--short", "refs/remotes/origin/HEAD"), "origin/"); name != "" {
		return name
	}
	if name := line("symbolic-ref", "--short", "HEAD"); name != "" {
		_, err := l.git(ctx, l.repo, "remote", "get-url", "origin")
		if err != nil || line("rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+name) != "" {
			return name
		}
	}
	if name := line("config", "init.defaultBranch"); name != "" {
		return name
	}
	return "main"
}

// gitToStderr runs git in dir, copying its output to the launcher's stderr as well as to output,
// which the receipt quotes.
func (l *launch) gitToStderr(ctx context.Context, output io.Writer, dir string, args ...string) error {
	cmd := l.opts.Env.Command(ctx, "git", append([]string{"-C", dir}, args...)...)
	out := output
	if l.opts.Stderr != nil {
		out = io.MultiWriter(output, l.opts.Stderr)
	}
	cmd.Stdout, cmd.Stderr = out, out
	return cmd.Run()
}

// tabLabel is what the loop's tab rename sets, so the first cycle leaves a
// freshly launched tab alone.
func (l *launch) tabLabel() string {
	return herdr.TabLabel(l.req.Ticket, l.req.Title)
}
