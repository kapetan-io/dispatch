package rules

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/ledger"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/stops"
	"github.com/kapetan-io/dispatch/internal/worker"
	"github.com/kapetan-io/dispatch/internal/world"
)

// record is one thing a rule can read: a ticket, a PR, a worker, a tree.
type record struct {
	kind string
	// id is unique within the kind. subject is the ticket the record is about,
	// which keys the ledger, and is the id for a ticket.
	id, subject string
	// created orders admission, oldest first. It is empty when unknown.
	created string
	seq     int
	// hidden marks a record rules can read through a lookup but never run over.
	hidden bool
	// v is a pointer to the kind's struct.
	v reflect.Value
}

func newRecord(kind, id, subject string, v any) *record {
	return &record{kind: kind, id: id, subject: subject, v: reflect.ValueOf(v)}
}

// fieldIndex maps a kind's property name to its struct field.
var fieldIndex = func() map[reflect.Type]map[string]int {
	out := map[reflect.Type]map[string]int{}
	index := func(t reflect.Type) {
		m := map[string]int{}
		for i := range t.NumField() {
			if name, _, _ := strings.Cut(t.Field(i).Tag.Get("js"), ","); name != "" {
				m[name] = i
			}
		}
		out[t] = m
	}
	for _, k := range Kinds {
		index(k.Type)
	}
	return out
}()

// fact returns the Fact struct behind a property, or false when the kind has none by that name.
func (r *record) fact(name string) (reflect.Value, bool) {
	i, ok := fieldIndex[r.v.Elem().Type()][name]
	if !ok {
		return reflect.Value{}, false
	}
	return r.v.Elem().Field(i), true
}

// unknownAll marks every fact of the record unread for reason.
func (r *record) unknownAll(reason string) {
	for i := range r.v.Elem().NumField() {
		f := r.v.Elem().Field(i)
		if f.Kind() == reflect.Struct && f.NumField() == 3 {
			f.FieldByName("Unknown").SetString(reason)
		}
	}
}

// pool is a repo's treehouse pool as rules see it.
type pool struct {
	returns int
	unknown string
}

// blockers are the tickets blocking one ticket.
type blockers struct {
	read      []Blocker
	truncated bool
}

// State is everything a cycle reads: the records, the lookups between them,
// the ledger and the time. A cycle is a function of it.
type State struct {
	Now    time.Time
	Config config.Config
	Ledger *ledger.Ledger
	// Viewer names whose tickets are tracked.
	Viewer string

	kinds    map[string][]*record
	index    map[string]*record
	parents  map[string]string
	children map[string][]string
	// childrenUnread holds the reason a parent's children weren't read.
	childrenUnread map[string]string
	blockers       map[string]blockers
	// waiting holds when each ticket's current stretch of waiting on blockers
	// began. A ticket without an entry has waited since its status began.
	waiting       map[string]int64
	workers       map[string]*record
	workersByTree map[string]*record
	pools         map[string]pool
	// leased holds the tickets that hold a tree. leasedUnread holds the reason
	// a ticket's lease can't be told, per ticket.
	leased       map[string]bool
	leasedUnread map[string]string
	herdrUnread  string
	// maxInProgress is the cap admission enforces.
	maxInProgress int
	seq           int
	// started is when this dispatch began, for the dispatch record.
	started time.Time
}

// NewState returns an empty state to fill with records.
func NewState(cfg config.Config, now time.Time) *State {
	return &State{
		Now: now, Config: cfg, Ledger: &ledger.Ledger{},
		kinds: map[string][]*record{}, index: map[string]*record{},
		parents: map[string]string{}, children: map[string][]string{}, childrenUnread: map[string]string{},
		blockers: map[string]blockers{}, waiting: map[string]int64{}, workers: map[string]*record{}, workersByTree: map[string]*record{},
		pools: map[string]pool{}, leased: map[string]bool{}, leasedUnread: map[string]string{},
		maxInProgress: cfg.Dispatch.MaxInProgress, started: now,
	}
}

