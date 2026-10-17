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
// changed what the rules decided for it (a want, wait, hold, throw or dropped event), with
// what each rule read, and each receipt. It returns ErrNoRecord when history never mentions the ticket.
func Trace(dir, ticket string, out io.Writer) error {
	paths, err := history.Segments(dir)
	if err != nil {
		return err
	}
	tr := &tracing{out: out, ticket: ticket, printed: map[string]bool{}, last: unprinted}
	for _, p := range paths {
		seg, err := history.Read(p)
		if errors.Is(err, history.ErrIncomplete) {
			continue
		}
		if err != nil {
			return err
		}
		tr.segment(seg)
		if tr.err != nil {
			return tr.err
		}
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
	// cycles numbers the cycle lines read across the kept segments, so a number is a position in this
	// printout and not an id.
	cycles int
	// printed holds the receipts already shown, because a snapshot carries the receipts of the segments before it.
	printed map[string]bool
	// last is what the previous cycle decided for the ticket, without what the rules read. It starts as
	// a value no decision equals, so the first cycle that knows the ticket prints.
	last string
	// lastHTTP is the exchanges most recently printed, so an answer that changed prints even when the decision didn't.
	lastHTTP []string
	// err is the first write to out that failed.
	err error
}

// unprinted is the value of tracing.last before any decision has been shown.
const unprinted = "\x00"

// decision is what one rule decided for the ticket.
type decision struct{ rule, text string }

func (t *tracing) printf(format string, args ...any) {
	if t.err == nil {
		_, t.err = fmt.Fprintf(t.out, format, args...)
	}
}

func (t *tracing) segment(seg history.Segment) {
	for _, rc := range seg.Snapshot.Ledger.Receipts {
		if rc.Subject == t.ticket {
			t.seen = true
			t.receipt(rc)
		}
	}
	for fact := range seg.Snapshot.World.Facts {
		if strings.HasPrefix(fact, "linear.ticket/"+t.ticket+"/") {
			t.seen = true
		}
	}
	// Only a rotated segment continues the run before it, so every other segment (a start or a reset)
	// shows its first cycle again.
	if seg.Header.Why != "rotate" {
		t.last, t.lastHTTP = unprinted, nil
	}
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
		case history.LineWorld:
			var w history.World
			if json.Unmarshal(raw, &w) == nil {
				for _, c := range w.Changes {
					if c.Record == "linear.ticket/"+t.ticket {
						t.seen = true
					}
				}
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

// cycle prints the cycle when what it decided for the ticket differs from the cycle before, and the
// exchanges the ticket's rules waited on when those differ from the last ones printed.
func (t *tracing) cycle(c history.Cycle) {
	t.cycles++
	decided := t.decisions(c)
	keys := make([]string, len(decided))
	for i, d := range decided {
		keys[i] = d.rule + "  " + d.text
	}
	key := strings.Join(keys, "\n")
	if len(decided) > 0 {
		t.seen = true
	}
	exchanges := t.exchanges(c)
	// A ticket history knows but no rule has wanted anything for still shows its first, empty decision.
	newDecision := !(len(decided) == 0 && !t.seen) && key != t.last
	newAnswers := len(exchanges) > 0 && !slices.Equal(exchanges, t.lastHTTP)
	if !newDecision && !newAnswers {
		return
	}
	prefix := fmt.Sprintf("%s cycle #%d", clock(c.Now), t.cycles)
	if c.Why != "" {
		prefix += " (" + c.Why + ")"
	}
	if newDecision {
		t.last = key
		if len(decided) == 0 {
			t.printf("%s  wants nothing for %s\n", prefix, t.ticket)
		}
		for i, d := range decided {
			read := ""
			if r := c.Reads[d.rule+"/"+t.ticket]; len(r) > 0 {
				read = "  read " + strings.Join(r, " ")
			}
			t.printf("%s  %s%s\n", prefix, keys[i], read)
		}
	}
	t.lastHTTP = exchanges
	for _, line := range exchanges {
		t.printf("%s  %s\n", prefix, line)
	}
}

// exchanges lists the responses the ticket's rules waited on in the cycle, one line each.
func (t *tracing) exchanges(c history.Cycle) []string {
	var out []string
	for _, ex := range c.HTTP {
		if ex.Ticket != t.ticket {
			continue
		}
		answer := fmt.Sprintf("%d %s", ex.Status, ex.Body)
		if ex.Unknown != "" {
			answer = "no answer: " + ex.Unknown
		}
		out = append(out, fmt.Sprintf("http  %s  GET %s %s  %s", ex.Rule, ex.Service, ex.Path, answer))
	}
	return out
}

// decisions lists what the cycle decided for the ticket, without what the rules read, so a rule that
// reads a new value and decides the same thing doesn't make another cycle.
func (t *tracing) decisions(c history.Cycle) []decision {
	var out []decision
	add := func(rule, text string) { out = append(out, decision{rule, text}) }
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
	slices.SortFunc(out, func(a, b decision) int { return strings.Compare(a.rule+a.text, b.rule+b.text) })
	return out
}

func (t *tracing) receipt(r ledger.Receipt) {
	when, state := r.Ended, string(r.Outcome)
	if when == 0 {
		when, state = r.Started, "attempted"
	}
	key := fmt.Sprint(r.Key(), r.Started, state)
	if t.printed[key] {
		return
	}
	t.printed[key] = true
	if r.Reset {
		state += " (reset, never sent)"
	}
	text := fmt.Sprintf("%s receipt  %s %s %s once %s", clock(when), r.Kind, r.Subject, state, FormatOnce(r.Once))
	if r.Detail != "" {
		text += ": " + r.Detail
	}
	t.printf("%s\n", text)
}

func clock(ms int64) string { return time.UnixMilli(ms).Format(time.TimeOnly) }
