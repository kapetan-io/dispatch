package loop

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/history"
	"github.com/kapetan-io/dispatch/internal/ledger"
	"github.com/kapetan-io/dispatch/internal/rules"
	"github.com/kapetan-io/dispatch/internal/world"
)

// waiter is a tick waiting for the loop to settle.
type waiter struct {
	ticket string
	reply  chan Response
	// seen is how many actions had ended when the tick asked. Later ones are its news.
	seen int
}

func cycleLine(now int64, why string, res rules.Result) history.Cycle {
	c := history.Cycle{Line: history.LineCycle, Now: now, Why: why, Reads: map[string][]string{}}
	for _, w := range res.Wants {
		c.Wants = append(c.Wants, history.Entry{Rule: w.Rule, Subject: w.Subject, Kind: w.Receipt, Once: w.Once})
		c.Reads[w.Rule+"/"+w.Subject] = res.Reads[w.Rule+"/"+w.Subject]
	}
	for _, w := range res.Waiting {
		c.Waiting = append(c.Waiting, history.Entry{Rule: w.Rule, Subject: w.Subject, Kind: w.Receipt, Once: w.Once, Reason: w.Reason})
	}
	for _, h := range res.Held {
		c.Held = append(c.Held, history.Entry{Rule: h.Rule, Subject: h.Subject, Reason: h.Reason})
		c.Reads[h.Rule+"/"+h.Subject] = h.Reads
	}
	for _, cv := range res.Covered {
		c.Covered = append(c.Covered, history.Entry{Rule: cv.Rule, Subject: cv.Subject, Kind: cv.Receipt.Kind, Once: cv.Once, Reason: coveredBy(cv.Receipt)})
	}
	for _, t := range res.Throws {
		c.Throws = append(c.Throws, history.Throw{Rule: t.Rule, Subject: t.Subject, Error: t.Error, Timeout: t.Timeout})
		c.Reads[t.Rule+"/"+t.Subject] = res.Reads[t.Rule+"/"+t.Subject]
	}
	if len(c.Reads) == 0 {
		c.Reads = nil
	}
	return c
}

// coveredBy says how a receipt stands in for running an action again.
func coveredBy(rc ledger.Receipt) string {
	outcome := string(rc.Outcome)
	if outcome == "" {
		outcome = "attempted"
	}
	return outcome + " " + time.UnixMilli(rc.Started).Format("15:04")
}