func (s *State) add(r *record) *record {
	r.seq = s.seq
	s.seq++
	key := r.kind + "/" + r.id
	if old, ok := s.index[key]; ok {
		s.kinds[r.kind][slices.Index(s.kinds[r.kind], old)] = r
	} else {
		s.kinds[r.kind] = append(s.kinds[r.kind], r)
	}
	s.index[key] = r
	return r
}

func (s *State) lookup(kind, id string) (*record, bool) {
	r, ok := s.index[kind+"/"+id]
	return r, ok
}

func (s *State) ms() int64 { return s.Now.UnixMilli() }

// statusKey names a status by its config key, so rules survive a renamed workspace status.
func statusKey(name string, s config.Statuses) string {
	switch name {
	case s.InProgress:
		return "in_progress"
	case s.InReview:
		return "in_review"
	case s.NeedsHuman:
		return "needs_human"
	case s.Todo:
		return "todo"
	}
	return name
}

// StatusName is the status a config key names.
func StatusName(key string, s config.Statuses) (string, bool) {
	switch key {
	case "in_progress":
		return s.InProgress, true
	case "in_review":
		return s.InReview, true
	case "needs_human":
		return s.NeedsHuman, true
	case "todo":
		return s.Todo, true
	case "close_parent":
		return s.CloseParent, true
	case "blocker_done":
		return s.BlockerDone, true
	}
	return "", false
}

const labelsCutShort = "the ticket has more labels than dispatch reads"

// ticketRecord builds the record of one Linear ticket.
func (s *State) ticketRecord(t linear.Ticket) *record {
	cfg := s.Config.Plugins.Linear
	at := s.ms()
	rec := &Ticket{
		ID:          Known(t.Identifier, at),
		Title:       Known(t.Title, at),
		URL:         Known(t.URL, at),
		Status:      Known(statusKey(t.Status, cfg.Statuses), at),
		StatusSince: Known(at, at),
		HasChildren: Known(t.HasChildren, at),
		Finished:    Known(slices.Contains(cfg.Statuses.Finished, t.Status), at),
		Paused:      Known(slices.Contains(cfg.Statuses.Paused, t.Status), at),
	}
	if t.CreatedAt != "" {
		rec.CreatedAt = Known(&t.CreatedAt, at)
	}
	if t.LastComment != "" {
		rec.LastHumanComment = Known(&Seen{At: t.LastComment, SeenAt: at}, at)
	}
	labels := make([]Label, len(t.Labels))
	for i, l := range t.Labels {
		labels[i] = Label{Name: l.Name, Group: l.Group}
	}
	rec.Labels = Known(labels, at)
	repo, hasRepo := t.LabelIn(cfg.RepoLabelGroup)
	switch {
	case t.LabelsTruncated:
		rec.Labels = Unread[[]Label](labelsCutShort)
		rec.OptedIn = Unread[bool](labelsCutShort)
		rec.Repo = Unread[*string](labelsCutShort)
		if t.HasLabel(cfg.AILabel) {
			rec.OptedIn = Known(true, at)
		}
		if hasRepo {
			rec.Repo = Known(&repo, at)
		}
	default:
		rec.OptedIn = Known(t.HasLabel(cfg.AILabel), at)
		if hasRepo {
			rec.Repo = Known(&repo, at)
		}
	}
	r := newRecord("linear.ticket", t.Identifier, t.Identifier, rec)
	r.created = t.CreatedAt
	list := make([]Blocker, len(t.Blockers))
	for i, b := range t.Blockers {
		list[i] = Blocker{ID: b.Identifier, Status: b.Status, Done: b.Status == cfg.Statuses.BlockerDone, Abandoned: b.Abandoned}
	}
	s.blockers[t.Identifier] = blockers{read: list, truncated: t.BlockersTruncated}
	return s.add(r)
}

