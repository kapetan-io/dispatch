// Package dispatch is the stateless dispatcher. Each tick reads what Linear
// says should happen, compares it with the herdr workspaces and treehouse
// trees that exist, and fixes the difference. `dispatch run` ticks on an
// interval in the foreground, in a herdr window the user watches, starts and
// stops.
package dispatch

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/cache"
	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/delivery"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/launcher"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/lock"
	"github.com/kapetan-io/dispatch/internal/pullrequest"
	"github.com/kapetan-io/dispatch/internal/reconcile"
	"github.com/kapetan-io/dispatch/internal/stops"
	"github.com/kapetan-io/dispatch/internal/treehouse"
	"github.com/kapetan-io/dispatch/internal/worker"
)

// Options points the dispatcher at its config, its state and the tools it
// drives. Zero values take the defaults.
type Options struct {
	// ConfigPath defaults to ~/.config/dispatch/config.toml.
	ConfigPath string
	// StateDir holds cache.json, tick.lock and the Stop records. Defaults to ~/.local/state/dispatch.
	StateDir string
	// LinearURL defaults to Linear's GraphQL endpoint.
	LinearURL string
	// LinearKey overrides LINEAR_API_KEY and the keychain entry named in config.
	LinearKey string
	// Herdr and Treehouse are the commands to run. Default "herdr" and "treehouse".
	Herdr     string
	Treehouse string
	// GH is the command that reads pull requests. Defaults to "gh".
	GH string
	// PRTimeout bounds each gh call, so a stalled network can't hold the tick
	// lock. Defaults to 30 seconds.
	PRTimeout time.Duration
	// Osascript is the command that shows notifications. Defaults to "osascript".
	Osascript string
	// Launcher, when set, is an external program run in place of the built-in launcher. Tests use it to fake launches.
	Launcher string
	// Dir is where `dispatch launch` looks for the target repo. Defaults to the
	// working directory.
	Dir string
	// LaunchPace is the pause between launcher polls of herdr. Defaults to 1 second.
	LaunchPace time.Duration
	// Now is the clock. Defaults to time.Now.
	Now    func() time.Time
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

const usage = `usage: dispatch tick [--dry-run]
       dispatch run [--interval DURATION]
       dispatch tell <ticket> "<text>"
       dispatch ticket-hint
       dispatch launch <ticket> <branch> [--base REF] [--model NAME] [--kickoff TEXT] [--title TEXT]
       dispatch record-stop
       dispatch watch <ticket>
       dispatch unwatch <ticket>

  tick         run one pass: launch a worker for each approved ticket that has none,
               and clean up after finished and paused tickets
  --dry-run    print the actions a tick would take, one per line, without acting
  run          tick now and then once per interval until interrupted (Ctrl-C)
  --interval   time between ticks; defaults to the config's interval_minutes
  tell         type text into the ticket's worker; fails when the worker is blocked or absent
  ticket-hint  print a reminder to use the dispatch:ticket skill when run in a repo listed under
               [repos] in the config, or a worktree of one; prints nothing elsewhere.
               The plugin's SessionStart hook runs it
  launch       lease a tree, open a herdr workspace and start a worker typed --kickoff, which
               defaults to "/dispatch:build <ticket>". Run it from inside the repo. Prints one
               JSON result and exits 0 only when a worker is running
  record-stop  read a Stop hook's JSON on stdin and record when the worker in this tree last ended a
               turn and whether it still has background tasks running; does nothing outside a
               worktree of a repo listed under [repos]. The plugin's Stop hook runs it
  watch        make this Claude Code session follow the ticket, so each event a tick notifies about
               for it is also sent to this session as a message. A session can follow several
               tickets, and a ticket can have several followers. Fails outside a Claude Code
               session or when the socket path is not absolute
  unwatch      stop this session following the ticket`

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
		_, err := tick(ctx, opts, *dryRun, &autoMergeCheck{})
		return err
	case "tell":
		if len(args) != 3 {
			_, _ = fmt.Fprintln(opts.Stderr, usage)
			return errors.New(`tell takes a ticket and the text to type`)
		}
		return tell(ctx, opts, args[1], args[2])
	case "ticket-hint":
		return ticketHint(ctx, opts)
	case "launch":
		return launchWorker(ctx, opts, args[1:])
	case "record-stop":
		return recordStop(ctx, opts)
	case "watch", "unwatch":
		if len(args) != 2 {
			_, _ = fmt.Fprintln(opts.Stderr, usage)
			return fmt.Errorf("%s takes a ticket", args[0])
		}
		return follow(opts, args[0] == "watch", strings.ToUpper(args[1]))
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
	if o.GH == "" {
		o.GH = "gh"
	}
	if o.PRTimeout == 0 {
		o.PRTimeout = 30 * time.Second
	}
	if o.Osascript == "" {
		o.Osascript = "osascript"
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Stdin == nil {
		o.Stdin = os.Stdin
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
	check := &autoMergeCheck{}
	for {
		start := time.Now()
		sum, err := tick(ctx, opts, false, check)
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

func tick(ctx context.Context, opts Options, dryRun bool, check *autoMergeCheck) (sum tickSummary, err error) {
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
	check.warn(ctx, opts, cfg)
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

	// A failed lease lookup skips teardown only; launches and nudges still run.
	holders, err := leaseHolders(ctx, lin, cfg, trees, tickets)
	lookupFailed := err != nil
	if lookupFailed {
		_, _ = fmt.Fprintf(opts.Stderr, "dispatch: while reading the tickets that hold trees: %v\n", err)
	}
	leased := map[string]bool{}
	for _, tree := range trees {
		if tree.LeaseHolder != "" {
			leased[tree.LeaseHolder] = true
		}
	}
	now := opts.Now()
	workers := map[string]worker.Worker{}
	lastStops := map[string]stops.Record{}
	for _, t := range tickets {
		w := worker.Find(t.Identifier, trees, snap)
		workers[t.Identifier] = w
		if w.State == worker.Idle && t.Status == cfg.Linear.Statuses.InProgress {
			if rec, ok := stops.Read(opts.StateDir, w.TreePath, w.Session); ok {
				lastStops[t.Identifier] = rec
			}
		}
	}
	sum.tickets = len(tickets)
	input := reconcile.Input{
		Tickets:        tickets,
		Workers:        workers,
		InProgress:     cfg.Linear.Statuses.InProgress,
		InReview:       cfg.Linear.Statuses.InReview,
		NeedsHuman:     cfg.Linear.Statuses.NeedsHuman,
		MaxInProgress:  cfg.MaxInProgress,
		NudgeLimit:     cfg.NudgeLimit,
		Now:            now,
		Stops:          lastStops,
		RenotifyTicks:  cfg.BlockedRenotifyTicks,
		Cache:          c,
		RepoLabelGroup: cfg.Linear.RepoLabelGroup,
		Repos:          cfg.Repos,
		UnreadRepos:    unread,
		Done:           cfg.Linear.Statuses.Done,
		Paused:         cfg.Linear.Statuses.Paused,
		LeaseHolders:   holders,
		Leases:         leaseWorkers(holders, trees, snap),
		Todo:           cfg.Linear.Statuses.Todo,
		AILabel:        cfg.Linear.AILabel,
		Teams:          cfg.Linear.Teams,
		BlockerDone:    cfg.Linear.Statuses.BlockerDone,
		Leased:         leased,
		AutoMerge:      autoMergeSet(cfg.AutoMerge),
	}
	// Only promotion and closing wait on the children, so a failed read holds
	// back those two and nothing else.
	if parents := input.Parents(); len(parents) > 0 {
		ids := make([]string, len(parents))
		for i, parent := range parents {
			ids[i] = parent.ID
		}
		var readErr error
		if input.Children, readErr = lin.ParentChildren(ctx, ids); readErr != nil {
			_, _ = fmt.Fprintf(opts.Stderr, "dispatch: while reading the children of open parents: %v\n", readErr)
		}
	}
	input.Unfinished, input.CheckErrors, input.Untracked = inspectTrees(ctx, input)
	input.PRs, input.PRReadFailed = readPRs(ctx, opts, input)
	actions := reconcile.Plan(input)

	sum.actions = len(actions)
	if dryRun {
		post := delivery.Post{StateDir: opts.StateDir}
		for _, a := range actions {
			_, _ = fmt.Fprintln(opts.Stdout, a)
			if ev, ok := eventOf(a); ok {
				for _, line := range post.Preview(ev) {
					_, _ = fmt.Fprintln(opts.Stdout, line)
				}
			}
		}
		return sum, nil
	}

	// A comment that failed after its ticket moved to Needs Human is the only
	// record of why, and Plan never revisits a Needs Human ticket. It is
	// retried from the cache, because the ticket may since have left the
	// started states that ManagedTickets reads.
	for id, e := range c {
		if e.PendingComment == "" {
			continue
		}
		if err := lin.Comment(ctx, e.TicketID, e.PendingComment); err != nil {
			_, _ = fmt.Fprintf(opts.Stderr, "dispatch: while commenting on %s: %v\n", id, err)
			continue
		}
		e.PendingComment = ""
		e.TicketID = ""
		c[id] = e
	}

	// Who still holds a lease is unknown when the lookup failed or a pool could
	// not be read, and settle then keeps every warning.
	held := heldFinished(input)
	if lookupFailed || len(unread) > 0 {
		held = nil
	}
	settle(c, now, tickets, workers, input.PRs, cfg.Linear.Statuses, held, input.PendingComments(), leased)

	post := delivery.Post{Herdr: opts.Herdr, Osascript: opts.Osascript, StateDir: opts.StateDir, Stderr: opts.Stderr}
	current := map[string]string{}
	for _, ws := range snap.Workspaces {
		current[ws.ID] = ws.Label
	}
	// A failed rename is retried next tick, because the label still differs.
	for _, r := range input.Renames(current) {
		if err := herdr.RenameWorkspace(ctx, opts.Herdr, r.WorkspaceID, r.Label); err != nil {
			_, _ = fmt.Fprintf(opts.Stderr, "dispatch: %v\n", err)
		}
	}
	tabs := map[string]string{}
	for _, tab := range snap.Tabs {
		tabs[tab.ID] = tab.Label
	}
	for _, r := range input.TabRenames(tabs) {
		if err := herdr.RenameTab(ctx, opts.Herdr, r.TabID, r.Label); err != nil {
			_, _ = fmt.Fprintf(opts.Stderr, "dispatch: %v\n", err)
		}
	}
	nudgeFailed := map[string]bool{}
	launch := func(a reconcile.Action) launcher.Result {
		model, kickoff := cfg.WorkerModel, ""
		if a.Design {
			model, kickoff = cfg.DesignModel, "/dispatch:design "+a.Ticket.Identifier
		}
		if opts.Launcher != "" {
			return launcher.Launch(ctx, opts.Launcher, a.RepoDir, a.Ticket.Identifier, a.Ticket.Branch, model, kickoff)
		}
		lo := launcherOptions(opts, cfg.PluginRoot)
		lo.RepoDir = a.RepoDir
		return launcher.Run(ctx, launcher.Request{Ticket: a.Ticket.Identifier, Title: a.Ticket.Title, Branch: a.Ticket.Branch, Model: model, Kickoff: kickoff}, lo)
	}
	// Only a ticket that stopped waiting gets a fresh comment later. A pool that
	// couldn't be read this tick says nothing about its blockers.
	for _, t := range tickets {
		if e := c[t.Identifier]; e.HoldNoted != "" && !input.WaitsOnBlockers(t) {
			e.HoldNoted = ""
			c[t.Identifier] = e
		}
	}
	for _, a := range actions {
		switch a.Kind {
		case reconcile.Skip, reconcile.Wait:
			_, _ = fmt.Fprintln(opts.Stdout, a)
			if e := c[a.Ticket.Identifier]; a.Note != "" && e.HoldNoted != a.Note {
				if err := lin.Comment(ctx, a.Ticket.ID, a.Note); err != nil {
					_, _ = fmt.Fprintf(opts.Stderr, "dispatch: while commenting on %s: %v\n", a.Ticket.Identifier, err)
					continue
				}
				e.HoldNoted = a.Note
				c[a.Ticket.Identifier] = e
			}
		case reconcile.Escalate:
			// One ticket's failed escalation must not hold up the others.
			if err := escalate(ctx, lin, cfg, c, post, a.Ticket, a.Reason); err != nil {
				_, _ = fmt.Fprintf(opts.Stderr, "dispatch: while escalating %s: %v\n", a.Ticket.Identifier, err)
				continue
			}
			// The renames above ran before this move, and the label must match the new status by the end of the tick.
			if id := workers[a.Ticket.Identifier].WorkspaceID; id != "" {
				if err := herdr.RenameWorkspace(ctx, opts.Herdr, id, reconcile.Label(reconcile.EmojiNeedsHuman, a.Ticket)); err != nil {
					_, _ = fmt.Fprintf(opts.Stderr, "dispatch: %v\n", err)
				}
			}
			_, _ = fmt.Fprintln(opts.Stdout, a)
		case reconcile.Nudge:
			// A refused nudge is not recorded, so the Stop stays un-nudged and the
			// stall count stays true.
			if err := post.Nudge(ctx, workers[a.Ticket.Identifier], a.Text); err != nil {
				_, _ = fmt.Fprintf(opts.Stderr, "dispatch: while nudging %s: %v\n", a.Ticket.Identifier, err)
				nudgeFailed[a.Ticket.Identifier] = true
				continue
			}
			e := c[a.Ticket.Identifier]
			switch {
			case a.Reason == reconcile.ReasonComment:
				e.CommentSeen = a.Ticket.LastComment
			case a.ActivityAt != "":
				e.PRActivitySeen = a.ActivityAt
			case a.Reason == reconcile.ReasonStall:
				e.Nudges++
				e.StallNudges++
				e.NudgedStop, e.NudgedAt, e.NudgeNotified = a.AnsweredStop, now, false
			case a.Reason == reconcile.ReasonResume:
				e.Nudges++
				e.ResumePending = false
				e.NudgedStop, e.NudgedAt, e.NudgeNotified = a.AnsweredStop, time.Time{}, false
			default:
				// A PR nudge, whose reason carries the problem text.
				e.Nudges++
				e.ResumePending = false
			}
			c[a.Ticket.Identifier] = e
			_, _ = fmt.Fprintln(opts.Stdout, a)
		// One tree's failure is reported, not returned, so the remaining trees still get their turn.
		case reconcile.Teardown:
			if err := teardown(ctx, opts, a); err != nil {
				_, _ = fmt.Fprintf(opts.Stderr, "dispatch: while tearing down %s: %v\n", a.Ticket.Identifier, err)
			}
		case reconcile.Stop:
			if err := stop(ctx, opts, a); err != nil {
				_, _ = fmt.Fprintf(opts.Stderr, "dispatch: while stopping %s: %v\n", a.Ticket.Identifier, err)
			}
		case reconcile.Keep:
			if err := keep(ctx, opts, lin, c, a); err != nil {
				_, _ = fmt.Fprintf(opts.Stderr, "dispatch: while keeping the tree of %s: %v\n", a.Ticket.Identifier, err)
			}
		case reconcile.Promote:
			if err := move(ctx, lin, a.Ticket, cfg.Linear.Statuses.InProgress); err != nil {
				_, _ = fmt.Fprintf(opts.Stderr, "dispatch: while promoting %s: %v\n", a.Ticket.Identifier, err)
				continue
			}
			_, _ = fmt.Fprintln(opts.Stdout, a)
		case reconcile.Close:
			if err := move(ctx, lin, a.Ticket, cfg.Linear.Statuses.CloseParent); err != nil {
				_, _ = fmt.Fprintf(opts.Stderr, "dispatch: while closing %s: %v\n", a.Ticket.Identifier, err)
				continue
			}
			_, _ = fmt.Fprintln(opts.Stdout, a)
		case reconcile.Merge:
			e := c[a.Ticket.Identifier]
			if err := pullrequest.Merge(ctx, opts.GH, a.PRURL, a.Head, opts.PRTimeout); err != nil {
				_, _ = fmt.Fprintf(opts.Stderr, "dispatch: while merging %s: %v\n", a.Ticket.Identifier, err)
				// Recorded before the alert, so a failed alert can't make the next tick retry the merge.
				e.MergeFailedHead = a.Head
				c[a.Ticket.Identifier] = e
				msg, _, _ := strings.Cut(err.Error(), "\n")
				if err := post.NotifyOnce(ctx, delivery.Event{Ticket: a.Ticket, Reason: a.Reason, Text: fmt.Sprintf("%s: merging %s failed: %s", a.Ticket.Identifier, a.PRURL, msg)}); err != nil {
					_, _ = fmt.Fprintf(opts.Stderr, "dispatch: while notifying about %s: %v\n", a.Ticket.Identifier, err)
				}
				continue
			}
			// Keeps the ready notification from firing for a head that was merged.
			e.ReadyHead = a.Head
			c[a.Ticket.Identifier] = e
			_, _ = fmt.Fprintln(opts.Stdout, a)
			if err := post.NotifyOnce(ctx, delivery.Event{Ticket: a.Ticket, Reason: a.Reason, Text: a.Text}); err != nil {
				_, _ = fmt.Fprintf(opts.Stderr, "dispatch: while notifying about %s: %v\n", a.Ticket.Identifier, err)
			}
		case reconcile.Notify:
			if a.AfterNudge && nudgeFailed[a.Ticket.Identifier] {
				continue
			}
			if a.AfterNudge || a.Reason == reconcile.ReasonUnresponsive {
				// A failed alert is retried next tick, for a nudge count through
				// StallNotifyPending and for an unresponsive worker because
				// NudgeNotified stays unset.
				e := c[a.Ticket.Identifier]
				if err := post.Notify(ctx, delivery.Event{Ticket: a.Ticket, Reason: a.Reason, Text: a.Text}); err != nil {
					_, _ = fmt.Fprintf(opts.Stderr, "dispatch: while notifying about %s: %v\n", a.Ticket.Identifier, err)
					e.StallNotifyPending = e.StallNotifyPending || a.AfterNudge
					c[a.Ticket.Identifier] = e
					continue
				}
				if a.AfterNudge {
					e.StallNotifyPending = false
				}
				if a.Reason == reconcile.ReasonUnresponsive {
					e.NudgeNotified = true
				}
				c[a.Ticket.Identifier] = e
				_, _ = fmt.Fprintln(opts.Stdout, a)
				continue
			}
			if a.Reason == reconcile.ReasonReady {
				// A failed alert leaves the head unrecorded, so the next tick sends it again.
				if err := post.Notify(ctx, delivery.Event{Ticket: a.Ticket, Reason: a.Reason, Text: a.Text}); err != nil {
					_, _ = fmt.Fprintf(opts.Stderr, "dispatch: while notifying about %s: %v\n", a.Ticket.Identifier, err)
					continue
				}
				e := c[a.Ticket.Identifier]
				e.ReadyHead = a.Head
				c[a.Ticket.Identifier] = e
				_, _ = fmt.Fprintln(opts.Stdout, a)
				continue
			}
			if err := post.Notify(ctx, delivery.Event{Ticket: a.Ticket, Reason: a.Reason, Text: a.Text}); err != nil {
				// Settle already advanced the counters, so reset them to make Plan
				// find this notification due again.
				_, _ = fmt.Fprintf(opts.Stderr, "dispatch: while notifying about %s: %v\n", a.Ticket.Identifier, err)
				e := c[a.Ticket.Identifier]
				e.BlockedTicks = 0
				e.NotifyPending = a.Reason == reconcile.ReasonEnteredNeedsHuman
				c[a.Ticket.Identifier] = e
				continue
			}
			e := c[a.Ticket.Identifier]
			e.NotifyPending = false
			c[a.Ticket.Identifier] = e
			_, _ = fmt.Fprintln(opts.Stdout, a)
		case reconcile.Launch:
			r := launch(a)
			e := c[a.Ticket.Identifier]
			if !r.Failed() {
				e.LaunchFailures = 0
				if r.Status == launcher.StatusOK && a.ActivityAt != "" {
					e.PRActivitySeen = a.ActivityAt
				}
				if r.Status == launcher.StatusOK && a.Ticket.Status == cfg.Linear.Statuses.InReview && !a.ThreadOnly {
					e.Relaunches++
				}
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
			if err := escalate(ctx, lin, cfg, c, post, a.Ticket, reason); err != nil {
				_, _ = fmt.Fprintf(opts.Stderr, "dispatch: while escalating %s: %v\n", a.Ticket.Identifier, err)
				continue
			}
			e = c[a.Ticket.Identifier]
			e.LaunchFailures = 0
			c[a.Ticket.Identifier] = e
			_, _ = fmt.Fprintf(opts.Stdout, "escalate %s: launch failed %d times\n", a.Ticket.Identifier, cfg.LaunchFailureLimit)
		}
	}
	return sum, nil
}

// settle rolls the cache forward to this tick. Order matters. Plan has already
// read last tick's counters, and the actions that follow add to these ones
// (Nudges++) or undo them when a nudge or notification fails, so settling
// after the actions would lose those changes.
//
// A ticket back in In Progress stays resume-pending until a resume nudge
// lands, so a worker that was blocked or working when the answer arrived still
// gets the resume text once it goes idle.
//
// held names the finished tickets that still hold a lease. A nil held means the
// leases could not be read this tick, and no dirty-tree warning is dropped.
//
// leased names the tickets that hold a treehouse lease.
//
// pending holds back a ticket's comment time (see Input.PendingComments), so
// its nudge is retried instead of lost. A successful nudge advances it.
func settle(c cache.Cache, now time.Time, tickets []linear.Ticket, workers map[string]worker.Worker, prs map[string]pullrequest.PR, statuses config.Statuses, held, pending, leased map[string]bool) {
	seen := map[string]bool{}
	for _, t := range tickets {
		e := c[t.Identifier]
		e.PRActivitySeen = e.ActivityBaseline(t.Status, statuses.InReview)
		if pr, ok := prs[t.Identifier]; ok && e.PRActivitySeen == "" && t.Status == statuses.InReview {
			// No baseline yet, so the PR's current activity is taken as already seen.
			e.PRActivitySeen = cmp.Or(pr.ActivityAt, "0")
		}
		if e.LastStatus != t.Status {
			e.Nudges = 0
			e.Relaunches = 0
			e.TeardownWarned = false
			e.StallNudges = 0
			e.StallNotifyPending = false
			e.NudgedStop, e.NudgedAt, e.NudgeNotified = time.Time{}, time.Time{}, false
		}
		// IdleSince is when this stretch of idling began. A worker that works
		// and goes idle again, or a ticket that changed status, starts a new one.
		if t.Status == statuses.InProgress && workers[t.Identifier].State == worker.Idle {
			if e.IdleSince.IsZero() || e.LastStatus != t.Status {
				e.IdleSince = now
			}
		} else {
			e.IdleSince = time.Time{}
		}
		if e.CommentSeen == "" || !pending[t.Identifier] {
			// "0" sorts before any RFC 3339 time and, unlike "", marks the ticket seen.
			e.CommentSeen = "0"
			if t.LastComment != "" {
				e.CommentSeen = t.LastComment
			}
		}
		if workers[t.Identifier].State == worker.Blocked {
			e.BlockedTicks++
		} else {
			e.BlockedTicks = 0
		}
		if t.Status != statuses.InProgress {
			e.ResumePending = false
		} else if e.LastStatus == statuses.NeedsHuman {
			e.ResumePending = true
		}
		if t.Status != statuses.NeedsHuman {
			e.NotifyPending = false
		}
		// A moved head means a push. An unread PR leaves the last head in place,
		// so the push is still seen once the PR is read again.
		if pr, ok := prs[t.Identifier]; ok {
			e.Nudges = e.NudgesAt(t.Status, pr.Head)
			e.Relaunches = e.RelaunchesAt(t.Status, pr.Head)
			e.PRHead = pr.Head
		} else if t.Status != statuses.InReview {
			// The head means nothing outside In Review, and a stale one would hide a push later.
			e.PRHead = ""
		}
		e.LastStatus = t.Status
		c[t.Identifier] = e
		seen[t.Identifier] = true
	}
	// A ticket missing from the read was paused or finished, and its worker may
	// have been torn down and relaunched since. Nudge counters from before would
	// escalate it on a PR nudge it never received, so only what must outlive the
	// ticket's absence is kept.
	for id, e := range c {
		if seen[id] {
			continue
		}
		// A finished ticket keeps its dirty-tree warning while it holds a lease,
		// and the warning goes once the tree is returned. A nil held means the
		// leases could not be read this tick, so no warning is dropped.
		kept := cache.Entry{LaunchFailures: e.LaunchFailures, PendingComment: e.PendingComment, TicketID: e.TicketID, TeardownWarned: e.TeardownWarned && (held == nil || held[id])}
		// A worker that outlives its ticket's absence must still hear about a
		// comment posted meanwhile, so the comment time stays while a tree is leased.
		if leased[id] {
			kept.CommentSeen = e.CommentSeen
			// Without it, a worker relaunched on the kept tree would announce a PR head twice.
			kept.ReadyHead = e.ReadyHead
			kept.PRActivitySeen = e.PRActivitySeen
			kept.MergeFailedHead = e.MergeFailedHead
		}
		c[id] = kept
		if c[id] == (cache.Entry{}) {
			delete(c, id)
		}
	}
}

// readPRs reads the open PR of each ticket for which in.ReadsPR is true. A PR
// that can't be read is reported and left out, so it holds back only its own
// ticket, and its ticket is named in failed to tell it from a ticket with no
// open PR.
func readPRs(ctx context.Context, opts Options, in reconcile.Input) (prs map[string]pullrequest.PR, failed map[string]bool) {
	prs, failed = map[string]pullrequest.PR{}, map[string]bool{}
	for _, t := range in.Tickets {
		if !in.ReadsPR(t) {
			continue
		}
		repo, ok := t.LabelIn(in.RepoLabelGroup)
		dir := in.Repos[repo]
		if !ok || dir == "" {
			continue
		}
		pr, found, err := pullrequest.Read(ctx, opts.GH, dir, t.Branch, opts.PRTimeout)
		if err != nil {
			_, _ = fmt.Fprintf(opts.Stderr, "dispatch: while reading the PR for %s: %v\n", t.Identifier, err)
			failed[t.Identifier] = true
			continue
		}
		if found {
			prs[t.Identifier] = pr
		}
	}
	return prs, failed
}

// move sets a ticket's status by name.
func move(ctx context.Context, lin linear.Client, t linear.Ticket, status string) error {
	state, err := lin.StateID(ctx, t.Team, status)
	if err != nil {
		return err
	}
	return lin.Move(ctx, t.ID, state)
}

// escalate moves a ticket to Needs Human with one comment saying why.
func escalate(ctx context.Context, lin linear.Client, cfg config.Config, c cache.Cache, post delivery.Post, t linear.Ticket, reason string) error {
	state, err := lin.StateID(ctx, t.Team, cfg.Linear.Statuses.NeedsHuman)
	if err != nil {
		return err
	}
	// Move before commenting: a move that fails leaves the ticket In Progress,
	// and commenting first would post the same comment again on every tick.
	if err := lin.Move(ctx, t.ID, state); err != nil {
		return err
	}
	// Tell the user now instead of a tick later. Setting last_status here is
	// what keeps the next tick's Plan from notifying a second time, and a
	// failed alert marks the notification pending so the next tick retries it.
	e := c[t.Identifier]
	e.LastStatus = cfg.Linear.Statuses.NeedsHuman
	e.Nudges = 0
	e.Relaunches = 0
	e.NotifyPending = post.Notify(ctx, delivery.Event{Ticket: t, Text: reconcile.NeedsHumanText(t)}) != nil
	c[t.Identifier] = e
	body := "The dispatcher moved this ticket to Needs Human because " + reason
	if err := lin.Comment(ctx, t.ID, body); err != nil {
		e = c[t.Identifier]
		e.PendingComment = body
		e.TicketID = t.ID
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

// eventOf is the event a tick raises for a, for the dry run's preview. A live
// tick can still send less, because a failed merge or nudge changes or skips it.
func eventOf(a reconcile.Action) (delivery.Event, bool) {
	switch a.Kind {
	case reconcile.Notify, reconcile.Merge:
		return delivery.Event{Ticket: a.Ticket, Reason: a.Reason, Text: a.Text}, true
	case reconcile.Escalate:
		return delivery.Event{Ticket: a.Ticket, Text: reconcile.NeedsHumanText(a.Ticket)}, true
	}
	return delivery.Event{}, false
}

// follow makes the calling Claude Code session start or stop following a ticket.
func follow(opts Options, watch bool, ticket string) error {
	if !ticketID.MatchString(ticket) {
		return fmt.Errorf("%q is not a ticket identifier such as ENG-123", ticket)
	}
	socket := os.Getenv(messagingSocketEnv)
	if socket == "" {
		return fmt.Errorf("%s is not set, so this is not a Claude Code session; run watch from one", messagingSocketEnv)
	}
	if !watch {
		if err := delivery.Unwatch(opts.StateDir, ticket, socket); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(opts.Stdout, "unwatching %s\n", ticket)
		return nil
	}
	if err := delivery.Watch(opts.StateDir, ticket, socket); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(opts.Stdout, "watching %s\n", ticket)
	return nil
}

// ticketID is the shape of a Linear identifier.
var ticketID = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9]+$`)

// messagingSocketEnv names the variable Claude Code sets to a session's inbox socket.
const messagingSocketEnv = "CLAUDE_CODE_MESSAGING_SOCKET"
