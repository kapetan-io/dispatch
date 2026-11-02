package rules

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/inbox"
	"github.com/kapetan-io/dispatch/internal/pullrequest"
	"github.com/kapetan-io/dispatch/internal/sessionlog"
	"github.com/kapetan-io/dispatch/internal/stackrecord"
	"github.com/kapetan-io/dispatch/internal/stops"
	"github.com/kapetan-io/dispatch/internal/ticket"
	"github.com/kapetan-io/dispatch/internal/treehouse"
	"github.com/kapetan-io/dispatch/internal/world"
)

// stamp is when a scenario first saw each value, so a fact's since survives
// the rebuild of the world after every step.
type stamp struct {
	status, seen, gone, idle, blocked, comment, activity, thread int64
	previous                                                     string
	lastStop                                                     int64
	// waiting is when the ticket's current stretch of waiting on blockers
	// began, and waitingNow whether it waits now.
	waiting    int64
	waitingNow bool
}

// treeModel is a tree in a scenario.
type treeModel struct {
	path, holder   string
	pushed         bool
	trackedChanges bool
	since          int64
	// clean is when the tree last became safe to return.
	clean int64
}

// scenarioModel is the part of a scenario's world the fixtures describe.
type scenarioModel struct {
	tickets map[string]*ticket.Ticket
	order   []string
	parent  map[string]string
	workers map[string]*world.Worker
	stops   map[string]stops.Record
	// sessions are the commands a worker's session took and the turns it ended, and sessionUnread the
	// workers whose session record can't be read, with the reason.
	sessions      map[string]sessionlog.Log
	sessionUnread map[string]string
	prs           map[string]*pullrequest.PullRequest
	// stacks holds the stack record fixture of each ticket's branch. A record with only one key
	// is partial.
	stacks map[string]stackrecord.Record
	trees  []*treeModel
	stamps map[string]*stamp
}

func newModel() *scenarioModel {
	return &scenarioModel{
		tickets: map[string]*ticket.Ticket{}, parent: map[string]string{}, workers: map[string]*world.Worker{},
		stops: map[string]stops.Record{}, sessions: map[string]sessionlog.Log{}, sessionUnread: map[string]string{}, prs: map[string]*pullrequest.PullRequest{}, stacks: map[string]stackrecord.Record{}, stamps: map[string]*stamp{},
	}
}

func (m *scenarioModel) stamp(id string) *stamp {
	if m.stamps[id] == nil {
		m.stamps[id] = &stamp{}
	}
	return m.stamps[id]
}

func str(m map[string]any, key string) (string, bool) {
	v, ok := m[key].(string)
	return v, ok
}

func boolean(m map[string]any, key string) (bool, bool) {
	v, ok := m[key].(bool)
	return v, ok
}

// statusesOf is the statuses of the ticket system the id belongs to: [plugins.linear.statuses], or
// the default local statuses.
func (s *scenario) statusesOf(id string) ticket.Statuses {
	if ticket.SystemOf(id) == ticket.SystemLocal {
		return ticket.DefaultStatuses()
	}
	return s.cfg.Plugins.Linear.Statuses.Table()
}

// statusName turns a scenario's status, a job key or a status name in any case, into the name the
// ticket's own ticket system uses.
func (s *scenario) statusName(id, status string) string {
	table := s.statusesOf(id)
	if slices.Contains(movableJobs, status) {
		if name, ok := table.NameOf(ticket.Job(status)); ok {
			return name
		}
	}
	if st, ok := table.Find(status); ok {
		return st.Name
	}
	return status
}

// apply merges one fixture into the scenario's world. A fixture seen after the
// start also raises the event it stands for.
func (s *scenario) apply(f map[string]any, seen bool) {
	fields, _ := f["fields"].(map[string]any)
	if fields == nil {
		fields = map[string]any{}
	}
	switch f["__kind"] {
	case "ticket":
		s.applyTicket(f["id"].(string), fields)
	case "worker":
		s.applyWorker(f["ticket"].(string), f["state"].(string), fields)
	case "pool":
		repo := f["repo"].(string)
		if v, ok := fields["returns"]; ok {
			s.pools[repo] = int(toInt(v))
		}
		if v, ok := fields["github"]; ok {
			s.github[repo] = fmt.Sprint(v)
		}
		if _, _, ok := s.cfg.Repos.Find(repo); !ok {
			s.cfg.Repos[repo] = config.Repo{Path: "/work/" + repo, OpenPRAs: config.OpenPRAsOpen}
		}
	case "comment":
		s.applyComment(f["ticket"].(string), fields, seen)
	case "pr":
		s.applyPR(f["ticket"].(string), fields)
	case "tree":
		s.applyTree(f["path"].(string), fields)
	case "inbox":
		s.applyInbox(f["id"].(string), fields)
	default:
		s.fail("unknown fixture %v", f["__kind"])
	}
}

