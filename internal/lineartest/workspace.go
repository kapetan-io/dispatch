// Package lineartest is an in-process stand-in for a Linear workspace that
// tracks tickets and refuses what Linear refuses.
package lineartest

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kapetan-io/dispatch/internal/journal"
	"github.com/kapetan-io/dispatch/internal/linear"
)

// Key is the API key a new Workspace accepts.
const Key = "test-key"

// Linear reports an hourly request budget on every response, and the fake
// reports this one.
const (
	RequestsLimit     = 2500
	RequestsRemaining = 2400
)

// Move and Comment name the kinds of Change.
const (
	Move    = "move"
	Comment = "comment"
)

// Issue is one ticket in the workspace.
type Issue struct {
	linear.Ticket
	// Assignee is the id of the user the ticket is assigned to.
	Assignee string
	// Parent is the identifier of the ticket's parent, if it has one.
	Parent string
	// Archived hides the ticket from every read except LeasedTickets, which
	// asks Linear for archived tickets too.
	Archived bool
}

// Change is a move or a comment that the workspace accepted.
type Change struct {
	Kind     string
	TicketID string
	// StateID is the status a Move set.
	StateID string
	// Body is the text a Comment added.
	Body string
}

// State is a workflow status of the team. Type is Linear's own: backlog,
// unstarted, started, completed or canceled.
type State struct{ Name, Type string }

// Workspace is a linear.Workspace backed by memory. Its refusals come from the
// state it holds: a status the team lacks, a ticket that doesn't exist, a key it
// doesn't accept, a read that overflows a page. Failures that don't come from
// that state are injected with FailOn and never change what it accepts.
type Workspace struct {
	journal *journal.Journal
	// Now stamps the comments the workspace accepts.
	Now func() time.Time

	mu      sync.Mutex
	viewer  linear.Viewer
	key     string
	team    string
	states  []State
	issues  []Issue
	changes []Change
	reads   [][]string
	calls   map[string]int
	failed  map[string]bool
}

// New returns a workspace for the Ada Lovelace account, which accepts Key and
// has team ENG with a standard set of workflow statuses.
func New(j *journal.Journal) *Workspace {
	return &Workspace{
		journal: j,
		Now:     time.Now,
		viewer:  linear.Viewer{ID: "viewer-1", Name: "Ada Lovelace"},
		key:     Key,
		team:    "ENG",
		states: []State{
			{"Backlog", "backlog"}, {"Todo", "unstarted"}, {"In Progress", "started"},
			{"In Review", "started"}, {"Needs Human", "started"},
			{"Done", "completed"}, {"Canceled", "canceled"}, {"Duplicate", "canceled"},
		},
		calls:  map[string]int{},
		failed: map[string]bool{},
	}
}

// Viewer is the account the workspace belongs to.
func (w *Workspace) Viewer() linear.Viewer { return w.viewer }

// StateID is the id the workspace gives the named status. Ids derive from
// names so a test can predict them.
func StateID(name string) string {
	return "state-" + strings.ReplaceAll(strings.ToLower(name), " ", "-")
}

// Connect opens the workspace for key. Every call on a workspace opened with a
// key it doesn't accept fails as Linear does.
func (w *Workspace) Connect(key string) linear.Workspace {
	return &session{w: w, key: key}
}

// AddState gives the team a workflow status of the given Linear type.
func (w *Workspace) AddState(name, stateType string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.states = append(w.states, State{name, stateType})
}

// Seed replaces the tickets in the workspace. It panics on a status the team
// doesn't have and on a ticket flagged HasChildren that no unarchived seeded ticket names
// as its parent, so a test can't hold a ticket Linear couldn't. A test that
// needs a failed children read uses FailOn(OnChildren).
func (w *Workspace) Seed(issues ...Issue) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, issue := range issues {
		if w.stateOf(issue.Status) == nil {
			panic(fmt.Sprintf("lineartest: %s has status %q, which team %s lacks", issue.Identifier, issue.Status, w.team))
		}
		if issue.HasChildren && !slices.ContainsFunc(issues, func(other Issue) bool { return !other.Archived && other.Parent == issue.Identifier }) {
			panic(fmt.Sprintf("lineartest: %s is flagged HasChildren, but no unarchived seeded ticket has it as its parent", issue.Identifier))
		}
	}
	w.issues = slices.Clone(issues)
}

