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
func Trace(dir, ticket string, out io.Writer) error { return trace(dir, ticket, out, false) }

// TraceJSON is Trace printing each line as one JSON object (a TraceLine) instead of text.
func TraceJSON(dir, ticket string, out io.Writer) error { return trace(dir, ticket, out, true) }

func trace(dir, ticket string, out io.Writer, asJSON bool) error {
	paths, err := history.Segments(dir)
	if err != nil {
		return err
	}
	tr := &tracing{out: out, asJSON: asJSON, ticket: ticket, printed: map[string]bool{}, last: unprinted}
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

// TraceLine is one line of `dispatch trace --json`. Type is "cycle", "http" or "receipt". A field that
// doesn't apply to the line type, or holds its zero value, is left out.
type TraceLine struct {
	Type string `json:"type"`
	// Time is the cycle's time, or when the receipt started (state attempted) or ended.
	Time time.Time `json:"time"`
	// Cycle is the cycle line's position in this printout, as the text form numbers it.
	Cycle int `json:"cycle,omitempty"`
	// Why is what started the cycle.
	Why  string `json:"why,omitempty"`
	Rule string `json:"rule,omitempty"`
	// Decision is wants, waits, held, dropped, covered, threw, timeout or nothing.
	Decision string `json:"decision,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Subject  string `json:"subject,omitempty"`
	// State is attempted, or the receipt's outcome.
	State string `json:"state,omitempty"`
	// Once is the once key as the text form prints it.
	Once   string `json:"once,omitempty"`
	Detail string `json:"detail,omitempty"`
	Reset  bool   `json:"reset,omitempty"`
	// Read is what the rule read, as the text form prints it after "read".
	Read    []string `json:"read,omitempty"`
	Service string   `json:"service,omitempty"`
	Path    string   `json:"path,omitempty"`
	Status  int      `json:"status,omitempty"`
	// Body is the response body, as the JSON the service answered with.
	Body json.RawMessage `json:"body,omitempty"`
	// Unknown is why no answer is known.
	Unknown string `json:"unknown,omitempty"`
}

// with is the cycle's own fields (type, time, cycle number, why and subject) on top of what a rule's line says.
func (head TraceLine) with(line TraceLine) TraceLine {
	line.Type, line.Time, line.Cycle, line.Why, line.Subject = head.Type, head.Time, head.Cycle, head.Why, head.Subject
	return line
}

type tracing struct {
	out    io.Writer
	asJSON bool
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
	lastHTTP []exchange
	// err is the first write to out that failed.
	err error
}

// unprinted is the value of tracing.last before any decision has been shown.
const unprinted = "\x00"

// decision is what one rule decided for the ticket, as the text form prints it and as a TraceLine.
type decision struct {
	rule, text string
	line       TraceLine
}

// emit writes one line, as text or as the JSON object for it.
func (t *tracing) emit(text string, line TraceLine) {
	if t.err != nil {
		return
	}
	if t.asJSON {
		t.err = json.NewEncoder(t.out).Encode(line)
		return
	}
	_, t.err = fmt.Fprintln(t.out, text)
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
	newDecision := (len(decided) > 0 || t.seen) && key != t.last
	newAnswers := len(exchanges) > 0 && !slices.EqualFunc(exchanges, t.lastHTTP, func(a, b exchange) bool { return a.text == b.text })
	if !newDecision && !newAnswers {
		return
	}
	at := time.UnixMilli(c.Now)
	prefix := fmt.Sprintf("%s cycle #%d", clock(c.Now), t.cycles)
	if c.Why != "" {
		prefix += " (" + c.Why + ")"
	}
	head := TraceLine{Type: "cycle", Time: at.UTC(), Cycle: t.cycles, Why: c.Why, Subject: t.ticket}
	if newDecision {
		t.last = key
		if len(decided) == 0 {
			t.emit(fmt.Sprintf("%s  wants nothing for %s", prefix, t.ticket), head.with(TraceLine{Decision: "nothing"}))
		}
		for i, d := range decided {
			read := ""
			line := head.with(d.line)
			if r := c.Reads[d.rule+"/"+t.ticket]; len(r) > 0 {
				read = "  read " + strings.Join(r, " ")
				line.Read = r
			}
			t.emit(fmt.Sprintf("%s  %s%s", prefix, keys[i], read), line)
		}
	}
	t.lastHTTP = exchanges
	for _, ex := range exchanges {
		line := head.with(ex.line)
		line.Type = "http"
		t.emit(prefix+"  "+ex.text, line)
	}
}

// exchange is one response a rule waited on, as the text form prints it and as a TraceLine.
type exchange struct {
	text string
	line TraceLine
}

// exchanges lists the responses the ticket's rules waited on in the cycle, one line each.
func (t *tracing) exchanges(c history.Cycle) []exchange {
	var out []exchange
	for _, ex := range c.HTTP {
		if ex.Ticket != t.ticket {
			continue
		}
		answer := fmt.Sprintf("%d %s", ex.Status, ex.Body)
		line := TraceLine{Rule: ex.Rule, Service: ex.Service, Path: ex.Path, Status: ex.Status, Body: ex.Body}
		if ex.Unknown != "" {
			answer = "no answer: " + ex.Unknown
			line = TraceLine{Rule: ex.Rule, Service: ex.Service, Path: ex.Path, Unknown: ex.Unknown}
		}
		out = append(out, exchange{text: fmt.Sprintf("http  %s  GET %s %s  %s", ex.Rule, ex.Service, ex.Path, answer), line: line})
	}
	return out
}

// decisions lists what the cycle decided for the ticket, without what the rules read, so a rule that
// reads a new value and decides the same thing doesn't make another cycle.
func (t *tracing) decisions(c history.Cycle) []decision {
	var out []decision
	add := func(rule, text string, line TraceLine) {
		line.Rule = rule
		out = append(out, decision{rule: rule, text: text, line: line})
	}
	for _, e := range c.Wants {
		if e.Subject == t.ticket {
			once := FormatOnce(e.Once)
			add(e.Rule, fmt.Sprintf("wants %s once %s", e.Kind, once), TraceLine{Decision: "wants", Kind: e.Kind, Once: once})
		}
	}
	for _, e := range c.Waiting {
		if e.Subject == t.ticket {
			add(e.Rule, fmt.Sprintf("waits to %s: %s", e.Kind, e.Reason), TraceLine{Decision: "waits", Kind: e.Kind, Detail: e.Reason})
		}
	}
	for _, e := range c.Held {
		if e.Subject != t.ticket {
			continue
		}
		if e.Dropped {
			add(e.Rule, "dropped: "+e.Reason, TraceLine{Decision: "dropped", Detail: e.Reason})
		} else {
			add(e.Rule, "held: "+e.Reason, TraceLine{Decision: "held", Detail: e.Reason})
		}
	}
	for _, e := range c.Covered {
		if e.Subject == t.ticket {
			once := FormatOnce(e.Once)
			add(e.Rule, fmt.Sprintf("wants %s once %s, covered: %s", e.Kind, once, e.Reason), TraceLine{Decision: "covered", Kind: e.Kind, Once: once, Detail: e.Reason})
		}
	}
	for _, e := range c.Throws {
		if e.Subject == t.ticket {
			how, word := "threw", "threw"
			if e.Timeout {
				how, word = "ran out of time", "timeout"
			}
			add(e.Rule, how+": "+e.Error, TraceLine{Decision: word, Detail: e.Error})
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
	line := TraceLine{
		Type: "receipt", Time: time.UnixMilli(when).UTC(), Kind: r.Kind, Subject: r.Subject, State: state,
		Once: FormatOnce(r.Once), Detail: r.Detail, Reset: r.Reset, Rule: r.Rule,
	}
	if r.Reset {
		state += " (reset, never sent)"
	}
	text := fmt.Sprintf("%s receipt  %s %s %s once %s", clock(when), r.Kind, r.Subject, state, line.Once)
	if r.Detail != "" {
		text += ": " + r.Detail
	}
	t.emit(text, line)
}

func clock(ms int64) string { return time.UnixMilli(ms).Format(time.TimeOnly) }