func (s *scenario) applyTicket(id string, fields map[string]any) {
	m := s.model()
	t, exists := m.tickets[id]
	if !exists {
		t = &ticket.Ticket{ID: "id-" + id, Identifier: id, Title: id, URL: "https://linear.test/" + id, Branch: strings.ToLower(id), Team: "ENG", Mine: true}
		if ticket.SystemOf(id) == ticket.SystemLocal {
			t = &ticket.Ticket{Identifier: id, Title: id, Branch: strings.ToLower(id), Mine: true, StatusAt: s.now.UTC().Format(time.RFC3339Nano)}
			if len(s.cfg.Repos) > 0 {
				t.Repo = slices.Sorted(maps.Keys(s.cfg.Repos))[0]
			}
		}
		t.Status = s.statusName(id, "todo")
		m.tickets[id] = t
		m.order = append(m.order, id)
		m.stamp(id).status = s.ms()
		m.stamp(id).gone = s.ms()
	}
	if status, ok := str(fields, "status"); ok {
		name := s.statusName(id, status)
		if exists && name != t.Status {
			m.stamp(id).status = s.ms()
			m.stamp(id).previous = s.statusesOf(id).Key(t.Status)
			t.StatusAt = s.now.UTC().Format(time.RFC3339Nano)
		}
		t.Status = name
	}
	if v, ok := str(fields, "title"); ok {
		t.Title = v
	}
	if v, ok := str(fields, "branch"); ok {
		t.Branch = v
	}
	if v, ok := fields["stack"].(map[string]any); ok {
		parent, _ := str(v, "parent")
		fork, _ := str(v, "forkPoint")
		m.stacks[id] = stackrecord.Record{Parent: parent, ForkPoint: fork}
	}
	if v, ok := str(fields, "createdAt"); ok {
		t.CreatedAt = v
	}
	if v, ok := str(fields, "previousStatus"); ok {
		m.stamp(id).previous = v
	}
	if v, ok := boolean(fields, "hasChildren"); ok {
		t.HasChildren = v
	}
	if v, ok := str(fields, "parent"); ok {
		m.parent[id] = v
		if p, ok := m.tickets[v]; ok {
			p.HasChildren = true
		}
	}
	if v, ok := boolean(fields, "labelsTruncated"); ok {
		t.LabelsTruncated = v
	}
	if v, ok := boolean(fields, "blockersTruncated"); ok {
		t.BlockersTruncated = v
	}
	if blockers, ok := fields["blockers"].([]any); ok {
		t.Blockers = nil
		for _, b := range blockers {
			bm, _ := b.(map[string]any)
			bid, _ := str(bm, "id")
			status, _ := str(bm, "status")
			abandoned, _ := boolean(bm, "abandoned")
			t.Blockers = append(t.Blockers, ticket.Blocker{Identifier: bid, Status: s.statusName(bid, status), Abandoned: abandoned})
		}
	}
	if attrs, ok := fields["attributes"].(map[string]any); ok && ticket.SystemOf(id) == ticket.SystemLocal {
		t.Attributes = map[string]string{}
		for k, v := range attrs {
			t.Attributes[k] = fmt.Sprint(v)
		}
	}
	// Labels are rebuilt from the fields that name them.
	labels := t.Labels
	if optedIn, ok := boolean(fields, "optedIn"); ok {
		labels = slices.DeleteFunc(labels, func(l ticket.Label) bool { return strings.EqualFold(l.Name, s.cfg.Plugins.Linear.AILabel) })
		if optedIn {
			labels = append(labels, ticket.Label{Name: s.cfg.Plugins.Linear.AILabel})
		}
	}
	if repo, ok := str(fields, "repo"); ok && ticket.SystemOf(id) == ticket.SystemLocal {
		t.Repo = repo
	} else if ok {
		labels = slices.DeleteFunc(labels, func(l ticket.Label) bool { return strings.EqualFold(l.Group, s.cfg.Plugins.Linear.RepoLabelGroup) })
		labels = append(labels, ticket.Label{Name: repo, Group: s.cfg.Plugins.Linear.RepoLabelGroup})
	}
	if names, ok := fields["labels"].([]any); ok {
		for _, n := range names {
			switch n := n.(type) {
			case string:
				labels = append(labels, ticket.Label{Name: n})
			case map[string]any:
				name, _ := str(n, "name")
				group, _ := str(n, "group")
				labels = append(labels, ticket.Label{Name: name, Group: group})
			}
		}
	}
	t.Labels = labels
}

