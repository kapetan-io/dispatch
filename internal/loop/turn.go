package loop

import (
	"fmt"
	"strings"

	"github.com/kapetan-io/dispatch/internal/ledger"
	"github.com/kapetan-io/dispatch/internal/sessionlog"
	"github.com/kapetan-io/dispatch/internal/stops"
	"github.com/kapetan-io/dispatch/internal/world"
)

// turn answers the turn the ticket's worker waits at and its closing message. It refuses while
// the worker is in a turn, because what it would print is a turn the worker has moved past.
func (l *Loop) turn(r request) Response {
	ticket := strings.ToUpper(r.Ticket)
	refuse := func(format string, args ...any) Response {
		return Response{Code: CodeFailed, Error: fmt.Sprintf(format, args...)}
	}
	if l.wd.MultiplexerErr != nil {
		return refuse("%v", l.wd.MultiplexerErr)
	}
	wk := world.FindWorker(ticket, l.wd.Trees, l.wd.Snapshot)
	inTurn := refuse("%s's worker is in a turn; nothing to read until it ends", ticket)
	if l.o.Executor == nil {
		return refuse("dispatch has no harnesses to ask")
	}
	switch wk.State {
	case world.NoWorker:
		return refuse("%s", l.noWorker(ticket))
	case world.Blocked:
		return refuse("%s's worker is blocked on a question; answer it in its workspace", ticket)
	case world.Working:
		return inTurn
	}
	dir := l.o.StateDir
	rec, ok := stops.Read(dir, wk.TreePath, wk.Session)
	if !ok {
		return refuse("%s's worker has ended no turn yet", ticket)
	}
	// A log that can't be read leaves the Stop alone to decide, as the world does.
	log, _ := sessionlog.Read(dir, wk.TreePath, wk.Session)
	if rec.InTurn(log, l.clock.Now()) {
		return inTurn
	}
	closing, ok := stops.ReadClosing(dir, wk.TreePath, wk.Session, rec.At.UnixMilli())
	if !ok {
		return refuse("%s's turn has no closing message; read its workspace", ticket)
	}
	if took, known := l.harnessTookMessage(wk, closing); took || !known {
		return inTurn
	}
	if closing.Message == "" {
		return refuse("%s's turn has no closing message; read its workspace", ticket)
	}
	return Response{Turn: &Turn{At: closing.At, Message: closing.Message, Cut: closing.Cut}}
}

// tellTurn sends text to the ticket's worker at most once for the turn that ended at r.Turn. The
// ledger decides "already told" before the worker's state does, so a repeat answers the same
// after the worker moved on or its session relaunched, and an attempt whose outcome was lost
// still counts as sent.
func (l *Loop) tellTurn(r request) error {
	ticket := strings.ToUpper(r.Ticket)
	at := r.Turn
	refuse := func(format string, args ...any) error {
		r.reply <- Response{Code: CodeFailed, Error: fmt.Sprintf(format, args...)}
		return nil
	}
	if refusal, ok := l.refuseTell(ticket); ok {
		r.reply <- refusal
		return nil
	}
	once := ledger.Once{"tell-turn", ticket, at}
	if l.led.Done(ticket, once) {
		r.reply <- Response{Lines: []string{fmt.Sprintf("already told %s for turn %d", ticket, at)}}
		return nil
	}
	if l.o.Executor == nil {
		return refuse("dispatch has no harnesses to ask")
	}
	wk := world.FindWorker(ticket, l.wd.Trees, l.wd.Snapshot)
	dir := l.o.StateDir
	rec, stopped := stops.Read(dir, wk.TreePath, wk.Session)
	log, _ := sessionlog.Read(dir, wk.TreePath, wk.Session)
	ended := stopped && rec.At.UnixMilli() == at
	later := stopped && rec.At.UnixMilli() > at
	for _, t := range log.Turns {
		ended = ended || t.At == at
	}
	if !ended {
		return refuse("%s's worker has no turn that ended at %d", ticket, at)
	}
	moved := "%s's worker has moved past that turn; nothing sent"
	if later || log.TookMessageAfter(at) {
		return refuse(moved, ticket)
	}
	working := "%s's worker is working; nothing sent"
	if wk.State == world.Working || (stopped && rec.At.UnixMilli() == at && rec.Busy(l.clock.Now())) {
		return refuse(working, ticket)
	}
	closing, ok := stops.ReadClosing(dir, wk.TreePath, wk.Session, at)
	if !ok {
		return refuse(working, ticket)
	}
	took, known := l.harnessTookMessage(wk, closing)
	if !known {
		return refuse(working, ticket)
	}
	if took {
		return refuse(moved, ticket)
	}
	return l.sendTell(r, ticket, once)
}

// harnessTookMessage asks the worker's harness whether its session took a message after the turn
// closing describes. A hook report lost while dispatch was down leaves no trace on disk, so the
// harness's own record is the only witness. known is false when the harness can't say.
func (l *Loop) harnessTookMessage(wk world.Worker, closing stops.Closing) (took, known bool) {
	h, ok := l.o.Executor.Harnesses.ForAgent(wk.Kind)
	if !ok {
		return false, false
	}
	took, err := h.TookMessageAfter(closing.Transcript, closing.At)
	return took, err == nil
}