func sameCycle(a, b history.Cycle) bool {
	a.Now, b.Now, a.Why, b.Why = 0, 0, "", ""
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// onRequest answers a client.
func (l *Loop) onRequest(r request) error {
	switch r.Op {
	case OpIdle:
		r.reply <- Response{Idle: l.idle, Busy: l.busy()}
	case OpPending:
		r.reply <- Response{Lines: l.pendingLines(), Code: CodeFailed}
	case OpSettle:
		l.waiters = append(l.waiters, &waiter{ticket: strings.ToUpper(r.Ticket), reply: r.reply})
		if !l.busy() {
			l.release()
		}
	case OpTick:
		seen := len(l.log)
		if err := l.onResync("dispatch.tick"); err != nil {
			return err
		}
		l.waiters = append(l.waiters, &waiter{ticket: strings.ToUpper(r.Ticket), reply: r.reply, seen: seen})
	case OpTell:
		l.want(world.FromLocal)
		l.afterRead = append(l.afterRead, func() { l.tell(r) })
	default:
		r.reply <- Response{Code: CodeFailed, Error: fmt.Sprintf("unknown request %q", r.Op)}
	}
	return nil
}

// release answers the ticks that were waiting for the loop to settle.
func (l *Loop) release() {
	for _, w := range l.waiters {
		w.reply <- l.report(w)
	}
	l.waiters = nil
}

// pendingLines names what a tick that gave up was still waiting for.
func (l *Loop) pendingLines() []string {
	var out []string
	for _, f := range l.flights {
		out = append(out, fmt.Sprintf("%s %s is still running (%s)", f.want.Subject, f.want.Receipt, f.want.Rule))
	}
	if l.reading || l.pending != 0 {
		out = append(out, "sources are still being read")
	}
	slices.Sort(out)
	return out
}

var verbs = map[string]string{
	"launch": "launched", "nudge": "nudged", "move": "moved", "comment": "commented", "escalate": "escalated",
	"close": "closed", "return": "returned", "merge": "merged", "notify": "notified",
}

// report is what a tick prints once the loop has settled, one line per ticket in
// plan's words, and the exit code it ends with.
func (l *Loop) report(w *waiter) Response {
	var lines, failures []string
	code := CodeSettled
	for _, e := range l.log[w.seen:] {
		rc := e.rc
		if w.ticket != "" && rc.Subject != w.ticket {
			continue
		}
		detail := strings.TrimPrefix(rc.Detail, "ok")
		detail = strings.TrimPrefix(detail, ": ")
		switch rc.Outcome {
		case ledger.Done:
			lines = append(lines, join(rc.Subject, verbs[rc.Kind], detail))
		case ledger.Refused:
			lines = append(lines, join(rc.Subject, rc.Kind+" refused", rc.Detail))
			if rc.Detail != "pool_exhausted" {
				code = CodeFailed
				failures = append(failures, fmt.Sprintf("%s %s refused: %s", rc.Subject, rc.Kind, rc.Detail))
			}
		case ledger.Failed:
			lines = append(lines, join(rc.Subject, rc.Kind+" failed", rc.Detail))
			code = CodeFailed
			failures = append(failures, fmt.Sprintf("%s %s failed: %s", rc.Subject, rc.Kind, rc.Detail))
		}
	}
	for _, x := range l.result.Waiting {
		if w.ticket == "" || x.Subject == w.ticket {
			lines = append(lines, fmt.Sprintf("%s queued: %s, %s", x.Subject, x.Receipt, x.Reason))
		}
	}
	for _, h := range l.result.Held {
		if w.ticket == "" || h.Subject == w.ticket {
			lines = append(lines, fmt.Sprintf("%s held: %s", h.Subject, h.Reason))
		}
	}
	return Response{Lines: dedupe(lines), Code: code, Error: strings.Join(failures, "; ")}
}

func join(subject, verb, detail string) string {
	out := subject + " " + verb
	if detail != "" {
		out += ": " + detail
	}
	return out
}

func dedupe(lines []string) []string {
	var out []string
	for _, s := range lines {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// tellResponse is what a tell prints once its nudge has a receipt.
func tellResponse(w rules.Want, rc ledger.Receipt) Response {
	switch {
	case rc.Outcome == ledger.Done:
		return Response{Lines: []string{"told " + w.Subject}}
	case rc.Detail == "blocked":
		return Response{Code: CodeFailed, Error: fmt.Sprintf("%s's worker is blocked on a question; answer it in its herdr workspace", w.Subject)}
	}
	return Response{Code: CodeFailed, Error: rc.Detail}
}

// tell types text into a ticket's worker through the nudge path, so it is
// receipted like any other nudge and counts as the last one.
func (l *Loop) tell(r request) {
	ticket := strings.ToUpper(r.Ticket)
	if l.wd.HerdrErr != nil {
		r.reply <- Response{Code: CodeFailed, Error: l.wd.HerdrErr.Error()}
		return
	}
	wk := workerOf(l.wd, ticket)
	switch wk {
	case "none":
		var unread []string
		for label, err := range l.wd.PoolErrs {
			unread = append(unread, fmt.Sprintf("%s (%v)", label, err))
		}
		slices.Sort(unread)
		msg := fmt.Sprintf("%s has no worker", ticket)
		if len(unread) > 0 {
			msg += "; could not read the pool of " + strings.Join(unread, ", ")
		}
		r.reply <- Response{Code: CodeFailed, Error: msg}
		return
	case "blocked":
		r.reply <- Response{Code: CodeFailed, Error: fmt.Sprintf("%s's worker is blocked on a question; answer it in its herdr workspace", ticket)}
		return
	}
	l.tells++
	want := rules.Want{
		Rule: "tell", Kind: "herdr.nudge", Receipt: "nudge", Subject: ticket,
		Once: ledger.Once{"tell", l.ingest(), l.tells}, Args: map[string]any{"text": r.Text},
	}
	if err := l.begin(want, nil, r.reply); err != nil {
		l.fatal(err)
		r.reply <- Response{Code: CodeFailed, Error: err.Error()}
	}
}