func (s *scenario) model() *scenarioModel {
	if s.m == nil {
		s.m = newModel()
	}
	return s.m
}

func (s *scenario) applyWorker(id, state string, fields map[string]any) {
	m := s.model()
	old, had := m.workers[id]
	wk := &world.Worker{State: world.WorkerState(state), TreePath: "/trees/" + id, RepoDir: "/work", WorkspaceID: "ws-" + id, PaneID: "pane-" + id, TabID: "tab-" + id, Session: "session-" + id}
	if state == string(world.NoWorker) {
		wk = &world.Worker{State: world.NoWorker}
	}
	st := m.stamp(id)
	now := s.ms()
	if !had {
		st.gone = now
	}
	prev := world.NoWorker
	if had {
		prev = old.State
	}
	switch {
	case wk.State != world.NoWorker && prev == world.NoWorker:
		st.seen = now
	}
	if wk.State == world.NoWorker && prev != world.NoWorker {
		st.gone = now
	}
	if wk.State == world.Idle && prev != world.Idle {
		st.idle = now
		st.lastStop = 0
	}
	if wk.State == world.Blocked && prev != world.Blocked {
		st.blocked = now
	}
	if v, ok := fields["unconfirmed"]; ok {
		wk.UnconfirmedSince = scenarioStart.UnixMilli() + toInt(v)
	}
	m.workers[id] = wk
	turns, hasTurns := fields["turns"].([]any)
	commands, hasCommands := fields["commands"].([]any)
	if hasTurns || hasCommands {
		log := sessionlog.Log{Commands: []sessionlog.Command{}, Turns: []sessionlog.Turn{}}
		for _, item := range turns {
			turn, _ := item.(map[string]any)
			running, _ := boolean(turn, "running")
			log.Turns = append(log.Turns, sessionlog.Turn{At: scenarioStart.UnixMilli() + toInt(turn["at"]), Running: running})
		}
		for _, item := range commands {
			cmd, _ := item.(map[string]any)
			name, _ := str(cmd, "name")
			args, _ := str(cmd, "args")
			cut, _ := boolean(cmd, "cut")
			by, _ := str(cmd, "by")
			log.Commands = append(log.Commands, sessionlog.Command{
				At: scenarioStart.UnixMilli() + toInt(cmd["at"]), Name: name, Args: args, Cut: cut, By: sessionlog.Sender(by),
			})
		}
		m.sessions[id] = log
	}
	if v, ok := str(fields, "sessionUnread"); ok {
		m.sessionUnread[id] = cmp.Or(v, "the session's record can't be read")
	}
	if v, ok := fields["lastStop"]; ok {
		at := scenarioStart.UnixMilli() + toInt(v)
		busy, _ := boolean(fields, "busy")
		rec := stops.Record{At: time.UnixMilli(at), Reported: true}
		if busy {
			rec.Running = 1
		}
		m.stops[id] = rec
	}
}

func (s *scenario) applyComment(ticket string, fields map[string]any, seen bool) {
	m := s.model()
	t, ok := m.tickets[ticket]
	if !ok {
		s.fail("comment(%q): there is no ticket %s. Give it with ticket() first", ticket, ticket)
	}
	at, ok := str(fields, "at")
	if !ok {
		at = s.now.UTC().Format(time.RFC3339)
	}
	t.LastComment = at
	m.stamp(ticket).comment = s.ms()
	if !seen {
		return
	}
	body, _ := str(fields, "body")
	author, _ := str(fields, "author")
	byDispatch, _ := boolean(fields, "byDispatch")
	s.events = append(s.events, Event{Type: "comment.posted", Subject: ticket, Payload: &Comment{
		ID: fmt.Sprintf("comment-%d", len(s.events)+1), Body: body, Author: author, At: at, ByDispatch: byDispatch,
		Ticket: Ref{Kind: "ticket", ID: ticket},
	}})
}

