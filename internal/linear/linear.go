// Package linear reads and writes the tickets the dispatcher manages, through
// Linear's GraphQL API. The dispatcher is a script, so it can't use the MCP
// connector that Claude sessions use.
package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kapetan-io/dispatch/internal/ticket"
)

// DefaultURL is Linear's GraphQL endpoint.
const DefaultURL = "https://api.linear.app/graphql"

// Workspace is a Linear workspace as its API key's owner sees it. Client is
// the real one, and lineartest.Workspace is the in-memory fake.
type Workspace interface {
	// ManagedTickets returns the started tickets assigned to the key's owner
	// that carry label, limited to teams.
	ManagedTickets(ctx context.Context, label string, teams []string) ([]ticket.Ticket, error)
	// LeasedTickets returns the tickets named by identifiers in any status,
	// archived ones included, limited to the key owner's own.
	LeasedTickets(ctx context.Context, label string, teams, identifiers []string) ([]ticket.Ticket, error)
	// ParentChildren returns every child, in every status, of the parents with
	// these Linear ids, keyed by the parent's identifier.
	ParentChildren(ctx context.Context, parents []string) (map[string][]ticket.Ticket, error)
	// StateID returns the id of the named workflow status on a team, or NoTeam
	// when the key matches no team.
	StateID(ctx context.Context, team, name string) (string, error)
	// CreateTicket files a ticket assigned to the key's owner and returns its identifier. It
	// refuses a team, status or label that doesn't exist and creates none of them.
	CreateTicket(ctx context.Context, t NewTicket) (identifier string, err error)
	// ManagedDescription returns the id and description of the ticket named by identifier, or
	// an error matching ErrNotManaged when it isn't assigned to the key's owner, doesn't carry
	// label, or is outside teams.
	ManagedDescription(ctx context.Context, label string, teams []string, identifier string) (Described, error)
	// SetDescription replaces a ticket's description.
	SetDescription(ctx context.Context, ticketID, description string) error
	// Move sets a ticket's status.
	Move(ctx context.Context, ticketID, stateID string) error
	// Comment adds a comment to a ticket.
	Comment(ctx context.Context, ticketID, body string) error
	// Viewer returns the user who owns the API key.
	Viewer(ctx context.Context) (Viewer, error)
	// Usage reports the calls made so far.
	Usage() Stats
}

// Connect opens the workspace that key belongs to.
type Connect func(key string) Workspace

// Client is the Workspace that calls Linear's GraphQL API as the API key's owner.
type Client struct {
	URL  string
	Key  string
	HTTP *http.Client
	// stats records every call this client makes, and mu guards it because actions and
	// reads call the client from different goroutines.
	mu    sync.Mutex
	stats Stats
}

var _ Workspace = (*Client)(nil)

// NewClient returns a Workspace that calls the Linear API at url with key.
func NewClient(url, key string) *Client { return &Client{URL: url, Key: key} }

// Usage reports the calls this client has made and the request budget Linear
// last reported.
func (c *Client) Usage() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}

// Stats counts a workspace's calls to Linear and keeps the hourly request budget
// Linear reported on the last response. The budget fields stay zero until a
// response carries them.
type Stats struct {
	Calls             int
	Elapsed           time.Duration
	RequestsRemaining int
	RequestsLimit     int
}

func (s *Stats) record(start time.Time, resp *http.Response) {
	if s == nil {
		return
	}
	s.Calls++
	s.Elapsed += time.Since(start)
	if resp == nil {
		return
	}
	if n, err := strconv.Atoi(resp.Header.Get("X-RateLimit-Requests-Remaining")); err == nil {
		s.RequestsRemaining = n
	}
	if n, err := strconv.Atoi(resp.Header.Get("X-RateLimit-Requests-Limit")); err == nil {
		s.RequestsLimit = n
	}
}

// PageSize is how many tickets one read asks Linear for. A second page is an
// error rather than a loop: tickets left off it would go unlaunched, their
// live workers uncounted against max_in_progress, and a parent with an
// unfinished child could close.
const PageSize = 100

