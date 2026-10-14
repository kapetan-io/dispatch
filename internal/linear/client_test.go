package linear_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/linear"
)

// wire is a web server that answers Linear's GraphQL operations by name with
// canned data. It exists to test what the adapter sends and how it reads
// answers, which the in-memory fake hides.
type wire struct {
	server    *httptest.Server
	answers   map[string]any
	status    int
	envelope  map[string]any
	variables map[string]map[string]any
	auth      string
}

func newWire(t *testing.T) *wire {
	w := &wire{answers: map[string]any{}, variables: map[string]map[string]any{}}
	w.server = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		var req struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.auth = r.Header.Get("Authorization")
		w.variables[req.OperationName] = req.Variables
		rw.Header().Set("X-RateLimit-Requests-Limit", "2500")
		rw.Header().Set("X-RateLimit-Requests-Remaining", "2400")
		if w.status != 0 {
			http.Error(rw, "refused", w.status)
			return
		}
		if w.envelope != nil {
			_ = json.NewEncoder(rw).Encode(w.envelope)
			return
		}
		_ = json.NewEncoder(rw).Encode(map[string]any{"data": w.answers[req.OperationName]})
	}))
	t.Cleanup(w.server.Close)
	return w
}

func (w *wire) client() *linear.Client { return linear.NewClient(w.server.URL, "secret") }

func TestClientSendsTheKeyWithoutAScheme(t *testing.T) {
	w := newWire(t)
	w.answers["Viewer"] = map[string]any{"viewer": map[string]string{"id": "u1", "name": "Ada"}}

	viewer, err := w.client().Viewer(context.Background())

	require.NoError(t, err)
	assert.Equal(t, linear.Viewer{ID: "u1", Name: "Ada"}, viewer)
	assert.Equal(t, "secret", w.auth)
}

func TestClientReportsAnUnauthorizedKey(t *testing.T) {
	w := newWire(t)
	w.status = http.StatusUnauthorized

	_, err := w.client().Viewer(context.Background())

	require.ErrorIs(t, err, linear.ErrUnauthorized)
}

func TestClientReportsGraphQLErrors(t *testing.T) {
	w := newWire(t)
	w.envelope = map[string]any{"errors": []map[string]string{{"message": "Entity not found: Issue"}}}

	err := w.client().Move(context.Background(), "id", "state")

	require.ErrorContains(t, err, "linear MoveTicket: Entity not found: Issue")
}

func TestClientReportsAMutationLinearAnswersWithSuccessFalse(t *testing.T) {
	w := newWire(t)
	w.answers["MoveTicket"] = map[string]any{"issueUpdate": map[string]bool{"success": false}}
	w.answers["CommentTicket"] = map[string]any{"commentCreate": map[string]bool{"success": false}}

	err := w.client().Move(context.Background(), "t1", "s1")
	require.ErrorContains(t, err, "linear MoveTicket: Linear reported success=false for t1")

	err = w.client().Comment(context.Background(), "t1", "hi")
	require.ErrorContains(t, err, "linear CommentTicket: Linear reported success=false for t1")
}

func TestClientKeepsLinearsRequestBudget(t *testing.T) {
	w := newWire(t)
	w.answers["Viewer"] = map[string]any{"viewer": map[string]string{"id": "u1"}}
	client := w.client()

	_, err := client.Viewer(context.Background())
	require.NoError(t, err)

	usage := client.Usage()
	assert.Equal(t, 1, usage.Calls)
	assert.Equal(t, 2400, usage.RequestsRemaining)
	assert.Equal(t, 2500, usage.RequestsLimit)
}

func TestClientFailsWhenManagedTicketsNeedASecondPage(t *testing.T) {
	w := newWire(t)
	w.answers["ManagedTickets"] = map[string]any{"viewer": map[string]any{"assignedIssues": map[string]any{
		"nodes": []any{}, "pageInfo": map[string]bool{"hasNextPage": true},
	}}}

	_, err := w.client().ManagedTickets(context.Background(), "ai", []string{"ENG"})

	require.EqualError(t, err, linear.ManagedOverflow("ai").Error())
}