func (s *scenario) applyPR(ticket string, fields map[string]any) {
	m := s.model()
	pr, exists := m.prs[ticket]
	if !exists {
		pr = &pullrequest.PullRequest{Number: 1, URL: "https://github.test/pr/" + ticket, Head: "head-1"}
		m.prs[ticket] = pr
	}
	if v, ok := str(fields, "head"); ok {
		pr.Head = v
	}
	if v, ok := boolean(fields, "draft"); ok {
		pr.Draft = v
	}
	if v, ok := boolean(fields, "approved"); ok {
		pr.Approved = v
	}
	if v, ok := boolean(fields, "ready"); ok {
		pr.Ready = v
	}
	if v, ok := boolean(fields, "conflict"); ok {
		pr.Conflict = v
	}
	if problems, ok := fields["problems"].([]any); ok {
		pr.Problems = nil
		for _, p := range problems {
			pr.Problems = append(pr.Problems, fmt.Sprint(p))
		}
	}
	if v, ok := str(fields, "activityAt"); ok {
		pr.ActivityAt = v
		m.stamp(ticket).activity = s.ms()
	}
	if v, ok := str(fields, "activitySummary"); ok {
		pr.ActivitySummary = v
	}
	if v, ok := str(fields, "threadAt"); ok {
		pr.ThreadAt = v
		m.stamp(ticket).thread = s.ms()
	}
}

func (s *scenario) applyTree(path string, fields map[string]any) {
	m := s.model()
	var tm *treeModel
	for _, t := range m.trees {
		if t.path == path {
			tm = t
		}
	}
	if tm == nil {
		tm = &treeModel{path: path, pushed: true, since: s.ms(), clean: s.ms()}
		m.trees = append(m.trees, tm)
	}
	if v, ok := str(fields, "holder"); ok {
		tm.holder = v
		tm.since = s.ms()
	}
	wasSafe := tm.pushed && !tm.trackedChanges
	if v, ok := boolean(fields, "pushed"); ok {
		tm.pushed = v
	}
	if v, ok := boolean(fields, "trackedChanges"); ok {
		tm.trackedChanges = v
	}
	if !wasSafe && tm.pushed && !tm.trackedChanges {
		tm.clean = s.ms()
	}
}

