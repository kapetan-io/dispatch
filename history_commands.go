package dispatch

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/history"
	"github.com/kapetan-io/dispatch/internal/ledger"
	"github.com/kapetan-io/dispatch/internal/lock"
	"github.com/kapetan-io/dispatch/internal/loop"
	"github.com/kapetan-io/dispatch/internal/rules"
	"github.com/kapetan-io/dispatch/internal/world"
)

// codeNotReplayable is the exit code of a replay over history that holds no record of the world.
const codeNotReplayable = 2

// traceCommand prints why dispatch acted on a ticket, from history.
func traceCommand(opts Options, ticket string) error {
	ticket = strings.ToUpper(ticket)
	if !ticketID.MatchString(ticket) {
		return fmt.Errorf("%q is not a ticket identifier such as ENG-123", ticket)
	}
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return err
	}
	err = loop.Trace(cfg.History.Dir, ticket, opts.Stdout)
	if errors.Is(err, loop.ErrNoRecord) {
		return fmt.Errorf("history has no record of %s", ticket)
	}
	return err
}

// replayCommand runs a stretch of history again and compares it with what the live run decided.
func replayCommand(opts Options, args []string) error {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	fs.SetOutput(opts.Stderr)
	from := fs.String("from", "", "the first cycle to run again")
	to := fs.String("to", "", "the last cycle to run again")
	rulesDir := fs.String("rules", "", "a directory of rules to run over the recorded history")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New("replay takes flags only")
	}
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return err
	}
	now := opts.Clock.Now()
	options := loop.ReplayOptions{Dir: cfg.History.Dir}
	if options.From, err = parseWhen("--from", *from, now); err != nil {
		return err
	}
	if options.To, err = parseWhen("--to", *to, now); err != nil {
		return err
	}
	if *rulesDir != "" {
		if options.Rules, err = loadRulesIn(opts, config.ExpandHome(*rulesDir)); err != nil {
			return err
		}
	}
	res, err := loop.Replay(options)
	for _, why := range res.Skipped {
		_, _ = fmt.Fprintln(opts.Stderr, "dispatch: skipped", why)
	}
	if errors.Is(err, loop.ErrNotReplayable) {
		return exitError{code: codeNotReplayable, err: err}
	}
	if err != nil {
		return err
	}
	if res.Difference == "" {
		_, _ = fmt.Fprintf(opts.Stdout, "%s, %d match\n", countNoun(res.Cycles, "cycle"), res.Matched)
		return nil
	}
	_, _ = fmt.Fprintln(opts.Stdout, res.Difference)
	if options.Rules != nil {
		return nil
	}
	return fmt.Errorf("replay differs from the live run after %s, %d match", countNoun(res.Cycles-1, "cycle"), res.Matched)
}

func countNoun(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// parseWhen reads a time given as RFC 3339, as a local date and time, or as a local time of day today.
func parseWhen(flagName, text string, now time.Time) (time.Time, error) {
	if text == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, text); err == nil {
		return t, nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, text, now.Location()); err == nil {
			return t, nil
		}
	}
	for _, layout := range []string{"15:04:05", "15:04"} {
		if t, err := time.ParseInLocation(layout, text, now.Location()); err == nil {
			y, m, d := now.Date()
			return time.Date(y, m, d, t.Hour(), t.Minute(), t.Second(), 0, now.Location()), nil
		}
	}
	return time.Time{}, fmt.Errorf("%s %q is not a time. Use RFC 3339, \"2006-01-02 15:04\" or \"15:04\"", flagName, text)
}

// stateCommand is `dispatch state reset`.
func stateCommand(ctx context.Context, opts Options, args []string) error {
	if len(args) == 0 || args[0] != "reset" {
		_, _ = fmt.Fprintln(opts.Stderr, usage)
		return errors.New("state takes reset")
	}
	fs := flag.NewFlagSet("state reset", flag.ContinueOnError)
	fs.SetOutput(opts.Stderr)
	quiet := fs.Bool("quiet", false, "record the messages the first cycle wants as sent")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New("state reset takes only --quiet")
	}
	return resetState(ctx, opts, *quiet)
}

