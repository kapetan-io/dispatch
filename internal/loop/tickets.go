package loop

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/kapetan-io/dispatch/internal/history"
	"github.com/kapetan-io/dispatch/internal/localticket"
	"github.com/kapetan-io/dispatch/internal/ticket"
	"github.com/kapetan-io/dispatch/internal/world"
)

// LocalOff is what a ticket request gets from a dispatch that started without a [plugins.local] table.
const LocalOff = "local tickets are off: config.toml has no [plugins.local] table"

// onTicket answers a `dispatch ticket` verb on the loop goroutine.
func (l *Loop) onTicket(call *TicketCall) Response {
	if l.local == nil {
		return Response{Code: CodeFailed, Error: LocalOff}
	}
	if call == nil {
		return Response{Code: CodeFailed, Error: "a ticket request needs a call"}
	}
	if call.Verb == VerbTracked {
		snap, err := l.local.Listing()
		if err != nil {
			return Response{Code: CodeFailed, Error: err.Error()}
		}
		return Response{Tracked: &snap}
	}
	lines, err := l.ticketVerb(call)
	if err != nil {
		return Response{Code: CodeFailed, Error: err.Error()}
	}
	switch call.Verb {
	case VerbCreate, VerbEdit, VerbMove, VerbComment:
		l.readAgain(world.FromLocalTickets)
	}
	return Response{Lines: lines}
}

func (l *Loop) ticketVerb(c *TicketCall) ([]string, error) {
	switch c.Verb {
	case VerbCreate:
		if c.Draft == nil {
			return nil, errors.New("create needs a draft")
		}
		// The filer is never taken from the request: a rule's text reaches a worker through show.
		draft := *c.Draft
		draft.FiledBy = "you"
		id, err := l.local.Create(draft)
		return []string{id}, err
	case VerbEdit:
		if c.Edit == nil {
			return nil, errors.New("edit needs changes")
		}
		return []string{c.ID}, l.local.Edit(c.ID, *c.Edit)
	case VerbMove:
		status, err := l.local.Move(c.ID, c.Status)
		if err != nil {
			return nil, err
		}
		return []string{c.ID + " " + status}, nil
	case VerbComment:
		return nil, l.local.Comment(c.ID, l.commentAuthor(c.Tree), c.Body)
	case VerbShow:
		d, err := l.local.Show(c.ID)
		if err != nil {
			return nil, err
		}
		return d.Lines(), nil
	case VerbList:
		var f localticket.Filter
		if c.Filter != nil {
			f = *c.Filter
		}
		found, err := l.local.Find(f)
		return localticket.ListLines(found), err
	case VerbStatuses:
		return localticket.StatusLines(l.local.Statuses()), nil
	}
	return nil, fmt.Errorf("unknown ticket verb %q", c.Verb)
}

// commentAuthor is the ticket that leases the tree a comment was written in, else you. A tree is
// named by whoever sends it, so this is attribution, not authentication.
func (l *Loop) commentAuthor(tree string) string {
	if tree != "" {
		for _, t := range l.wd.Trees {
			if t.Path == tree && t.LeaseHolder != "" {
				return t.LeaseHolder
			}
		}
	}
	return "you"
}

var localIDPattern = regexp.MustCompile(`\b` + ticket.LocalPrefix + `([0-9]+)\b`)

// highestLocal is the highest LOC-<n> any of the values name, once marshaled to JSON. It matches the
// text of every field, so a LOC- id quoted in a title counts too, and the start refuses the file loudly.
func highestLocal(values ...any) (uint64, error) {
	var highest uint64
	for _, v := range values {
		raw, err := json.Marshal(v)
		if err != nil {
			return 0, err
		}
		for _, m := range localIDPattern.FindAllSubmatch(raw, -1) {
			if n, err := strconv.ParseUint(string(m[1]), 10, 64); err == nil {
				highest = max(highest, n)
			}
		}
	}
	return highest, nil
}

// openLocal opens the database, refusing a file whose next number is at or below any LOC- id in history, memory or a tree's lease.
func (l *Loop) openLocal(seg history.Segment, st history.State, loaded bool) error {
	if l.o.Local == nil {
		return nil
	}
	o := *l.o.Local
	o.Now = l.clock.Now
	if loaded {
		var trees any
		if r := seg.Snapshot.Read; r != nil && r.World != nil {
			trees = r.World.Trees
		}
		var err error
		if o.Highest, err = highestLocal(st.Memory, st.Ledger.Entries, trees); err != nil {
			return err
		}
		o.Highest = max(o.Highest, seg.Header.LocalHighest)
	}
	store, err := localticket.Open(o)
	if err != nil {
		return err
	}
	l.local = store
	l.o.Sources.Local = store
	if l.o.Executor != nil {
		l.o.Executor.UseLocal(store)
	}
	return nil
}
