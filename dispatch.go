// Package dispatch is the stateless dispatcher. Each tick reads what Linear
// says should happen, compares it with the herdr workspaces and treehouse
// trees that exist, and fixes the difference. `dispatch run` ticks on an
// interval in the foreground, in a herdr window the user watches, starts and
// stops.
package dispatch

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/cache"
	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/launcher"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/lock"
	"github.com/kapetan-io/dispatch/internal/reconcile"
	"github.com/kapetan-io/dispatch/internal/treehouse"
	"github.com/kapetan-io/dispatch/internal/worker"
)

// Options points the dispatcher at its config, its state and the tools it
// drives. Zero values take the defaults.
type Options struct {
	// ConfigPath defaults to ~/.config/dispatch/config.toml.
	ConfigPath string
	// StateDir holds cache.json and tick.lock. Defaults to ~/.local/state/dispatch.
	StateDir string
	// LinearURL defaults to Linear's GraphQL endpoint.
	LinearURL string
	// LinearKey overrides LINEAR_API_KEY and the keychain entry named in config.
	LinearKey string
	// Herdr and Treehouse are the commands to run. Default "herdr" and "treehouse".
	Herdr     string
	Treehouse string
	// Launcher defaults to bin/dispatch-launch under the config's plugin_root.
	Launcher string
	Stdout   io.Writer
	Stderr   io.Writer
}

const usage = `usage: dispatch tick [--dry-run]
       dispatch run [--interval DURATION]

  tick         run one pass: launch a worker for each approved ticket that has none
  --dry-run    print the actions a tick would take, one per line, without acting
  run          tick now and then once per interval until interrupted (Ctrl-C)
  --interval   time between ticks; defaults to the config's interval_minutes`