// FromWorld builds the state a cycle reads from one reading of the world.
// Every fact's since is the reading's time, because nothing here has seen the
// world before. viewer names whose tickets are tracked.
func FromWorld(w world.World, cfg config.Config, led *ledger.Ledger, viewer string) *State {
	s := NewState(cfg, w.At)
	if led != nil {
		s.Ledger = led
	}
	s.Viewer = viewer
	at := s.ms()
	if w.HerdrErr != nil {
		s.herdrUnread = "herdr unread: " + w.HerdrErr.Error()
	}
	for label, err := range w.PoolErrs {
		s.pools[label] = pool{unknown: fmt.Sprintf("treehouse pool %s unread: %v", label, err)}
	}
	for label := range cfg.Repos {
		if _, ok := s.pools[label]; !ok {
			s.pools[label] = pool{}
		}
	}

	s.add(newRecord("dispatch", "dispatch", "dispatch", &Dispatch{ID: Known("dispatch", at), StartedAt: Known(at, at)}))

	for _, t := range w.Tickets {
		s.ticketRecord(t)
	}
	for _, t := range w.LeaseHolders {
		if _, ok := s.lookup("linear.ticket", t.Identifier); !ok {
			s.ticketRecord(t)
		}
	}
	for _, parent := range world.ScopeOf(cfg).Parents(w.Tickets) {
		kids, ok := w.Children[parent.Identifier]
		if !ok {
			reason := "the children of " + parent.Identifier + " were not read"
			if w.ChildrenErr != nil {
				reason += ": " + w.ChildrenErr.Error()
			}
			s.childrenUnread[parent.Identifier] = reason
			continue
		}
		for _, c := range kids {
			s.parents[c.Identifier] = parent.Identifier
			s.children[parent.Identifier] = append(s.children[parent.Identifier], c.Identifier)
			if !(c.Mine && slices.Contains(cfg.Plugins.Linear.Teams, c.Team)) {
				// Someone else's ticket still counts toward "every child is finished",
				// but dispatch has nothing to do for it.
				if _, ok := s.lookup("linear.ticket", c.Identifier); !ok {
					s.ticketRecordHidden(c)
				}
				continue
			}
			if _, ok := s.lookup("linear.ticket", c.Identifier); !ok {
				s.ticketRecord(c)
			}
		}
	}

	for _, t := range w.Tickets {
		s.workerRecord(w, t.Identifier, w.Workers[t.Identifier], w.Stops[t.Identifier])
	}
	for _, holder := range w.LeaseHolders {
		for i, wk := range w.Leases[holder.Identifier] {
			id := holder.Identifier
			if i > 0 {
				id += "@" + wk.TreePath
			}
			s.workerRecord(w, id, wk, stops.Record{})
		}
	}
	for holder := range w.Leased {
		s.leased[holder] = true
	}
	for _, t := range w.Tickets {
		s.leasedUnknown(w, t)
	}
	for _, t := range s.kinds["linear.ticket"] {
		s.leasedUnknown(w, ticketOf(t))
	}
	s.treeRecords(w)
	s.prRecords(w)
	s.addEscalations()
	s.addIdleWorkers(w)
	return s
}

// addIdleWorkers gives every ticket and tree that has no worker record the
// record of a missing worker, so a rule can always ask. These are never run over.
func (s *State) addIdleWorkers(w world.World) {
	for _, t := range slices.Clone(s.kinds["linear.ticket"]) {
		if _, ok := s.workers[t.id]; !ok {
			s.workerRecord(w, t.id, worker.Worker{State: worker.None}, stops.Record{})
			s.workers[t.id].hidden = true
		}
	}
	for _, tree := range slices.Clone(s.kinds["treehouse.tree"]) {
		if _, ok := s.workersByTree[tree.id]; !ok {
			s.workerRecord(w, "tree:"+tree.id, worker.Worker{State: worker.None, TreePath: tree.id}, stops.Record{})
			s.workersByTree[tree.id].hidden = true
		}
	}
}

// ticketRecordHidden remembers a ticket that is only a child, without making
// it something rules run over.
func (s *State) ticketRecordHidden(t linear.Ticket) {
	s.ticketRecord(t).hidden = true
}

// ticketOf returns the linear.Ticket facts a record was built from, just
// enough to tell its repo.
func ticketOf(r *record) linear.Ticket {
	t := r.v.Interface().(*Ticket)
	out := linear.Ticket{Identifier: r.id}
	if t.Repo.Unknown == "" && t.Repo.Value != nil {
		out.Labels = []linear.Label{{Name: *t.Repo.Value, Group: "repo"}}
	}
	return out
}