// Changes lists the moves and comments the workspace accepted, in order.
func (w *Workspace) Changes() []Change {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.changes)
}

// Calls counts the requests that reached the workspace, to all of its operations.
func (w *Workspace) Calls() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	total := 0
	for _, n := range w.calls {
		total += n
	}
	return total
}

// CallsTo counts the requests that reached one operation, such as OnLeased.
func (w *Workspace) CallsTo(operation string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls[operation]
}

// Reads lists the parents each ParentChildren call asked about.
func (w *Workspace) Reads() [][]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.reads)
}

// Operations that FailOn can make fail.
const (
	OnManaged  = "ManagedTickets"
	OnLeased   = "LeasedTickets"
	OnChildren = "ParentChildren"
	OnMove     = "MoveTicket"
	OnComment  = "CommentTicket"
	// OnUnsuccessfulMove and OnUnsuccessfulComment are not operations. They make
	// a move or comment that reaches Linear come back with success:false, and
	// CallsTo does not count them.
	OnUnsuccessfulMove    = "MoveTicket/success:false"
	OnUnsuccessfulComment = "CommentTicket/success:false"
)

// FailOn makes the operation fail like a Linear outage until FailOn is called
// again with on false. The workspace's state stays as it was.
func (w *Workspace) FailOn(operation string, on bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.failed[operation] = on
}

func (w *Workspace) stateOf(name string) *State {
	for i := range w.states {
		if w.states[i].Name == name {
			return &w.states[i]
		}
	}
	return nil
}

// withChildren is the ticket with HasChildren set when any ticket in the
// workspace names it as its parent, as Linear derives it. An archived child
// doesn't count, as in ParentChildren.
func (w *Workspace) withChildren(issue Issue) linear.Ticket {
	if slices.ContainsFunc(w.issues, func(other Issue) bool { return !other.Archived && other.Parent == issue.Identifier }) {
		issue.HasChildren = true
	}
	return issue.Ticket
}

func (w *Workspace) find(id string) int {
	return slices.IndexFunc(w.issues, func(i Issue) bool { return i.ID == id })
}

type session struct {
	w     *Workspace
	key   string
	calls int
}

func (s *session) Usage() linear.Stats {
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	return linear.Stats{Calls: s.calls, RequestsRemaining: RequestsRemaining, RequestsLimit: RequestsLimit}
}

// request counts the call, as the real client does for every attempt, then
// fails it when the key is wrong or the operation has been made to fail. It
// returns with the workspace locked on success.
func (s *session) request(operation string) error {
	s.w.mu.Lock()
	s.w.calls[operation]++
	s.calls++
	if s.key != s.w.key {
		s.w.mu.Unlock()
		return fmt.Errorf("linear %s: HTTP 401: %w", operation, linear.ErrUnauthorized)
	}
	if s.w.failed[operation] {
		s.w.mu.Unlock()
		return fmt.Errorf("linear %s: HTTP 500: %s refused", operation, operation)
	}
	return nil
}

// notFound is Linear's refusal of an id that names nothing.
func notFound(operation, entity string) error {
	return fmt.Errorf("linear %s: Entity not found: %s - Could not find referenced %s.", operation, entity, entity)
}

func (s *session) Viewer(context.Context) (linear.Viewer, error) {
	if err := s.request("Viewer"); err != nil {
		return linear.Viewer{}, err
	}
	defer s.w.mu.Unlock()
	return s.w.viewer, nil
}

func (s *session) ManagedTickets(_ context.Context, label string, teams []string) ([]linear.Ticket, error) {
	if err := s.request(OnManaged); err != nil {
		return nil, err
	}
	defer s.w.mu.Unlock()
	var found []linear.Ticket
	for _, issue := range s.w.issues {
		state := s.w.stateOf(issue.Status)
		if issue.Archived || issue.Assignee != s.w.viewer.ID || state.Type != "started" || !issue.HasLabel(label) || !slices.Contains(teams, issue.Team) {
			continue
		}
		// Fields the real query leaves out come back zero, so a caller that reads
		// one fails here as it would against Linear. LeasedTickets and
		// ParentChildren do the same.
		issue.Mine = false
		found = append(found, s.w.withChildren(issue))
	}
	if len(found) > linear.PageSize {
		return nil, linear.ManagedOverflow(label)
	}
	return found, nil
}