// world builds the world the scenario's fixtures describe.
func (s *scenario) world() world.World {
	m := s.model()
	cfg := s.cfg
	scope := world.ScopeOf(cfg)
	scope.Local = ticket.DefaultStatuses()
	w := world.World{
		At: s.now, LocalStatuses: scope.Local, Workers: map[string]world.Worker{}, Stops: map[string]stops.Record{},
		Sessions: map[string]sessionlog.Log{}, SessionErrs: map[string]error{},
		Leases: map[string][]world.Worker{}, Leased: map[string]bool{}, Children: map[string][]ticket.Ticket{},
		Unfinished: map[string]string{}, Work: map[string]treehouse.Work{}, CheckErrors: map[string]error{},
		Untracked: map[string][]string{}, PRs: map[string]pullrequest.PullRequest{}, PRReadFailed: map[string]bool{},
		PoolErrs: map[string]error{}, UnreadRepos: map[string]bool{},
	}
	started := func(t *ticket.Ticket) bool {
		return scope.Is(*t, ticket.InProgress) || scope.Is(*t, ticket.InReview) || scope.Is(*t, ticket.NeedsHuman)
	}
	workerOf := func(id string) world.Worker {
		if wk, ok := m.workers[id]; ok {
			return *wk
		}
		return world.Worker{State: world.NoWorker}
	}
	for _, id := range m.order {
		t := m.tickets[id]
		if started(t) {
			w.Tickets = append(w.Tickets, *t)
			w.Workers[id] = workerOf(id)
			if rec, ok := m.stops[id]; ok && w.Workers[id].State == world.Idle {
				w.Stops[id] = rec
			}
			if w.Workers[id].State != world.NoWorker && (scope.Is(*t, ticket.InProgress) || scope.Is(*t, ticket.InReview)) {
				if reason, ok := m.sessionUnread[id]; ok {
					w.SessionErrs[id] = errors.New(reason)
				} else if log, ok := m.sessions[id]; ok {
					w.Sessions[id] = log
				} else {
					w.Sessions[id] = sessionlog.Log{Commands: []sessionlog.Command{}, Turns: []sessionlog.Turn{}}
				}
			}
		}
	}
	trees := slices.Clone(m.trees)
	for _, id := range m.order {
		if wk := workerOf(id); wk.State != world.NoWorker && !slices.ContainsFunc(trees, func(t *treeModel) bool { return t.path == wk.TreePath }) {
			trees = append(trees, &treeModel{path: wk.TreePath, holder: id, pushed: true, since: s.ms()})
		}
	}
	for _, tm := range trees {
		w.Trees = append(w.Trees, treehouse.Tree{Path: tm.path, Status: "leased", LeaseHolder: tm.holder, RepoDir: "/work"})
		if tm.holder != "" {
			w.Leased[tm.holder] = true
		}
	}
	for _, tm := range trees {
		holder, ok := m.tickets[tm.holder]
		if tm.holder == "" || !ok || started(holder) {
			continue
		}
		w.LeaseHolders = append(w.LeaseHolders, *holder)
		wk := workerOf(tm.holder)
		wk.TreePath = tm.path
		if wk.State == world.NoWorker {
			wk = world.Worker{State: world.NoWorker, TreePath: tm.path}
		}
		w.Leases[tm.holder] = append(w.Leases[tm.holder], wk)
		if scope.Retires(*holder) {
			work := treehouse.Work{TrackedChanges: tm.trackedChanges}
			if !tm.pushed {
				work.Unpushed = 1
			}
			w.Work[tm.path], w.Unfinished[tm.path], w.Untracked[tm.path] = work, work.String(), nil
		}
	}
	for _, parent := range scope.Parents(w.Tickets) {
		var kids []ticket.Ticket
		for _, id := range m.order {
			if m.parent[id] == parent.Identifier {
				kids = append(kids, *m.tickets[id])
			}
		}
		w.Children[parent.Identifier] = kids
	}
	w.StackRecords, w.StackErrs = map[string]stackrecord.Records{}, map[string]error{}
	for label := range cfg.Repos {
		w.StackRecords[label] = stackrecord.Records{Whole: map[string]stackrecord.Record{}, Partial: map[string]bool{}}
	}
	for id, rec := range m.stacks {
		t := m.tickets[id]
		label, _ := scope.RepoOf(*t)
		name, _, ok := cfg.Repos.Find(label)
		if !ok {
			continue
		}
		if rec.Parent == "" || rec.ForkPoint == "" {
			w.StackRecords[name].Partial[t.Branch] = true
			continue
		}
		w.StackRecords[name].Whole[t.Branch] = rec
	}
	for id, pr := range m.prs {
		t, ok := m.tickets[id]
		if !ok || !scope.ReadsPR(*t, workerOf(id).State) {
			continue
		}
		w.PRs[id] = *pr
	}
	s.readInbox()
	s.readRemotes()
	w.Inbox, w.InboxReadAt = s.inboxGood, s.inboxReadAt
	w.Remotes, w.RemoteErrs = maps.Clone(s.remotesGood), map[string]error{}
	for label := range s.remotesFailed {
		w.RemoteErrs[label] = errors.New("scenario marked it unread")
	}
	if s.inboxFailed {
		w.InboxErr = errors.New("gh: HTTP 502 (the scenario failed the inbox read)")
	}
	return w
}

// inboxModel is an inbox item a fixture gave. The API URL defaults to .../pulls/<id> whatever the
// subjectType; give apiUrl when a rule reads it.
type inboxModel struct {
	item   inbox.Item
	apiSet bool
}

// applyInbox gives the item id the fields named, and defaults for the rest when the id is new.
func (s *scenario) applyInbox(id string, fields map[string]any) {
	m, ok := s.inbox[id]
	if !ok {
		m = &inboxModel{item: inbox.Item{
			ID: id, Reason: "subscribed", Unread: true, UpdatedAt: s.now.UTC().Format(time.RFC3339),
			RepoName: "acme/widgets", SubjectType: "PullRequest", Title: id,
		}}
		s.inbox[id] = m
		s.inboxOrder = append(s.inboxOrder, id)
	}
	if v, ok := str(fields, "reason"); ok {
		m.item.Reason = v
	}
	if v, ok := boolean(fields, "unread"); ok {
		m.item.Unread = v
	}
	if v, ok := str(fields, "updatedAt"); ok {
		if _, err := time.Parse(time.RFC3339, v); err != nil {
			s.fail("inbox(%q): updatedAt %q is not RFC 3339", id, v)
		}
		m.item.UpdatedAt = v
	}
	if v, ok := str(fields, "repoName"); ok {
		m.item.RepoName = v
	}
	if v, ok := str(fields, "subjectType"); ok {
		m.item.SubjectType = v
	}
	if v, ok := str(fields, "title"); ok {
		m.item.Title = v
	}
	if v, ok := str(fields, "apiUrl"); ok {
		m.item.APIURL, m.apiSet = v, true
	}
}

