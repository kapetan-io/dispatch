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
	"github.com/kapetan-io/dispatch/internal/stackrecord"
	"github.com/kapetan-io/dispatch/internal/treehouse"
	"github.com/kapetan-io/dispatch/internal/typed"
	"github.com/kapetan-io/dispatch/internal/workers"
)

// Statuses Run reports besides the ones in launcher.go.
const (
	StatusPoolError              = "pool_error"
	StatusMultiplexerUnavailable = "herdr_unavailable"
	StatusUsageError             = "usage_error"
	StatusNotARepo               = "not_a_repo"
	StatusNotPooled              = "not_pooled"
	StatusLeaseFailed            = "lease_failed"
	StatusSwitchFailed           = "switch_failed"
	StatusStackRecordFailed      = "stack_record_failed"
	StatusBranchTaken            = "branch_taken"
	StatusWorkspaceFailed        = "workspace_failed"
	StatusAgentStartFailed       = "agent_start_failed"
	StatusInjectionUnconfirmed   = "injection_unconfirmed"
	StatusProgramExited          = "program_exited"
	// StatusWorkerRowFailed is a launch that could not write the ticket's worker row. It counts
	// toward launch_failure_limit, because a database that cannot be written is not fixed by waiting.
	StatusWorkerRowFailed = "worker_row_failed"
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
	// Multiplexer hosts the worker. Required: the caller builds it for the config's host.
	Multiplexer herdr.Multiplexer
	// Treehouse is the pool to lease from. Defaults to the installed treehouse.
	Treehouse treehouse.Pool
	// Stderr receives progress. Defaults to discarding it.
	Stderr io.Writer
	// Workers holds the worker row a launch writes before each step it takes, so a launch that
	// dies is found by the next one. Required.
	Workers *workers.Store
	// Clock stamps a launch's kickoff record, which the rules compare with the loop's clock,
	// and runs the preflight deadline. Defaults to the wall clock.
	Clock clock.Clock
	// Typed is told the kickoff before it is typed, so the prompt hook labels it as dispatch's.
	// Defaults to noting nothing.
	Typed Typing
	// PreflightTimeout bounds the wait for the multiplexer to answer. Defaults to 60s.
	PreflightTimeout time.Duration
	// Pace is the unit of waiting between polls for the multiplexer, which waits 5 units.
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
// starts the worker in the multiplexer that hosts it.
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
// The multiplexer handling depends on nested worktree workspaces, an env-stamped tab,
// waiting for the shell before starting the agent, answering the consent and
// folder-trust dialogs, and confirming the kickoff landed. The adapters
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
	// prior is the kickoff record the row held before this launch wrote its own, which a launch
	// that types nothing puts back.
	prior *workers.Kickoff
	// fresh is set when this run took the lease, as opposed to finding one from
	// an earlier run.
	fresh bool
	// stacked is what Result.Stacked reports.
	stacked bool
}

func (l *launch) log(format string, args ...any) {
	_, _ = fmt.Fprintf(l.opts.Stderr, "launch: "+format+"\n", args...)
}