func (s *session) LeasedTickets(_ context.Context, label string, teams, identifiers []string) ([]linear.Ticket, error) {
	var asked []string
	for _, id := range identifiers {
		if team, _, ok := linear.SplitIdentifier(id); ok && slices.Contains(teams, team) {
			asked = append(asked, id)
		}
	}
	if len(asked) == 0 {
		return nil, nil
	}
	if err := s.request(OnLeased); err != nil {
		return nil, err
	}
	defer s.w.mu.Unlock()
	var found []linear.Ticket
	named := 0
	for _, issue := range s.w.issues {
		if !slices.Contains(asked, issue.Identifier) {
			continue
		}
		// Linear pages the query's own filter, which names only team and number.
		named++
		if issue.Assignee != s.w.viewer.ID {
			continue
		}
		if !issue.LabelsTruncated && !issue.HasLabel(label) {
			continue
		}
		issue.LastComment, issue.Mine, issue.CreatedAt, issue.Blockers, issue.BlockersTruncated = "", false, "", nil, false
		found = append(found, s.w.withChildren(issue))
	}
	if named > linear.PageSize {
		return nil, linear.LeasedOverflow()
	}
	return found, nil
}

func (s *session) ParentChildren(_ context.Context, parents []string) (map[string][]linear.Ticket, error) {
	if len(parents) == 0 {
		return nil, nil
	}
	if err := s.request(OnChildren); err != nil {
		return nil, err
	}
	defer s.w.mu.Unlock()
	s.w.reads = append(s.w.reads, slices.Clone(parents))
	var identifiers []string
	for _, id := range parents {
		if i := s.w.find(id); i >= 0 {
			identifiers = append(identifiers, s.w.issues[i].Identifier)
		}
	}
	children := map[string][]linear.Ticket{}
	total := 0
	for _, issue := range s.w.issues {
		if issue.Archived || !slices.Contains(identifiers, issue.Parent) {
			continue
		}
		total++
		issue.Mine, issue.URL, issue.LastComment = issue.Assignee == s.w.viewer.ID, "", ""
		children[issue.Parent] = append(children[issue.Parent], s.w.withChildren(issue))
	}
	if total > linear.PageSize {
		return nil, linear.ChildrenOverflow()
	}
	// Linear's order is undocumented, so the contract is the oldest child first.
	for _, kids := range children {
		slices.SortStableFunc(kids, func(a, b linear.Ticket) int { return strings.Compare(a.CreatedAt, b.CreatedAt) })
	}
	return children, nil
}

func (s *session) StateID(_ context.Context, team, name string) (string, error) {
	if err := s.request("TeamStates"); err != nil {
		return "", err
	}
	defer s.w.mu.Unlock()
	if team != s.w.team {
		return "", linear.NoTeam(team)
	}
	if s.w.stateOf(name) == nil {
		return "", linear.NoStatus(team, name)
	}
	return StateID(name), nil
}

func (s *session) Move(_ context.Context, ticketID, stateID string) error {
	if err := s.request(OnMove); err != nil {
		return err
	}
	defer s.w.mu.Unlock()
	ticket := s.w.find(ticketID)
	if ticket < 0 {
		return notFound("MoveTicket", "Issue")
	}
	state := slices.IndexFunc(s.w.states, func(st State) bool { return StateID(st.Name) == stateID })
	if state < 0 {
		return notFound("MoveTicket", "WorkflowState")
	}
	if s.w.failed[OnUnsuccessfulMove] {
		return linear.Unsuccessful("MoveTicket", ticketID)
	}
	s.w.issues[ticket].Status = s.w.states[state].Name
	s.w.changes = append(s.w.changes, Change{Kind: Move, TicketID: ticketID, StateID: stateID})
	s.w.journal.Record("linear", "move", ticketID, stateID)
	return nil
}

func (s *session) Comment(_ context.Context, ticketID, body string) error {
	if err := s.request(OnComment); err != nil {
		return err
	}
	defer s.w.mu.Unlock()
	ticket := s.w.find(ticketID)
	if ticket < 0 {
		return notFound("CommentTicket", "Issue")
	}
	if s.w.failed[OnUnsuccessfulComment] {
		return linear.Unsuccessful("CommentTicket", ticketID)
	}
	s.w.issues[ticket].LastComment = s.w.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	s.w.changes = append(s.w.changes, Change{Kind: Comment, TicketID: ticketID, Body: body})
	s.w.journal.Record("linear", "comment", ticketID, body)
	return nil
}