// readInbox is the scenario's read of the inbox: the items the fixtures gave, through the one
// sort and cap, unless a read already failed. It reads only while a loaded rule runs over inbox,
// as dispatch does.
func (s *scenario) readInbox() {
	if s.inboxFailed || !s.rules.RunsOver("inbox") {
		return
	}
	items := make([]inbox.Item, 0, len(s.inboxOrder))
	for _, id := range s.inboxOrder {
		m := s.inbox[id]
		item := m.item
		if !m.apiSet {
			item.APIURL = fmt.Sprintf("https://api.github.com/repos/%s/pulls/%s", item.RepoName, item.ID)
		}
		items = append(items, item)
	}
	s.inboxGood, s.inboxReadAt = inbox.Newest(items), s.now
}

// readRemotes is the scenario's read of each clone's chosen remote, made only while a loaded rule
// runs over inbox, as dispatch does. A clone whose read failed keeps what it last read.
func (s *scenario) readRemotes() {
	if !s.rules.RunsOver("inbox") {
		return
	}
	for label, name := range s.github {
		if s.remotesFailed[label] {
			continue
		}
		if name == "" {
			delete(s.remotesGood, label)
			continue
		}
		s.remotesGood[label] = name
	}
}

// trackWaiting starts a new stretch of waiting for each ticket that has
// open blockers now and had none at the last cycle.
func (s *scenario) trackWaiting() {
	m := s.model()
	for id, t := range m.tickets {
		open := t.BlockersTruncated
		for _, b := range t.Blockers {
			open = open || !s.statusesOf(b.Identifier).Has(b.Status, ticket.BlockerDone)
		}
		st := m.stamp(id)
		if open && !st.waitingNow {
			st.waiting = s.ms()
		}
		st.waitingNow = open
	}
}

// stamped applies the first-seen times the scenario kept to a freshly built state.
func (s *scenario) stamped(state *State) {
	m := s.model()
	for id, st := range m.stamps {
		if st.waiting != 0 {
			state.waiting[id] = st.waiting
		}
		if r, ok := state.lookup("ticket", id); ok {
			t := r.v.Interface().(*Ticket)
			if st.status != 0 {
				t.StatusSince = Known(st.status, st.status)
			}
			if st.previous != "" {
				prev := st.previous
				t.PreviousStatus = Known(&prev, st.status)
			}
			if t.LastHumanComment.Value != nil && st.comment != 0 {
				seen := *t.LastHumanComment.Value
				seen.SeenAt = st.comment
				t.LastHumanComment = Known(&seen, st.comment)
			}
		}
		if r, ok := state.workers[id]; ok && r.v.Interface().(*Worker).State.Unknown == "" {
			wk := r.v.Interface().(*Worker)
			if st.seen != 0 {
				wk.SeenAt = Known(st.seen, st.seen)
			}
			if st.gone != 0 {
				wk.GoneSince = Known(st.gone, st.gone)
			}
			if wk.IdleSince.Value != nil && st.idle != 0 {
				at := st.idle
				wk.IdleSince = Known(&at, at)
				if wk.LastStop.Value != nil && m.stops[id].At.IsZero() {
					wk.LastStop = Known(&Stop{At: at}, at)
				}
			}
			if wk.BlockedSince.Value != nil && st.blocked != 0 {
				at := st.blocked
				wk.BlockedSince = Known(&at, at)
			}
		}
		if r, ok := state.lookup("pr", id); ok {
			pr := r.v.Interface().(*PR)
			if pr.LastActivity.Value != nil && st.activity != 0 {
				seen := *pr.LastActivity.Value
				seen.SeenAt = st.activity
				pr.LastActivity = Known(&seen, st.activity)
			}
			if pr.LastThread.Value != nil && st.thread != 0 {
				seen := *pr.LastThread.Value
				seen.SeenAt = st.thread
				pr.LastThread = Known(&seen, st.thread)
			}
		}
	}
	for _, tm := range m.trees {
		if r, ok := state.lookup("tree", tm.path); ok {
			tree := r.v.Interface().(*Tree)
			tree.LeasedSince = Known(tm.since, tm.since)
			if tree.CleanSince.Unknown == "" {
				tree.CleanSince = Known(tm.clean, tm.clean)
			}
		}
	}
}