// Run executes one dispatch command. main is a thin wrapper around it.
func Run(ctx context.Context, args []string, opts Options) error {
	opts = withDefaults(opts)
	if len(args) == 0 {
		_, _ = fmt.Fprintln(opts.Stderr, usage)
		return errors.New("no command given")
	}
	switch args[0] {
	case "tick":
		fs := flag.NewFlagSet("tick", flag.ContinueOnError)
		fs.SetOutput(opts.Stderr)
		dryRun := fs.Bool("dry-run", false, "print actions without acting")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		_, err := tick(ctx, opts, *dryRun)
		return err
	case "run":
		fs := flag.NewFlagSet("run", flag.ContinueOnError)
		fs.SetOutput(opts.Stderr)
		interval := fs.Duration("interval", 0, "time between ticks")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return run(ctx, opts, *interval)
	default:
		_, _ = fmt.Fprintln(opts.Stderr, usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func withDefaults(o Options) Options {
	if o.ConfigPath == "" {
		o.ConfigPath = config.ExpandHome("~/.config/dispatch/config.toml")
	}
	if o.StateDir == "" {
		o.StateDir = config.ExpandHome("~/.local/state/dispatch")
	}
	if o.LinearURL == "" {
		o.LinearURL = linear.DefaultURL
	}
	if o.Herdr == "" {
		o.Herdr = "herdr"
	}
	if o.Treehouse == "" {
		o.Treehouse = "treehouse"
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	return o
}

// run ticks until ctx ends. A failed tick is reported and the loop goes on,
// because the next tick may succeed (a Linear outage, a herdr restart), and
// the user watching the window decides whether to stop it. The lock is held
// only during each tick, so a one-off `dispatch tick` still runs between them.
func run(ctx context.Context, opts Options, interval time.Duration) error {
	if interval <= 0 {
		cfg, err := config.Load(opts.ConfigPath)
		if err != nil {
			return err
		}
		interval = time.Duration(cfg.IntervalMinutes) * time.Minute
	}
	if interval <= 0 {
		return fmt.Errorf("interval must be positive, got %s", interval)
	}
	_, _ = fmt.Fprintf(opts.Stdout, "dispatch: ticking every %s, Ctrl-C to stop\n", interval)
	for {
		start := time.Now()
		sum, err := tick(ctx, opts, false)
		took := time.Since(start).Round(time.Millisecond)
		switch {
		case ctx.Err() != nil:
		case err != nil:
			_, _ = fmt.Fprintf(opts.Stderr, "%s dispatch: tick failed after %s: %v\n", start.Format(time.TimeOnly), took, err)
		case sum.skipped:
			_, _ = fmt.Fprintf(opts.Stdout, "%s tick skipped: another tick is running\n", start.Format(time.TimeOnly))
		default:
			_, _ = fmt.Fprintf(opts.Stdout, "%s tick: %s, %s, %s, took %s\n", start.Format(time.TimeOnly),
				count(sum.tickets, "ticket"), count(sum.actions, "action"), linearSummary(sum.linear), took)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
	}
}

// tickSummary is what `dispatch run` prints after each tick.
type tickSummary struct {
	skipped bool
	tickets int
	actions int
	linear  linear.Stats
}

func linearSummary(s linear.Stats) string {
	out := fmt.Sprintf("Linear %s in %s", count(s.Calls, "call"), s.Elapsed.Round(time.Millisecond))
	if s.RequestsLimit > 0 {
		out += fmt.Sprintf(" (%d of %d requests left this hour)", s.RequestsRemaining, s.RequestsLimit)
	}
	return out
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func tick(ctx context.Context, opts Options, dryRun bool) (sum tickSummary, err error) {
	if err := os.MkdirAll(opts.StateDir, 0o755); err != nil {
		return sum, fmt.Errorf("while creating state dir: %w", err)
	}
	l, err := lock.Acquire(filepath.Join(opts.StateDir, "tick.lock"))
	if errors.Is(err, lock.ErrHeld) {
		_, _ = fmt.Fprintln(opts.Stderr, "dispatch: another tick is running")
		return tickSummary{skipped: true}, nil
	}
	if err != nil {
		return sum, err
	}
	defer func() { _ = l.Release() }()

	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return sum, err
	}
	cachePath := filepath.Join(opts.StateDir, "cache.json")
	c, err := cache.Load(cachePath)
	if err != nil {
		return sum, err
	}
	// Save even when the tick fails partway, so a launch failure already
	// counted is not forgotten and the escalation limit is still reached.
	defer func() {
		if dryRun {
			return
		}
		if serr := c.Save(cachePath); serr != nil && err == nil {
			err = serr
		}
	}()
	key, err := linearKey(ctx, opts, cfg)
	if err != nil {
		return sum, err
	}
	var stats linear.Stats
	defer func() { sum.linear = stats }()
	lin := linear.Client{URL: opts.LinearURL, Key: key, Stats: &stats}

	snap, err := herdr.Read(ctx, opts.Herdr)
	if err != nil {
		return sum, err
	}
	// A repo whose pool can't be read holds back only its own tickets, so one
	// moved clone doesn't stop launches in every other repo.
	var trees []treehouse.Tree
	readErrs := map[string]error{}
	for label, dir := range cfg.Repos {
		t, err := treehouse.Status(ctx, opts.Treehouse, dir)
		if err != nil {
			readErrs[label] = err
			continue
		}
		trees = append(trees, t...)
	}
	unread := map[string]bool{}
	for label, err := range readErrs {
		if len(readErrs) == len(cfg.Repos) {
			return sum, err
		}
		_, _ = fmt.Fprintf(opts.Stderr, "dispatch: while reading the %s pool: %v\n", label, err)
		unread[label] = true
	}
	tickets, err := lin.ManagedTickets(ctx, cfg.Linear.AILabel, cfg.Linear.Teams)
	if err != nil {
		return sum, err
	}

	workers := map[string]worker.Worker{}
	for _, t := range tickets {
		workers[t.Identifier] = worker.Find(t.Identifier, trees, snap)
	}
	sum.tickets = len(tickets)
	actions := reconcile.Plan(reconcile.Input{
		Tickets:        tickets,
		Workers:        workers,
		InProgress:     cfg.Linear.Statuses.InProgress,
		MaxInProgress:  cfg.MaxInProgress,
		RepoLabelGroup: cfg.Linear.RepoLabelGroup,
		Repos:          cfg.Repos,
		UnreadRepos:    unread,
	})

	sum.actions = len(actions)
	if dryRun {
		for _, a := range actions {
			_, _ = fmt.Fprintln(opts.Stdout, a)
		}
		return sum, nil
	}

	// A comment that failed after its ticket moved to Needs Human is the only
	// record of why, and Plan never revisits a Needs Human ticket.
	for _, t := range tickets {
		e := c[t.Identifier]
		if e.PendingComment == "" {
			continue
		}
		if err := lin.Comment(ctx, t.ID, e.PendingComment); err != nil {
			_, _ = fmt.Fprintf(opts.Stderr, "dispatch: while commenting on %s: %v\n", t.Identifier, err)
			continue
		}
		e.PendingComment = ""
		c[t.Identifier] = e
	}

	launch := opts.Launcher
	if launch == "" {
		launch = filepath.Join(cfg.PluginRoot, "bin", "dispatch-launch")
	}
	for _, a := range actions {
		switch a.Kind {
		case reconcile.Skip, reconcile.Wait:
			_, _ = fmt.Fprintln(opts.Stdout, a)
		case reconcile.Escalate:
			// One ticket's failed escalation must not hold up the others.
			if err := escalate(ctx, lin, cfg, c, a.Ticket, a.Reason); err != nil {
				_, _ = fmt.Fprintf(opts.Stderr, "dispatch: while escalating %s: %v\n", a.Ticket.Identifier, err)
				continue
			}
			_, _ = fmt.Fprintln(opts.Stdout, a)
		case reconcile.Launch:
			r := launcher.Launch(ctx, launch, a.RepoDir, a.Ticket.Identifier, a.Ticket.Branch, cfg.WorkerModel)
			e := c[a.Ticket.Identifier]
			if !r.Failed() {
				e.LaunchFailures = 0
				c[a.Ticket.Identifier] = e
				// A full pool is a silent wait in Linear, so the window says who
				// holds every tree.
				if r.Status == launcher.StatusPoolExhausted {
					_, _ = fmt.Fprintf(opts.Stdout, "%s: %s: %s\n", a, r.Status, r.Detail)
					continue
				}
				_, _ = fmt.Fprintf(opts.Stdout, "%s: %s\n", a, r.Status)
				continue
			}
			e.LaunchFailures++
			c[a.Ticket.Identifier] = e
			_, _ = fmt.Fprintf(opts.Stdout, "%s: %s (failure %d of %d) %s\n", a, r.Status, e.LaunchFailures, cfg.LaunchFailureLimit, r.Detail)
			if e.LaunchFailures < cfg.LaunchFailureLimit {
				continue
			}
			reason := fmt.Sprintf("its worker failed to launch %d times in a row. The launcher said `%s`: %s", e.LaunchFailures, r.Status, r.Detail)
			if err := escalate(ctx, lin, cfg, c, a.Ticket, reason); err != nil {
				_, _ = fmt.Fprintf(opts.Stderr, "dispatch: while escalating %s: %v\n", a.Ticket.Identifier, err)
				continue
			}
			e = c[a.Ticket.Identifier]
			e.LaunchFailures = 0
			c[a.Ticket.Identifier] = e
			_, _ = fmt.Fprintf(opts.Stdout, "escalate %s: launch failed %d times\n", a.Ticket.Identifier, cfg.LaunchFailureLimit)
		}
	}
	for _, t := range tickets {
		e := c[t.Identifier]
		e.LastStatus = t.Status
		c[t.Identifier] = e
	}
	return sum, nil
}

// escalate moves a ticket to Needs Human with one comment saying why.
func escalate(ctx context.Context, lin linear.Client, cfg config.Config, c cache.Cache, t linear.Ticket, reason string) error {
	state, err := lin.StateID(ctx, t.Team, cfg.Linear.Statuses.NeedsHuman)
	if err != nil {
		return err
	}
	// Move before commenting: a move that fails leaves the ticket In Progress,
	// and commenting first would post the same comment again on every tick.
	if err := lin.Move(ctx, t.ID, state); err != nil {
		return err
	}
	body := "The dispatcher moved this ticket to Needs Human because " + reason
	if err := lin.Comment(ctx, t.ID, body); err != nil {
		e := c[t.Identifier]
		e.PendingComment = body
		c[t.Identifier] = e
		return fmt.Errorf("moved to Needs Human, but the comment failed and will be retried next tick (%q): %w", body, err)
	}
	return nil
}

func linearKey(ctx context.Context, opts Options, cfg config.Config) (string, error) {
	if opts.LinearKey != "" {
		return opts.LinearKey, nil
	}
	if k := os.Getenv("LINEAR_API_KEY"); k != "" {
		return k, nil
	}
	out, err := exec.CommandContext(ctx, "security", "find-generic-password", "-s", cfg.Linear.APIKeyKeychain, "-w").Output()
	if err != nil {
		return "", fmt.Errorf("no Linear API key: LINEAR_API_KEY is unset and the keychain has no %q entry", cfg.Linear.APIKeyKeychain)
	}
	return strings.TrimSpace(string(out)), nil
}
