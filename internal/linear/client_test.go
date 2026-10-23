package linear_test

// Package exception: this package wraps the Linear API, so every test here is an adapter or contract test of it.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/linear"
)

// wire is a web server that answers Linear's GraphQL operations by name with
// canned data. It exists to test what the adapter sends and how it reads
// answers, which the in-memory fake hides.
type wire struct {
	server  *httptest.Server
	answers map[string]any
	// respond takes precedence over answers for the operations it names.
	respond   map[string]func(query string, variables map[string]any) any
	status    int
	envelope  map[string]any
	variables map[string]map[string]any
	queries   map[string]string
	auth      string
}

func newWire(t *testing.T) *wire {
	w := &wire{answers: map[string]any{}, respond: map[string]func(string, map[string]any) any{}, variables: map[string]map[string]any{}, queries: map[string]string{}}
	w.server = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		var req struct {
			OperationName string         `json:"operationName"`
			Query         string         `json:"query"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.auth = r.Header.Get("Authorization")
		w.variables[req.OperationName] = req.Variables
		w.queries[req.OperationName] = req.Query
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
		if respond, ok := w.respond[req.OperationName]; ok {
			_ = json.NewEncoder(rw).Encode(map[string]any{"data": respond(req.Query, req.Variables)})
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

// A finished or paused ticket that still holds a tree may be archived, and
// Linear leaves archived tickets out unless asked.
func TestClientAsksForArchivedTicketsOnlyWhenLookingUpLeasedOnes(t *testing.T) {
	w := newWire(t)
	w.answers["LeasedTickets"] = map[string]any{
		"viewer": map[string]string{"id": "u1"},
		"issues": map[string]any{"nodes": []any{}, "pageInfo": map[string]bool{"hasNextPage": false}},
	}
	w.answers["ManagedTickets"] = map[string]any{
		"viewer": map[string]any{"assignedIssues": map[string]any{"nodes": []any{}, "pageInfo": map[string]bool{"hasNextPage": false}}},
	}

	_, err := w.client().LeasedTickets(context.Background(), "ai", []string{"ENG"}, []string{"ENG-1"})
	require.NoError(t, err)
	_, err = w.client().ManagedTickets(context.Background(), "ai", []string{"ENG"})
	require.NoError(t, err)

	assert.Contains(t, w.queries["LeasedTickets"], "includeArchived: true")
	assert.NotContains(t, w.queries["ManagedTickets"], "includeArchived")
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
			"id": "id1", "identifier": "ENG-1", "title": "t", "url": "https://x/ENG-1", "branchName": "b", "createdAt": "2026-10-01T00:00:00.000Z",
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
		LastComment: "2026-10-09T00:09:00.000Z", CreatedAt: "2026-10-01T00:00:00.000Z",
		Blockers: []linear.Blocker{{Identifier: "ENG-9", Status: "Canceled", Abandoned: true}},
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

func page(nodes ...any) map[string]any {
	return map[string]any{"nodes": nodes, "pageInfo": map[string]bool{"hasNextPage": false}}
}

func node(identifier, assignee string, labels ...string) map[string]any {
	var names []any
	for _, name := range labels {
		names = append(names, map[string]any{"name": name})
	}
	n := map[string]any{
		"id": "id-" + identifier, "identifier": identifier,
		"state": map[string]string{"name": "Todo"}, "team": map[string]string{"key": "ENG"},
		"labels": page(names...),
	}
	if assignee != "" {
		n["assignee"] = map[string]string{"id": assignee}
	}
	return n
}

func TestClientFailsWhenLeasedTicketsNeedASecondPage(t *testing.T) {
	w := newWire(t)
	w.answers["LeasedTickets"] = map[string]any{
		"viewer": map[string]string{"id": "u1"},
		"issues": map[string]any{"nodes": []any{}, "pageInfo": map[string]bool{"hasNextPage": true}},
	}

	_, err := w.client().LeasedTickets(context.Background(), "ai", []string{"ENG"}, []string{"ENG-1"})

	require.EqualError(t, err, linear.LeasedOverflow().Error())
}

func TestClientFailsWhenChildrenNeedASecondPage(t *testing.T) {
	w := newWire(t)
	w.answers["ParentChildren"] = map[string]any{
		"viewer": map[string]string{"id": "u1"},
		"issues": map[string]any{"nodes": []any{}, "pageInfo": map[string]bool{"hasNextPage": true}},
	}

	_, err := w.client().ParentChildren(context.Background(), []string{"id1"})

	require.EqualError(t, err, linear.ChildrenOverflow().Error())
}

func TestClientKeepsLeasedTicketsAssignedToTheOwnerWithTheLabelOrCutShortLabels(t *testing.T) {
	w := newWire(t)
	truncated := node("ENG-3", "u1")
	truncated["labels"] = map[string]any{"nodes": []any{}, "pageInfo": map[string]bool{"hasNextPage": true}}
	w.answers["LeasedTickets"] = map[string]any{
		"viewer": map[string]string{"id": "u1"},
		"issues": page(
			node("ENG-1", "u1", "ai"), node("ENG-2", "u1", "other"), truncated,
			node("ENG-4", "u2", "ai"), node("ENG-5", "", "ai"),
		),
	}

	found, err := w.client().LeasedTickets(context.Background(), "ai", []string{"ENG"}, []string{"ENG-1"})

	require.NoError(t, err)
	var got []string
	for _, ticket := range found {
		got = append(got, ticket.Identifier)
	}
	assert.Equal(t, []string{"ENG-1", "ENG-3"}, got)
}

func TestClientDropsManagedTicketsOutsideTheTeams(t *testing.T) {
	w := newWire(t)
	other := node("OTHER-1", "")
	other["team"] = map[string]string{"key": "OTHER"}
	w.answers["ManagedTickets"] = map[string]any{"viewer": map[string]any{"assignedIssues": page(node("ENG-1", ""), other)}}

	found, err := w.client().ManagedTickets(context.Background(), "ai", []string{"ENG"})

	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, "ENG-1", found[0].Identifier)
}

func TestClientSendsTheLabelAndTeamsAndPageSizeForManagedTickets(t *testing.T) {
	w := newWire(t)
	w.answers["ManagedTickets"] = map[string]any{"viewer": map[string]any{"assignedIssues": page()}}

	_, err := w.client().ManagedTickets(context.Background(), "ai", []string{"ENG", "OPS"})

	require.NoError(t, err)
	assert.Equal(t, "ai", w.variables["ManagedTickets"]["label"])
	assert.Equal(t, []any{"ENG", "OPS"}, w.variables["ManagedTickets"]["teams"])
	assert.EqualValues(t, linear.PageSize, w.variables["ManagedTickets"]["first"])
}

func TestClientReadsChildrenMineParentAndBlockers(t *testing.T) {
	w := newWire(t)
	mine, theirs, orphan := node("ENG-2", "u1"), node("ENG-3", "u2"), node("ENG-4", "u1")
	mine["parent"] = map[string]string{"identifier": "ENG-1"}
	theirs["parent"] = map[string]string{"identifier": "ENG-1"}
	mine["createdAt"] = "2026-10-01"
	mine["inverseRelations"] = map[string]any{"nodes": []any{map[string]any{
		"type": "blocks", "issue": map[string]any{"identifier": "ENG-9", "state": map[string]string{"name": "Todo", "type": "unstarted"}},
	}}, "pageInfo": map[string]bool{"hasNextPage": true}}
	w.answers["ParentChildren"] = map[string]any{"viewer": map[string]string{"id": "u1"}, "issues": page(mine, theirs, orphan)}

	children, err := w.client().ParentChildren(context.Background(), []string{"id1"})

	require.NoError(t, err)
	require.Len(t, children, 1)
	require.Len(t, children["ENG-1"], 2)
	assert.True(t, children["ENG-1"][1].Mine)
	assert.False(t, children["ENG-1"][0].Mine)
	assert.Equal(t, "2026-10-01", children["ENG-1"][1].CreatedAt)
	assert.Equal(t, []linear.Blocker{{Identifier: "ENG-9", Status: "Todo"}}, children["ENG-1"][1].Blockers)
	assert.True(t, children["ENG-1"][1].BlockersTruncated)
}

func TestClientMakesNoCallForNoParents(t *testing.T) {
	w := newWire(t)
	client := w.client()

	children, err := client.ParentChildren(context.Background(), nil)

	require.NoError(t, err)
	assert.Empty(t, children)
	assert.Zero(t, client.Usage().Calls)
}

// teamsPastTheFirstPage answers TeamStates like Linear does for a workspace
// of 216 teams whose last one is ARM. Without a team filter only the first
// 50 teams come back, so ARM is found only when the query names it.
func teamsPastTheFirstPage(w *wire) {
	const (
		workspaceTeams = 216
		linearPageSize = 50
	)
	w.respond["TeamStates"] = func(query string, variables map[string]any) any {
		// Sending the variable is not enough, the query has to apply it.
		filter, sent := variables["team"].(string)
		filtered := sent && strings.Contains(query, "teams(first: 1, filter: {key: {eq: $team}})")
		nodes := []any{}
		for i := range workspaceTeams {
			key := fmt.Sprintf("T%d", i)
			if i == workspaceTeams-1 {
				key = "ARM"
			}
			if filtered && key != filter {
				continue
			}
			if !filtered && len(nodes) == linearPageSize {
				break
			}
			nodes = append(nodes, map[string]any{"key": key, "states": map[string]any{"nodes": []any{
				map[string]string{"id": key + "-done", "name": "Done"},
			}}})
		}
		return map[string]any{"teams": map[string]any{"nodes": nodes}}
	}
}

func TestClientFindsAStatusOfATeamPastLinearsFirst50(t *testing.T) {
	w := newWire(t)
	teamsPastTheFirstPage(w)

	id, err := w.client().StateID(context.Background(), "ARM", "Done")

	require.NoError(t, err)
	assert.Equal(t, "ARM-done", id)
}

// teamWithManyStatuses answers TeamStates like Linear does for a team of 60
// statuses. A states connection returns only as many as its first: argument
// asks for, and 50 when it has none. Like Linear it rejects a query priced over
// 10,000 points, which a connection costs at its page size however few it
// returns, and here a rejection shows as no team.
func teamWithManyStatuses(w *wire) {
	const (
		teamStatuses   = 60
		linearPageSize = 50
		maxComplexity  = 10000
	)
	w.respond["TeamStates"] = func(query string, variables map[string]any) any {
		pageSize := func(connection string) int {
			if match := regexp.MustCompile(connection + `\(first: (\d+)`).FindStringSubmatch(query); match != nil {
				size, _ := strconv.Atoi(match[1])
				return size
			}
			return linearPageSize
		}
		size := pageSize("states")
		const statusPoints = 1.2 // an object and two properties
		if float64(pageSize("teams")*size)*statusPoints > maxComplexity {
			return map[string]any{"teams": map[string]any{"nodes": []any{}}}
		}
		nodes := []any{}
		for i := range min(teamStatuses, size) {
			nodes = append(nodes, map[string]string{"id": fmt.Sprintf("S%d", i), "name": fmt.Sprintf("Status %d", i)})
		}
		return map[string]any{"teams": map[string]any{"nodes": []any{
			map[string]any{"key": "BIG", "states": map[string]any{"nodes": nodes}},
		}}}
	}
}

func TestClientFindsAStatusPastATeamsFirst50Statuses(t *testing.T) {
	w := newWire(t)
	teamWithManyStatuses(w)

	id, err := w.client().StateID(context.Background(), "BIG", "Status 55")

	require.NoError(t, err)
	assert.Equal(t, "S55", id)
}

func TestClientReportsAMissingStatusOfATeamWithMoreThan50(t *testing.T) {
	w := newWire(t)
	teamWithManyStatuses(w)

	_, err := w.client().StateID(context.Background(), "BIG", "Nope")

	require.EqualError(t, err, linear.NoStatus("BIG", "Nope").Error())
}

func TestClientReportsAStatusATeamLacks(t *testing.T) {
	w := newWire(t)
	teamsPastTheFirstPage(w)

	_, err := w.client().StateID(context.Background(), "ARM", "Nope")

	require.EqualError(t, err, linear.NoStatus("ARM", "Nope").Error())
}

func TestClientReportsAnUnknownTeam(t *testing.T) {
	w := newWire(t)
	teamsPastTheFirstPage(w)

	_, err := w.client().StateID(context.Background(), "NONE", "Done")

	require.EqualError(t, err, linear.NoTeam("NONE").Error())
}

func TestClientReportsAnUnexpectedHTTPStatus(t *testing.T) {
	w := newWire(t)
	w.status = http.StatusInternalServerError

	_, err := w.client().ManagedTickets(context.Background(), "ai", []string{"ENG"})

	require.ErrorContains(t, err, "linear ManagedTickets: HTTP 500: refused")
	assert.NotErrorIs(t, err, linear.ErrUnauthorized)
}

func TestClientSendsAMissingKeyAsEmptyAndReportsWhatLinearAnswers(t *testing.T) {
	w := newWire(t)
	w.status = http.StatusUnauthorized

	_, err := linear.NewClient(w.server.URL, "").ManagedTickets(context.Background(), "ai", []string{"ENG"})

	require.ErrorIs(t, err, linear.ErrUnauthorized)
	assert.Empty(t, w.auth)
}

func TestClientCountsFailedCallsInItsUsage(t *testing.T) {
	w := newWire(t)
	w.status = http.StatusUnauthorized
	client := w.client()

	_, _ = client.Viewer(context.Background())
	_, _ = client.Viewer(context.Background())

	assert.Equal(t, 2, client.Usage().Calls)
}

func TestClientSendsTheMutationVariables(t *testing.T) {
	w := newWire(t)
	w.answers["MoveTicket"] = map[string]any{"issueUpdate": map[string]bool{"success": true}}
	w.answers["CommentTicket"] = map[string]any{"commentCreate": map[string]bool{"success": true}}

	require.NoError(t, w.client().Move(context.Background(), "t1", "s1"))
	require.NoError(t, w.client().Comment(context.Background(), "t1", "hi"))

	assert.Equal(t, map[string]any{"id": "t1", "state": "s1"}, w.variables["MoveTicket"])
	assert.Equal(t, map[string]any{"id": "t1", "body": "hi"}, w.variables["CommentTicket"])
}

func TestClientAsksForManagedTicketsIgnoringTheCaseOfTheLabel(t *testing.T) {
	w := newWire(t)
	w.answers["ManagedTickets"] = map[string]any{"viewer": map[string]any{"assignedIssues": page()}}

	_, err := w.client().ManagedTickets(context.Background(), "ai", []string{"ENG"})

	require.NoError(t, err)
	assert.Contains(t, w.queries["ManagedTickets"], "name: {eqIgnoreCase: $label}")
}
