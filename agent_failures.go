package dispatch

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"time"

	"github.com/kapetan-io/dispatch/internal/agentfailure"
	"github.com/kapetan-io/dispatch/internal/transcript"
)

const (
	// failureWindow is how far back collect-agent-failures reads when --from is not given.
	failureWindow = 7 * 24 * time.Hour
	// patternMinerMarker is in every line the pattern miner's own session sends, whose tool
	// failures come from reading other sessions' failures and would count them twice.
	patternMinerMarker = "DAILY-PATTERN-MINING"
)

// collectAgentFailures is `dispatch collect-agent-failures`.
func collectAgentFailures(opts Options, args []string) error {
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
	sessions, err := transcript.Read(dir, transcript.Window{From: from, To: to}, patternMinerMarker)
	if err != nil {
		return fmt.Errorf("reading transcripts in %s: %w", dir, err)
	}
	report := agentfailure.Collect(sessions, agentfailure.ReadDenyRules(filepath.Dir(dir)), opts.Env.ExpandHome("~"))
	if *asJSON {
		enc := json.NewEncoder(opts.Stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(struct {
			From time.Time `json:"from"`
			To   time.Time `json:"to"`
			agentfailure.Report
		}{from, to, report})
	}
	return report.WriteMarkdown(opts.Stdout, from.Format(time.RFC3339), to.Format(time.RFC3339))
}

// parseWindowEdge is parseWhen that also takes a bare date, as the start of that day in local
// time.
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