// ErrOverflow matches the errors for a read with more tickets than fit on a page.
var ErrOverflow = errors.New("more tickets than fit on one page")

type overflow string

func (e overflow) Error() string        { return string(e) }
func (e overflow) Is(target error) bool { return target == ErrOverflow }

// ManagedOverflow is the error for more started tickets with label than fit on a page.
func ManagedOverflow(label string) error {
	return overflow(fmt.Sprintf("more than %d started %q tickets are assigned to you; dispatch reads one page and would miss the rest", PageSize, label))
}

// LeasedOverflow is the error for more leased tickets than fit on a page.
func LeasedOverflow() error {
	return overflow(fmt.Sprintf("more than %d tickets hold treehouse leases; dispatch reads one page and would miss the rest", PageSize))
}

// ChildrenOverflow is the error for more children than fit on a page.
func ChildrenOverflow() error {
	return fmt.Errorf("the open parents have more than %d children between them; dispatch reads one page and could close a parent that still has unfinished children", PageSize)
}

// The nested connections are capped because Linear charges a query by its
// page sizes, and only the presence of a child matters. Linear lists comments
// newest first, so comments(first: 1) is the newest and last: 1 the oldest.
const managedTickets = `query ManagedTickets($label: String!, $teams: [String!], $first: Int!) {
  viewer {
    assignedIssues(first: $first, filter: {labels: {name: {eqIgnoreCase: $label}}, state: {type: {eq: "started"}}, team: {key: {in: $teams}}}) {
      nodes {
        id identifier title description url branchName createdAt
        state { name }
        team { key }
        labels(first: 25) { nodes { name parent { name } } pageInfo { hasNextPage } }
        children(first: 1) { nodes { id } }
        comments(first: 1) { nodes { createdAt } }
        inverseRelations(first: 10) { nodes { type issue { identifier state { name type } } } pageInfo { hasNextPage } }
      }
      pageInfo { hasNextPage }
    }
  }
}`

// issueNode is the shape of one issue in the ticket queries.
type issueNode struct {
	ID          string `json:"id"`
	Identifier  string `json:"identifier"`
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
	BranchName  string `json:"branchName"`
	State       struct {
		Name string `json:"name"`
	} `json:"state"`
	Team struct {
		Key string `json:"key"`
	} `json:"team"`
	Assignee *struct {
		ID string `json:"id"`
	} `json:"assignee"`
	Labels struct {
		Nodes []struct {
			Name   string `json:"name"`
			Parent *struct {
				Name string `json:"name"`
			} `json:"parent"`
		} `json:"nodes"`
		PageInfo struct {
			HasNextPage bool `json:"hasNextPage"`
		} `json:"pageInfo"`
	} `json:"labels"`
	Children struct {
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
	} `json:"children"`
	Comments struct {
		Nodes []struct {
			CreatedAt string `json:"createdAt"`
		} `json:"nodes"`
	} `json:"comments"`
	CreatedAt string `json:"createdAt"`
	Parent    *struct {
		Identifier string `json:"identifier"`
	} `json:"parent"`
	InverseRelations struct {
		Nodes []struct {
			Type  string `json:"type"`
			Issue struct {
				Identifier string `json:"identifier"`
				State      struct {
					Name string `json:"name"`
					Type string `json:"type"`
				} `json:"state"`
			} `json:"issue"`
		} `json:"nodes"`
		PageInfo struct {
			HasNextPage bool `json:"hasNextPage"`
		} `json:"pageInfo"`
	} `json:"inverseRelations"`
}