func (l *launch) run(ctx context.Context) Result {
	req := &l.req
	if req.Ticket == "" || req.Branch == "" {
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
	// The worker's agent name is the lowercased ticket id, so a relaunch
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
	if r, ok := l.beginKickoff(ctx); !ok {
		return r
	}
	started, r, ok := l.startWorker(ctx, workerName)
	if !ok {
		l.restoreKickoff()
		return r
	}
	if err := l.opts.Workers.RecordSession(req.Ticket, req.Repo, started.Session); err != nil {
		// The session got no kickoff, so closing its workspace loses no work.
		if l.opts.Multiplexer.CloseWorkspace(context.WithoutCancel(ctx), l.workspace) == nil {
			l.restoreKickoff()
		}
		return Result{Status: StatusWorkerRowFailed, Detail: err.Error(), Workspace: l.workspace, Pane: started.Pane, Worktree: l.tree}
	}
	text := l.message(started.Session)
	l.opts.Typed.Clear(l.tree)
	l.opts.Typed.Note(l.tree, text)

	// An unconfirmed kickoff closes the workspace. Left open, its idle program
	// would look like a live worker, and no cycle would retry or escalate.
	if err := l.opts.Multiplexer.Kickoff(ctx, started.Pane, text, req.Program.Startup); err != nil {
		// When the close fails the idle session is still there, and the open record stays so a
		// later cycle closes it.
		if l.opts.Multiplexer.CloseWorkspace(context.WithoutCancel(ctx), l.workspace) == nil {
			l.deleteKickoff()
		}
		l.opts.Typed.Clear(l.tree)
		program, name := req.Program.Name, l.opts.Multiplexer.Name()
		var exited *herdr.ProgramExited
		if errors.As(err, &exited) {
			detail := program + " exited"
			if exited.Exit != "" {
				detail += " (" + exited.Exit + ")"
			}
			detail += " before " + name + " showed the kickoff running."
			if exited.Screen != "" {
				detail += " Its screen ended with: " + exited.Screen + "."
			}
			return Result{
				Status: StatusProgramExited, Detail: detail + " The workspace was closed and the next resync retries.",
				Workspace: l.workspace, Pane: started.Pane, Worktree: l.tree,
			}
		}
		detail := program + " started but " + name
		switch {
		case errors.Is(err, herdr.ErrEnterNotSent):
			detail = program + " never started, because its launch command still showed at the shell prompt and " + name + " could not send Enter to it, and " + name
		case errors.Is(err, herdr.ErrNeverStarted):
			detail = program + " never started, because its launch command still showed at the shell prompt after Enter was pressed on it, and " + name
		}
		return Result{
			Status:    StatusInjectionUnconfirmed,
			Detail:    detail + " never showed the kickoff running, so the workspace was closed and the next resync retries. Record the screens by starting `dispatch run` with DISPATCH_LAUNCH_TRACE=<file>.",
			Workspace: l.workspace, Pane: started.Pane, Worktree: l.tree,
		}
	}
	// The kickoff ran, so a record left open by a failed write is closed by the worker's first Stop.
	if err := l.opts.Workers.CloseKickoff(req.Ticket, req.Repo); err != nil {
		l.log("%v", err)
	}
	return Result{
		Status: StatusOK, Ticket: req.Ticket, Branch: req.Branch, Worktree: l.tree,
		Workspace: l.workspace, Pane: started.Pane, Agent: started.Agent, Kickoff: text, Resumed: text == resumed, Stacked: l.stacked,
	}
}

// resumed is typed in place of the kickoff into a resumed session that already took it, because
// typing the kickoff again tells the worker to start the job it is in the middle of.
const resumed = "Dispatch restarted your terminal and resumed this conversation. Carry on from where you stopped."

// message is what the session is typed: resumed when the kickoff record the row held before this
// launch shows this session already took this same kickoff, and the kickoff otherwise. A kickoff
// that differs, such as a restack, tells the session something new, so it is typed.
func (l *launch) message(session string) string {
	k := l.prior
	if session == "" || k == nil || k.Session != session || k.Text != l.req.Kickoff {
		return l.req.Kickoff
	}
	if k.Open && !l.stoppedSince(session, k.At) {
		return l.req.Kickoff
	}
	return resumed
}

// beginKickoff writes the kickoff record, open, before the harness starts. A launch killed
// before it types leaves the record open, which is how a later cycle knows the session in this
// tree never got its kickoff.
func (l *launch) beginKickoff(ctx context.Context) (Result, bool) {
	prior, err := l.opts.Workers.KickoffOf(l.req.Ticket, l.req.Repo)
	if err == nil {
		l.prior = prior
		err = l.opts.Workers.SetKickoff(l.req.Ticket, l.req.Repo, &workers.Kickoff{
			Text: l.req.Kickoff, At: l.opts.Clock.Now().UnixMilli(), Open: true,
		})
	}
	if err != nil {
		l.giveBack(ctx)
		return Result{Status: StatusWorkerRowFailed, Detail: err.Error(), Workspace: l.workspace, Worktree: l.tree}, false
	}
	return Result{}, true
}

// restoreKickoff puts back the record the row held, for a launch that typed nothing: a session
// that took its kickoff keeps the record of the launch that started it.
func (l *launch) restoreKickoff() {
	// A session an earlier launch left without its kickoff is still without it, and the open
	// record dates the wait from now. A Stop later than the earlier record shows its kickoff
	// ran, so that record goes back with its own time.
	kickoff := l.prior
	if l.prior != nil && l.prior.Open && !l.stoppedSince("", l.prior.At) {
		kickoff = &workers.Kickoff{Text: l.prior.Text, At: l.opts.Clock.Now().UnixMilli(), Open: true, Session: l.prior.Session}
	}
	if err := l.opts.Workers.SetKickoff(l.req.Ticket, l.req.Repo, kickoff); err != nil && !errors.Is(err, workers.ErrNoRow) {
		l.log("%v", err)
	}
}

// stoppedSince reports whether the worker in the tree recorded a Stop of session after the time, in
// milliseconds. An empty session counts a Stop of any session.
func (l *launch) stoppedSince(session string, at int64) bool {
	worker, _, err := l.opts.Workers.OnTree(l.tree)
	if err != nil {
		return false
	}
	stop, ok := worker.Stop(session)
	return ok && stop.At.UnixMilli() > at
}

// deleteKickoff removes the record of a kickoff that failed to type, once its workspace is closed.
func (l *launch) deleteKickoff() {
	if err := l.opts.Workers.SetKickoff(l.req.Ticket, l.req.Repo, nil); err != nil && !errors.Is(err, workers.ErrNoRow) {
		l.log("%v", err)
	}
}

func (l *launch) preflight(ctx context.Context) (Result, bool) {
	waitCtx, cancel := clock.WithTimeout(ctx, l.opts.Clock, "launch preflight", l.opts.PreflightTimeout)
	defer cancel()
	unanswered := func(readErr error) Result {
		return Result{Status: StatusMultiplexerUnavailable, Detail: fmt.Sprintf("%s server not answering: %v", l.opts.Multiplexer.Name(), readErr)}
	}
	for {
		_, err := l.opts.Multiplexer.Read(ctx)
		if err == nil {
			return Result{}, true
		}
		if waitCtx.Err() != nil && ctx.Err() == nil {
			return unanswered(err), false
		}
		l.log("waiting for %s server...", l.opts.Multiplexer.Name())
		if !l.sleep(waitCtx, 5*l.opts.Pace) {
			if ctx.Err() == nil {
				return unanswered(err), false
			}
			return Result{Status: StatusMultiplexerUnavailable, Detail: "interrupted while waiting for the " + l.opts.Multiplexer.Name() + " server"}, false
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
	existed := l.trackBranch(ctx)
	if res := l.lease(ctx); res.Status != "" {
		return res, false
	}
	// The row names the tree before anything is done in it, so a launch that dies here is
	// found by the next one and its tree can be returned.
	if err := l.opts.Workers.RecordTree(l.req.Ticket, l.req.Repo, l.tree); err != nil {
		l.giveBack(ctx)
		return Result{Status: StatusWorkerRowFailed, Detail: err.Error(), Worktree: l.tree}, false
	}
	return l.checkout(ctx, existed)
}

// trackBranch reports whether the branch already exists, locally or on origin,
// creating a local tracking branch for one that exists only there.
func (l *launch) trackBranch(ctx context.Context) bool {
	l.adoptKeyed(ctx)
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

// adoptKeyed moves the request onto the branch keyed to the ticket when no branch has the requested
// name. The world already names that branch, so this covers a launch whose keys the world could not
// read. Without it the launch would start a new, empty branch beside the worker's own.
func (l *launch) adoptKeyed(ctx context.Context) {
	b := l.req.Branch
	if _, err := l.git(ctx, l.repo, "show-ref", "--verify", "--quiet", "refs/heads/"+b); err == nil {
		return
	}
	if _, err := l.git(ctx, l.repo, "ls-remote", "--exit-code", "--heads", "origin", "refs/heads/"+b); err == nil {
		return
	}
	recs, err := stackrecord.Read(ctx, l.opts.Env, l.repo)
	if keyed, ok := recs.BranchOf(l.req.Ticket); err == nil && ok {
		l.log("branch %s does not exist, so the launch uses %s, the branch keyed to the ticket", b, keyed)
		l.req.Branch = keyed
	}
}

// lease reuses the tree already holding this ticket's lease, so a relaunch
// resumes in place. A tree is never adopted just for having the branch checked
// out, since it could be the user's own clone or a pool tree treehouse may hand
// to someone else. If the branch is checked out elsewhere, the switch fails
// loudly. A failure is reported as it is. The next resync launches again.
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

// checkout puts the leased tree on the ticket's branch. A lease this launch took goes back to the pool when
// that fails.
func (l *launch) checkout(ctx context.Context, existed bool) (Result, bool) {
	if l.fresh {
		return l.switchTree(ctx, existed)
	}
	// A lease from an earlier run is already on its branch, unless that run died between leasing
	// the tree and switching it. Such a tree is as the pool handed it out, so until it is on the
	// branch the lease is treated as this launch's own and goes back when the switch fails.
	unused, err := l.unused(ctx)
	if err != nil {
		return Result{Status: StatusWorkerRowFailed, Detail: err.Error(), Worktree: l.tree}, false
	}
	if !unused {
		return Result{}, true
	}
	l.fresh = true
	r, ok := l.switchTree(ctx, existed)
	l.fresh = false
	return r, ok
}

// switchTree switches the tree to the ticket's branch, creating the branch when it is new, and
// claims the branch for the ticket.
func (l *launch) switchTree(ctx context.Context, existed bool) (Result, bool) {
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
		return l.keyBranch(ctx)
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
	l.clearIndexLock(ctx)
	l.giveBack(ctx)
	return Result{Status: StatusSwitchFailed, Detail: fmt.Sprintf("git switch to %s%s failed in leased tree %s (lease returned): %s", req.Branch, from, l.tree, said)}, false
}

// keyBranch writes the ticket branch key on the branch the tree was just switched to, which is how
// every later read finds the ticket's branch whatever name its ticket system reports. A branch keyed
// to another ticket is refused, since one branch carries one ticket's work. A key that cannot be
// read or written is logged and the launch goes on, since the world finds the branch by the id.
func (l *launch) keyBranch(ctx context.Context) (Result, bool) {
	req := l.req
	recs, err := stackrecord.Read(ctx, l.opts.Env, l.repo)
	if err != nil {
		l.log("%v", err)
		return Result{}, true
	}
	switch owner := recs.Tickets[req.Branch]; {
	case strings.EqualFold(owner, req.Ticket):
	case owner != "":
		l.log("branch %s belongs to %s (%s)", req.Branch, owner, l.leaseFate())
		l.giveBack(ctx)
		return Result{Status: StatusBranchTaken, Detail: fmt.Sprintf("branch %s belongs to %s, not %s (%s); `git config --unset branch.%s.dispatchTicket` in %s hands it over", req.Branch, owner, req.Ticket, l.leaseFate(), req.Branch, l.repo)}, false
	default:
		if err := stackrecord.WriteTicket(ctx, l.opts.Env, l.repo, req.Branch, req.Ticket); err != nil {
			l.log("%v", err)
		}
	}
	return Result{}, true
}

// clearIndexLock removes the index.lock a git switch killed by the launch deadline leaves in the
// tree's git directory. Neither git clean nor treehouse return touches it, so the next worker in
// the tree could not commit. Only a tree no worker has run in reaches here, so no worker's git
// can hold the lock.
func (l *launch) clearIndexLock(ctx context.Context) {
	ctx = context.WithoutCancel(ctx)
	dir, err := l.git(ctx, l.tree, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return
	}
	lock := filepath.Join(strings.TrimSpace(dir), "index.lock")
	switch err := os.Remove(lock); {
	case err == nil:
		l.log("removed %s left by the cut-off switch", lock)
	case !os.IsNotExist(err):
		l.log("could not remove %s, the next worker in this tree cannot commit: %v", lock, err)
	}
}

// writeStackRecord fetches the stack parent's branch so the fork point is present locally, then
// records the stack parent and the fork point. It is not retried, since git waits out a lock on the
// clone's config itself. A failed write returns a lease this launch took, like a failed switch.
func (l *launch) writeStackRecord(ctx context.Context) (Result, bool) {
	req := l.req
	// The head may already be local, and the switch that follows fails loudly when it is not.
	_, _ = l.git(ctx, l.repo, "fetch", "--no-tags", "origin", req.Stack.Branch)
	rec := stackrecord.Record{Parent: req.Stack.Ticket, ForkPoint: req.Stack.Head}
	if err := stackrecord.Write(ctx, l.opts.Env, l.repo, req.Branch, rec); err != nil {
		l.log("%v", err)
		l.giveBack(ctx)
		return Result{Status: StatusStackRecordFailed, Detail: fmt.Sprintf("stack record for %s failed in %s (%s): %v", req.Branch, l.repo, l.leaseFate(), err)}, false
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

// unused reports whether the tree is as the pool handed it out, so that nothing a worker made is
// in it: no kickoff record says a harness started here, HEAD is detached at a commit some branch
// or remote holds, no git operation is stopped part way and no file is changed or untracked.
func (l *launch) unused(ctx context.Context) (bool, error) {
	head, err := l.git(ctx, l.tree, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || strings.TrimSpace(head) != "HEAD" || l.midOperation(ctx) {
		return false, nil
	}
	if ran, err := l.opts.Workers.KickoffOf(l.req.Ticket, l.req.Repo); err != nil || ran != nil {
		return false, err
	}
	held, err := l.git(ctx, l.tree, "for-each-ref", "--count=1", "--contains", "HEAD")
	if err != nil || strings.TrimSpace(held) == "" {
		return false, nil
	}
	changed, err := l.git(ctx, l.tree, "status", "--porcelain")
	return err == nil && strings.TrimSpace(changed) == "", nil
}

// midOperation reports whether git stopped part way through a rebase, merge, cherry-pick, revert
// or bisect in the tree, which a tree fresh from the pool never has.
func (l *launch) midOperation(ctx context.Context) bool {
	dir, err := l.git(ctx, l.tree, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return false
	}
	for _, name := range []string{"rebase-merge", "rebase-apply", "MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "BISECT_LOG"} {
		if _, err := os.Stat(filepath.Join(strings.TrimSpace(dir), name)); err == nil {
			return true
		}
	}
	return false
}

// leaseFate says in a failure's detail what giveBack did with the lease.
func (l *launch) leaseFate() string {
	if l.fresh {
		return "lease returned"
	}
	return "lease kept"
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
	if l.workspace != "" && l.opts.Multiplexer.CloseWorkspace(ctx, l.workspace) != nil {
		l.log("workspace %s did not close, keeping the lease on %s", l.workspace, l.tree)
		return
	}
	l.log("returning the lease on %s", l.tree)
	if !l.returnLease(ctx) {
		return
	}
	// The row stays, with no tree, for the next launch to reuse.
	if err := l.opts.Workers.ForgetTree(l.req.Ticket, l.req.Repo); err != nil && !errors.Is(err, workers.ErrNoRow) {
		l.log("%v", err)
	}
}

func (l *launch) returnLease(ctx context.Context) bool {
	if err := l.opts.Treehouse.Return(context.WithoutCancel(ctx), l.repo, l.tree, l.req.Ticket); err != nil {
		l.log("while returning %s to the pool: %v", l.tree, err)
		return false
	}
	return true
}

func (l *launch) openWorkspace(ctx context.Context) (Result, bool) {
	snap, err := l.opts.Multiplexer.Read(ctx)
	if err != nil {
		l.giveBack(ctx)
		return Result{Status: StatusWorkspaceFailed, Detail: fmt.Sprintf("while reading %s for %s: %v", l.opts.Multiplexer.Name(), l.tree, err), Worktree: l.tree}, false
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
	id, err := l.opts.Multiplexer.OpenWorkspace(ctx, herdr.NewWorkspace{Repo: filepath.Dir(strings.TrimSpace(common)), Path: l.tree, Label: l.req.Ticket})
	if err != nil {
		l.giveBack(ctx)
		return Result{Status: StatusWorkspaceFailed, Detail: err.Error(), Worktree: l.tree}, false
	}
	l.workspace = id
	return Result{}, true
}

func (l *launch) startWorker(ctx context.Context, name string) (herdr.Started, Result, bool) {
	started, err := l.opts.Multiplexer.StartWorker(ctx, herdr.Worker{
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
