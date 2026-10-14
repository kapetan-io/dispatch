package lineartest_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/journal"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/lineartest"
)

const viewerID = "viewer-1"

func issue(n int, status string) lineartest.Issue {
	id := fmt.Sprintf("ENG-%d", n)
	return lineartest.Issue{
		Ticket: linear.Ticket{
			ID: "id-" + id, Identifier: id, Status: status, Team: "ENG", URL: "https://x/" + id,
			Labels: []linear.Label{{Name: "ai"}}, LastComment: "2026-10-01T00:00:00.000Z",
			CreatedAt: "2026-10-01T00:00:00.000Z", Blockers: []linear.Blocker{{Identifier: "ENG-99"}},
			BlockersTruncated: true,
		},
		Assignee: viewerID,
	}
}

func TestManagedTicketsDropFieldsLinearDoesNotSend(t *testing.T) {
	workspace := lineartest.New(&journal.Journal{})
	mine := issue(1, "In Progress")
	mine.Mine = true
	workspace.Seed(mine)

	found, err := workspace.Connect(lineartest.Key).ManagedTickets(context.Background(), "ai", []string{"ENG"})

	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.False(t, found[0].Mine)
	assert.Empty(t, found[0].CreatedAt)
	assert.Equal(t, "2026-10-01T00:00:00.000Z", found[0].LastComment)
	assert.Equal(t, "https://x/ENG-1", found[0].URL)
	assert.Equal(t, []linear.Blocker{{Identifier: "ENG-99"}}, found[0].Blockers)
	assert.True(t, found[0].BlockersTruncated)
}

func TestManagedTicketsIncludeEveryStartedStatus(t *testing.T) {
	workspace := lineartest.New(&journal.Journal{})
	workspace.AddState("QA", "started")
	workspace.Seed(issue(1, "In Progress"), issue(2, "In Review"), issue(3, "Needs Human"), issue(4, "QA"),
		issue(5, "Backlog"), issue(6, "Canceled"), issue(7, "Duplicate"))

	found, err := workspace.Connect(lineartest.Key).ManagedTickets(context.Background(), "ai", []string{"ENG"})

	require.NoError(t, err)
	assert.Len(t, found, 4)
}

func TestManagedTicketsAtExactlyAPageSucceed(t *testing.T) {
	workspace := lineartest.New(&journal.Journal{})
	var issues []lineartest.Issue
	for n := range linear.PageSize {
		issues = append(issues, issue(n+1, "In Progress"))
	}
	workspace.Seed(issues...)

	found, err := workspace.Connect(lineartest.Key).ManagedTickets(context.Background(), "ai", []string{"ENG"})

	require.NoError(t, err)
	assert.Len(t, found, linear.PageSize)
}

func TestManagedTicketsFilterByLabelName(t *testing.T) {
	workspace := lineartest.New(&journal.Journal{})
	workspace.Seed(issue(1, "In Progress"))

	found, err := workspace.Connect(lineartest.Key).ManagedTickets(context.Background(), "other", []string{"ENG"})

	require.NoError(t, err)
	assert.Empty(t, found)
}

func TestSeedRefusesATicketWithNoStatus(t *testing.T) {
	workspace := lineartest.New(&journal.Journal{})

	assert.Panics(t, func() { workspace.Seed(issue(1, "")) })
}

func TestSeedRefusesAStatusTheTeamLacks(t *testing.T) {
	workspace := lineartest.New(&journal.Journal{})

	assert.Panics(t, func() { workspace.Seed(issue(1, "Nope")) })
}

func TestLeasedTicketsLabelRules(t *testing.T) {
	workspace := lineartest.New(&journal.Journal{})
	unlabeled := issue(1, "Done")
	unlabeled.Labels = nil
	truncated := issue(2, "Done")
	truncated.Labels, truncated.LabelsTruncated = nil, true
	stranger := issue(3, "Done")
	stranger.Assignee = "someone-else"
	workspace.Seed(unlabeled, truncated, stranger, issue(4, "Done"))

	found, err := workspace.Connect(lineartest.Key).LeasedTickets(context.Background(), "ai", []string{"ENG"},
		[]string{"ENG-1", "ENG-2", "ENG-3", "ENG-4"})

	require.NoError(t, err)
	var got []string
	for _, ticket := range found {
		got = append(got, ticket.Identifier)
	}
	assert.Equal(t, []string{"ENG-2", "ENG-4"}, got)
}