func (n issueNode) ticket() ticket.Ticket {
	t := ticket.Ticket{
		ID:                n.ID,
		Identifier:        n.Identifier,
		Title:             n.Title,
		URL:               n.URL,
		Branch:            n.BranchName,
		Status:            n.State.Name,
		Team:              n.Team.Key,
		HasChildren:       len(n.Children.Nodes) > 0,
		LabelsTruncated:   n.Labels.PageInfo.HasNextPage,
		BlockersTruncated: n.InverseRelations.PageInfo.HasNextPage,
		Attributes:        AttributesIn(n.Description),
	}
	for _, r := range n.InverseRelations.Nodes {
		if r.Type == "blocks" {
			t.Blockers = append(t.Blockers, ticket.Blocker{Identifier: r.Issue.Identifier, Status: r.Issue.State.Name, Abandoned: r.Issue.State.Type == "canceled"})
		}
	}
	if len(n.Comments.Nodes) > 0 {
		t.LastComment = n.Comments.Nodes[0].CreatedAt
	}
	for _, l := range n.Labels.Nodes {
		label := ticket.Label{Name: l.Name}
		if l.Parent != nil {
			label.Group = l.Parent.Name
		}
		t.Labels = append(t.Labels, label)
	}
	return t
}

// AttributesIn returns the key-value pairs under the description's
// level-2 "## Attributes" heading (any case), which runs to the next heading
// or the end. A heading is hashes then a space, so "#42: x" is a pair. Each
// line splits on its first colon and both sides are trimmed; a line with no
// colon or a blank key is skipped, a later duplicate key wins and a list
// marker stays in the key. It returns nil, not an empty map, when there is no
// pair, so the rules' default of {} applies.
func AttributesIn(description string) map[string]string {
	var attrs map[string]string
	in := false
	for _, line := range strings.Split(description, "\n") {
		line = strings.TrimSpace(line)
		if hashes := len(line) - len(strings.TrimLeft(line, "#")); hashes > 0 && hashes < len(line) && line[hashes] == ' ' {
			in = hashes == 2 && strings.EqualFold(strings.TrimSpace(line[hashes:]), "attributes")
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		key = strings.TrimSpace(key)
		if !in || !ok || key == "" {
			continue
		}
		if attrs == nil {
			attrs = map[string]string{}
		}
		attrs[key] = strings.TrimSpace(value)
	}
	return attrs
}

// ManagedTickets returns the started tickets assigned to the key's owner that
// carry label, limited to teams. In Progress, In Review and Needs Human are
// all started statuses.
func (c *Client) ManagedTickets(ctx context.Context, label string, teams []string) ([]ticket.Ticket, error) {
	var resp struct {
		Viewer struct {
			AssignedIssues struct {
				Nodes    []issueNode `json:"nodes"`
				PageInfo struct {
					HasNextPage bool `json:"hasNextPage"`
				} `json:"pageInfo"`
			} `json:"assignedIssues"`
		} `json:"viewer"`
	}
	if err := c.do(ctx, "ManagedTickets", managedTickets, map[string]any{"label": label, "teams": teams, "first": PageSize}, &resp); err != nil {
		return nil, err
	}
	if resp.Viewer.AssignedIssues.PageInfo.HasNextPage {
		return nil, ManagedOverflow(label)
	}
	var tickets []ticket.Ticket
	for _, n := range resp.Viewer.AssignedIssues.Nodes {
		if !contains(teams, n.Team.Key) {
			continue
		}
		t := n.ticket()
		t.CreatedAt = n.CreatedAt
		tickets = append(tickets, t)
	}
	return tickets, nil
}

const leasedTickets = `query LeasedTickets($filter: IssueFilter!, $first: Int!) {
  viewer { id }
  issues(first: $first, includeArchived: true, filter: $filter) {
    nodes {
      id identifier title description url branchName
      state { name }
      team { key }
      assignee { id }
      labels(first: 25) { nodes { name parent { name } } pageInfo { hasNextPage } }
      children(first: 1) { nodes { id } }
    }
    pageInfo { hasNextPage }
  }
}`

// LeasedTickets returns the tickets named by identifiers, whatever their
// status and including archived ones, so a finished or paused ticket that
// still holds a tree can be found. Only tickets assigned to the key's owner
// come back, and of those the ones that carry label, plus any whose labels were
// cut short, because the caller can't tell whether they do. Identifiers outside
// teams, or not shaped like a ticket id, are ignored.
func (c *Client) LeasedTickets(ctx context.Context, label string, teams, identifiers []string) ([]ticket.Ticket, error) {
	var filters []map[string]any
	for _, id := range identifiers {
		team, number, ok := SplitIdentifier(id)
		if !ok || !contains(teams, team) {
			continue
		}
		// Linear ignores an "or" element that holds two field filters, so each
		// pair is wrapped in "and".
		filters = append(filters, map[string]any{"and": []map[string]any{
			{"team": map[string]any{"key": map[string]any{"eq": team}}},
			{"number": map[string]any{"eq": number}},
		}})
	}
	if len(filters) == 0 {
		return nil, nil
	}
	var resp struct {
		Viewer struct {
			ID string `json:"id"`
		} `json:"viewer"`
		Issues struct {
			Nodes    []issueNode `json:"nodes"`
			PageInfo struct {
				HasNextPage bool `json:"hasNextPage"`
			} `json:"pageInfo"`
		} `json:"issues"`
	}
	vars := map[string]any{"filter": map[string]any{"or": filters}, "first": PageSize}
	if err := c.do(ctx, "LeasedTickets", leasedTickets, vars, &resp); err != nil {
		return nil, err
	}
	if resp.Issues.PageInfo.HasNextPage {
		return nil, LeasedOverflow()
	}
	var tickets []ticket.Ticket
	for _, n := range resp.Issues.Nodes {
		if n.Assignee == nil || n.Assignee.ID != resp.Viewer.ID {
			continue
		}
		t := n.ticket()
		if t.LabelsTruncated || t.HasLabel(label) {
			tickets = append(tickets, t)
		}
	}
	return tickets, nil
}

const parentChildren = `query ParentChildren($parents: [ID!], $first: Int!) {
  viewer { id }
  issues(first: $first, filter: {parent: {id: {in: $parents}}}) {
    nodes {
      id identifier title description branchName createdAt
      state { name }
      team { key }
      assignee { id }
      parent { identifier }
      labels(first: 25) { nodes { name parent { name } } pageInfo { hasNextPage } }
      children(first: 1) { nodes { id } }
      inverseRelations(first: 10) { nodes { type issue { identifier state { name type } } } pageInfo { hasNextPage } }
    }
    pageInfo { hasNextPage }
  }
}`

// ParentChildren returns every child of the parents with these Linear ids, in
// every status, keyed by the parent's identifier. A parent closes only when all
// its children are finished, so the finished ones come back too. A second page
// is an error, because a child left off it could be the unfinished one.
func (c *Client) ParentChildren(ctx context.Context, parents []string) (map[string][]ticket.Ticket, error) {
	if len(parents) == 0 {
		return nil, nil
	}
	var resp struct {
		Viewer struct {
			ID string `json:"id"`
		} `json:"viewer"`
		Issues struct {
			Nodes    []issueNode `json:"nodes"`
			PageInfo struct {
				HasNextPage bool `json:"hasNextPage"`
			} `json:"pageInfo"`
		} `json:"issues"`
	}
	if err := c.do(ctx, "ParentChildren", parentChildren, map[string]any{"parents": parents, "first": PageSize}, &resp); err != nil {
		return nil, err
	}
	if resp.Issues.PageInfo.HasNextPage {
		return nil, ChildrenOverflow()
	}
	children := map[string][]ticket.Ticket{}
	for _, n := range resp.Issues.Nodes {
		if n.Parent == nil {
			continue
		}
		t := n.ticket()
		t.Mine = n.Assignee != nil && n.Assignee.ID == resp.Viewer.ID
		t.CreatedAt = n.CreatedAt
		children[n.Parent.Identifier] = append(children[n.Parent.Identifier], t)
	}
	// Linear's order is not documented, and promotion under a full cap goes to
	// the oldest child first.
	for _, kids := range children {
		slices.SortStableFunc(kids, func(a, b ticket.Ticket) int { return strings.Compare(a.CreatedAt, b.CreatedAt) })
	}
	return children, nil
}

// SplitIdentifier splits "ENG-123" into its team key and number.
func SplitIdentifier(id string) (team string, number int, ok bool) {
	i := strings.LastIndex(id, "-")
	if i <= 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(id[i+1:])
	if err != nil || n <= 0 {
		return "", 0, false
	}
	return id[:i], n, true
}

// Filtering on the key keeps the answer to one team, because an unfiltered
// teams connection stops at its first page of 50. A states connection has the
// same default, so it asks for 250, the most Linear returns per page. Linear
// rejects a query over 10,000 complexity points and prices a connection at its
// page size, so teams asks for one: 50 teams of 250 states would cost 15,000.
const teamStates = `query TeamStates($team: String!) {
  teams(first: 1, filter: {key: {eq: $team}}) { nodes { key states(first: 250) { nodes { id name } } } }
}`

// StateID returns the id of the named workflow status on a team.
func (c *Client) StateID(ctx context.Context, team, name string) (string, error) {
	var resp struct {
		Teams struct {
			Nodes []struct {
				Key    string `json:"key"`
				States struct {
					Nodes []struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"nodes"`
				} `json:"states"`
			} `json:"nodes"`
		} `json:"teams"`
	}
	if err := c.do(ctx, "TeamStates", teamStates, map[string]any{"team": team}, &resp); err != nil {
		return "", err
	}
	for _, t := range resp.Teams.Nodes {
		if t.Key != team {
			continue
		}
		for _, s := range t.States.Nodes {
			if s.Name == name {
				return s.ID, nil
			}
		}
	}
	if len(resp.Teams.Nodes) == 0 {
		return "", NoTeam(team)
	}
	return "", NoStatus(team, name)
}

const moveTicket = `mutation MoveTicket($id: String!, $state: String!) {
  issueUpdate(id: $id, input: {stateId: $state}) { success }
}`

// Move sets a ticket's status.
func (c *Client) Move(ctx context.Context, ticketID, stateID string) error {
	var out struct {
		IssueUpdate struct {
			Success bool `json:"success"`
		} `json:"issueUpdate"`
	}
	if err := c.do(ctx, "MoveTicket", moveTicket, map[string]any{"id": ticketID, "state": stateID}, &out); err != nil {
		return err
	}
	if !out.IssueUpdate.Success {
		return Unsuccessful("MoveTicket", ticketID)
	}
	return nil
}

const commentTicket = `mutation CommentTicket($id: String!, $body: String!) {
  commentCreate(input: {issueId: $id, body: $body}) { success }
}`

// Comment adds a comment to a ticket.
func (c *Client) Comment(ctx context.Context, ticketID, body string) error {
	var out struct {
		CommentCreate struct {
			Success bool `json:"success"`
		} `json:"commentCreate"`
	}
	if err := c.do(ctx, "CommentTicket", commentTicket, map[string]any{"id": ticketID, "body": body}, &out); err != nil {
		return err
	}
	if !out.CommentCreate.Success {
		return Unsuccessful("CommentTicket", ticketID)
	}
	return nil
}

// NoStatus is the error for a status name the team doesn't have.
func NoStatus(team, name string) error {
	return fmt.Errorf("team %s has no %q status", team, name)
}

// NoTeam is the error for a team key that matches no Linear team.
func NoTeam(team string) error {
	return fmt.Errorf("team %s not found", team)
}

// Unsuccessful is the error for a mutation Linear answered with success:false.
func Unsuccessful(operation, ticketID string) error {
	return fmt.Errorf("linear %s: Linear reported success=false for %s", operation, ticketID)
}

// ErrUnauthorized means Linear rejected the API key.
var ErrUnauthorized = errors.New("Linear rejected the API key")

// Viewer is the user who owns the client's API key.
type Viewer struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Viewer returns the user who owns the API key.
func (c *Client) Viewer(ctx context.Context) (Viewer, error) {
	var out struct {
		Viewer Viewer `json:"viewer"`
	}
	err := c.do(ctx, "Viewer", `query Viewer { viewer { id name } }`, nil, &out)
	return out.Viewer, err
}

func (c *Client) do(ctx context.Context, operation, query string, variables map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"operationName": operation, "query": query, "variables": variables})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// Linear personal API keys go in Authorization without a scheme.
	req.Header.Set("Authorization", c.Key)
	httpc := c.HTTP
	if httpc == nil {
		httpc = http.DefaultClient
	}
	start := time.Now()
	resp, err := httpc.Do(req)
	c.mu.Lock()
	c.stats.record(start, resp)
	c.mu.Unlock()
	if err != nil {
		return fmt.Errorf("while calling Linear %s: %w", operation, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("while reading Linear %s: %w", operation, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("linear %s: HTTP %d: %w", operation, resp.StatusCode, ErrUnauthorized)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("linear %s: HTTP %d: %s", operation, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("linear %s: %w", operation, err)
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("linear %s: %s", operation, envelope.Errors[0].Message)
	}
	return json.Unmarshal(envelope.Data, out)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// LabelRef names a label to put on a new ticket. A Group restricts the match to labels in that
// label group.
type LabelRef struct{ Name, Group string }

// NewTicket is a ticket to file. Team is a team key, Status a workflow status name, and label
// names match ignoring case, as Linear does.
type NewTicket struct {
	Team        string
	Title       string
	Description string
	Status      string
	Labels      []LabelRef
}

// Described is a ticket's Linear id and description.
type Described struct {
	ID          string
	Description string
}

// ErrNotManaged matches the error for a ticket dispatch doesn't manage.
var ErrNotManaged = errors.New("dispatch doesn't manage this ticket")

// NotManaged is the error for a ticket on a team dispatch doesn't read, or one that is not assigned
// to the key's owner or lacks label.
func NotManaged(identifier, label string) error {
	return fmt.Errorf("%s: %w: it must be on a team dispatch reads, assigned to the owner of dispatch's Linear API key, and carry the %q label", identifier, ErrNotManaged, label)
}

// NoLabel is the error for a label the team and workspace lack.
func NoLabel(team string, ref LabelRef) error {
	if ref.Group != "" {
		return fmt.Errorf("team %s has no %q label in the %q label group", team, ref.Name, ref.Group)
	}
	return fmt.Errorf("team %s has no %q label", team, ref.Name)
}

const filingContext = `query FilingContext($team: String!) {
  viewer { id }
  teams(first: 1, filter: {key: {eq: $team}}) { nodes { id key states(first: 250) { nodes { id name } } } }
  issueLabels(first: 250, filter: {or: [{team: {key: {eq: $team}}}, {team: {null: true}}]}) {
    nodes { id name parent { name } }
    pageInfo { hasNextPage }
  }
}`

const createTicket = `mutation CreateTicket($input: IssueCreateInput!) {
  issueCreate(input: $input) { success issue { identifier } }
}`

// CreateTicket files a ticket assigned to the key's owner.
func (c *Client) CreateTicket(ctx context.Context, t NewTicket) (string, error) {
	var found struct {
		Viewer struct {
			ID string `json:"id"`
		} `json:"viewer"`
		Teams struct {
			Nodes []struct {
				ID     string `json:"id"`
				Key    string `json:"key"`
				States struct {
					Nodes []struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"nodes"`
				} `json:"states"`
			} `json:"nodes"`
		} `json:"teams"`
		IssueLabels struct {
			Nodes []struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				Parent *struct {
					Name string `json:"name"`
				} `json:"parent"`
			} `json:"nodes"`
			PageInfo struct {
				HasNextPage bool `json:"hasNextPage"`
			} `json:"pageInfo"`
		} `json:"issueLabels"`
	}
	if err := c.do(ctx, "FilingContext", filingContext, map[string]any{"team": t.Team}, &found); err != nil {
		return "", err
	}
	if len(found.Teams.Nodes) == 0 || found.Teams.Nodes[0].Key != t.Team {
		return "", NoTeam(t.Team)
	}
	team := found.Teams.Nodes[0]
	input := map[string]any{"teamId": team.ID, "title": t.Title, "assigneeId": found.Viewer.ID}
	if t.Description != "" {
		input["description"] = t.Description
	}
	stateID := ""
	for _, s := range team.States.Nodes {
		if s.Name == t.Status {
			stateID = s.ID
		}
	}
	if stateID == "" {
		return "", NoStatus(t.Team, t.Status)
	}
	input["stateId"] = stateID
	labelIDs := []string{}
	for _, ref := range t.Labels {
		id := ""
		for _, l := range found.IssueLabels.Nodes {
			if strings.EqualFold(l.Name, ref.Name) && (ref.Group == "" || (l.Parent != nil && strings.EqualFold(l.Parent.Name, ref.Group))) {
				id = l.ID
				break
			}
		}
		if id == "" && found.IssueLabels.PageInfo.HasNextPage {
			return "", fmt.Errorf("team %s has more labels than fit on one page, so dispatch can't tell whether %q exists", t.Team, ref.Name)
		}
		if id == "" {
			return "", NoLabel(t.Team, ref)
		}
		labelIDs = append(labelIDs, id)
	}
	input["labelIds"] = labelIDs
	var out struct {
		IssueCreate struct {
			Success bool `json:"success"`
			Issue   struct {
				Identifier string `json:"identifier"`
			} `json:"issue"`
		} `json:"issueCreate"`
	}
	if err := c.do(ctx, "CreateTicket", createTicket, map[string]any{"input": input}, &out); err != nil {
		return "", err
	}
	if !out.IssueCreate.Success || out.IssueCreate.Issue.Identifier == "" {
		return "", Unsuccessful("CreateTicket", t.Title)
	}
	return out.IssueCreate.Issue.Identifier, nil
}

