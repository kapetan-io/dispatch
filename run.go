package dispatch

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/executor"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/lock"
	"github.com/kapetan-io/dispatch/internal/loop"
	"github.com/kapetan-io/dispatch/internal/rules"
	"github.com/kapetan-io/dispatch/internal/world"
)

// Clock owns every timer the loop sets and stamps ingest times. Tests swap in a
// fake to fire a resync without waiting for one.
type Clock = loop.Clock

// Timer fires once on C unless stopped.
type Timer = loop.Timer

// lockName is the file whose lock allows one `dispatch run` per state directory.
const lockName = "dispatch.lock"

// startupDeadline bounds the one Linear read that runs before the loop starts.
const startupDeadline = 30 * time.Second

// exitError is a failure that ends the process with a code other than 1.
type exitError struct {
	code int
	err  error
}

func (e exitError) Error() string { return e.err.Error() }
func (e exitError) Unwrap() error { return e.err }

// ExitCode is the exit code a failed command ends the process with: 3 when a
// command that needs the running loop found none, and 1 otherwise.
func ExitCode(err error) int {
	var e exitError
	if errors.As(err, &e) {
		return e.code
	}
	return 1
}

func notRunning() error {
	return exitError{code: loop.CodeNotRunning, err: errors.New("not running")}
}

// keyedLinear is the Linear workspace of the key found the way `run` finds it. A
// 401 drops the key, so a rotated key is picked up by running api_key_command again.
type keyedLinear struct {
	mu   sync.Mutex
	opts Options
	cfg  config.Config
	keys *keyLookup
	cur  linear.Workspace
}

func (k *keyedLinear) workspace(ctx context.Context) (linear.Workspace, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.cur != nil {
		return k.cur, nil
	}
	key, err := k.keys.find(ctx, k.opts, k.cfg)
	if err != nil {
		return nil, err
	}
	k.cur = k.opts.Linear(key)
	return k.cur, nil
}

func (k *keyedLinear) rotated(err error) {
	if errors.Is(err, linear.ErrUnauthorized) {
		k.reset()
	}
}

func (k *keyedLinear) reset() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.cur, k.keys.fromCommand = nil, ""
}

func (k *keyedLinear) ManagedTickets(ctx context.Context, label string, teams []string) ([]linear.Ticket, error) {
	ws, err := k.workspace(ctx)
	if err != nil {
		return nil, err
	}
	out, err := ws.ManagedTickets(ctx, label, teams)
	k.rotated(err)
	return out, err
}

func (k *keyedLinear) LeasedTickets(ctx context.Context, label string, teams, ids []string) ([]linear.Ticket, error) {
	ws, err := k.workspace(ctx)
	if err != nil {
		return nil, err
	}
	out, err := ws.LeasedTickets(ctx, label, teams, ids)
	k.rotated(err)
	return out, err
}

func (k *keyedLinear) ParentChildren(ctx context.Context, parents []string) (map[string][]linear.Ticket, error) {
	ws, err := k.workspace(ctx)
	if err != nil {
		return nil, err
	}
	out, err := ws.ParentChildren(ctx, parents)
	k.rotated(err)
	return out, err
}

func (k *keyedLinear) StateID(ctx context.Context, team, name string) (string, error) {
	ws, err := k.workspace(ctx)
	if err != nil {
		return "", err
	}
	out, err := ws.StateID(ctx, team, name)
	k.rotated(err)
	return out, err
}

func (k *keyedLinear) Move(ctx context.Context, ticketID, stateID string) error {
	ws, err := k.workspace(ctx)
	if err != nil {
		return err
	}
	err = ws.Move(ctx, ticketID, stateID)
	k.rotated(err)
	return err
}

func (k *keyedLinear) Comment(ctx context.Context, ticketID, body string) error {
	ws, err := k.workspace(ctx)
	if err != nil {
		return err
	}
	err = ws.Comment(ctx, ticketID, body)
	k.rotated(err)
	return err
}

func (k *keyedLinear) Viewer(ctx context.Context) (linear.Viewer, error) {
	ws, err := k.workspace(ctx)
	if err != nil {
		return linear.Viewer{}, err
	}
	out, err := ws.Viewer(ctx)
	k.rotated(err)
	return out, err
}

func (k *keyedLinear) Usage() linear.Stats {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.cur == nil {
		return linear.Stats{}
	}
	return k.cur.Usage()
}