func (s *State) repoOf(r *record) (string, bool) {
	t := r.v.Interface().(*Ticket)
	if t.Repo.Unknown != "" || t.Repo.Value == nil {
		return "", false
	}
	return *t.Repo.Value, true
}

// leasedUnknown notes that a ticket's lease can't be told when its repo's pool wasn't read.
func (s *State) leasedUnknown(w world.World, t linear.Ticket) {
	for _, label := range t.Labels {
		if label.Group == s.Config.Plugins.Linear.RepoLabelGroup && w.UnreadRepos[label.Name] {
			s.leasedUnread[t.Identifier] = "treehouse pool " + label.Name + " unread"
		}
	}
}

func (s *State) workerRecord(w world.World, id string, wk worker.Worker, stop stops.Record) {
	at := s.ms()
	subject := strings.SplitN(id, "@", 2)[0]
	rec := &Worker{
		State:     Known(string(wk.State), at),
		Ticket:    Known(Ref{Kind: "linear.ticket", ID: subject}, at),
		Busy:      Known(false, at),
		SeenAt:    Known(at, at),
		GoneSince: Known(at, at),
	}
	if wk.TreePath != "" {
		rec.Tree = Known(&wk.TreePath, at)
	}
	if wk.WorkspaceID != "" {
		rec.Workspace = Known(&wk.WorkspaceID, at)
	}
	switch wk.State {
	case worker.Idle:
		rec.IdleSince = Known(&at, at)
		lastStop := Stop{At: at}
		if stop.Reported && !stop.At.IsZero() {
			lastStop.At = stop.At.UnixMilli()
			rec.Busy = Known(stop.Busy(s.Now), at)
		}
		rec.LastStop = Known(&lastStop, at)
	case worker.Blocked:
		rec.BlockedSince = Known(&at, at)
	}
	r := newRecord("herdr.worker", id, subject, rec)
	if w.HerdrErr != nil {
		r.unknownAll(s.herdrUnread)
	} else if wk.State == worker.None {
		if t, ok := s.lookup("linear.ticket", subject); ok {
			if repo, ok := s.repoOf(t); ok && w.UnreadRepos[repo] {
				r.unknownAll(s.pools[repo].unknown)
			}
		}
	}
	s.add(r)
	if _, ok := s.workers[subject]; !ok {
		s.workers[subject] = r
	}
	if wk.TreePath != "" {
		s.workersByTree[wk.TreePath] = r
	}
}

func (s *State) treeRecords(w world.World) {
	at := s.ms()
	for _, tree := range w.Trees {
		holder := tree.LeaseHolder
		rec := &Tree{
			Path:        Known(tree.Path, at),
			Leased:      Known(holder != "", at),
			LeasedSince: Known(at, at),
		}
		switch {
		case holder == "":
			rec.Holder = Known(Ref{}, at)
		default:
			if _, ok := s.lookup("linear.ticket", holder); !ok {
				if w.HoldersErr != nil {
					rec.Holder = Unread[Ref]("the tickets that hold trees were not read: " + w.HoldersErr.Error())
				} else {
					s.foreignTicket(holder)
				}
			}
			if rec.Holder.Unknown == "" {
				rec.Holder = Known(Ref{Kind: "linear.ticket", ID: holder}, at)
			}
		}
		inspected, tried := w.Work[tree.Path], false
		_, tried = w.Unfinished[tree.Path]
		switch {
		case w.CheckErrors[tree.Path] != nil:
			reason := fmt.Sprintf("can't tell whether %s holds unpushed work: %v", tree.Path, w.CheckErrors[tree.Path])
			rec.Pushed, rec.TrackedChanges, rec.Unfinished = Unread[bool](reason), Unread[bool](reason), Unread[string](reason)
		case !tried:
			reason := tree.Path + " was not inspected, because its ticket isn't finished"
			rec.Pushed, rec.TrackedChanges, rec.Unfinished = Unread[bool](reason), Unread[bool](reason), Unread[string](reason)
		default:
			rec.Pushed = Known(inspected.Unpushed == 0, at)
			rec.TrackedChanges = Known(inspected.TrackedChanges, at)
			rec.Unfinished = Known(w.Unfinished[tree.Path], at)
		}
		r := newRecord("treehouse.tree", tree.Path, holder, rec)
		if holder == "" {
			r.subject = tree.Path
		}
		s.add(r)
	}
}

