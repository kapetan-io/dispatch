package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

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

// Request is one ticket to start a worker for.
type Request struct {
	Ticket string
	// Title is the ticket's title. The worker's tab is labeled "<Ticket>
	// <Title>", or just <Ticket> when it is empty.
	Title  string
	Branch string
	// Base is the ref a new branch starts from. Defaults to main.
	Base string
	// Model is the Claude model the worker runs. Defaults to sonnet.
	Model string
	// Kickoff is the first command typed into the worker. Defaults to
	// "/dispatch:build <Ticket>".
	Kickoff string
}

// Options names the tools to drive and the places to run them. Zero values
// take the defaults.
type Options struct {
	// RepoDir is where the launch starts looking for the target repo. Defaults
	// to the working directory.
	RepoDir string
	// PluginRoot holds scripts/shims and is handed to the worker as its plugin.
	// It has no default.
	PluginRoot string
	// Herdr and Git are the commands to run.
	Herdr string
	Git   string
	// Treehouse is the pool to lease from. Defaults to the installed treehouse.
	Treehouse treehouse.Pool
	// Stderr receives progress. Defaults to discarding it.
	Stderr io.Writer
	// Trace, when set, is a file that every screen the kickoff step reads is
	// appended to.
	Trace string
	// PreflightTimeout bounds the wait for the herdr server. Defaults to 60s.
	PreflightTimeout time.Duration
	// Pace is the unit of waiting between herdr polls. The server wait uses 5 and
	// the agent restart 3. Defaults to 1s.
	Pace time.Duration
}