// runCommand starts the reconcile loop and runs it until ctx ends. It fails to start
// on a bad config key, rules that don't load, a lock another run holds, and a history
// that can't be written.
func runCommand(ctx context.Context, opts Options) error {
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return err
	}
	if cfg.Dispatch.Resync <= 0 {
		return fmt.Errorf("[dispatch].resync must be positive, got %s", time.Duration(cfg.Dispatch.Resync))
	}
	dir, err := rulesDir(opts, "", &cfg)
	if err != nil {
		return err
	}
	loaded, err := rules.Load(dir, rulesEntry, rules.Timeouts(opts.RuleTimeout))
	if err != nil {
		var lerr *rules.LoadError
		if errors.As(err, &lerr) {
			for _, p := range lerr.Problems {
				_, _ = fmt.Fprintln(opts.Stderr, "finding:", p)
			}
			return fmt.Errorf("%d problems found in the rules, so none loaded", len(lerr.Problems))
		}
		return err
	}
	if err := os.MkdirAll(opts.StateDir, 0o700); err != nil {
		return fmt.Errorf("while creating state dir: %w", err)
	}
	l, err := lock.Acquire(filepath.Join(opts.StateDir, lockName))
	if errors.Is(err, lock.ErrHeld) {
		return fmt.Errorf("another dispatch run holds the lock on %s", opts.StateDir)
	}
	if err != nil {
		return err
	}
	defer func() { _ = l.Release() }()

	text, err := os.ReadFile(opts.ConfigPath)
	if err != nil {
		return err
	}
	lin := &keyedLinear{opts: opts, cfg: cfg, keys: &keyLookup{}}
	// No key, or a key Linear rejects, can't be fixed by waiting. A network that is down at
	// boot can, and the loop reads Linear again at every resync.
	// Ctrl-C while the key is still being read is a clean stop, as it is once the loop runs.
	if _, err := lin.workspace(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	// Linear can accept the connection and stop answering, and nothing else has started yet.
	viewCtx, cancelView := context.WithTimeout(ctx, startupDeadline)
	viewer, err := lin.Viewer(viewCtx)
	cancelView()
	if errors.Is(err, linear.ErrUnauthorized) {
		return err
	}
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		_, _ = fmt.Fprintf(opts.Stderr, "dispatch: while asking Linear who owns the key: %v\n", err)
	}
	exec := executor.New(executor.Env{
		Config: cfg, Linear: func() linear.Workspace { return lin }, Herdr: opts.Herdr, Treehouse: opts.Treehouse,
		PullRequests: opts.PullRequests, Launcher: launcherFor(opts, cfg.Plugins.Herdr.PluginRoot, nil), Alerts: opts.Alerts,
		StateDir: opts.StateDir, Stderr: opts.Stderr, LaunchDeadline: opts.LaunchTimeout, Deadline: opts.ActionTimeout,
	})
	return loop.New(loop.Options{
		Config: cfg, ConfigText: string(text), StateDir: opts.StateDir, Rules: loaded, Clock: opts.Clock,
		Sources: world.Sources{
			Linear: lin, Herdr: opts.Herdr, Treehouse: opts.Treehouse, PullRequests: opts.PullRequests,
			StateDir: opts.StateDir, Stderr: opts.Stderr,
		},
		Executor: exec, Herdr: opts.Herdr, Alerts: opts.Alerts, Usage: lin.Usage, Unauthorized: lin.reset,
		Viewer: viewer.Name, Out: opts.Stdout, Err: opts.Stderr,
	}).Run(ctx)
}

const defaultTickTimeout = 2 * time.Minute

// tickCommand asks the running loop to re-read every source and waits for it to
// settle. It never runs a cycle itself, which would make a second writer.
func tickCommand(ctx context.Context, opts Options, args []string) error {
	fs := flag.NewFlagSet("tick", flag.ContinueOnError)
	fs.SetOutput(opts.Stderr)
	timeout := fs.Duration("timeout", defaultTickTimeout, "how long to wait for the loop to settle")
	var ticket string
	var flags []string
	for i := 0; i < len(args); i++ {
		switch {
		case strings.HasPrefix(args[i], "-"):
			flags = append(flags, args[i])
			if !strings.Contains(args[i], "=") && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		case ticket == "":
			ticket = strings.ToUpper(args[i])
		default:
			_, _ = fmt.Fprintln(opts.Stderr, usage)
			return errors.New("tick takes at most one ticket")
		}
	}
	if err := fs.Parse(flags); err != nil {
		return err
	}
	tctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	resp, err := loop.Ask(tctx, opts.StateDir, loop.Request{Op: loop.OpTick, Ticket: ticket})
	switch {
	case errors.Is(err, loop.ErrNotRunning):
		return notRunning()
	case err != nil && ctx.Err() == nil && (tctx.Err() != nil || errors.Is(err, os.ErrDeadlineExceeded)):
		askCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		pending, perr := loop.Ask(askCtx, opts.StateDir, loop.Request{Op: loop.OpPending})
		if perr == nil {
			for _, line := range pending.Lines {
				_, _ = fmt.Fprintln(opts.Stdout, line)
			}
		}
		return fmt.Errorf("timed out after %s waiting for the loop to settle. What is pending keeps going", *timeout)
	case err != nil:
		return err
	}
	for _, line := range resp.Lines {
		_, _ = fmt.Fprintln(opts.Stdout, line)
	}
	if resp.Code != loop.CodeSettled {
		return errors.New(resp.Error)
	}
	return nil
}

// tellCommand types text into a ticket's worker through the running loop.
func tellCommand(ctx context.Context, opts Options, ticket, text string) error {
	resp, err := loop.Ask(ctx, opts.StateDir, loop.Request{Op: loop.OpTell, Ticket: ticket, Text: text})
	if errors.Is(err, loop.ErrNotRunning) {
		return notRunning()
	}
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	for _, line := range resp.Lines {
		_, _ = fmt.Fprintln(opts.Stdout, line)
	}
	return nil
}

// planCommand prints what the latest cycle wants and holds, from history.
func planCommand(opts Options) error {
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return err
	}
	rows, err := loop.Plan(cfg.History.Dir)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(opts.Stdout, 0, 0, 2, ' ', 0)
	for _, r := range rows {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.State, r.Subject, r.Action, r.Rule, r.Detail)
	}
	return tw.Flush()
}
