package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/reconcile"
	"github.com/kapetan-io/dispatch/internal/treehouse"
)

// Statuses Run reports besides the ones in launcher.go.
const (
	StatusPoolError            = "pool_error"
	StatusHerdrUnavailable     = "herdr_unavailable"
	StatusUsageError           = "usage_error"
	StatusNotARepo             = "not_a_repo"
	StatusNotPooled            = "not_pooled"
	StatusCheckoutFailed       = "checkout_failed"
	StatusWorkspaceFailed      = "workspace_failed"
	StatusAgentStartFailed     = "agent_start_failed"
	StatusInjectionUnconfirmed = "injection_unconfirmed"
)

// Options names the tools a Local launcher drives and the places it runs them.
// Zero values take the defaults.
type Options struct {
	// PluginRoot holds scripts/shims and is handed to the worker as its plugin.
	// It has no default.
	PluginRoot string
	// Herdr hosts the worker. Defaults to the installed herdr.
	Herdr herdr.Herdr
	// Treehouse is the pool to lease from. Defaults to the installed treehouse.
	Treehouse treehouse.Pool
	// Stderr receives progress. Defaults to discarding it.
	Stderr io.Writer
	// PreflightTimeout bounds the wait for the herdr server. Defaults to 60s.
	PreflightTimeout time.Duration
	// Pace is the unit of waiting between polls for the herdr server, which waits 5 units.
	// Defaults to 1s.
	Pace time.Duration
}

func (o Options) withDefaults() Options {
	if o.Herdr == nil {
		o.Herdr = herdr.NewCLI("herdr")
	}
	if o.Treehouse == nil {
		o.Treehouse = treehouse.NewCLI("treehouse")
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
	return o
}

// Local is the Launcher that leases a tree from the pool on this machine and
// starts the worker in the herdr that hosts it.
type Local struct {
	opts Options
}

// New returns a Launcher over the tools in opts.
func New(opts Options) *Local { return &Local{opts: opts.withDefaults()} }

// Launch leases a treehouse tree for the ticket, opens a herdr workspace on it and
// starts a Claude worker that is typed the kickoff. The Result says what
// happened, and progress goes to Options.Stderr.
//
// pool_exhausted is proven, never assumed. After a lease failure the pool is
// read again and exhaustion is reported only when no tree is available and the
// pool is at max_trees. Every other lease failure is pool_error, a fault to
// diagnose rather than a queue to wait in.
//
// The herdr handling depends on nested worktree workspaces, an env-stamped tab,
// waiting for the shell before starting the agent, answering the consent and
// folder-trust dialogs, and confirming the kickoff landed. The herdr adapter
// does all of it behind OpenWorkspace, StartWorker and Kickoff.
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
	// fresh is set when this run took the lease, as opposed to finding one from
	// an earlier run.
	fresh bool
}

func (l *launch) log(format string, args ...any) {
	_, _ = fmt.Fprintf(l.opts.Stderr, "dispatch launch: "+format+"\n", args...)
}

