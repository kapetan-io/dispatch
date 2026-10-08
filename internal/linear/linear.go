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
	Branch     string
	Status     string
	Team       string
	Labels     []Label
	// HasChildren marks a parent ticket, which never gets a dispatched worker.
	HasChildren bool
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

const managedTickets = `query ManagedTickets($label: String!) {
  viewer {
    assignedIssues(first: 250, filter: {labels: {name: {eq: $label}}, state: {type: {eq: "started"}}}) {
      nodes {
        id identifier title branchName
        state { name }
        team { key }
        labels { nodes { name parent { name } } }
        children { nodes { id } }
      }
    }
  }
}`

// ManagedTickets returns the started tickets assigned to the key's owner that
// carry label, limited to teams. In Progress, In Review and Needs Human are
// all started statuses.
func (c Client) ManagedTickets(ctx context.Context, label string, teams []string) ([]Ticket, error) {
	var resp struct {
		Viewer struct {
			AssignedIssues struct {
				Nodes []struct {
					ID         string `json:"id"`
					Identifier string `json:"identifier"`
					Title      string `json:"title"`
					BranchName string `json:"branchName"`
					State      struct {
						Name string `json:"name"`
					} `json:"state"`
					Team struct {
						Key string `json:"key"`
					} `json:"team"`
					Labels struct {
						Nodes []struct {
							Name   string `json:"name"`
							Parent *struct {
								Name string `json:"name"`
							} `json:"parent"`
						} `json:"nodes"`
					} `json:"labels"`
					Children struct {
						Nodes []struct {
							ID string `json:"id"`
						} `json:"nodes"`
					} `json:"children"`
				} `json:"nodes"`
			} `json:"assignedIssues"`
		} `json:"viewer"`
	}
	if err := c.do(ctx, "ManagedTickets", managedTickets, map[string]any{"label": label}, &resp); err != nil {
		return nil, err
	}
	var tickets []Ticket
	for _, n := range resp.Viewer.AssignedIssues.Nodes {
		if !contains(teams, n.Team.Key) {
			continue
		}
		t := Ticket{
			ID:          n.ID,
			Identifier:  n.Identifier,
			Title:       n.Title,
			Branch:      n.BranchName,
			Status:      n.State.Name,
			Team:        n.Team.Key,
			HasChildren: len(n.Children.Nodes) > 0,
		}
		for _, l := range n.Labels.Nodes {
			label := Label{Name: l.Name}
			if l.Parent != nil {
				label.Group = l.Parent.Name
			}
			t.Labels = append(t.Labels, label)
		}
		tickets = append(tickets, t)
	}
	return tickets, nil
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
	return c.do(ctx, "MoveTicket", moveTicket, map[string]any{"id": ticketID, "state": stateID}, nil)
}

const commentTicket = `mutation CommentTicket($id: String!, $body: String!) {
  commentCreate(input: {issueId: $id, body: $body}) { success }
}`

// Comment adds a comment to a ticket.
func (c Client) Comment(ctx context.Context, ticketID, body string) error {
	return c.do(ctx, "CommentTicket", commentTicket, map[string]any{"id": ticketID, "body": body}, nil)
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
	if out == nil {
		return nil
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