func TestLeasedTicketsDropFieldsLinearDoesNotSend(t *testing.T) {
	workspace := lineartest.New(&journal.Journal{})
	full := issue(1, "Done")
	full.Mine = true
	workspace.Seed(full)

	found, err := workspace.Connect(lineartest.Key).LeasedTickets(context.Background(), "ai", []string{"ENG"}, []string{"ENG-1"})

	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Empty(t, found[0].LastComment)
	assert.False(t, found[0].Mine)
	assert.Empty(t, found[0].CreatedAt)
	assert.Empty(t, found[0].Blockers)
	assert.False(t, found[0].BlockersTruncated)
	assert.Equal(t, "https://x/ENG-1", found[0].URL)
}

func TestLeasedTicketsOverflowWhenMoreThanAPageIsNamed(t *testing.T) {
	workspace := lineartest.New(&journal.Journal{})
	var issues []lineartest.Issue
	var names []string
	for n := range linear.PageSize + 1 {
		issues = append(issues, issue(n+1, "Done"))
		names = append(names, fmt.Sprintf("ENG-%d", n+1))
	}
	workspace.Seed(issues...)

	_, err := workspace.Connect(lineartest.Key).LeasedTickets(context.Background(), "ai", []string{"ENG"}, names)

	require.EqualError(t, err, linear.LeasedOverflow().Error())
}

func TestLeasedTicketsWithNothingPlaceableMakeNoCall(t *testing.T) {
	workspace := lineartest.New(&journal.Journal{})
	work := workspace.Connect("wrong-key")

	found, err := work.LeasedTickets(context.Background(), "ai", []string{"ENG"}, []string{"OTHER-1", "junk"})

	require.NoError(t, err)
	assert.Empty(t, found)
	assert.Zero(t, workspace.Calls())
	assert.Zero(t, work.Usage().Calls)
}

func TestParentChildrenDropFieldsLinearDoesNotSend(t *testing.T) {
	workspace := lineartest.New(&journal.Journal{})
	parent, child := issue(1, "In Progress"), issue(2, "Todo")
	child.Parent = "ENG-1"
	workspace.Seed(parent, child)

	children, err := workspace.Connect(lineartest.Key).ParentChildren(context.Background(), []string{parent.ID})

	require.NoError(t, err)
	require.Len(t, children["ENG-1"], 1)
	got := children["ENG-1"][0]
	assert.Empty(t, got.URL)
	assert.Empty(t, got.LastComment)
	assert.True(t, got.Mine)
	assert.Equal(t, "2026-10-01T00:00:00.000Z", got.CreatedAt)
	assert.Len(t, got.Blockers, 1)
}

func TestParentChildrenOverflowCountsChildrenAcrossParents(t *testing.T) {
	workspace := lineartest.New(&journal.Journal{})
	issues := []lineartest.Issue{issue(1, "Todo"), issue(2, "Todo")}
	for n := range linear.PageSize + 1 {
		child := issue(n+3, "Todo")
		child.Parent = issues[n%2].Identifier
		issues = append(issues, child)
	}
	workspace.Seed(issues...)
	work := workspace.Connect(lineartest.Key)

	_, err := work.ParentChildren(context.Background(), []string{"id-ENG-1"})
	require.NoError(t, err)

	_, err = work.ParentChildren(context.Background(), []string{"id-ENG-1", "id-ENG-2"})
	require.EqualError(t, err, linear.ChildrenOverflow().Error())
}

func TestParentChildrenRecordsReadsAndSkipsEmptyAsks(t *testing.T) {
	workspace := lineartest.New(&journal.Journal{})
	work := workspace.Connect(lineartest.Key)

	_, err := work.ParentChildren(context.Background(), nil)
	require.NoError(t, err)
	_, err = work.ParentChildren(context.Background(), []string{"a", "b"})
	require.NoError(t, err)

	assert.Equal(t, [][]string{{"a", "b"}}, workspace.Reads())
	assert.Equal(t, 1, workspace.CallsTo(lineartest.OnChildren))
}