// resetState starts a new history segment with an empty ledger. The world's memory carries over, so no
// wait starts again. With quiet, the messages the first cycle wants get a Done receipt flagged as reset,
// so a restart does not tell every worker again what it was told before.
func resetState(ctx context.Context, opts Options, quiet bool) error {
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(opts.StateDir, 0o700); err != nil {
		return fmt.Errorf("while creating state dir: %w", err)
	}
	l, err := lock.Acquire(filepath.Join(opts.StateDir, lockName))
	if errors.Is(err, lock.ErrHeld) {
		return fmt.Errorf("dispatch run holds the lock on %s, so state cannot be reset until it stops", opts.StateDir)
	}
	if err != nil {
		return err
	}
	defer func() { _ = l.Release() }()

	text, err := os.ReadFile(opts.ConfigPath)
	if err != nil {
		return err
	}
	at := opts.Clock.Now()
	mem, resync := world.NewMemory(), 0
	if seg, ok, err := history.Latest(cfg.History.Dir); err != nil {
		return fmt.Errorf("while reading history: %w", err)
	} else if ok {
		st, err := seg.Load()
		if err != nil {
			return fmt.Errorf("while loading %s: %w", seg.Path, err)
		}
		mem, resync = st.Memory, st.Resync
	}
	header := history.Header{Why: "reset", Config: string(text), Started: at.UnixMilli(), At: at.UnixMilli()}
	var receipts []ledger.Receipt
	if quiet {
		if receipts, mem, err = quietReceipts(ctx, opts, cfg, mem, at, resync); err != nil {
			return err
		}
	}
	w, err := history.Start(cfg.History.Dir, at, header,
		history.Snapshot{At: at.UnixMilli(), World: mem, Ledger: history.LedgerState{Receipts: receipts}, Resync: resync})
	if err != nil {
		return err
	}
	if err := w.Close(true); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(opts.Stdout, "state reset: started %s with an empty ledger", filepath.Base(w.Path()))
	if quiet {
		_, _ = fmt.Fprintf(opts.Stdout, " and %s for the messages the first cycle wants", countNoun(len(receipts), "receipt"))
	}
	_, _ = fmt.Fprintln(opts.Stdout)
	return nil
}

// quietReceipts runs the first cycle over an empty ledger and returns a Done receipt for each message
// it wants, with the memory that cycle leaves, because a message's once key can hold a time that
// memory first saw.
func quietReceipts(ctx context.Context, opts Options, cfg config.Config, mem *world.Memory, at time.Time, resync int) ([]ledger.Receipt, *world.Memory, error) {
	loaded, err := loadRules(opts, cfg)
	if err != nil {
		return nil, nil, err
	}
	lin := &keyedLinear{opts: opts, cfg: cfg, keys: &keyLookup{}}
	if _, err := lin.workspace(ctx); err != nil {
		return nil, nil, err
	}
	wd, err := world.Read(ctx, world.Sources{
		Linear: lin, Herdr: opts.Herdr, Treehouse: opts.Treehouse, PullRequests: opts.PullRequests,
		StateDir: opts.StateDir, Stderr: opts.Stderr, Tolerant: true, Resilient: true,
	}, world.ScopeOf(cfg), at)
	if err != nil {
		return nil, nil, err
	}
	if err := firstUnread(wd); err != nil {
		return nil, nil, fmt.Errorf("cannot record the messages the first cycle wants while a source is down: %w", err)
	}
	state := rules.FromWorld(wd, cfg, &ledger.Ledger{}, "")
	next := state.Carry(mem, at.UnixMilli())
	var receipts []ledger.Receipt
	for _, want := range loaded.Cycle(state, nil).Wants {
		if !slices.Contains(loop.MessageKinds, want.Receipt) {
			continue
		}
		receipts = append(receipts, ledger.Receipt{
			Kind: want.Receipt, Subject: want.Subject, Once: want.Once, Rule: want.Rule, Outcome: ledger.Done,
			Detail: "state reset", Started: at.UnixMilli(), Ended: at.UnixMilli(), Resync: resync, Reset: true,
		})
	}
	return receipts, next, nil
}

// firstUnread names a source the read could not reach. A rule holds back every message for what it
// cannot read, so a quiet reset over such a read would record nothing, and the next run would send it all.
func firstUnread(w world.World) error {
	for label, err := range w.PoolErrs {
		return fmt.Errorf("the %s pool: %w", label, err)
	}
	for _, err := range []error{w.LinearErr, w.HerdrErr, w.HoldersErr, w.ChildrenErr} {
		if err != nil {
			return err
		}
	}
	for id := range w.PRReadFailed {
		return fmt.Errorf("the pull request of %s", id)
	}
	for path, err := range w.CheckErrors {
		return fmt.Errorf("the tree at %s: %w", path, err)
	}
	return nil
}
