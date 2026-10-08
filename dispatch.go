// Package dispatch is the stateless dispatcher. Each tick reads what Linear
// says should happen, compares it with the herdr workspaces and treehouse
// trees that exist, and fixes the difference. launchd runs `dispatch tick`
// on an interval.
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

  tick         run one pass: launch a worker for each approved ticket that has none
  --dry-run    print the actions a tick would take, one per line, without acting`

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
		return tick(ctx, opts, *dryRun)
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

func tick(ctx context.Context, opts Options, dryRun bool) error {
	if err := os.MkdirAll(opts.StateDir, 0o755); err != nil {
		return fmt.Errorf("while creating state dir: %w", err)
	}
	l, err := lock.Acquire(filepath.Join(opts.StateDir, "tick.lock"))
	if errors.Is(err, lock.ErrHeld) {
		_, _ = fmt.Fprintln(opts.Stderr, "dispatch: another tick is running")
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = l.Release() }()

	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return err
	}
	cachePath := filepath.Join(opts.StateDir, "cache.json")
	c, err := cache.Load(cachePath)
	if err != nil {
		return err
	}
	key, err := linearKey(ctx, opts, cfg)
	if err != nil {
		return err
	}
	lin := linear.Client{URL: opts.LinearURL, Key: key}

	snap, err := herdr.Read(ctx, opts.Herdr)
	if err != nil {
		return err
	}
	var trees []treehouse.Tree
	for _, dir := range cfg.Repos {
		t, err := treehouse.Status(ctx, opts.Treehouse, dir)
		if err != nil {
			return err
		}
		trees = append(trees, t...)
	}
	tickets, err := lin.ManagedTickets(ctx, cfg.Linear.AILabel, cfg.Linear.Teams)
	if err != nil {
		return err
	}

	workers := map[string]worker.Worker{}
	for _, t := range tickets {
		workers[t.Identifier] = worker.Find(t.Identifier, trees, snap)
	}
	actions := reconcile.Plan(reconcile.Input{
		Tickets:        tickets,
		Workers:        workers,
		InProgress:     cfg.Linear.Statuses.InProgress,
		MaxInProgress:  cfg.MaxInProgress,
		RepoLabelGroup: cfg.Linear.RepoLabelGroup,
		Repos:          cfg.Repos,
	})

	if dryRun {
		for _, a := range actions {
			_, _ = fmt.Fprintln(opts.Stdout, a)
		}
		return nil
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
			if err := escalate(ctx, lin, cfg, a.Ticket, a.Reason); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(opts.Stdout, a)
		case reconcile.Launch:
			r := launcher.Launch(ctx, launch, a.RepoDir, a.Ticket.Identifier, a.Ticket.Branch, cfg.WorkerModel)
			e := c[a.Ticket.Identifier]
			if !r.Failed() {
				e.LaunchFailures = 0
				c[a.Ticket.Identifier] = e
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
			if err := escalate(ctx, lin, cfg, a.Ticket, reason); err != nil {
				return err
			}
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
	return c.Save(cachePath)
}

// escalate moves a ticket to Needs Human with one comment saying why.
func escalate(ctx context.Context, lin linear.Client, cfg config.Config, t linear.Ticket, reason string) error {
	state, err := lin.StateID(ctx, t.Team, cfg.Linear.Statuses.NeedsHuman)
	if err != nil {
		return err
	}
	if err := lin.Comment(ctx, t.ID, "The dispatcher moved this ticket to Needs Human because "+reason); err != nil {
		return err
	}
	return lin.Move(ctx, t.ID, state)
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