func TestFailOnMakesOnlyThatOperationFailUntilCleared(t *testing.T) {
	for _, operation := range []string{lineartest.OnManaged, lineartest.OnLeased, lineartest.OnChildren, lineartest.OnMove, lineartest.OnComment} {
		t.Run(operation, func(t *testing.T) {
			workspace := lineartest.New(&journal.Journal{})
			workspace.Seed(issue(1, "In Progress"), issue(2, "Todo"))
			work := workspace.Connect(lineartest.Key)
			state, err := work.StateID(context.Background(), "ENG", "Done")
			require.NoError(t, err)
			call := map[string]func() error{
				lineartest.OnManaged: func() error {
					_, err := work.ManagedTickets(context.Background(), "ai", []string{"ENG"})
					return err
				},
				lineartest.OnLeased: func() error {
					_, err := work.LeasedTickets(context.Background(), "ai", []string{"ENG"}, []string{"ENG-1"})
					return err
				},
				lineartest.OnChildren: func() error {
					_, err := work.ParentChildren(context.Background(), []string{"id-ENG-1"})
					return err
				},
				lineartest.OnMove:    func() error { return work.Move(context.Background(), "id-ENG-1", state) },
				lineartest.OnComment: func() error { return work.Comment(context.Background(), "id-ENG-1", "hi") },
			}

			workspace.FailOn(operation, true)
			for name, fn := range call {
				if name == operation {
					require.Error(t, fn())
				} else {
					require.NoError(t, fn())
				}
			}

			workspace.FailOn(operation, false)
			require.NoError(t, call[operation]())
		})
	}
}

func TestAFailedMutationLeavesTheWorkspaceUnchanged(t *testing.T) {
	workspace := lineartest.New(&journal.Journal{})
	workspace.Seed(issue(1, "In Progress"))
	work := workspace.Connect(lineartest.Key)
	state, err := work.StateID(context.Background(), "ENG", "Done")
	require.NoError(t, err)

	workspace.FailOn(lineartest.OnMove, true)
	require.Error(t, work.Move(context.Background(), "id-ENG-1", state))
	workspace.FailOn(lineartest.OnComment, true)
	require.Error(t, work.Comment(context.Background(), "id-ENG-1", "hi"))

	assert.Empty(t, workspace.Changes())
	found, err := work.ManagedTickets(context.Background(), "ai", []string{"ENG"})
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, "In Progress", found[0].Status)
}

func TestUnsuccessfulMoveReportsSuccessFalseAndChangesNothing(t *testing.T) {
	workspace := lineartest.New(&journal.Journal{})
	workspace.Seed(issue(1, "In Progress"))
	work := workspace.Connect(lineartest.Key)
	state, err := work.StateID(context.Background(), "ENG", "Done")
	require.NoError(t, err)
	workspace.FailOn(lineartest.OnUnsuccessfulMove, true)

	err = work.Move(context.Background(), "id-ENG-1", state)

	require.EqualError(t, err, linear.Unsuccessful("MoveTicket", "id-ENG-1").Error())
	assert.Empty(t, workspace.Changes())
}

func TestMoveAndCommentRecordChangesAndJournal(t *testing.T) {
	j := &journal.Journal{}
	workspace := lineartest.New(j)
	workspace.Now = func() time.Time { return time.Date(2026, 10, 9, 1, 2, 3, 0, time.UTC) }
	workspace.Seed(issue(1, "In Progress"))
	work := workspace.Connect(lineartest.Key)

	require.NoError(t, work.Move(context.Background(), "id-ENG-1", lineartest.StateID("In Review")))
	require.NoError(t, work.Comment(context.Background(), "id-ENG-1", "hello"))

	assert.Equal(t, []lineartest.Change{
		{Kind: lineartest.Move, TicketID: "id-ENG-1", StateID: "state-in-review"},
		{Kind: lineartest.Comment, TicketID: "id-ENG-1", Body: "hello"},
	}, workspace.Changes())
	assert.Equal(t, "linear", j.Calls()[0].Tool)
	assert.Equal(t, "move", j.Calls()[0].Name)
	assert.Equal(t, "comment", j.Calls()[1].Name)
	found, err := work.ManagedTickets(context.Background(), "ai", []string{"ENG"})
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, "In Review", found[0].Status)
	assert.Equal(t, "2026-10-09T01:02:03.000Z", found[0].LastComment)
}

