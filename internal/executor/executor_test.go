package executor_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/executor"
	"github.com/kapetan-io/dispatch/internal/journal"
	"github.com/kapetan-io/dispatch/internal/ledger"
	"github.com/kapetan-io/dispatch/internal/localticket"
	"github.com/kapetan-io/dispatch/internal/pullrequesttest"
	"github.com/kapetan-io/dispatch/internal/rules"
	"github.com/kapetan-io/dispatch/internal/ticket"
	"github.com/kapetan-io/dispatch/internal/world"
)

// cycle is a local ticket In Review as one cycle read it, with the executor over the real store.
func cycle(t *testing.T) (*executor.Executor, *localticket.Store, *pullrequesttest.Repository, world.World) {
	t.Helper()
	store, err := localticket.Open(localticket.Options{
		Path: filepath.Join(t.TempDir(), "tickets.db"), Repos: []string{"api"}, Now: time.Now, LockTimeout: time.Second,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	_, err = store.Create(localticket.Draft{Title: "work", Repo: "api", Status: "In Review"})
	require.NoError(t, err)
	tracked, err := store.Tracked(nil, func(ticket.Ticket) bool { return false })
	require.NoError(t, err)
	prs := pullrequesttest.New(&journal.Journal{})
	exec := executor.New(executor.Env{Systems: executor.Systems{Local: store}, PullRequests: prs})
	return exec, store, prs, world.World{Tickets: tracked.Tickets}
}

func TestRunFailsAMoveOfALocalTicketThatMovedSinceTheCycleRead(t *testing.T) {
	exec, store, _, seen := cycle(t)
	_, err := store.Move("LOC-1", "Todo")
	require.NoError(t, err)

	res := exec.Run(context.Background(), rules.Want{Receipt: "move", Subject: "LOC-1", Args: map[string]any{"status": "needs_human", "why": "x"}}, seen)

	assert.Equal(t, ledger.Failed, res.Outcome)
	assert.Equal(t, "LOC-1 is Todo now, not the In Review the cycle saw", res.Detail)
	detail, err := store.Show("LOC-1")
	require.NoError(t, err)
	assert.Equal(t, "Todo", detail.Status)
}

func TestRunFailsAnEscalationOfALocalTicketThatMovedSinceTheCycleReadAndWritesNoComment(t *testing.T) {
	exec, store, _, seen := cycle(t)
	_, err := store.Move("LOC-1", "Todo")
	require.NoError(t, err)

	res := exec.Run(context.Background(), rules.Want{Receipt: "escalate", Subject: "LOC-1", Args: map[string]any{"why": "stuck"}}, seen)

	assert.Equal(t, ledger.Failed, res.Outcome)
	assert.Equal(t, "LOC-1 is Todo now, not the In Review the cycle saw", res.Detail)
	assert.Equal(t, ledger.Failed, res.Steps[0].Outcome)
	detail, err := store.Show("LOC-1")
	require.NoError(t, err)
	assert.Equal(t, "Todo", detail.Status)
	assert.Empty(t, detail.Comments)
}

func TestRunEscalatesALocalTicketWithOneCommentByDispatch(t *testing.T) {
	exec, store, _, seen := cycle(t)

	res := exec.Run(context.Background(), rules.Want{Receipt: "escalate", Subject: "LOC-1", Args: map[string]any{"why": "stuck"}}, seen)

	assert.Equal(t, ledger.Done, res.Outcome)
	detail, err := store.Show("LOC-1")
	require.NoError(t, err)
	assert.Equal(t, "Needs Human", detail.Status)
	require.Len(t, detail.Comments, 1)
	assert.Equal(t, "dispatch", detail.Comments[0].Author)
	assert.Equal(t, "The dispatcher moved this ticket to Needs Human because stuck", detail.Comments[0].Body)
}

func TestRunFailsTheMergeOfALocalTicketThatGotACommentSinceTheCycleRead(t *testing.T) {
	exec, store, prs, seen := cycle(t)
	require.NoError(t, store.Comment("LOC-1", "you", "one more thing"))

	res := exec.Run(context.Background(), rules.Want{
		Receipt: "merge", Subject: "LOC-1", Target: map[string]string{"url": "https://github.com/acme/api/pull/7", "head": "a1"},
	}, seen)

	assert.Equal(t, ledger.Failed, res.Outcome)
	assert.Equal(t, "LOC-1 changed since the cycle, so the merge waits for a new decision", res.Detail)
	assert.Equal(t, world.FromLocalTickets, res.Reread)
	assert.Empty(t, prs.Merged())
}
