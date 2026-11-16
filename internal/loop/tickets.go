package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/history"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/localticket"
	"github.com/kapetan-io/dispatch/internal/ticket"
	"github.com/kapetan-io/dispatch/internal/world"
)

// LocalOff is what a ticket request gets from a dispatch that started without a [plugins.local] table.
const LocalOff = "local tickets are off: config.toml has no [plugins.local] table"

// onTicket answers a `dispatch ticket` verb. A verb that writes to Linear is answered once the
// write ends off the loop goroutine, every other verb before onTicket returns.
func (l *Loop) onTicket(call *TicketCall, reply chan Response) {
	if call == nil {
		reply <- Response{Code: CodeFailed, Error: "a ticket request needs a call"}
		return
	}
	createsOnLinear := call.Verb == VerbCreate && call.System == ticket.SystemLinear
	editsOnLinear := call.Verb == VerbEdit && ticket.SystemOf(call.ID) == ticket.SystemLinear
	if createsOnLinear || editsOnLinear {
		l.startLinear(call, reply)
		return
	}
	reply <- l.localTicket(call)
}

// localTicket answers a `dispatch ticket` verb that makes no Linear request.
func (l *Loop) localTicket(call *TicketCall) Response {
	if call.Verb == VerbCreate && call.System != ticket.SystemLinear && call.System != ticket.SystemLocal {
		return Response{Code: CodeFailed, Error: "create takes a ticket system first: " + l.systemNames()}
	}
	if call.Team != "" && call.System != ticket.SystemLinear {
		return Response{Code: CodeFailed, Error: "--team applies to create linear only"}
	}
	if call.Verb == VerbTracked {
		return l.tracked()
	}
	if l.local == nil {
		return Response{Code: CodeFailed, Error: LocalOff}
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

// systemNames lists the ticket systems create can file in.
func (l *Loop) systemNames() string {
	if l.local == nil {
		return ticket.SystemLinear
	}
	return ticket.SystemLinear + " or " + ticket.SystemLocal
}

// linearTimeout bounds a Linear write made for a `dispatch ticket` request. An edit's limit starts
// when it reaches Linear, after the edits of the same ticket ahead of it.
const linearTimeout = 30 * time.Second

// linearWrite is a Linear write a `dispatch ticket` request asked for. edits is the Linear ticket
// an edit changes, as team and number, empty for a create.
type linearWrite struct {
	edits string
	run   func(context.Context) ([]string, error)
	reply chan Response
}

// linearDone is a Linear write that ended off the loop goroutine.
type linearDone struct {
	write linearWrite
	lines []string
	err   error
}

// startLinear makes the Linear write a request asks for off the loop goroutine. An edit waits behind
// a running edit of the same ticket, because each reads the description and writes it back whole.
func (l *Loop) startLinear(c *TicketCall, reply chan Response) {
	w, err := l.linearWriteOf(c)
	if err != nil {
		reply <- Response{Code: CodeFailed, Error: err.Error()}
		return
	}
	w.reply = reply
	if w.edits != "" {
		if waiting, running := l.editing[w.edits]; running {
			l.editing[w.edits] = append(waiting, w)
			return
		}
		// Present with no edits waiting means one is running.
		l.editing[w.edits] = nil
	}
	l.runLinear(w)
}

func (l *Loop) runLinear(w linearWrite) {
	l.writing++
	go func() {
		ctx, cancel := context.WithTimeout(l.runCtx, linearTimeout)
		defer cancel()
		lines, err := w.run(ctx)
		select {
		case l.inbox <- linearDone{write: w, lines: lines, err: err}:
		case <-l.stopped:
		}
	}()
}

// onLinear answers the request whose Linear write ended, after asking for Linear to be read again
// when the write landed, and starts the next edit of the same ticket.
func (l *Loop) onLinear(m linearDone) {
	l.writing--
	if m.err != nil {
		m.write.reply <- Response{Code: CodeFailed, Error: m.err.Error()}
	} else {
		l.readAgain(world.FromLinear)
		m.write.reply <- Response{Lines: m.lines}
	}
	if m.write.edits == "" {
		return
	}
	// A stopping loop starts the waiting edits too, and they fail at once on its ended context.
	waiting := l.editing[m.write.edits]
	if len(waiting) == 0 {
		delete(l.editing, m.write.edits)
		return
	}
	l.editing[m.write.edits] = waiting[1:]
	l.runLinear(waiting[0])
}

// linearWriteOf checks a request to file a Linear ticket or edit the attributes of a managed one, and
// returns the Linear write it asks for. The checks make no network call.
func (l *Loop) linearWriteOf(c *TicketCall) (linearWrite, error) {
	cfg := l.o.Config.Plugins.Linear
	filing := linear.Filing{Teams: cfg.Teams, AILabel: cfg.AILabel, RepoLabelGroup: cfg.RepoLabelGroup, Todo: cfg.Statuses.Todo, Paused: cfg.Statuses.Paused}
	ws := l.o.Sources.Linear
	switch c.Verb {
	case VerbCreate:
		d := c.Draft
		if d == nil {
			return linearWrite{}, errors.New("create needs a draft")
		}
		switch {
		case d.Parent != "":
			return linearWrite{}, errors.New("create linear does not take --parent yet")
		case len(d.BlockedBy) > 0:
			return linearWrite{}, errors.New("create linear does not take --blocked-by yet")
		case d.StartPR != 0:
			return linearWrite{}, errors.New("create linear does not take --start-pr yet")
		}
		repo, _, ok := l.o.Config.Repos.Find(d.Repo)
		if !ok {
			return linearWrite{}, fmt.Errorf("--repo %q is not a [repos.<label>] table in config.toml, so dispatch does not manage it", d.Repo)
		}
		r := linear.Request{
			Team: strings.ToUpper(c.Team), Repo: repo, Title: d.Title, Description: d.Description, Status: d.Status, Labels: d.Labels, Attributes: d.Attributes,
		}
		return linearWrite{run: func(ctx context.Context) ([]string, error) {
			id, err := linear.File(ctx, ws, filing, r)
			return []string{id}, err
		}}, nil
	case VerbEdit:
		e := c.Edit
		if e == nil {
			return linearWrite{}, errors.New("edit needs changes")
		}
		if e.Title != nil || e.Description != nil || e.Repo != nil || e.Parent != nil || e.BlockedBy != nil || e.StartPR != nil || len(e.AddLabels) > 0 || len(e.RemoveLabels) > 0 {
			return linearWrite{}, fmt.Errorf("edit changes only attributes (--attr, --unset-attr) on %s, a Linear ticket", c.ID)
		}
		if len(e.SetAttributes) == 0 && len(e.UnsetAttributes) == 0 {
			return linearWrite{}, errors.New("edit needs --attr or --unset-attr")
		}
		team, number, ok := linear.SplitIdentifier(c.ID)
		if !ok {
			return linearWrite{}, linear.NotManaged(c.ID, cfg.AILabel)
		}
		id, set, unset := c.ID, e.SetAttributes, e.UnsetAttributes
		return linearWrite{edits: fmt.Sprintf("%s-%d", strings.ToUpper(team), number), run: func(ctx context.Context) ([]string, error) {
			return []string{id}, linear.EditAttributes(ctx, ws, filing, id, set, unset)
		}}, nil
	}
	return linearWrite{}, fmt.Errorf("%s works on local tickets only", c.Verb)
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

// tracked answers VerbTracked with the worker rows, and with the local tickets when they are on.
func (l *Loop) tracked() Response {
	rows, err := l.o.Workers.Snapshot()
	if err != nil {
		return Response{Code: CodeFailed, Error: err.Error()}
	}
	resp := Response{Rows: rows}
	if l.local == nil {
		return resp
	}
	snap, err := l.local.Listing()
	if err != nil {
		return Response{Code: CodeFailed, Error: err.Error()}
	}
	resp.Tracked = &snap
	return resp
}