// TestClientAsksForLeasedTicketsInAFormLinearApplies pins ENG-238. Linear
// ignores an "or" element that holds two field filters and then matches every
// ticket, so each team-and-number pair must sit inside one "and".
func TestClientAsksForLeasedTicketsInAFormLinearApplies(t *testing.T) {
	w := newWire(t)
	w.answers["LeasedTickets"] = map[string]any{
		"viewer": map[string]string{"id": "u1"},
		"issues": map[string]any{"nodes": []any{}, "pageInfo": map[string]bool{"hasNextPage": false}},
	}

	_, err := w.client().LeasedTickets(context.Background(), "ai", []string{"ENG"}, []string{"ENG-1", "ENG-22", "OTHER-3", "garbage"})

	require.NoError(t, err)
	filter, err := json.Marshal(w.variables["LeasedTickets"]["filter"])
	require.NoError(t, err)
	assert.JSONEq(t, `{"or":[
		{"and":[{"team":{"key":{"eq":"ENG"}}},{"number":{"eq":1}}]},
		{"and":[{"team":{"key":{"eq":"ENG"}}},{"number":{"eq":22}}]}
	]}`, string(filter))
}

func TestClientMakesNoCallToLookUpTicketsItCannotPlace(t *testing.T) {
	w := newWire(t)
	client := w.client()

	found, err := client.LeasedTickets(context.Background(), "ai", []string{"ENG"}, []string{"OTHER-3", "garbage"})

	require.NoError(t, err)
	assert.Empty(t, found)
	assert.Zero(t, client.Usage().Calls)
}

func TestClientReadsEveryFieldOfAManagedTicket(t *testing.T) {
	w := newWire(t)
	w.answers["ManagedTickets"] = map[string]any{"viewer": map[string]any{"assignedIssues": map[string]any{
		"nodes": []any{map[string]any{
			"id": "id1", "identifier": "ENG-1", "title": "t", "url": "https://x/ENG-1", "branchName": "b",
			"state":    map[string]string{"name": "In Progress"},
			"team":     map[string]string{"key": "ENG"},
			"labels":   map[string]any{"nodes": []any{map[string]any{"name": "dispatch", "parent": map[string]string{"name": "repo"}}}, "pageInfo": map[string]bool{"hasNextPage": true}},
			"children": map[string]any{"nodes": []any{map[string]string{"id": "c"}}},
			"comments": map[string]any{"nodes": []any{map[string]string{"createdAt": "2026-10-09T00:09:00.000Z"}}},
			"inverseRelations": map[string]any{"nodes": []any{
				map[string]any{"type": "blocks", "issue": map[string]any{"identifier": "ENG-9", "state": map[string]string{"name": "Canceled", "type": "canceled"}}},
				map[string]any{"type": "related", "issue": map[string]any{"identifier": "ENG-8", "state": map[string]string{"name": "Todo", "type": "unstarted"}}},
			}, "pageInfo": map[string]bool{"hasNextPage": false}},
		}},
		"pageInfo": map[string]bool{"hasNextPage": false},
	}}}

	found, err := w.client().ManagedTickets(context.Background(), "ai", []string{"ENG"})

	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, linear.Ticket{
		ID: "id1", Identifier: "ENG-1", Title: "t", URL: "https://x/ENG-1", Branch: "b",
		Status: "In Progress", Team: "ENG", HasChildren: true, LabelsTruncated: true,
		Labels:      []linear.Label{{Name: "dispatch", Group: "repo"}},
		LastComment: "2026-10-09T00:09:00.000Z",
		Blockers:    []linear.Blocker{{Identifier: "ENG-9", Status: "Canceled", Abandoned: true}},
	}, found[0])
}

func TestClientListsTheOldestChildFirst(t *testing.T) {
	w := newWire(t)
	child := func(id, created string) map[string]any {
		return map[string]any{
			"id": id, "identifier": id, "createdAt": created,
			"state": map[string]string{"name": "Todo"}, "team": map[string]string{"key": "ENG"},
			"assignee": map[string]string{"id": "u1"}, "parent": map[string]string{"identifier": "ENG-1"},
		}
	}
	w.answers["ParentChildren"] = map[string]any{
		"viewer": map[string]string{"id": "u1"},
		"issues": map[string]any{"nodes": []any{child("ENG-3", "2026-10-02"), child("ENG-2", "2026-10-01")}, "pageInfo": map[string]bool{"hasNextPage": false}},
	}

	children, err := w.client().ParentChildren(context.Background(), []string{"id1"})

	require.NoError(t, err)
	var order []string
	for _, c := range children["ENG-1"] {
		order = append(order, fmt.Sprint(c.Identifier, c.Mine))
	}
	assert.Equal(t, []string{"ENG-2true", "ENG-3true"}, order)
}
