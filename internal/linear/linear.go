// Package linear reads and writes the tickets the dispatcher manages, through
// Linear's GraphQL API. The dispatcher is a script, so it can't use the MCP
// connector that Claude sessions use.
package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// DefaultURL is Linear's GraphQL endpoint.
const DefaultURL = "https://api.linear.app/graphql"

// Ticket is one managed Linear issue.
type Ticket struct {
	// ID is Linear's internal id, used for mutations.
	ID string
	// Identifier is the human id, such as ENG-123. It is also the treehouse lease holder.
	Identifier string
	Title      string
	URL        string
	Branch     string
	Status     string
	Team       string
	Labels     []Label
	// HasChildren marks a parent ticket, which never gets a dispatched worker.
	HasChildren bool
	// LabelsTruncated means Labels is only the first page, so a repo or design
	// label may be missing from it.
	LabelsTruncated bool
	// LastComment is the creation time of the ticket's newest comment, or empty
	// when it has none. It is RFC 3339 and sorts as text. Only ManagedTickets
	// fills it in, since only its tickets get comment nudges.
	LastComment string
	// Mine means the ticket is assigned to the key's owner. Only ParentChildren
	// sets it, because ManagedTickets returns nothing else.
	Mine bool
	// CreatedAt is Linear's RFC 3339 creation time, which sorts as text. Only
	// ParentChildren fills it in.
	CreatedAt string
	// Blockers are the tickets that block this one. ManagedTickets and
	// ParentChildren fill them in.
	Blockers []Blocker
	// BlockersTruncated means the ticket has more than 10 relations of any kind,
	// so an unfinished blocker may be missing from Blockers.
	BlockersTruncated bool
}

// Blocker is a ticket that blocks another and the status it is in.
type Blocker struct {
	Identifier string
	Status     string
	// Abandoned means the blocker's status is of Linear's canceled type, which
	// covers Canceled and Duplicate. It never becomes Done.
	Abandoned bool
}

// Label is a Linear label and the label group it belongs to, if any.
type Label struct {
	Name  string
	Group string
}

// HasLabel reports whether the ticket carries a label with this name.
func (t Ticket) HasLabel(name string) bool {
	for _, l := range t.Labels {
		if l.Name == name {
			return true
		}
	}
	return false
}

// LabelIn returns the ticket's label in the given group, such as its repo.
func (t Ticket) LabelIn(group string) (string, bool) {
	for _, l := range t.Labels {
		if l.Group == group {
			return l.Name, true
		}
	}
	return "", false
}

// Client talks to one Linear workspace as the API key's owner.
type Client struct {
	URL  string
	Key  string
	HTTP *http.Client
	// Stats, when set, records every call this client makes.
	Stats *Stats
}

// Stats counts a client's calls to Linear and keeps the hourly request budget
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

// maxManaged is the page size for managed tickets. A second page is an error
// rather than a loop: tickets left off it would go unlaunched, and their live
// workers uncounted against max_in_progress.
const maxManaged = 100