func TestUsageCountsEachSessionApartAndIgnoresARejectedKey(t *testing.T) {
	workspace := lineartest.New(&journal.Journal{})
	good, bad := workspace.Connect(lineartest.Key), workspace.Connect("lin_api_rejected")

	_, err := good.Viewer(context.Background())
	require.NoError(t, err)
	_, err = good.ManagedTickets(context.Background(), "ai", []string{"ENG"})
	require.NoError(t, err)
	_, err = bad.Viewer(context.Background())
	require.ErrorIs(t, err, linear.ErrUnauthorized)

	assert.Equal(t, linear.Stats{Calls: 2, RequestsRemaining: lineartest.RequestsRemaining, RequestsLimit: lineartest.RequestsLimit}, good.Usage())
	assert.Zero(t, bad.Usage().Calls)
	assert.Equal(t, 2, workspace.Calls())
	assert.Equal(t, 1, workspace.CallsTo(lineartest.OnManaged))
}

func TestARejectedKeyIsUnauthorizedOnEveryOperation(t *testing.T) {
	workspace := lineartest.New(&journal.Journal{})
	workspace.Seed(issue(1, "In Progress"))
	bad := workspace.Connect("lin_api_rejected")
	ctx := context.Background()

	_, err := bad.ManagedTickets(ctx, "ai", []string{"ENG"})
	assert.ErrorIs(t, err, linear.ErrUnauthorized)
	_, err = bad.LeasedTickets(ctx, "ai", []string{"ENG"}, []string{"ENG-1"})
	assert.ErrorIs(t, err, linear.ErrUnauthorized)
	_, err = bad.ParentChildren(ctx, []string{"id-ENG-1"})
	assert.ErrorIs(t, err, linear.ErrUnauthorized)
	_, err = bad.StateID(ctx, "ENG", "Done")
	assert.ErrorIs(t, err, linear.ErrUnauthorized)
	assert.ErrorIs(t, bad.Move(ctx, "id-ENG-1", lineartest.StateID("Done")), linear.ErrUnauthorized)
	assert.ErrorIs(t, bad.Comment(ctx, "id-ENG-1", "x"), linear.ErrUnauthorized)
	assert.Empty(t, workspace.Changes())
}

func TestMoveRefusesAMissingTicketAndAnUnknownState(t *testing.T) {
	workspace := lineartest.New(&journal.Journal{})
	workspace.Seed(issue(1, "In Progress"))
	work := workspace.Connect(lineartest.Key)

	assert.ErrorContains(t, work.Move(context.Background(), "nope", lineartest.StateID("Done")), "Entity not found: Issue")
	assert.ErrorContains(t, work.Move(context.Background(), "id-ENG-1", "state-nowhere"), "Entity not found: WorkflowState")
	assert.Empty(t, workspace.Changes())
}

func TestStateIDRoundTripsAddedStatuses(t *testing.T) {
	workspace := lineartest.New(&journal.Journal{})
	workspace.AddState("Ready For QA", "started")

	id, err := workspace.Connect(lineartest.Key).StateID(context.Background(), "ENG", "Ready For QA")

	require.NoError(t, err)
	assert.Equal(t, "state-ready-for-qa", id)
}

func TestUnsuccessfulCommentReportsSuccessFalseAndChangesNothing(t *testing.T) {
	workspace := lineartest.New(&journal.Journal{})
	workspace.Seed(issue(1, "In Progress"))
	work := workspace.Connect(lineartest.Key)
	workspace.FailOn(lineartest.OnUnsuccessfulComment, true)

	err := work.Comment(context.Background(), "id-ENG-1", "hi")

	require.EqualError(t, err, linear.Unsuccessful("CommentTicket", "id-ENG-1").Error())
	assert.Empty(t, workspace.Changes())
}

func TestATicketWithAChildHasChildren(t *testing.T) {
	workspace := lineartest.New(&journal.Journal{})
	parent, child := issue(1, "In Progress"), issue(2, "In Progress")
	child.Parent = parent.Identifier
	workspace.Seed(parent, child)

	found, err := workspace.Connect(lineartest.Key).ManagedTickets(context.Background(), "ai", []string{"ENG"})

	require.NoError(t, err)
	require.Len(t, found, 2)
	assert.True(t, found[0].HasChildren)
	assert.False(t, found[1].HasChildren)
}
