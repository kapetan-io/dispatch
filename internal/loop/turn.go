package loop

import (
	"fmt"
	"strings"

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
	// A hook report lost while dispatch was down leaves no trace on disk, so the harness's own
	// record of the session decides, and a harness that can't say is read as a message taken.
	h, ok := l.o.Executor.Harnesses.ForAgent(wk.Kind)
	if !ok {
		return inTurn
	}
	took, err := h.TookMessageAfter(closing.Transcript, closing.At)
	if err != nil || took {
		return inTurn
	}
	if closing.Message == "" {
		return refuse("%s's turn has no closing message; read its workspace", ticket)
	}
	return Response{Turn: &Turn{At: closing.At, Message: closing.Message, Cut: closing.Cut}}
}