// foreignTicket adds the stand-in for a ticket that holds a tree but isn't one of yours, so
// a rule asking whether it is finished gets "no" and nothing else about it.
func (s *State) foreignTicket(id string) {
	at := s.ms()
	reason := id + " isn't one of your tickets"
	rec := &Ticket{ID: Known(id, at), Finished: Known(false, at), Paused: Known(false, at)}
	r := newRecord("linear.ticket", id, id, rec)
	r.unknownExcept(reason, "id", "finished", "paused")
	r.hidden = true
	s.add(r)
}

// unknownExcept marks every fact unread for reason except the named properties.
func (r *record) unknownExcept(reason string, keep ...string) {
	t := r.v.Elem().Type()
	for name, i := range fieldIndex[t] {
		if slices.Contains(keep, name) {
			continue
		}
		r.v.Elem().Field(i).FieldByName("Unknown").SetString(reason)
	}
}

func (s *State) prRecords(w world.World) {
	at := s.ms()
	for _, t := range w.Tickets {
		pr, ok := w.PRs[t.Identifier]
		failed := w.PRReadFailed[t.Identifier]
		if !ok && !failed {
			continue
		}
		repo, _ := t.LabelIn(s.Config.Plugins.Linear.RepoLabelGroup)
		settings := s.Config.Repos[repo]
		rec := &PR{
			Ticket:          Known(Ref{Kind: "linear.ticket", ID: t.Identifier}, at),
			Number:          Known(int64(pr.Number), at),
			URL:             Known(pr.URL, at),
			Head:            Known(pr.Head, at),
			Closed:          Known(false, at),
			Draft:           Known(pr.Draft, at),
			Approved:        Known(pr.Approved, at),
			Ready:           Known(pr.Ready, at),
			NeedsWork:       Known(pr.NeedsWork(), at),
			Problem:         Known(pr.Problem(), at),
			ActivitySummary: Known(pr.ActivitySummary, at),
			OpenAs:          Known(settings.OpenPRAs, at),
			AutoMerge:       Known(settings.AutoMerge, at),
		}
		if pr.ActivityAt != "" {
			rec.LastActivity = Known(&Seen{At: pr.ActivityAt, SeenAt: at}, at)
		}
		if pr.ThreadAt != "" {
			rec.LastThread = Known(&Seen{At: pr.ThreadAt, SeenAt: at}, at)
		}
		r := newRecord("github.pr", t.Identifier, t.Identifier, rec)
		if failed {
			r.unknownExcept("the PR for "+t.Identifier+" could not be read", "ticket")
		}
		s.add(r)
	}
}

// Subjects of a record, as plans and receipts name them.
func (r *record) String() string { return r.kind + "/" + r.id }

// ReceiptEscalations adds an escalation record for each escalate receipt in the ledger.
func (s *State) addEscalations() {
	at := s.ms()
	for _, rc := range s.Ledger.Entries {
		if rc.Kind != "escalate" {
			continue
		}
		step := func(name string) string {
			for _, st := range rc.Steps {
				if st.Name == name {
					if st.Outcome == "" {
						return "attempted"
					}
					return string(st.Outcome)
				}
			}
			return "pending"
		}
		e := &Escalation{
			Ticket:    Known(Ref{Kind: "linear.ticket", ID: rc.Subject}, at),
			Reason:    Known(rc.Detail, at),
			Moved:     Known(step("move"), at),
			Commented: Known(step("comment"), at),
			Started:   Known(rc.Started, at),
		}
		s.add(newRecord("ledger.escalation", fmt.Sprintf("%s@%d", rc.Subject, rc.Started), rc.Subject, e))
	}
}
