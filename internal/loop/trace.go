package loop

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/history"
	"github.com/kapetan-io/dispatch/internal/ledger"
)

// ErrNoRecord means kept history says nothing about a ticket.
var ErrNoRecord = errors.New("history has no record of the ticket")

// Trace prints why dispatch acted on a ticket, from every segment kept in dir: each cycle that
// changed what a rule wanted for it, with what the rule read, each hold, wait, throw and dropped
// event, and each receipt. It returns ErrNoRecord when history never mentions the ticket.
func Trace(dir, ticket string, out io.Writer) error {
	paths, err := history.Segments(dir)
	if err != nil {
		return err
	}
	tr := &tracing{out: out, ticket: ticket}
	for _, p := range paths {
		seg, err := history.Read(p)
		if errors.Is(err, history.ErrIncomplete) {
			continue
		}
		if err != nil {
			return err
		}
		tr.segment(seg)
	}
	if !tr.seen {
		return ErrNoRecord
	}
	return nil
}

type tracing struct {
	out    io.Writer
	ticket string
	// seen is true once history has mentioned the ticket.
	seen bool
	// cycles counts the cycle lines read, so each prints with a number the next trace of it agrees on.
	cycles int
	// last is what the previous cycle decided for the ticket.
	last string
}

func (t *tracing) segment(seg history.Segment) {
	for _, rc := range seg.Snapshot.Ledger.Receipts {
		if rc.Subject == t.ticket {
			t.seen = true
		}
	}
	t.last = ""
	for _, raw := range seg.Lines {
		var head struct {
			Line string `json:"line"`
		}
		_ = json.Unmarshal(raw, &head)
		switch head.Line {
		case history.LineCycle:
			var c history.Cycle
			if json.Unmarshal(raw, &c) == nil {
				t.cycle(c)
			}
		case history.LineReceipt:
			var r history.Receipt
			if json.Unmarshal(raw, &r) == nil && r.Subject == t.ticket {
				t.seen = true
				t.receipt(r.Receipt)
			}
		}
	}
}

// cycle prints the cycle when what it decided for the ticket differs from the cycle before.
func (t *tracing) cycle(c history.Cycle) {
	t.cycles++
	lines := t.decisions(c)
	key := strings.Join(lines, "\n")
	if len(lines) > 0 {
		t.seen = true
	}
	if key == t.last {
		return
	}
	t.last = key
	when := clock(c.Now)
	why := ""
	if c.Why != "" {
		why = " (" + c.Why + ")"
	}
	if len(lines) == 0 {
		_, _ = fmt.Fprintf(t.out, "%s cycle #%d%s  wants nothing for %s\n", when, t.cycles, why, t.ticket)
		return
	}
	for _, line := range lines {
		_, _ = fmt.Fprintf(t.out, "%s cycle #%d%s  %s\n", when, t.cycles, why, line)
	}
}

// decisions lists what the cycle decided for the ticket, one line per rule.
func (t *tracing) decisions(c history.Cycle) []string {
	var lines []string
	add := func(rule, text string) {
		read := ""
		if r := c.Reads[rule+"/"+t.ticket]; len(r) > 0 {
			read = "  read " + strings.Join(r, " ")
		}
		lines = append(lines, fmt.Sprintf("%s  %s%s", rule, text, read))
	}
	for _, e := range c.Wants {
		if e.Subject == t.ticket {
			add(e.Rule, fmt.Sprintf("wants %s once %s", e.Kind, FormatOnce(e.Once)))
		}
	}
	for _, e := range c.Waiting {
		if e.Subject == t.ticket {
			add(e.Rule, fmt.Sprintf("waits to %s: %s", e.Kind, e.Reason))
		}
	}
	for _, e := range c.Held {
		if e.Subject != t.ticket {
			continue
		}
		if e.Dropped {
			add(e.Rule, "dropped: "+e.Reason)
		} else {
			add(e.Rule, "held: "+e.Reason)
		}
	}
	for _, e := range c.Covered {
		if e.Subject == t.ticket {
			add(e.Rule, fmt.Sprintf("wants %s once %s, covered: %s", e.Kind, FormatOnce(e.Once), e.Reason))
		}
	}
	for _, e := range c.Throws {
		if e.Subject == t.ticket {
			how := "threw"
			if e.Timeout {
				how = "ran out of time"
			}
			add(e.Rule, how+": "+e.Error)
		}
	}
	slices.Sort(lines)
	return lines
}

func (t *tracing) receipt(r ledger.Receipt) {
	when, state := r.Ended, string(r.Outcome)
	if when == 0 {
		when, state = r.Started, "attempted"
	}
	text := fmt.Sprintf("%s receipt  %s %s %s once %s", clock(when), r.Kind, r.Subject, state, FormatOnce(r.Once))
	if r.Detail != "" {
		text += ": " + r.Detail
	}
	_, _ = fmt.Fprintln(t.out, text)
}

func clock(ms int64) string { return time.UnixMilli(ms).Format(time.TimeOnly) }
