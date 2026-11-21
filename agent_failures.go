package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/kapetan-io/dispatch/internal/agentfailure"
	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/rework"
	"github.com/kapetan-io/dispatch/internal/transcript"
)

const (
	// failureWindow is how far back collect-agent-failures reads when --from is not given.
	failureWindow = 7 * 24 * time.Hour
	// patternMinerMarker marks the pattern miner's own session, which reads other sessions'
	// transcripts, so its failures come from the mining rather than from work. It is built from
	// two halves so that no source file and nothing dispatch prints, such as its usage, contains
	// it, since a session that read either would otherwise be skipped.
	patternMinerMarker = "DAILY-" + "PATTERN-MINING"
)

// collectAgentFailures is `dispatch collect-agent-failures`.
func collectAgentFailures(ctx context.Context, opts Options, args []string) error {
	fs := flag.NewFlagSet("collect-agent-failures", flag.ContinueOnError)
	fs.SetOutput(opts.Stderr)
	fromText := fs.String("from", "", "the start of the window; 7 days before --to when unset")
	toText := fs.String("to", "", "the end of the window; now when unset")
	asJSON := fs.Bool("json", false, "print the rows as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New("collect-agent-failures takes flags only")
	}
	now := opts.Clock.Now()
	to, err := parseWindowEdge("--to", *toText, now)
	if err != nil {
		return err
	}
	if to.IsZero() {
		to = now
	}
	from, err := parseWindowEdge("--from", *fromText, now)
	if err != nil {
		return err
	}
	if from.IsZero() {
		from = to.Add(-failureWindow)
	}
	if !from.Before(to) {
		return fmt.Errorf("--from %s is not before --to %s", from.Format(time.RFC3339), to.Format(time.RFC3339))
	}
	dir := transcript.Dir(opts.Env)
	sessions := transcript.Read(dir, transcript.Window{From: from, To: to}, patternMinerMarker)
	for _, why := range sessions.Unreadable {
		_, _ = fmt.Fprintln(opts.Stderr, "dispatch: skipped", why)
	}
	report := agentfailure.Collect(sessions, agentfailure.ReadDenyRules(filepath.Dir(dir)), opts.Env.ExpandHome("~"))
	tables := reworkOfMergedPRs(ctx, opts, sessions, from, to)
	if *asJSON {
		enc := json.NewEncoder(opts.Stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(struct {
			From time.Time `json:"from"`
			To   time.Time `json:"to"`
			agentfailure.Report
			rework.Tables
		}{from, to, report, tables})
	}
	if err := report.WriteMarkdown(opts.Stdout, from.Format(time.RFC3339), to.Format(time.RFC3339)); err != nil {
		return err
	}
	return tables.WriteMarkdown(opts.Stdout)
}

// reworkOfMergedPRs reads the fix chains and changed tests of the PRs that merged in the window,
// from the repos the config lists. A config that fails to load leaves the tables empty with a
// note, so the transcript tables, which need no config, still print.
func reworkOfMergedPRs(ctx context.Context, opts Options, sessions transcript.Sessions, from, to time.Time) rework.Tables {
	cfg, err := loadConfig(opts)
	if err != nil {
		tables := rework.NewTables()
		tables.Notes = append(tables.Notes, fmt.Sprintf("merged PRs not read: %v", err))
		return tables
	}
	var repos []rework.Repo
	for _, label := range cfg.Repos.Labels() {
		repo := cfg.Repos[label]
		repos = append(repos, rework.Repo{Label: label, Path: repo.Path, Remote: repo.ChosenRemote()})
	}
	return rework.Collect(ctx, rework.Inputs{
		Repos: repos, GitHub: opts.PullRequests, TicketLabels: ticketLabels{opts, cfg}, Sessions: sessions,
		Teams: cfg.Plugins.Linear.Teams, Env: opts.Env, From: from, To: to,
	})
}

// ticketLabels reads ticket labels from Linear with LeasedTickets, which sees the key owner's
// tickets that carry the ai label, archived ones included. A ticket whose labels Linear cut short
// is left out, since a missing label could be on the part not read.
type ticketLabels struct {
	opts Options
	cfg  config.Config
}

func (l ticketLabels) Labels(ctx context.Context, tickets []string) (map[string][]string, error) {
	key, err := findLinearKey(ctx, l.opts, l.cfg)
	if err != nil {
		return nil, err
	}
	lin := l.opts.Linear(key)
	out := map[string][]string{}
	for batch := range slices.Chunk(tickets, linear.PageSize) {
		found, err := lin.LeasedTickets(ctx, l.cfg.Plugins.Linear.AILabel, l.cfg.Plugins.Linear.Teams, batch)
		if err != nil {
			return out, err
		}
		for _, t := range found {
			if t.LabelsTruncated {
				continue
			}
			names := []string{}
			for _, label := range t.Labels {
				names = append(names, label.Name)
			}
			out[t.Identifier] = names
		}
	}
	return out, nil
}

// parseWindowEdge is parseWhen that also takes a bare date, as the start of that day in local
// time, so --to 2026-11-19 ends the window as that day starts.
func parseWindowEdge(flagName, text string, now time.Time) (time.Time, error) {
	if t, err := time.ParseInLocation(time.DateOnly, text, now.Location()); err == nil {
		return t, nil
	}
	t, err := parseWhen(flagName, text, now)
	if err != nil {
		return t, fmt.Errorf("%w, or a date such as 2026-11-13", err)
	}
	return t, nil
}