const managedDescription = `query ManagedDescription($team: String!, $number: Float!) {
  viewer { id }
  issues(first: 1, includeArchived: true, filter: {team: {key: {eq: $team}}, number: {eq: $number}}) {
    nodes {
      id description
      team { key }
      assignee { id }
      labels(first: 25) { nodes { name } pageInfo { hasNextPage } }
    }
  }
}`

// ManagedDescription returns the id and description of the ticket named by identifier when the
// key's owner is its assignee and it carries label.
func (c *Client) ManagedDescription(ctx context.Context, label string, teams []string, identifier string) (Described, error) {
	team, number, ok := SplitIdentifier(identifier)
	if !ok || !contains(teams, team) {
		return Described{}, NotManaged(identifier, label)
	}
	var resp struct {
		Viewer struct {
			ID string `json:"id"`
		} `json:"viewer"`
		Issues struct {
			Nodes []issueNode `json:"nodes"`
		} `json:"issues"`
	}
	if err := c.do(ctx, "ManagedDescription", managedDescription, map[string]any{"team": team, "number": number}, &resp); err != nil {
		return Described{}, err
	}
	if len(resp.Issues.Nodes) == 0 {
		return Described{}, NotManaged(identifier, label)
	}
	n := resp.Issues.Nodes[0]
	// A ticket with more labels than one page may carry the label on a later page.
	t := n.ticket()
	if n.Assignee == nil || n.Assignee.ID != resp.Viewer.ID || !(t.HasLabel(label) || t.LabelsTruncated) {
		return Described{}, NotManaged(identifier, label)
	}
	return Described{ID: n.ID, Description: n.Description}, nil
}

const setDescription = `mutation SetDescription($id: String!, $description: String!) {
  issueUpdate(id: $id, input: {description: $description}) { success }
}`

// SetDescription replaces a ticket's description.
func (c *Client) SetDescription(ctx context.Context, ticketID, description string) error {
	var out struct {
		IssueUpdate struct {
			Success bool `json:"success"`
		} `json:"issueUpdate"`
	}
	if err := c.do(ctx, "SetDescription", setDescription, map[string]any{"id": ticketID, "description": description}, &out); err != nil {
		return err
	}
	if !out.IssueUpdate.Success {
		return Unsuccessful("SetDescription", ticketID)
	}
	return nil
}