func (o Options) withDefaults() Options {
	if o.Herdr == "" {
		o.Herdr = "herdr"
	}
	if o.Treehouse == nil {
		o.Treehouse = treehouse.NewCLI("treehouse")
	}
	if o.Git == "" {
		o.Git = "git"
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

// Run leases a treehouse tree for the ticket, opens a herdr workspace on it and
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
// folder-trust dialogs, and confirming the kickoff landed.
func Run(ctx context.Context, req Request, opts Options) Result {
	l := &launch{req: req, opts: opts.withDefaults()}
	return l.run(ctx)
}

type launch struct {
	req  Request
	opts Options

	repo      string
	tree      string
	workspace string
	pane      string
	agent     string
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
	if req.Base == "" {
		req.Base = "main"
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

	top, err := l.git(ctx, l.opts.RepoDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return Result{Status: StatusNotARepo, Detail: "run from inside the target repo"}
	}
	l.repo = strings.TrimSpace(top)
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
	if r, ok := l.startAgent(ctx, workerName); !ok {
		return r
	}

	// An unconfirmed kickoff closes the workspace. Left open, its idle Claude
	// would look like a live worker, and no tick would retry or escalate.
	if !l.inject(ctx) {
		_ = l.herdr(context.WithoutCancel(ctx), "workspace", "close", l.workspace)
		return Result{
			Status:    StatusInjectionUnconfirmed,
			Detail:    "Claude started but herdr never showed the kickoff running, so the workspace was closed and the next tick retries. Record the screens with Options.Trace (DISPATCH_LAUNCH_TRACE for `dispatch launch`).",
			Workspace: l.workspace, Pane: l.pane, Worktree: l.tree,
		}
	}
	return Result{
		Status: StatusOK, Ticket: req.Ticket, Branch: req.Branch, Worktree: l.tree,
		Workspace: l.workspace, Pane: l.pane, Agent: l.agent, Kickoff: req.Kickoff,
	}
}

func (l *launch) preflight(ctx context.Context) (Result, bool) {
	deadline := time.Now().Add(l.opts.PreflightTimeout)
	for l.herdr(ctx, "workspace", "list") != nil {
		if !time.Now().Before(deadline) {
			return Result{Status: StatusHerdrUnavailable, Detail: "herdr server not answering"}, false
		}
		l.log("waiting for herdr server...")
		if !l.sleep(ctx, 5*l.opts.Pace) {
			return Result{Status: StatusHerdrUnavailable, Detail: "interrupted while waiting for the herdr server"}, false
		}
	}
	return Result{}, true
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
	if existed {
		switchErr = l.gitToStderr(ctx, l.tree, "switch", req.Branch)
	} else {
		_, _ = l.git(ctx, l.repo, "fetch", "--no-tags", "origin", req.Base)
		switchErr = l.gitToStderr(ctx, l.tree, "switch", "--no-track", "-c", req.Branch, "origin/"+req.Base)
		if switchErr != nil {
			switchErr = l.gitToStderr(ctx, l.tree, "switch", "-c", req.Branch, req.Base)
		}
	}
	if switchErr != nil {
		l.log("checkout of %s failed in leased tree, returning the lease", req.Branch)
		l.returnLease(ctx)
		return Result{Status: StatusCheckoutFailed, Detail: fmt.Sprintf("git switch to %s failed in leased tree %s (lease returned)", req.Branch, l.tree)}, false
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
	if l.workspace != "" && l.herdr(ctx, "workspace", "close", l.workspace) != nil {
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

type pane struct {
	PaneID      string `json:"pane_id"`
	WorkspaceID string `json:"workspace_id"`
	TabID       string `json:"tab_id"`
	Agent       string `json:"agent"`
}

func (l *launch) panes(ctx context.Context) []pane {
	var r struct {
		Result struct {
			Panes []pane `json:"panes"`
		} `json:"result"`
	}
	_ = l.herdrJSON(ctx, &r, "pane", "list")
	return r.Result.Panes
}

func (l *launch) openWorkspace(ctx context.Context) (Result, bool) {
	var list struct {
		Result struct {
			Workspaces []struct {
				WorkspaceID string `json:"workspace_id"`
				Worktree    *struct {
					CheckoutPath string `json:"checkout_path"`
				} `json:"worktree"`
			} `json:"workspaces"`
		} `json:"result"`
	}
	_ = l.herdrJSON(ctx, &list, "workspace", "list")
	for _, w := range list.Result.Workspaces {
		if w.Worktree != nil && w.Worktree.CheckoutPath == l.tree {
			l.workspace = w.WorkspaceID
			break
		}
	}

	if l.workspace != "" {
		for _, p := range l.panes(ctx) {
			if p.WorkspaceID == l.workspace && p.Agent == "claude" {
				return Result{
					Status:    StatusAlreadyRunning,
					Detail:    fmt.Sprintf("workspace %s already has a Claude pane (%s); not typing a second kickoff", l.workspace, p.PaneID),
					Workspace: l.workspace, Pane: p.PaneID, Worktree: l.tree,
				}, false
			}
		}
	}

	// The checkout opens as a worktree so its workspace nests under the repo in
	// herdr's sidebar. There is no bare-workspace fallback, since herdr reports
	// a bare workspace with no worktree path, so the dispatcher could never find
	// its worker and would launch another one on every tick.
	if l.workspace == "" {
		// herdr refuses a linked worktree as the source, so name the main
		// checkout even when the launcher runs from inside a pool tree.
		common, _ := l.git(ctx, l.repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
		mainCheckout := filepath.Dir(strings.TrimSpace(common))
		out, _ := l.herdrCombined(ctx, "worktree", "open", "--cwd", mainCheckout, "--path", l.tree, "--label", l.req.Ticket, "--no-focus", "--json")
		var opened struct {
			Result struct {
				Worktree struct {
					OpenWorkspaceID string `json:"open_workspace_id"`
				} `json:"worktree"`
				Workspace struct {
					WorkspaceID string `json:"workspace_id"`
				} `json:"workspace"`
			} `json:"result"`
		}
		_ = json.Unmarshal([]byte(out), &opened)
		l.workspace = opened.Result.Worktree.OpenWorkspaceID
		if l.workspace == "" {
			l.workspace = opened.Result.Workspace.WorkspaceID
		}
		if l.workspace == "" {
			l.giveBack(ctx)
			return Result{Status: StatusWorkspaceFailed, Detail: fmt.Sprintf("herdr worktree open failed for %s: %s", l.tree, out), Worktree: l.tree}, false
		}
	}

	// The shim dir carries a `claude` wrapper that re-adds the plugin and worker
	// flags when herdr restarts it with a bare `claude --resume`, since herdr
	// does not replay agent argv. worktree open carries no --env but tab create
	// does, so the worker runs in a fresh stamped tab and the agent-less tabs
	// the workspace opened with are closed.
	shims := filepath.Join(l.opts.PluginRoot, "scripts", "shims")
	out, _ := l.herdrCombined(ctx, "tab", "create", "--workspace", l.workspace, "--cwd", l.tree, "--label", l.tabLabel(),
		"--env", "PATH="+shims+":"+os.Getenv("PATH"),
		"--env", "AUTONOMOUS_WORKER=1",
		"--env", "DISPATCH_PLUGIN_ROOT="+l.opts.PluginRoot,
		"--no-focus")
	var created struct {
		Result struct {
			RootPane struct {
				PaneID string `json:"pane_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	_ = json.Unmarshal([]byte(out), &created)
	l.pane = created.Result.RootPane.PaneID
	if l.pane == "" {
		l.giveBack(ctx)
		return Result{Status: StatusWorkspaceFailed, Detail: fmt.Sprintf("herdr tab create failed in %s: %s", l.workspace, out), Workspace: l.workspace, Worktree: l.tree}, false
	}
	stale := map[string]bool{}
	for _, p := range l.panes(ctx) {
		if p.WorkspaceID == l.workspace && p.PaneID != l.pane && p.Agent == "" {
			stale[p.TabID] = true
		}
	}
	tabs := make([]string, 0, len(stale))
	for id := range stale {
		if id != "" {
			tabs = append(tabs, id)
		}
	}
	sort.Strings(tabs)
	for _, id := range tabs {
		_ = l.herdr(ctx, "tab", "close", id)
	}
	return Result{}, true
}

type agentInfo struct {
	Name   string `json:"name"`
	PaneID string `json:"pane_id"`
	Agent  string `json:"agent"`
	Status string `json:"agent_status"`
}

func (l *launch) agents(ctx context.Context) []agentInfo {
	var r struct {
		Result struct {
			Agents []agentInfo `json:"agents"`
		} `json:"result"`
	}
	_ = l.herdrJSON(ctx, &r, "agent", "list")
	return r.Result.Agents
}

func (l *launch) paneHasClaude(ctx context.Context) bool {
	for _, a := range l.agents(ctx) {
		if a.PaneID == l.pane && a.Agent == "claude" {
			return true
		}
	}
	return false
}

var shellPrompt = regexp.MustCompile(`[>❯$%]`)

func (l *launch) startAgent(ctx context.Context, workerName string) (Result, bool) {
	start := func() bool {
		l.agent = workerName
		for _, a := range l.agents(ctx) {
			if a.Name == l.agent {
				l.agent = fmt.Sprintf("%s-r%d", workerName, rand.IntN(32768))
				break
			}
		}
		// A fresh pane rejects agent start until its shell has drawn a prompt.
		for try := 1; try <= 15; try++ {
			if out, err := l.herdrOut(ctx, "pane", "read", l.pane); err == nil && shellPrompt.MatchString(out) {
				break
			}
			l.log("pane %s shell not ready yet (%d/15)", l.pane, try)
			l.sleep(ctx, l.opts.Pace)
		}
		out, err := l.herdrCombined(ctx, "agent", "start", l.agent, "--kind", "claude", "--pane", l.pane, "--timeout", "90000", "--",
			"--model", l.req.Model, "--plugin-dir", l.opts.PluginRoot,
			"--disallowedTools", "EnterPlanMode,ExitPlanMode", "--dangerously-skip-permissions")
		if err != nil {
			l.log("agent start failed: %s", out)
			return false
		}
		return true
	}
	// A timed-out start may still bring Claude up, so retry only into a pane
	// that has no Claude agent.
	ok := start()
	if !ok {
		l.sleep(ctx, 3*l.opts.Pace)
		ok = l.paneHasClaude(ctx) || start()
	}
	if !ok && !l.paneHasClaude(ctx) {
		l.giveBack(ctx)
		return Result{Status: StatusAgentStartFailed, Detail: "herdr agent start failed in pane " + l.pane, Workspace: l.workspace, Pane: l.pane, Worktree: l.tree}, false
	}
	return Result{}, true
}

// inject types the kickoff and reports whether it landed.
//
// A submitted kickoff is confirmed by herdr's agent status leaving idle, never
// by finding its text on screen. While a skill runs, Claude's screen shows only
// the spinner and an empty input box, and a text probe misread that as a lost
// kickoff and typed it again. The kickoff is retyped only while the agent is
// still idle with the text gone from its input box.
func (l *launch) inject(ctx context.Context) bool {
	kickoff := l.req.Kickoff
	// A prefix is enough to find the typed kickoff, which Claude may wrap.
	probe := kickoff
	if r := []rune(kickoff); len(r) > 24 {
		probe = string(r[:24])
	}
	visible := func() string {
		out, _ := l.herdrOut(ctx, "agent", "read", l.pane, "--source", "visible", "--lines", "30")
		return strings.ReplaceAll(out, "\n", " ")
	}
	_ = l.herdr(ctx, "agent", "wait", l.pane, "--until", "idle", "--timeout", "15000")
	ready := false
	consentTry := 0
	for range 30 {
		screen := visible()
		l.trace("dialogs", screen)
		lower := strings.ToLower(screen)
		// One-time bypass-permissions consent. Match the option text, never
		// "bypass permissions", which the status line shows forever after.
		if strings.Contains(lower, "yes, i accept") {
			if consentTry%2 == 0 {
				_ = l.herdr(ctx, "pane", "send-keys", l.pane, "Down", "Enter")
			} else {
				_ = l.herdr(ctx, "pane", "send-keys", l.pane, "2")
				_ = l.herdr(ctx, "pane", "send-keys", l.pane, "Enter")
			}
			consentTry++
			l.sleep(ctx, l.opts.Pace)
			continue
		}
		// First-run folder trust. The cursor may default to "No, exit".
		if strings.Contains(lower, "trust this folder") {
			if cursorOnNo.MatchString(screen) {
				_ = l.herdr(ctx, "pane", "send-keys", l.pane, "Down")
			}
			_ = l.herdr(ctx, "pane", "send-keys", l.pane, "Enter")
			l.sleep(ctx, l.opts.Pace)
			continue
		}
		if strings.Contains(screen, "❯") {
			ready = true
			break
		}
		l.sleep(ctx, l.opts.Pace)
	}
	if !ready {
		return false
	}
	wait := func() bool {
		return l.herdr(ctx, "agent", "wait", l.pane, "--until", "working", "--until", "blocked", "--timeout", "15000") == nil
	}
	for try := 1; try <= 3; try++ {
		l.trace("typing", fmt.Sprintf("%s (try %d)", kickoff, try))
		_ = l.herdr(ctx, "pane", "run", l.pane, kickoff)
		_ = l.herdr(ctx, "pane", "send-keys", l.pane, "Enter")
		if wait() {
			return true
		}
		screen := visible()
		l.trace("not-working", screen)
		// Retype only while the agent sits idle. done means a fast kickoff
		// already ran, and working or blocked means it is running, so never type
		// it again.
		var status string
		for _, a := range l.agents(ctx) {
			if a.PaneID == l.pane {
				status = a.Status
				break
			}
		}
		switch status {
		case "idle": // fall through to the retype
		case "working", "blocked", "done":
			return true
		default:
			return false
		}
		// Typed but never submitted, so press Enter rather than type it twice.
		if strings.Contains(screen, probe) {
			_ = l.herdr(ctx, "pane", "send-keys", l.pane, "Enter")
			return wait()
		}
	}
	return false
}

var cursorOnNo = regexp.MustCompile(`(?i)❯ *(no|don.t)`)

func (l *launch) trace(label, body string) {
	if l.opts.Trace == "" {
		return
	}
	f, err := os.OpenFile(l.opts.Trace, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "=== %s %s\n%s\n", time.Now().Format("15:04:05"), label, body)
}

func (l *launch) sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

func (l *launch) herdr(ctx context.Context, args ...string) error {
	return exec.CommandContext(ctx, l.opts.Herdr, args...).Run()
}

func (l *launch) herdrOut(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, l.opts.Herdr, args...).Output()
	return string(out), err
}

func (l *launch) herdrCombined(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, l.opts.Herdr, args...).CombinedOutput()
	return strings.TrimRight(string(out), "\n"), err
}

func (l *launch) herdrJSON(ctx context.Context, into any, args ...string) error {
	out, err := l.herdrOut(ctx, args...)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(out), into)
}

func (l *launch) git(ctx context.Context, dir string, args ...string) (string, error) {
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	out, err := exec.CommandContext(ctx, l.opts.Git, args...).Output()
	return string(out), err
}

func (l *launch) gitToStderr(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, l.opts.Git, append([]string{"-C", dir}, args...)...)
	cmd.Stdout, cmd.Stderr = l.opts.Stderr, l.opts.Stderr
	return cmd.Run()
}

// tabLabel is what the tick's tab rename sets, so the first tick leaves a
// freshly launched tab alone.
func (l *launch) tabLabel() string {
	return reconcile.TabLabel(linear.Ticket{Identifier: l.req.Ticket, Title: l.req.Title})
}