// The nested connections are capped because Linear charges a query by its
// page sizes, and only the presence of a child matters. Linear lists comments
// newest first, so comments(first: 1) is the newest and last: 1 the oldest.
const managedTickets = `query ManagedTickets($label: String!, $teams: [String!], $first: Int!) {
  viewer {
    assignedIssues(first: $first, filter: {labels: {name: {eq: $label}}, state: {type: {eq: "started"}}, team: {key: {in: $teams}}}) {
      nodes {
        id identifier title url branchName
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
	ID         string `json:"id"`
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	BranchName string `json:"branchName"`
	State      struct {
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

func (n issueNode) ticket() Ticket {
	t := Ticket{
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
	}
	for _, r := range n.InverseRelations.Nodes {
		if r.Type == "blocks" {
			t.Blockers = append(t.Blockers, Blocker{Identifier: r.Issue.Identifier, Status: r.Issue.State.Name, Abandoned: r.Issue.State.Type == "canceled"})
		}
	}
	if len(n.Comments.Nodes) > 0 {
		t.LastComment = n.Comments.Nodes[0].CreatedAt
	}
	for _, l := range n.Labels.Nodes {
		label := Label{Name: l.Name}
		if l.Parent != nil {
			label.Group = l.Parent.Name
		}
		t.Labels = append(t.Labels, label)
	}
	return t
}

// ManagedTickets returns the started tickets assigned to the key's owner that
// carry label, limited to teams. In Progress, In Review and Needs Human are
// all started statuses.
func (c Client) ManagedTickets(ctx context.Context, label string, teams []string) ([]Ticket, error) {
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
	if err := c.do(ctx, "ManagedTickets", managedTickets, map[string]any{"label": label, "teams": teams, "first": maxManaged}, &resp); err != nil {
		return nil, err
	}
	if resp.Viewer.AssignedIssues.PageInfo.HasNextPage {
		return nil, fmt.Errorf("more than %d started %q tickets are assigned to you; dispatch reads one page and would miss the rest", maxManaged, label)
	}
	var tickets []Ticket
	for _, n := range resp.Viewer.AssignedIssues.Nodes {
		if !contains(teams, n.Team.Key) {
			continue
		}
		tickets = append(tickets, n.ticket())
	}
	return tickets, nil
}

const leasedTickets = `query LeasedTickets($filter: IssueFilter!, $first: Int!) {
  viewer { id }
  issues(first: $first, includeArchived: true, filter: $filter) {
    nodes {
      id identifier title url branchName
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
func (c Client) LeasedTickets(ctx context.Context, label string, teams, identifiers []string) ([]Ticket, error) {
	var filters []map[string]any
	for _, id := range identifiers {
		team, number, ok := splitIdentifier(id)
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
	vars := map[string]any{"filter": map[string]any{"or": filters}, "first": maxManaged}
	if err := c.do(ctx, "LeasedTickets", leasedTickets, vars, &resp); err != nil {
		return nil, err
	}
	if resp.Issues.PageInfo.HasNextPage {
		return nil, fmt.Errorf("more than %d tickets hold treehouse leases; dispatch reads one page and would miss the rest", maxManaged)
	}
	var tickets []Ticket
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
      id identifier title branchName createdAt
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
func (c Client) ParentChildren(ctx context.Context, parents []string) (map[string][]Ticket, error) {
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
	if err := c.do(ctx, "ParentChildren", parentChildren, map[string]any{"parents": parents, "first": maxManaged}, &resp); err != nil {
		return nil, err
	}
	if resp.Issues.PageInfo.HasNextPage {
		return nil, fmt.Errorf("the open parents have more than %d children between them; dispatch reads one page and could close a parent that still has unfinished children", maxManaged)
	}
	children := map[string][]Ticket{}
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
		slices.SortStableFunc(kids, func(a, b Ticket) int { return strings.Compare(a.CreatedAt, b.CreatedAt) })
	}
	return children, nil
}

// splitIdentifier splits "ENG-123" into its team key and number.
func splitIdentifier(id string) (team string, number int, ok bool) {
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

const teamStates = `query TeamStates {
  teams { nodes { key states { nodes { id name } } } }
}`

// StateID returns the id of the named workflow status on a team.
func (c Client) StateID(ctx context.Context, team, name string) (string, error) {
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
	if err := c.do(ctx, "TeamStates", teamStates, nil, &resp); err != nil {
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
	return "", fmt.Errorf("team %s has no %q status", team, name)
}

const moveTicket = `mutation MoveTicket($id: String!, $state: String!) {
  issueUpdate(id: $id, input: {stateId: $state}) { success }
}`

// Move sets a ticket's status.
func (c Client) Move(ctx context.Context, ticketID, stateID string) error {
	var out struct {
		IssueUpdate struct {
			Success bool `json:"success"`
		} `json:"issueUpdate"`
	}
	if err := c.do(ctx, "MoveTicket", moveTicket, map[string]any{"id": ticketID, "state": stateID}, &out); err != nil {
		return err
	}
	if !out.IssueUpdate.Success {
		return fmt.Errorf("linear MoveTicket: Linear reported success=false for %s", ticketID)
	}
	return nil
}

const commentTicket = `mutation CommentTicket($id: String!, $body: String!) {
  commentCreate(input: {issueId: $id, body: $body}) { success }
}`

// Comment adds a comment to a ticket.
func (c Client) Comment(ctx context.Context, ticketID, body string) error {
	var out struct {
		CommentCreate struct {
			Success bool `json:"success"`
		} `json:"commentCreate"`
	}
	if err := c.do(ctx, "CommentTicket", commentTicket, map[string]any{"id": ticketID, "body": body}, &out); err != nil {
		return err
	}
	if !out.CommentCreate.Success {
		return fmt.Errorf("linear CommentTicket: Linear reported success=false for %s", ticketID)
	}
	return nil
}

func (c Client) do(ctx context.Context, operation, query string, variables map[string]any, out any) error {
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
	c.Stats.record(start, resp)
	if err != nil {
		return fmt.Errorf("while calling Linear %s: %w", operation, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("while reading Linear %s: %w", operation, err)
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