func (l *launch) run(ctx context.Context) Result {
	req := &l.req
	if req.Ticket == "" || req.Branch == "" {
		l.log("usage: dispatch launch <TICKET> <BRANCH> [--base REF] [--model NAME] [--kickoff TEXT] [--title TEXT]")
		return Result{Status: StatusUsageError, Detail: "TICKET and BRANCH are required"}
	}
	// Without it the worker starts with no dispatch plugin and no shims.
	if l.opts.PluginRoot == "" {
		return Result{Status: StatusUsageError, Detail: "no plugin root: set DISPATCH_PLUGIN_ROOT or plugin_root in the config"}
	}
	if req.Model == "" {
		req.Model = "sonnet"
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
	if req.Base == "" {
		req.Base = l.defaultBranch(ctx)
	}
	if _, err := os.Stat(filepath.Join(l.repo, "treehouse.toml")); err != nil {
		return Result{Status: StatusNotPooled, Detail: l.repo + " has no treehouse.toml. Workers only run on leased pool trees, so add one (treehouse init) before approving tickets in this repo."}
	}

	if r, ok := l.preflight(ctx); !ok {
		return r
	}
	existed := l.trackBranch(ctx)
	if r, ok := l.leaseTree(ctx, existed); !ok {
		return r
	}
	if r, ok := l.openWorkspace(ctx); !ok {
		return r
	}
	started, r, ok := l.startWorker(ctx, workerName)
	if !ok {
		return r
	}

	// An unconfirmed kickoff closes the workspace. Left open, its idle Claude
	// would look like a live worker, and no tick would retry or escalate.
	if err := l.opts.Herdr.Kickoff(ctx, started.Pane, req.Kickoff); err != nil {
		_ = l.opts.Herdr.CloseWorkspace(context.WithoutCancel(ctx), l.workspace)
		detail := "Claude started but herdr"
		switch {
		case errors.Is(err, herdr.ErrEnterNotSent):
			detail = "Claude never started, because its launch command still showed at the shell prompt and herdr could not send Enter to it, and herdr"
		case errors.Is(err, herdr.ErrClaudeNeverStarted):
			detail = "Claude never started, because its launch command still showed at the shell prompt after Enter was pressed on it, and herdr"
		}
		return Result{
			Status:    StatusInjectionUnconfirmed,
			Detail:    detail + " never showed the kickoff running, so the workspace was closed and the next tick retries. Record the screens with DISPATCH_LAUNCH_TRACE (`dispatch launch`).",
			Workspace: l.workspace, Pane: started.Pane, Worktree: l.tree,
		}
	}
	return Result{
		Status: StatusOK, Ticket: req.Ticket, Branch: req.Branch, Worktree: l.tree,
		Workspace: l.workspace, Pane: started.Pane, Agent: started.Agent, Kickoff: req.Kickoff,
	}
}

func (l *launch) preflight(ctx context.Context) (Result, bool) {
	deadline := time.Now().Add(l.opts.PreflightTimeout)
	for {
		if _, err := l.opts.Herdr.Read(ctx); err == nil {
			return Result{}, true
		}
		if !time.Now().Before(deadline) {
			return Result{Status: StatusHerdrUnavailable, Detail: "herdr server not answering"}, false
		}
		l.log("waiting for herdr server...")
		if !l.sleep(ctx, 5*l.opts.Pace) {
			return Result{Status: StatusHerdrUnavailable, Detail: "interrupted while waiting for the herdr server"}, false
		}
	}
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
// loudly.
func (l *launch) leaseTree(ctx context.Context, existed bool) (Result, bool) {
	req := l.req
	trees, _ := l.opts.Treehouse.Trees(ctx, l.repo)
	for _, t := range trees {
		if t.LeaseHolder == req.Ticket {
			l.tree = t.Path
			return Result{}, true
		}
	}

	l.log("leasing a treehouse tree for %s", req.Ticket)
	leased, err := l.opts.Treehouse.Lease(ctx, l.repo, req.Ticket)
	if err != nil {
		if errors.Is(err, treehouse.ErrNoPath) {
			return Result{Status: StatusPoolError, Detail: err.Error()}, false
		}
		if err.Error() != "" {
			l.log("treehouse get: %v", err)
		}
		return l.leaseFailure(ctx, err.Error()), false
	}
	l.tree = leased.Path
	l.log("leased %s", l.tree)
	l.fresh = true

	var switchErr error
	from := ""
	if existed {
		switchErr = l.gitToStderr(ctx, l.tree, "switch", req.Branch)
	} else {
		from = " from " + req.Base
		_, _ = l.git(ctx, l.repo, "fetch", "--no-tags", "origin", req.Base)
		switchErr = l.gitToStderr(ctx, l.tree, "switch", "--no-track", "-c", req.Branch, "origin/"+req.Base)
		if switchErr != nil {
			switchErr = l.gitToStderr(ctx, l.tree, "switch", "-c", req.Branch, req.Base)
		}
	}
	if switchErr != nil {
		l.log("checkout of %s failed in leased tree, returning the lease", req.Branch)
		l.returnLease(ctx)
		return Result{Status: StatusCheckoutFailed, Detail: fmt.Sprintf("git switch to %s%s failed in leased tree %s (lease returned)", req.Branch, from, l.tree)}, false
	}
	return Result{}, true
}

var maxTrees = regexp.MustCompile(`(?m)^max_trees *= *([0-9]+)`)

func (l *launch) leaseFailure(ctx context.Context, leaseErr string) Result {
	trees, _ := l.opts.Treehouse.Trees(ctx, l.repo)
	limit := -1
	if toml, err := os.ReadFile(filepath.Join(l.repo, "treehouse.toml")); err == nil {
		if m := maxTrees.FindSubmatch(toml); m != nil {
			limit, _ = strconv.Atoi(string(m[1]))
		}
	}
	var avail int
	var holders []string
	for _, t := range trees {
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
	return Result{Status: StatusPoolError, Detail: fmt.Sprintf("treehouse lease failed but the pool is not exhausted (%d of %d available, max_trees=%d; holders: %s). treehouse stderr: %s", avail, len(trees), limit, list, leaseErr)}
}

// giveBack closes the workspace and returns the lease when this run took the
// lease, so a failed launch holds no pool slot. Any Claude in the workspace
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
		for _, a := range snap.Agents {
			if a.WorkspaceID == l.workspace && a.Kind == "claude" {
				return Result{
					Status:    StatusAlreadyRunning,
					Detail:    fmt.Sprintf("workspace %s already has a Claude pane (%s); not typing a second kickoff", l.workspace, a.PaneID),
					Workspace: l.workspace, Pane: a.PaneID, Worktree: l.tree,
				}, false
			}
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
		Label:      l.tabLabel(),
		Name:       name,
		Model:      l.req.Model,
		PluginRoot: l.opts.PluginRoot,
	})
	if err == nil {
		return started, Result{}, true
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
	out, err := exec.CommandContext(ctx, "git", args...).Output()
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

func (l *launch) gitToStderr(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Stdout, cmd.Stderr = l.opts.Stderr, l.opts.Stderr
	return cmd.Run()
}

// tabLabel is what the tick's tab rename sets, so the first tick leaves a
// freshly launched tab alone.
func (l *launch) tabLabel() string {
	return reconcile.TabLabel(linear.Ticket{Identifier: l.req.Ticket, Title: l.req.Title})
}
