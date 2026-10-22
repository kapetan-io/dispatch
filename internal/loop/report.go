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
	"github.com/kapetan-io/dispatch/internal/sessionlog"
	"github.com/kapetan-io/dispatch/internal/stops"
	"github.com/kapetan-io/dispatch/internal/world"
)

// waiter is a tick waiting for the loop to settle.
type waiter struct {
	ticket string
	reply  chan Response
	// seen is how many actions had ended when the tick asked. Later ones are its news.
	seen int
}

func sameCycle(a, b history.Cycle) bool {
	a.Now, b.Now, a.Why, b.Why = 0, 0, "", ""
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// readingKey identifies the world and reservations of a read. It leaves out the clock in
// the world, which moves with every cycle.
func readingKey(r history.Reading) string {
	w := *r.World
	w.At = time.Time{}
	b, _ := json.Marshal(history.Reading{Reserved: r.Reserved, World: &w})
	return string(b)
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
		seen := l.endedBefore + len(l.ended)
		if err := l.onResync(history.EventTick); err != nil {
			return err
		}
		l.waiters = append(l.waiters, &waiter{ticket: strings.ToUpper(r.Ticket), reply: r.reply, seen: seen})
	case OpWake:
		l.readAgain(world.FromLocal)
		r.reply <- Response{}
	case OpTell:
		l.readAgain(world.FromLocal)
		l.afterRead = append(l.afterRead, func() error { return l.tell(r) })
	case OpStop:
		r.reply <- l.onStop(r.Stop)
	case OpPrompt:
		r.reply <- l.onPrompt(r.Prompt)
		l.readAgain(world.FromLocal)
	case OpTyped:
		r.reply <- l.onTyped(r.Typed)
	case OpWatch, OpUnwatch:
		r.reply <- l.onFollow(r)
	default:
		r.reply <- Response{Code: CodeFailed, Error: fmt.Sprintf("unknown request %q", r.Op)}
	}
	return nil
}

// onStop stores a turn a worker's Claude session ended, where the next read finds it. The
// loop is the only writer, so a read never races a half-written record.
func (l *Loop) onStop(s *Stop) Response {
	if s == nil || s.Tree == "" || s.Session == "" {
		return Response{Code: CodeFailed, Error: "a stop report needs a tree and a session"}
	}
	rec := stops.Record{At: time.UnixMilli(s.At).UTC(), Reported: s.Reported, Running: s.Running}
	// A report that waited in the inbox past its hook's timeout can arrive after a newer one, and
	// is dropped so the session's turns stay in order. A stored time ahead of the loop's clock came
	// from a clock that has since stepped back, and must not shut out every report after it.
	cur, ok := stops.Read(l.o.StateDir, s.Tree, s.Session)
	if ok && rec.At.Before(cur.At) && !cur.At.After(l.clock.Now()) {
		return Response{}
	}
	if err := stops.Write(l.o.StateDir, s.Tree, s.Session, rec); err != nil {
		return Response{Code: CodeFailed, Error: err.Error()}
	}
	turn := sessionlog.Turn{At: s.At, Running: s.Running > 0}
	if err := sessionlog.AppendTurn(l.o.StateDir, s.Tree, s.Session, turn); err != nil {
		return Response{Code: CodeFailed, Error: err.Error()}
	}
	return Response{}
}

// onPrompt stores a command a worker's Claude session took, with who typed it, where the next
// read finds it. A message that is not a command is only a wake. Take answers one message per
// send, so a person who types the same text afterward reads as a person.
func (l *Loop) onPrompt(p *Prompt) Response {
	if p == nil || p.Tree == "" || p.Session == "" {
		return Response{Code: CodeFailed, Error: "a prompt report needs a tree and a session"}
	}
	cmd, ok := sessionlog.ParseCommand(p.Text)
	if !ok {
		return Response{}
	}
	cmd.At, cmd.By = p.At, sessionlog.ByPerson
	if l.o.Typed.Take(p.Tree, p.Text) {
		cmd.By = sessionlog.ByDispatch
	}
	if err := sessionlog.AppendCommand(l.o.StateDir, p.Tree, p.Session, cmd); err != nil {
		return Response{Code: CodeFailed, Error: err.Error()}
	}
	return Response{}
}

// onTyped keeps the note of a launch that runs in another process.
func (l *Loop) onTyped(t *Typed) Response {
	if t == nil || t.Tree == "" {
		return Response{Code: CodeFailed, Error: "a typed report needs a tree"}
	}
	if t.Clear {
		l.o.Typed.Clear(t.Tree)
	} else {
		l.o.Typed.Note(t.Tree, t.Text)
	}
	return Response{}
}

// onFollow adds or removes a session on a ticket's followers.
func (l *Loop) onFollow(r request) Response {
	if l.o.Executor == nil {
		return Response{Code: CodeFailed, Error: "this dispatch has no followers to keep"}
	}
	f := l.o.Executor.Followers
	if r.Op == OpUnwatch {
		f.Unfollow(r.Ticket, r.Socket)
		return Response{}
	}
	if err := f.Follow(r.Ticket, r.Socket); err != nil {
		return Response{Code: CodeFailed, Error: err.Error()}
	}
	return Response{}
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
// `dispatch cycle`'s words, and the exit code it ends with.
func (l *Loop) report(w *waiter) Response {
	var lines, failures []string
	code := CodeSettled
	for _, e := range l.ended[max(0, w.seen-l.endedBefore):] {
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
	for _, t := range l.result.Throws {
		if w.ticket == "" || t.Subject == w.ticket {
			how := "threw"
			if t.Timeout {
				how = "ran out of time"
			}
			lines = append(lines, fmt.Sprintf("%s %s %s: %s", t.Subject, t.Rule, how, t.Error))
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
func (l *Loop) tell(r request) error {
	ticket := strings.ToUpper(r.Ticket)
	if l.wd.HerdrErr != nil {
		r.reply <- Response{Code: CodeFailed, Error: l.wd.HerdrErr.Error()}
		return nil
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
		return nil
	case "blocked":
		r.reply <- Response{Code: CodeFailed, Error: fmt.Sprintf("%s's worker is blocked on a question; answer it in its herdr workspace", ticket)}
		return nil
	}
	l.tells++
	want := rules.Want{
		Rule: "tell", Receipt: "nudge", Subject: ticket,
		Once: ledger.Once{"tell", l.ingest(), l.tells}, Args: map[string]any{"text": r.Text},
	}
	if err := l.begin(want, nil, r.reply); err != nil {
		r.reply <- Response{Code: CodeFailed, Error: err.Error()}
		return err
	}
	return nil
}
