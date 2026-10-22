package dispatch_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/herdr"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
)

// noLinearChanges requires that dispatch moved no ticket and posted no comment.
func noLinearChanges(t *testing.T, d *Dispatch) {
	t.Helper()
	assert.Empty(t, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
}

// TickAfterASecond moves the clock a second, so each tick starts a new idle stretch and a nudge sent
// by the one before it counts as answered, and runs a tick.
func (d *Dispatch) TickAfterASecond() error {
	d.t.Helper()
	d.Advance(time.Second)
	return d.Run("tick").Err
}

// PullRequestFailsAfterEntry starts the loop with ENG-1 In Review and its PR quiet, then changes the
// PR to p. A PR that fails after the ticket was seen In Review is what the nudge count measures from,
// and a nudge in the same instant as that first sight is not counted.
func (d *Dispatch) PullRequestFailsAfterEntry(p prState) {
	d.t.Helper()
	d.GitHub.PullRequest("ENG-1", prState{head: p.head, checks: "PENDING", mergeable: "MERGEABLE"})
	d.Restart()
	d.Advance(time.Second)
	d.GitHub.PullRequest("ENG-1", p)
}

// Attempts lists every receipt trace prints for the ticket, one for each attempt and each outcome,
// where Receipts keeps one for each action.
func (d *Dispatch) Attempts(ticket string) []Receipt {
	d.t.Helper()
	res := d.Run("trace", ticket, "--json")
	require.NoError(d.t, res.Err)
	var found []Receipt
	for _, text := range strings.Split(res.Stdout, "\n") {
		var line struct {
			Type, Kind, Subject, State, Once, Detail string
		}
		if text == "" || json.Unmarshal([]byte(text), &line) != nil || line.Type != "receipt" {
			continue
		}
		found = append(found, Receipt{Kind: line.Kind, Subject: line.Subject, State: line.State, Once: line.Once, Detail: line.Detail})
	}
	return found
}

// attemptsOf lists every attempt and outcome of one kind that trace prints for the ticket.
func (d *Dispatch) attemptsOf(ticket, kind string) []Receipt {
	d.t.Helper()
	var found []Receipt
	for _, receipt := range d.Attempts(ticket) {
		if receipt.Kind == kind {
			found = append(found, receipt)
		}
	}
	return found
}

// states is the state of each receipt, in order.
func states(receipts []Receipt) []string {
	var out []string
	for _, receipt := range receipts {
		out = append(out, receipt.State)
	}
	return out
}

// withFailingPR puts ENG-1 In Review with an idle worker and a PR whose check failed.
func withFailingPR(d *Dispatch) {
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
}

// BlockAfterSnapshot has the worker stop to ask a question after dispatch read herdr, so herdr refuses
// a prompt that the snapshot said was fine.
func (w *Workers) BlockAfterSnapshot(ticket string) { w.d.blockAfterSnapshot(ticket) }

// VanishAfterSnapshot closes the worker's pane after dispatch read herdr, so herdr no longer knows a pane
// the snapshot lists.
func (w *Workers) VanishAfterSnapshot(ticket string) { w.d.vanishAfterSnapshot(ticket) }

// Unblock undoes BlockAfterSnapshot.
func (w *Workers) Unblock(ticket string) { w.d.unblock(ticket) }

// Gone closes the ticket's herdr workspace, which leaves its tree leased and no worker for dispatch to see.
func (w *Workers) Gone(ticket string) {
	require.NoError(w.d.t, w.d.tools.Herdr.CloseWorkspace(context.Background(), "w-"+ticket))
}

// StallPrompts makes herdr take a prompt and never answer.
func (w *Workers) StallPrompts() { w.d.tools.Herdr.StallPrompts() }

// CheckedOut is Running on a tree that exists as a git worktree, as a worker's tree does when dispatch types into it.
func (w *Workers) CheckedOut(ticket, status string) string {
	w.d.t.Helper()
	tree := w.d.worktreeOf(ticket)
	w.Running(ticket, status)
	return tree
}

// Tree is the path of the tree Running leases for a ticket.
func (w *Workers) Tree(ticket string) string { return filepath.Join(w.d.dir, "trees", ticket) }

// Prompt has the person at the Claude session in tree submit a prompt, and returns once the loop has
// taken what the hook reported.
func (w *Workers) Prompt(tree, session, prompt string) {
	w.d.t.Helper()
	input, err := json.Marshal(map[string]string{"hook_event_name": "UserPromptSubmit", "session_id": session, "cwd": tree, "prompt": prompt})
	require.NoError(w.d.t, err)
	require.NoError(w.d.t, w.d.HookAndWait("wake", string(input)).Err)
}

// Type has the person of the ticket's worker type a prompt into the "worker" session.
func (w *Workers) Type(ticket, prompt string) {
	w.d.t.Helper()
	w.Prompt(w.d.worktreeOf(ticket), "worker", prompt)
}

// Kicked lists the kickoffs typed into new workers, as "<pane>|<text>".
func (w *Workers) Kicked() []string { return w.d.tools.Herdr.Kicked() }

// SetPaneSession changes the Claude session id herdr reports for a pane.
func (w *Workers) SetPaneSession(pane, session string) {
	w.d.tools.Herdr.SetSession(pane, session)
}

// FailKickoff makes herdr report that it never confirmed a kickoff.
func (w *Workers) FailKickoff() {
	w.d.tools.Herdr.Fail(herdrtest.OpKickoff, herdr.ErrKickoffUnconfirmed)
}

// LaunchResult sets what the fake launcher answers to a launch, as the launcher's JSON.
func (w *Workers) LaunchResult(result string) { w.d.launchResult(result) }

// Pool makes the dispatch repo a git repo that treehouse pools, with one tree in it, and returns the
// tree's path.
func (t *Trees) Pool() string { return t.d.pool() }

// Fail makes the notifier fail with err until it is called with nil.
func (a *Alerts) Fail(err error) { a.d.tools.Alerts.Fail(err) }

// FailMoves makes Linear refuse every status change.
func (l *Linear) FailMoves() { l.d.linear.failMoves() }

// Merged lists the pull requests that merged, as "<url>|<head>".
func (g *GitHub) Merged() []string { return g.d.merges() }

// Close closes the ticket's PR without merging it.
func (g *GitHub) Close(ticket string) { g.d.closePR(ticket) }

// PushAfterRead has someone push head to the ticket's branch right after dispatch reads the PR.
func (g *GitHub) PushAfterRead(ticket, head string) {
	g.d.tools.PullRequests.PushAfterRead(prBranch(ticket), head)
}

// Head is the commit the ticket's PR points at.
func (g *GitHub) Head(ticket string) string { return g.d.tools.PullRequests.Head(prBranch(ticket)) }

// FailRead makes GitHub fail every read of a pull request with err.
func (g *GitHub) FailRead(err error) { g.d.tools.PullRequests.FailRead(err) }

// FailMerge makes GitHub refuse every merge with err.
func (g *GitHub) FailMerge(err error) { g.d.tools.PullRequests.FailMerge(err) }

// RespondOlder sets what GitHub answers for the page of the PR's reviews or comments that ends before
// the cursor. kind is "reviews" or "comments".
func (g *GitHub) RespondOlder(kind, before string, items []activity, earlier string) {
	g.d.respondOlder(kind, before, items, earlier)
}

// OlderReads lists the reads of an older page of reviews or comments.
func (g *GitHub) OlderReads() []string { return g.d.tools.Calls("pullrequest", "older") }

func TestTickNudgesAnIdleWorkerWithoutCommenting(t *testing.T) {
	for _, status := range []string{"idle", "done"} {
		t.Run(status, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", status)
			d.PullRequestFailsAfterEntry(prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

			require.NoError(t, d.TickAfterASecond())

			assert.Equal(t, []string{prPrompt("a failed check")}, d.Workers.Prompts())
			noLinearChanges(t, d)
			assert.Empty(t, d.Workers.Launched())
		})
	}
}

func TestTickLeavesWorkingAndDesignWorkersAlone(t *testing.T) {
	for _, test := range []struct {
		name   string
		ticket fakeTicket
		status string
	}{
		{name: "Working", ticket: ticket("ENG-1", "In Progress", "dispatch"), status: "working"},
		{name: "Unknown", ticket: ticket("ENG-1", "In Progress", "dispatch"), status: "unknown"},
		{name: "DesignTicket", ticket: withLabel(ticket("ENG-1", "In Progress", "dispatch"), "design"), status: "idle"},
		{name: "ReviewTicket", ticket: withLabel(ticket("ENG-1", "In Progress", "dispatch"), "design-review"), status: "idle"},
		{name: "InReview", ticket: ticket("ENG-1", "In Review", "dispatch"), status: "idle"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(test.ticket)
			d.Workers.Running("ENG-1", test.status)

			res := d.Run("tick")

			require.NoError(t, res.Err)
			assert.Empty(t, res.Stdout)
			assert.Empty(t, d.Workers.Prompts())
			assert.Empty(t, d.Alerts.Shown())
			noLinearChanges(t, d)
		})
	}
}

func TestTickDoesNotCountANudgeHerdrRefused(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.PullRequestFailsAfterEntry(prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	d.Workers.BlockAfterSnapshot("ENG-1")

	for range 4 {
		err := d.TickAfterASecond()
		require.ErrorContains(t, err, "ENG-1 nudge failed: blocked")
	}

	assert.Empty(t, d.Workers.Prompts())
	noLinearChanges(t, d)
	// Each tick retries the nudge, and every attempt is its own receipt.
	var failed int
	for _, receipt := range d.Attempts("ENG-1") {
		assert.Equal(t, "nudge", receipt.Kind)
		assert.Contains(t, []string{"attempted", "failed"}, receipt.State)
		if receipt.State == "failed" {
			assert.Equal(t, "blocked", receipt.Detail)
			failed++
		}
	}
	assert.Equal(t, 4, failed)
}

func TestTickSendsTheResumeNudgeWhenAnswerMovesTicketBack(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "working")
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	d.Tick()
	// The nudge has to come after dispatch first saw the worker, as it does outside a test.
	d.Advance(time.Second)
	d.Workers.SetStatus("ENG-1", "idle")
	d.Tick()
	d.Advance(time.Minute)
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Tick()
	d.Advance(time.Minute)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	// A worker gets no nudge of any kind until it stops after the last one.
	d.Tick()
	assert.Equal(t, []string{prPrompt("a failed check")}, d.Workers.Prompts())

	d.Advance(time.Minute)
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Tick()

	assert.Equal(t, []string{prPrompt("a failed check"), "w-ENG-1:p1|" + resumeText1}, d.Workers.Prompts())
	noLinearChanges(t, d)
}

func TestTickNotifiesOnceWhenATicketEntersNeedsHuman(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "working")
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	assert.Empty(t, d.Alerts.Shown())

	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	for range 3 {
		d.Tick()
	}

	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1"}, d.Alerts.Shown())
	assert.Empty(t, d.Workers.Prompts())
	noLinearChanges(t, d)
}

func TestTickNotifiesWhenADispatcherEscalationMovesATicket(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", ""))

	d.Tick()
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", ""))
	d.Tick()

	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1"}, d.Alerts.Shown())
}

func TestTickRetriesANeedsHumanNotificationThatFailed(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "working")
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Alerts.Fail(errors.New("exit status 1"))

	require.ErrorContains(t, d.Run("tick").Err, "ENG-1 notify failed")
	assert.Empty(t, d.Alerts.Shown())

	d.Alerts.Fail(nil)
	d.Tick()
	d.Tick()

	assert.Len(t, d.Alerts.Shown(), 1)
}

func TestTickNotifiesAgainWhileAWorkerStaysBlocked(t *testing.T) {
	for _, status := range []string{"In Progress", "In Review"} {
		t.Run(status, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", status, "dispatch"))
			d.Workers.Running("ENG-1", "blocked")
			const want = "ENG-1 is waiting on your answer in its herdr workspace"

			var counts []int
			for i := range 7 {
				if i > 0 {
					d.Advance(5 * time.Minute)
				}
				d.Tick()
				counts = append(counts, len(d.Alerts.Shown()))
			}

			// The first notice, then one every 15 minutes.
			assert.Equal(t, []int{1, 1, 1, 2, 2, 2, 3}, counts)
			assert.Equal(t, want, d.Alerts.Shown()[0])
			assert.Empty(t, d.Workers.Prompts())
			noLinearChanges(t, d)
		})
	}
}

func TestTickNotifiesImmediatelyWhenAWorkerBlocksAgain(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "blocked")
	d.Tick()

	d.Workers.SetStatus("ENG-1", "working")
	d.Advance(time.Minute)
	d.Tick()
	d.Workers.SetStatus("ENG-1", "blocked")
	d.Advance(time.Minute)
	d.Tick()

	assert.Len(t, d.Alerts.Shown(), 2)
}

func TestTellTypesTextIntoAWorker(t *testing.T) {
	for _, status := range []string{"idle", "working"} {
		t.Run(status, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Workers.Running("ENG-1", status)

			res := d.Run("tell", "ENG-1", "use the v2 endpoint")

			require.NoError(t, res.Err)
			assert.Equal(t, "told ENG-1\n", res.Stdout)
			assert.Equal(t, []string{"w-ENG-1:p1|use the v2 endpoint"}, d.Workers.Prompts())
		})
	}
}

func TestTellFails(t *testing.T) {
	for _, test := range []struct {
		name    string
		worker  string
		args    []string
		wantErr string
	}{
		{name: "WorkerBlocked", worker: "blocked", args: []string{"tell", "ENG-1", "hi"}, wantErr: "ENG-1's worker is blocked on a question"},
		{name: "NoWorker", worker: "", args: []string{"tell", "ENG-2", "hi"}, wantErr: "ENG-2 has no worker"},
		{name: "WorkspaceWithoutClaude", worker: "none", args: []string{"tell", "ENG-1", "hi"}, wantErr: "ENG-1 has no worker"},
		{name: "MissingText", worker: "idle", args: []string{"tell", "ENG-1"}, wantErr: "tell takes a ticket and the text to type"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			switch test.worker {
			case "none":
				d.Workers.Running("ENG-1", "")
			case "":
			default:
				d.Workers.Running("ENG-1", test.worker)
			}

			require.ErrorContains(t, d.Run(test.args...).Err, test.wantErr)
			assert.Empty(t, d.Workers.Prompts())
		})
	}
}

func TestTellReportsAWorkerHerdrRefuses(t *testing.T) {
	for _, status := range []string{"idle", "working"} {
		t.Run(status, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Workers.Running("ENG-1", status)
			d.Workers.BlockAfterSnapshot("ENG-1")

			require.ErrorContains(t, d.Run("tell", "ENG-1", "hi").Err, "ENG-1's worker is blocked on a question")
			assert.Empty(t, d.Workers.Prompts())
		})
	}
}

func TestTellReportsAPaneHerdrNoLongerKnows(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "idle")
	d.Workers.VanishAfterSnapshot("ENG-1")

	res := d.Run("tell", "ENG-1", "hi")

	require.ErrorContains(t, res.Err, "agent target w-ENG-1:p1 not found")
	assert.NotContains(t, res.Err.Error(), "blocked on a question")
	assert.Empty(t, d.Workers.Prompts())
}

func TestTellNamesAPoolItCouldNotRead(t *testing.T) {
	d := newDispatch(t, dispatchOptions{
		Config: fmt.Sprintf("\n[repos.tiger]\npath = %q\n", filepath.Join(t.TempDir(), "moved")),
	})

	require.ErrorContains(t, d.Run("tell", "ENG-1", "hi").Err, "ENG-1 has no worker; could not read the pool of tiger")
}

func TestTickSendsOnlyOneResumeNudge(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "working")
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Tick()
	d.Advance(time.Second)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.SetStatus("ENG-1", "idle")

	d.Tick()
	assert.Equal(t, []string{"w-ENG-1:p1|" + resumeText1}, d.Workers.Prompts())
	d.Tick()

	assert.Equal(t, []string{"w-ENG-1:p1|" + resumeText1}, d.Workers.Prompts())
}

func TestTickNotifiesForNeedsHumanWhateverTheWorkerIs(t *testing.T) {
	for _, test := range []struct {
		name   string
		worker string
		none   bool
	}{
		{name: "NoLease", none: true},
		{name: "NoClaudeAgent", worker: ""},
		{name: "Blocked", worker: "blocked"},
		{name: "Idle", worker: "idle"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
			if !test.none {
				d.Workers.Running("ENG-1", test.worker)
			}

			for range 3 {
				d.Tick()
			}

			assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1"}, d.Alerts.Shown())
			assert.Empty(t, d.Workers.Prompts())
			assert.Empty(t, d.Workers.Launched())
			noLinearChanges(t, d)
		})
	}
}

func TestTickNotifiesEachTimeATicketReentersNeedsHuman(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "working")

	for _, status := range []string{"Needs Human", "In Progress", "Needs Human", "Needs Human"} {
		d.Linear.Tickets(ticket("ENG-1", status, "dispatch"))
		d.Tick()
		d.Advance(time.Minute)
	}

	assert.Len(t, d.Alerts.Shown(), 2)
}

func TestTickNotifiesAfterAnEscalationWhoseAlertFailed(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	d.Tick()
	// Nudges count from the ticket entering In Review, so the worker idles a moment after it.
	d.Advance(time.Second)
	d.Workers.SetStatus("ENG-1", "idle")
	d.Tick()
	d.Advance(5 * time.Minute)
	d.Alerts.Fail(errors.New("exit status 1"))

	// Two nudges went out, and the next pass finds the worker still ignoring them.
	d.Advance(5 * time.Minute)
	require.ErrorContains(t, d.Run("tick").Err, "notify failed")
	assert.Empty(t, d.Alerts.Shown())
	assert.Len(t, d.Linear.Comments(), 1)
	assert.Len(t, d.Linear.Moves(), 1)

	d.Alerts.Fail(nil)
	d.Tick()
	d.Tick()

	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1"}, d.Alerts.Shown())
	assert.Len(t, d.Linear.Comments(), 1)
	assert.Len(t, d.Linear.Moves(), 1)
}

func TestTickDoesNotNotifyForAnEscalationLinearRefused(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	d.Tick()
	d.Advance(time.Second)
	d.Workers.SetStatus("ENG-1", "idle")
	d.Tick()
	d.Advance(5 * time.Minute)
	d.Linear.FailMoves()
	d.Advance(5 * time.Minute)

	require.ErrorContains(t, d.Run("tick").Err, "ENG-1 escalate failed")
	assert.Empty(t, d.Alerts.Shown())
	noLinearChanges(t, d)
}

func TestTickRetriesABlockedNotificationThatFailed(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "blocked")
	d.Alerts.Fail(errors.New("exit status 1"))

	require.ErrorContains(t, d.Run("tick").Err, "ENG-1 notify failed")
	assert.Empty(t, d.Alerts.Shown())

	d.Alerts.Fail(nil)
	d.Tick()

	assert.Len(t, d.Alerts.Shown(), 1)
}

func TestTickTracksBlockedTicksPerTicket(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "blocked")
	d.Workers.Running("ENG-2", "working")
	const (
		first  = "ENG-1 is waiting on your answer in its herdr workspace"
		second = "ENG-2 is waiting on your answer in its herdr workspace"
	)

	d.Tick()
	d.Workers.SetStatus("ENG-2", "blocked")
	for range 4 {
		d.Advance(5 * time.Minute)
		d.Tick()
	}

	// ENG-1 at minutes 0 and 15, ENG-2 at minutes 5 and 20.
	assert.Equal(t, []string{first, second, first, second}, d.Alerts.Shown())
}

func TestTickNudgesAfterABlockedWorkerGoesIdle(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "blocked")
	d.PullRequestFailsAfterEntry(prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	for range 4 {
		d.Tick()
	}
	d.Workers.SetStatus("ENG-1", "idle")

	require.NoError(t, d.TickAfterASecond())

	assert.Equal(t, []string{prPrompt("a failed check")}, d.Workers.Prompts())
	noLinearChanges(t, d)
}

// Unreachable from the CLI: no command deletes the history directory.
func TestTickNudgesAgainWhenTheHistoryIsDeleted(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.PullRequestFailsAfterEntry(prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	require.NoError(t, d.TickAfterASecond())
	d.Stop()
	d.LoseHistory()
	d.Advance(5 * time.Minute)

	d.Tick()

	assert.Equal(t, []string{prPrompt("a failed check"), prPrompt("a failed check")}, d.Workers.Prompts())
	noLinearChanges(t, d)
}

func TestTickNudgesEachIdleTicketIndependently(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"), ticket("ENG-2", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Running("ENG-2", "working")
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "PENDING", mergeable: "MERGEABLE"})
	d.GitHub.PullRequest("ENG-2", prState{head: "a1", checks: "PENDING", mergeable: "MERGEABLE"})
	d.Restart()
	d.Advance(time.Second)
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	d.GitHub.PullRequest("ENG-2", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	d.Tick()
	d.Tick()
	d.Workers.SetStatus("ENG-2", "idle")

	require.NoError(t, d.TickAfterASecond())

	assert.Equal(t, []string{prPrompt("a failed check"), strings.ReplaceAll(prPrompt("a failed check"), "ENG-1", "ENG-2")}, d.Workers.Prompts())
	noLinearChanges(t, d)
}

func TestTellTypesTheTextVerbatim(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "idle")

	require.NoError(t, d.Run("tell", "ENG-1", `fix "it" && echo $HOME`).Err)

	assert.Equal(t, []string{`w-ENG-1:p1|fix "it" && echo $HOME`}, d.Workers.Prompts())
}

func TestTellRejectsExtraArguments(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "idle")

	require.ErrorContains(t, d.Run("tell", "ENG-1", "use", "v2").Err, "tell takes a ticket and the text to type")
	assert.Empty(t, d.Workers.Prompts())
}

func TestTickRetriesAResumeNudgeHerdrRefused(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Tick()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.BlockAfterSnapshot("ENG-1")
	require.ErrorContains(t, d.Run("tick").Err, "ENG-1 nudge failed: blocked")
	d.Workers.Unblock("ENG-1")

	d.Tick()

	assert.Equal(t, []string{"w-ENG-1:p1|" + resumeText1}, d.Workers.Prompts())
}

func TestTickSendsTheResumeNudgeToAWorkerThatWasBlockedWhenTheTicketMovedBack(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "working")
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Tick()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.SetStatus("ENG-1", "blocked")
	d.Tick()
	assert.Empty(t, d.Workers.Prompts())
	d.Workers.SetStatus("ENG-1", "idle")

	d.Tick()

	assert.Equal(t, []string{"w-ENG-1:p1|" + resumeText1}, d.Workers.Prompts())
}

func TestTickSendsTheResumeNudgeAfterANeedsHumanNotificationFailed(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.PullRequestFailsAfterEntry(prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	require.NoError(t, d.TickAfterASecond())
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Alerts.Fail(errors.New("exit status 1"))
	require.ErrorContains(t, d.Run("tick").Err, "ENG-1 notify failed")
	d.Alerts.Fail(nil)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	// The PR nudge went first, so the worker has to stop before it hears the resume.
	d.Advance(time.Minute)
	d.Workers.Stop("ENG-1", stopTasks{})
	d.Tick()

	assert.Equal(t, []string{prPrompt("a failed check"), "w-ENG-1:p1|" + resumeText1}, d.Workers.Prompts())
}

func TestTickNotifiesWhenAReviewWorkerIsBlockedOnAQuestion(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "blocked")
	d.Linear.Tickets(withLabel(ticket("ENG-1", "In Progress", "dispatch"), "design-review"))

	d.Tick()

	assert.Equal(t, []string{"ENG-1 is waiting on your answer in its herdr workspace"}, d.Alerts.Shown())
}

func TestTickNotifiesWhenAReviewTicketThatAlsoCarriesDesignIsBlocked(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "blocked")
	d.Linear.Tickets(withLabel(designTicket("ENG-1", "In Progress"), "design-review"))

	d.Tick()

	assert.Equal(t, []string{"ENG-1 is waiting on your answer in its herdr workspace"}, d.Alerts.Shown())
}

func TestTickNotifiesWhenAReviewTicketEntersNeedsHuman(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "working")
	d.Linear.Tickets(withLabel(ticket("ENG-1", "In Progress", "dispatch"), "design-review"))
	d.Tick()

	d.Linear.Tickets(withLabel(ticket("ENG-1", "Needs Human", "dispatch"), "design-review"))
	for range 3 {
		d.Tick()
	}

	assert.Equal(t, []string{"ENG-1 is in Needs Human: ticket ENG-1"}, d.Alerts.Shown())
}

func TestTickStartsFreshWhenAPausedTicketReturns(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.PullRequestFailsAfterEntry(prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	require.NoError(t, d.TickAfterASecond())
	d.Tick()
	// Moved to Todo, the ticket is paused and its worker is closed.
	d.Linear.Tickets()
	d.Linear.Finished(ticket("ENG-1", "Todo", "dispatch"))
	d.Advance(time.Minute)
	d.Tick()
	d.Linear.Finished()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Advance(time.Minute)

	d.Tick()

	// The worker it had is gone, so a fresh one starts instead of a second nudge.
	assert.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())
	assert.Len(t, d.Workers.Prompts(), 1)
	assert.Len(t, d.Workers.Launched(), 1)
	noLinearChanges(t, d)
}

func TestTickDropsAPendingResumeWhenTheTicketIsPaused(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "blocked")
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"))
	d.Tick()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Advance(time.Minute)
	d.Tick()
	d.Linear.Tickets()
	d.Linear.Finished(ticket("ENG-1", "Todo", "dispatch"))
	d.Advance(time.Minute)
	d.Tick()
	d.Linear.Finished()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Advance(time.Minute)

	d.Tick()

	// The paused ticket's worker was closed, so a fresh one starts and the resume nudge is gone.
	assert.Empty(t, d.Workers.Prompts())
	assert.Len(t, d.Workers.Launched(), 1)
}

func TestTickRenotifyIntervalIsTheConfiguredMinutes(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nresync = \"30s\"\n[rules]\nblocked_renotify_minutes = 1\n"})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "blocked")

	var counts []int
	for i := range 4 {
		if i > 0 {
			d.Advance(30 * time.Second)
		}
		d.Tick()
		counts = append(counts, len(d.Alerts.Shown()))
	}

	// A minute is two 30-second resyncs: the first notice, then one every minute.
	assert.Equal(t, []int{1, 1, 2, 2}, counts)
}

func TestTickNudgedWorkerGetsNoPRNudgeBeforeTheNextResync(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	d.Restart()
	require.Equal(t, []string{prPrompt("a failed check")}, d.Workers.Prompts())

	// herdr still reports the nudged worker idle a second later. A tell reads herdr
	// without a resync, and the worker must still count as busy.
	d.Advance(time.Second)
	require.NoError(t, d.Run("tell", "ENG-1", "check the logs").Err)
	assert.Equal(t, []string{prPrompt("a failed check"), "w-ENG-1:p1|check the logs"}, d.Workers.Prompts())

	// The next resync believes herdr again, and a worker still idle then gets its PR nudge.
	d.Advance(time.Second)
	d.Tick()
	assert.Equal(t, []string{prPrompt("a failed check"), "w-ENG-1:p1|check the logs", prPrompt("a failed check")}, d.Workers.Prompts())
}

func TestTickWorkerWithNoStopRecordIsNudgedAgainOnlyAtAResync(t *testing.T) {
	t.Run("TickOnTheSameClock", func(t *testing.T) {
		d := newDispatch(t, dispatchOptions{})
		d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
		d.Workers.Running("ENG-1", "idle")
		d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

		d.Restart()
		assert.Equal(t, []string{prPrompt("a failed check")}, d.Workers.Prompts())

		d.Tick()
		d.Tick()

		assert.Len(t, d.Workers.Prompts(), 1)
		assert.Empty(t, d.Alerts.Shown())
	})

	t.Run("ResyncPasses", func(t *testing.T) {
		d := newDispatch(t, dispatchOptions{Config: "[rules]\nnudge_limit = 9\n"})
		d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
		d.Workers.Running("ENG-1", "idle")
		d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})

		d.Restart()
		assert.Len(t, d.Workers.Prompts(), 1)

		d.Advance(5 * time.Minute)
		assert.Len(t, d.Workers.Prompts(), 2)
		d.Advance(5 * time.Minute)
		assert.Len(t, d.Workers.Prompts(), 3)
		assert.Empty(t, d.Alerts.Shown())
	})
}

func TestTickReadyNoticeAndMergeWaitWhileANudgedWorkerShowsAsWorking(t *testing.T) {
	for _, test := range []struct {
		name     string
		settings string
		done     func(d *Dispatch) int
	}{
		{name: "ReadyNotice", done: func(d *Dispatch) int { return len(d.Alerts.Shown()) }},
		{name: "Merge", settings: autoMergeSetting, done: func(d *Dispatch) int { return len(d.GitHub.Merged()) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{RepoSettings: test.settings})
			d.Workers.Running("ENG-1", "working")
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			pr := cleanPR("a1")
			pr.approved = []string{"a1"}
			d.GitHub.PullRequest("ENG-1", pr)
			d.Tick()
			require.Zero(t, test.done(d))

			// The worker goes idle as a review lands, so the activity nudge goes first.
			d.Advance(time.Second)
			d.Workers.SetStatus("ENG-1", "idle")
			pr.reviews = []activity{{"COMMENTED", time2, "alice", "", ""}}
			d.GitHub.PullRequest("ENG-1", pr)
			d.Tick()
			assert.Equal(t, []string{activityPrompt("a review by alice")}, d.Workers.Prompts())
			assert.Zero(t, test.done(d))

			// Once a resync finds the nudged worker idle again, the notice and the merge follow.
			d.Advance(time.Second)
			d.Tick()
			assert.Equal(t, 1, test.done(d))
		})
	}
}

// Unreachable from the CLI: a run that crashed between typing the text and writing its receipt.
func TestTickNeverSendsANudgeAttemptedBeforeACrash(t *testing.T) {
	for _, test := range []struct {
		name    string
		seeded  bool
		prompts int
	}{
		{name: "NoEarlierAttempt", seeded: false, prompts: 1},
		{name: "AttemptWithNoOutcome", seeded: true, prompts: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			d.Workers.Stop("ENG-1", stopTasks{})
			d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
			d.Advance(time.Second)
			if test.seeded {
				d.CrashedEarlierRun(earlierReceipt{
					Kind: "nudge", Subject: "ENG-1", Once: earlierOnce{"pr", "a1", d.Clock.Now().UnixMilli(), 0}, Rule: "nudge-pr-needs-work",
					Started: d.Clock.Now().Add(-time.Minute).UnixMilli(),
				})
			}

			d.Tick()
			d.Tick()

			assert.Len(t, d.Workers.Prompts(), test.prompts)
			noLinearChanges(t, d)
		})
	}
}

// A ticket comment is not an event dispatch nudges for. These tests pin that.
func TestTickNudgesNoWorkerAboutANewTicketComment(t *testing.T) {
	for _, test := range []struct {
		name   string
		worker string
		status string
	}{
		{name: "WorkingInProgress", worker: "working", status: "In Progress"},
		{name: "BlockedInProgress", worker: "blocked", status: "In Progress"},
		{name: "WorkingInReview", worker: "working", status: "In Review"},
		{name: "IdleInReview", worker: "idle", status: "In Review"},
		{name: "BlockedInReview", worker: "blocked", status: "In Review"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{})
			d.Workers.Running("ENG-1", test.worker)
			d.Linear.Tickets(withComment(ticket("ENG-1", test.status, "dispatch"), comment1))
			d.Tick()
			d.Advance(time.Second)
			d.Linear.Tickets(withComment(ticket("ENG-1", test.status, "dispatch"), comment2))

			d.Tick()

			for _, prompt := range d.Workers.Prompts() {
				assert.NotContains(t, prompt, "new comment")
			}
		})
	}
}

func TestTickPRNudgeIsNotHeldBackByATicketComment(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Tickets(withComment(ticket("ENG-1", "In Review", "dispatch"), comment1))
	d.Tick()
	d.Advance(time.Second)
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	d.Linear.Tickets(withComment(ticket("ENG-1", "In Review", "dispatch"), comment2))

	d.Tick()

	assert.Equal(t, []string{prPrompt("a failed check")}, d.Workers.Prompts())
}

func TestTickActivityNudgeFiresOnTheTickATicketCommentLands(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Tickets(withComment(ticket("ENG-1", "In Review", "dispatch"), comment1))
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	d.Tick()
	d.Advance(time.Second)
	pr := cleanPR("a1")
	pr.reviews = []activity{{"COMMENTED", time2, "alice", "", ""}}
	d.GitHub.PullRequest("ENG-1", pr)
	d.Linear.Tickets(withComment(ticket("ENG-1", "In Review", "dispatch"), comment2))

	d.Tick()

	assert.Equal(t, []string{activityPrompt("a review by alice")}, d.Workers.Prompts())
}

func TestTickResumeNudgeIgnoresANewTicketComment(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: "[rules]\nnudge_limit = 9\n"})
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Tickets(withComment(ticket("ENG-1", "Needs Human", "dispatch"), comment1))
	d.Tick()
	d.Advance(time.Second)
	d.Linear.Tickets(withComment(ticket("ENG-1", "In Progress", "dispatch"), comment2))

	d.Tick()

	assert.Equal(t, []string{"w-ENG-1:p1|" + resumeText1}, d.Workers.Prompts())
}

// watched starts an idle In Review worker whose PR has the reviews and comments of before, in a repo
// with the given settings, and runs the tick that records them as seen. The clock then moves, so
// whatever arrives next is newer than the worker dispatch knows.
func watched(t *testing.T, before prState, settings ...string) *Dispatch {
	d := newDispatch(t, dispatchOptions{RepoSettings: strings.Join(settings, "")})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", before)
	d.Tick()
	d.Advance(time.Second)
	return d
}

func TestTickNudgesAnIdleWorkerOnceForNewPRActivity(t *testing.T) {
	for _, test := range []struct {
		name string
		add  func(p *prState)
		by   string
	}{
		{name: "BodyOnlyReview", add: func(p *prState) { p.reviews = append(p.reviews, activity{"COMMENTED", time2, "alice", "", ""}) }, by: "a review by alice"},
		{name: "ChangesRequestedReview", add: func(p *prState) { p.reviews = append(p.reviews, activity{"CHANGES_REQUESTED", time2, "bot", "", ""}) }, by: "a review by bot"},
		{name: "ConversationComment", add: func(p *prState) { p.comments = append(p.comments, activity{"", time2, "bob", "", ""}) }, by: "a comment by bob"},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := cleanPR("a1")
			d := watched(t, before)
			test.add(&before)
			d.GitHub.PullRequest("ENG-1", before)

			notified := len(d.Alerts.Shown())

			d.Tick()

			assert.Equal(t, []string{activityPrompt(test.by)}, d.Workers.Prompts())
			noLinearChanges(t, d)

			// The nudge is not repeated, and the ready notice for this head already went out.
			d.Advance(time.Second)
			d.Tick()
			assert.Len(t, d.Workers.Prompts(), 1)
			assert.Len(t, d.Alerts.Shown(), notified)
		})
	}
}

func TestTickNamesTheNewestPRActivity(t *testing.T) {
	before := cleanPR("a1")
	d := watched(t, before)
	before.reviews = []activity{{"COMMENTED", time2, "alice", "", ""}}
	before.comments = []activity{{"", time3, "bob", "", ""}}
	d.GitHub.PullRequest("ENG-1", before)

	d.Tick()

	assert.Equal(t, []string{activityPrompt("a comment by bob")}, d.Workers.Prompts())
}

func TestTickDoesNotNudgeForAnApprovalOrADraftReview(t *testing.T) {
	for _, state := range []string{"APPROVED", "DISMISSED", "PENDING"} {
		t.Run(state, func(t *testing.T) {
			before := cleanPR("a1")
			d := watched(t, before)
			before.reviews = []activity{{state, time2, "alice", "", ""}}
			d.GitHub.PullRequest("ENG-1", before)

			d.Tick()

			assert.Empty(t, d.Workers.Prompts())
		})
	}
}

func TestTickSkipsPRActivityThatPredatesDispatchsCache(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	// A worker dispatch first sees beside the review already knows about it.
	pr := cleanPR("a1")
	pr.reviews = []activity{{"COMMENTED", time1, "alice", "", ""}}
	d.GitHub.PullRequest("ENG-1", pr)

	d.Tick()

	assert.Empty(t, d.Workers.Prompts())
}

func TestTickNudgesForActivityPresentWhenTheTicketEnteredInReview(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	require.Len(t, d.Workers.Launched(), 1)
	// Dispatch launched the worker, so it knows the worker from before the review arrived.
	d.Workers.Running("ENG-1", "working")
	d.Tick()
	d.Advance(time.Second)
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	pr := cleanPR("a1")
	pr.reviews = []activity{{"COMMENTED", time1, "alice", "", ""}}
	d.GitHub.PullRequest("ENG-1", pr)
	d.Tick()
	assert.Empty(t, d.Workers.Prompts())
	d.Workers.SetStatus("ENG-1", "idle")

	d.Tick()

	assert.Equal(t, []string{activityPrompt("a review by alice")}, d.Workers.Prompts())
}

func TestTickHoldsPRActivityUntilTheWorkerIsIdle(t *testing.T) {
	before := cleanPR("a1")
	d := watched(t, before)
	d.Workers.SetStatus("ENG-1", "working")
	before.reviews = []activity{{"COMMENTED", time2, "alice", "", ""}}
	d.GitHub.PullRequest("ENG-1", before)
	d.Tick()
	assert.Empty(t, d.Workers.Prompts())
	d.Workers.SetStatus("ENG-1", "idle")

	d.Tick()

	assert.Equal(t, []string{activityPrompt("a review by alice")}, d.Workers.Prompts())
}

func TestTickActivityNudgeDoesNotCountTowardThePRLimit(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: "[rules]\nnudge_limit = 1\n"})
	before := cleanPR("a1")
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", before)
	for range 2 {
		d.Tick()
	}
	d.Advance(time.Second)
	for i, at := range []string{time1, time2, time3} {
		before.comments = append(before.comments, activity{"", at, fmt.Sprintf("bob%d", i), "", ""})
		d.GitHub.PullRequest("ENG-1", before)

		d.Advance(time.Second)
		d.Tick()

		assert.Len(t, d.Workers.Prompts(), i+1)
		assert.Equal(t, activityPrompt(fmt.Sprintf("a comment by bob%d", i)), d.Workers.Prompts()[i])
	}
	noLinearChanges(t, d)
}

func TestTickNamesNewActivityInALaterNudgeWhenAPRProblemComesFirst(t *testing.T) {
	before := cleanPR("a1")
	d := watched(t, before)
	before.checks = "FAILURE"
	before.reviews = []activity{{"COMMENTED", time2, "alice", "", ""}}
	d.GitHub.PullRequest("ENG-1", before)
	d.Tick()
	require.Len(t, d.Workers.Prompts(), 1)
	assert.Equal(t, prPrompt("a failed check"), d.Workers.Prompts()[0])
	before.checks = "SUCCESS"
	d.GitHub.PullRequest("ENG-1", before)
	d.Advance(time.Second)

	d.Tick()

	assert.Equal(t, []string{prPrompt("a failed check"), activityPrompt("a review by alice")}, d.Workers.Prompts())
}

func TestTickHoldsAutoMergeForNewPRActivity(t *testing.T) {
	d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	// No approval yet, so the first read only records the worker.
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	d.Tick()
	d.Advance(time.Second)
	pr := approvedPR("a1")
	pr.comments = []activity{{"", time2, "alice", "", ""}}
	d.GitHub.PullRequest("ENG-1", pr)

	d.Tick()

	assert.Equal(t, []string{activityPrompt("a comment by alice")}, d.Workers.Prompts())
	assert.Empty(t, d.GitHub.Merged())

	d.Advance(time.Second)
	d.Tick()
	assert.Len(t, d.GitHub.Merged(), 1)
}

func TestTickDoesNotRepeatActivityAfterTheTicketLeavesAndReturnsToInReview(t *testing.T) {
	before := cleanPR("a1")
	d := watched(t, before)
	before.comments = []activity{{"", time2, "bob", "", ""}}
	d.GitHub.PullRequest("ENG-1", before)
	d.Tick()
	require.Len(t, d.Workers.Prompts(), 1)
	d.Workers.SetStatus("ENG-1", "working")
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Advance(time.Second)
	d.Tick()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Advance(time.Second)
	d.Tick()
	d.Workers.SetStatus("ENG-1", "idle")
	d.Advance(time.Second)

	d.Tick()

	assert.Len(t, d.Workers.Prompts(), 1)
}

func TestTickNudgesForAnApprovalThatCarriesABody(t *testing.T) {
	before := cleanPR("a1")
	d := watched(t, before)
	before.reviews = []activity{{"APPROVED", time2, "alice", "Approved, but please rename X.", ""}}
	d.GitHub.PullRequest("ENG-1", before)

	d.Tick()

	assert.Equal(t, []string{activityPrompt("a review by alice")}, d.Workers.Prompts())
}

func TestTickNudgesForAReviewSubmittedAfterItWasStarted(t *testing.T) {
	before := cleanPR("a1")
	before.comments = []activity{{"", time2, "bob", "", ""}}
	d := watched(t, before)
	d.Tick()
	// The draft was started at time1, before the comment, and submitted at time3.
	before.reviews = []activity{{"COMMENTED", time1, "alice", "please rename X", time3}}
	d.GitHub.PullRequest("ENG-1", before)

	d.Tick()

	assert.Equal(t, []string{activityPrompt("a review by alice")}, d.Workers.Prompts())
}

func TestTickKeepsPRActivityAcrossATicketAbsentFromTheRead(t *testing.T) {
	before := cleanPR("a1")
	d := watched(t, before)
	before.comments = []activity{{"", time2, "bob", "", ""}}
	d.GitHub.PullRequest("ENG-1", before)
	d.Tick()
	require.Len(t, d.Workers.Prompts(), 1)
	d.Trees.Leased("ENG-1", d.Trees.Clean("ENG-1"))
	d.Linear.Tickets()
	d.Advance(time.Second)
	d.Tick()
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Advance(time.Second)

	d.Tick()

	assert.Len(t, d.Workers.Prompts(), 1)
}

func TestTickRetriesAPRActivityNudgeHerdrRefused(t *testing.T) {
	before := cleanPR("a1")
	d := watched(t, before)
	before.reviews = []activity{{"COMMENTED", time2, "alice", "", ""}}
	d.GitHub.PullRequest("ENG-1", before)
	d.Workers.BlockAfterSnapshot("ENG-1")
	require.ErrorContains(t, d.Run("tick").Err, "blocked")
	assert.Empty(t, d.Workers.Prompts())
	d.Workers.Unblock("ENG-1")

	d.Tick()

	assert.Equal(t, []string{activityPrompt("a review by alice")}, d.Workers.Prompts())
}

func TestTickNudgesOnceForEachNewCommentOnAnOpenReviewThread(t *testing.T) {
	before := cleanPR("a1")
	d := watched(t, before)
	before.threads = []activity{{"", time2, "alice", "", ""}}
	d.GitHub.PullRequest("ENG-1", before)

	d.Tick()

	assert.Equal(t, []string{activityPrompt("a review thread comment by alice")}, d.Workers.Prompts())

	// The thread stays open, and later ticks stay quiet.
	for range 2 {
		d.Advance(time.Second)
		d.Tick()
	}
	assert.Len(t, d.Workers.Prompts(), 1)

	// A reply on the same thread is a new comment.
	before.threads = []activity{{"", time3, "bob", "", ""}}
	d.GitHub.PullRequest("ENG-1", before)
	d.Advance(time.Second)
	d.Tick()

	assert.Len(t, d.Workers.Prompts(), 2)
	assert.Equal(t, activityPrompt("a review thread comment by bob"), d.Workers.Prompts()[1])
}

func TestTickIgnoresCommentsOnResolvedReviewThreads(t *testing.T) {
	before := cleanPR("a1")
	d := watched(t, before)
	before.resolved = 1
	d.GitHub.PullRequest("ENG-1", before)

	d.Tick()

	assert.Empty(t, d.Workers.Prompts())
}

func TestTickHoldsTheReadyNoticeWhileAThreadStaysOpenAfterItsNudge(t *testing.T) {
	before := cleanPR("a1")
	d := watched(t, before)
	notified := len(d.Alerts.Shown())
	before.threads = []activity{{"", time2, "alice", "", ""}}
	d.GitHub.PullRequest("ENG-1", before)
	d.Tick()
	assert.Equal(t, []string{activityPrompt("a review thread comment by alice")}, d.Workers.Prompts())

	for range 2 {
		d.Advance(time.Second)
		d.Tick()
	}
	assert.Len(t, d.Alerts.Shown(), notified)

	// The reviewer resolves the thread and the worker pushes, so the new head is ready.
	before.threads = nil
	before.resolved = 1
	before.head = "a2"
	d.GitHub.PullRequest("ENG-1", before)
	d.Advance(time.Second)
	d.Tick()
	assert.Len(t, d.Alerts.Shown(), notified+1)
}

func TestTickDoesNotAutoMergeWhileAThreadStaysOpenAfterItsNudge(t *testing.T) {
	d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", cleanPR("a1"))
	d.Tick()
	d.Advance(time.Second)
	pr := approvedPR("a1")
	pr.threads = []activity{{"", time2, "alice", "", ""}}
	d.GitHub.PullRequest("ENG-1", pr)
	d.Tick()
	assert.Equal(t, []string{activityPrompt("a review thread comment by alice")}, d.Workers.Prompts())

	for range 2 {
		d.Advance(time.Second)
		d.Tick()
	}
	assert.Empty(t, d.GitHub.Merged())

	pr.threads = nil
	pr.resolved = 1
	d.GitHub.PullRequest("ENG-1", pr)
	d.Advance(time.Second)
	d.Tick()
	assert.Len(t, d.GitHub.Merged(), 1)
}

func TestTickNudgesAThreadCommentAfterAnotherPRProblemClears(t *testing.T) {
	before := cleanPR("a1")
	d := watched(t, before)
	before.checks = "FAILURE"
	before.threads = []activity{{"", time2, "alice", "", ""}}
	d.GitHub.PullRequest("ENG-1", before)

	d.Tick()
	assert.Equal(t, []string{prPrompt("a failed check")}, d.Workers.Prompts())

	before.checks = "SUCCESS"
	d.GitHub.PullRequest("ENG-1", before)
	d.Advance(time.Second)
	d.Tick()

	assert.Equal(t, []string{prPrompt("a failed check"), activityPrompt("a review thread comment by alice")}, d.Workers.Prompts())
}

func TestTickRelaunchesAGoneWorkerOnceForAThreadComment(t *testing.T) {
	before := cleanPR("a1")
	d := watched(t, before)
	d.Workers.Gone("ENG-1")
	before.threads = []activity{{"", time2, "alice", "", ""}}
	d.GitHub.PullRequest("ENG-1", before)

	for range 2 {
		d.Tick()
		assert.Len(t, d.Workers.Launched(), 1)
		d.Advance(time.Second)
	}
}

func TestTickDoesNotRelaunchAGoneWorkerForAThreadCommentAlreadySeen(t *testing.T) {
	before := cleanPR("a1")
	d := watched(t, before)
	before.threads = []activity{{"", time2, "alice", "", ""}}
	d.GitHub.PullRequest("ENG-1", before)
	d.Tick()
	require.Len(t, d.Workers.Prompts(), 1)
	d.Workers.Gone("ENG-1")
	d.Advance(time.Second)

	d.Tick()

	assert.Empty(t, d.Workers.Launched())
}

func TestTickDoesNotRelaunchAGoneWorkerForAPlainCommentWhileAThreadIsOpen(t *testing.T) {
	before := cleanPR("a1")
	before.threads = []activity{{"", time1, "alice", "", ""}}
	d := watched(t, before)
	before.comments = []activity{{"", time3, "bob", "", ""}}
	d.GitHub.PullRequest("ENG-1", before)
	d.Tick()
	require.Len(t, d.Workers.Prompts(), 1)
	d.Workers.Gone("ENG-1")
	d.Advance(time.Second)

	d.Tick()

	assert.Empty(t, d.Workers.Launched())
}

func TestTickRecordsAThreadCommentWhenAProblemRelaunchesAGoneWorker(t *testing.T) {
	before := cleanPR("a1")
	d := watched(t, before)
	d.Workers.Gone("ENG-1")
	before.checks = "FAILURE"
	before.threads = []activity{{"", time2, "alice", "", ""}}
	d.GitHub.PullRequest("ENG-1", before)
	d.Tick()
	require.Len(t, d.Workers.Launched(), 1)

	// The failed check clears while the worker is still gone.
	before.checks = "SUCCESS"
	d.GitHub.PullRequest("ENG-1", before)
	d.Advance(time.Second)
	d.Tick()

	assert.Len(t, d.Workers.Launched(), 1)
}

const (
	ignoreSetting = "ignore_comment_authors = [\"github-actions[bot]\", \"codeowners-bot\"]\n"
	listedBot     = "GitHub-Actions[bot]"
)

func TestTickIgnoresPRActivityByAListedAuthor(t *testing.T) {
	for _, test := range []struct {
		name string
		add  func(p *prState)
	}{
		{name: "ConversationComment", add: func(p *prState) { p.comments = append(p.comments, activity{"", time2, listedBot, "", ""}) }},
		{name: "BodyOnlyReview", add: func(p *prState) {
			p.reviews = append(p.reviews, activity{"COMMENTED", time2, "codeowners-bot", "", ""})
		}},
		{name: "ChangesRequestedReview", add: func(p *prState) {
			p.reviews = append(p.reviews, activity{"CHANGES_REQUESTED", time2, listedBot, "", ""})
			p.opinions = append(p.opinions, opinion{"CHANGES_REQUESTED", "a1", listedBot})
		}},
		{name: "ThreadComment", add: func(p *prState) { p.threads = append(p.threads, activity{"", time2, listedBot, "", ""}) }},
		{name: "ThreadOfOnlyListedAuthors", add: func(p *prState) {
			p.conversations = append(p.conversations, []activity{{"", time1, listedBot, "", ""}, {"", time2, "codeowners-bot", "", ""}})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := cleanPR("a1")
			d := watched(t, before, ignoreSetting)
			notified := d.Alerts.Shown()
			test.add(&before)
			d.GitHub.PullRequest("ENG-1", before)

			d.Tick()

			assert.Empty(t, d.Workers.Prompts())
			assert.Empty(t, d.Workers.Launched())
			assert.Equal(t, notified, d.Alerts.Shown())
		})
	}
}

func TestTickCountsPRActivityByAListedAuthorInARepoThatListsNobody(t *testing.T) {
	for _, test := range []struct {
		name string
		add  func(p *prState)
		want string
	}{
		{name: "ConversationComment", add: func(p *prState) { p.comments = []activity{{"", time2, listedBot, "", ""}} }, want: "a comment by " + listedBot},
		{name: "BodyOnlyReview", add: func(p *prState) { p.reviews = []activity{{"COMMENTED", time2, listedBot, "", ""}} }, want: "a review by " + listedBot},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := cleanPR("a1")
			d := watched(t, before)
			test.add(&before)
			d.GitHub.PullRequest("ENG-1", before)

			d.Tick()

			require.Len(t, d.Workers.Prompts(), 1)
			assert.Contains(t, d.Workers.Prompts()[0], test.want)
		})
	}
}

func TestTickCountsPRActivityByAnAuthorWhoIsNotListed(t *testing.T) {
	before := cleanPR("a1")
	d := watched(t, before, ignoreSetting)
	before.comments = []activity{{"", time2, "alice", "", ""}, {"", time3, listedBot, "", ""}}
	d.GitHub.PullRequest("ENG-1", before)

	d.Tick()

	require.Len(t, d.Workers.Prompts(), 1)
	assert.Contains(t, d.Workers.Prompts()[0], "a comment by alice")
	assert.NotContains(t, d.Workers.Prompts()[0], listedBot)
}

func TestTickReadyIgnoresThreadsThatOnlyListedAuthorsTouched(t *testing.T) {
	d := newDispatch(t, dispatchOptions{RepoSettings: ignoreSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	pr := cleanPR("a1")
	pr.conversations = [][]activity{{{"", time1, listedBot, "", ""}, {"", time2, "codeowners-bot", "", ""}}}
	d.GitHub.PullRequest("ENG-1", pr)

	d.Tick()

	assert.Equal(t, []string{"ENG-1 is ready for review: " + prURL1}, d.Alerts.Shown())
}

func TestTickReadyWaitsOnAThreadWithAnyUnlistedComment(t *testing.T) {
	d := newDispatch(t, dispatchOptions{RepoSettings: ignoreSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	pr := cleanPR("a1")
	pr.conversations = [][]activity{{{"", time1, "alice", "", ""}, {"", time2, listedBot, "", ""}}}
	d.GitHub.PullRequest("ENG-1", pr)

	d.Tick()

	assert.Empty(t, d.Alerts.Shown())
}

func TestTickIgnoresAListedAuthorsApprovalAndChangeRequest(t *testing.T) {
	d := newDispatch(t, dispatchOptions{RepoSettings: ignoreSetting + autoMergeSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	pr := cleanPR("a1")
	pr.opinions = []opinion{{"APPROVED", "a1", listedBot}}
	d.GitHub.PullRequest("ENG-1", pr)

	// A listed author's approval is not an approval.
	d.Tick()
	assert.Equal(t, []string{"ENG-1 is ready for review: " + prURL1}, d.Alerts.Shown())
	assert.Empty(t, d.GitHub.Merged())

	// Nor is its change request a veto on a person's approval.
	pr = cleanPR("a2")
	pr.opinions = []opinion{{"APPROVED", "a2", "alice"}, {"CHANGES_REQUESTED", "a2", "codeowners-bot"}}
	d.GitHub.PullRequest("ENG-1", pr)
	d.Tick()
	assert.Equal(t, []string{prURL1 + "|a2"}, d.GitHub.Merged())
}

func TestTickCountsAChangeRequestFromAnUnlistedAuthor(t *testing.T) {
	d := newDispatch(t, dispatchOptions{RepoSettings: ignoreSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	pr := cleanPR("a1")
	pr.opinions = []opinion{{"CHANGES_REQUESTED", "a1", "alice"}, {"CHANGES_REQUESTED", "a1", listedBot}}
	d.GitHub.PullRequest("ENG-1", pr)

	d.Tick()

	require.Len(t, d.Workers.Prompts(), 1)
	assert.Contains(t, d.Workers.Prompts()[0], "changes requested")
}

func TestTickReadsPastAWindowOfListedAuthorsToTheNewestUnlistedItem(t *testing.T) {
	for _, kind := range []string{"comments", "reviews"} {
		t.Run(kind, func(t *testing.T) {
			listed := func(at string) []activity {
				var out []activity
				for range 100 {
					a := activity{at: at, author: listedBot}
					if kind == "reviews" {
						a.state = "COMMENTED"
					}
					out = append(out, a)
				}
				return out
			}
			human := activity{at: time1, author: "alice"}
			if kind == "reviews" {
				human.state = "COMMENTED"
			}
			setup := func(p *prState, newest []activity) {
				if kind == "reviews" {
					p.reviews, p.reviewsBefore = newest, "page2"
				} else {
					p.comments, p.commentsBefore = newest, "page2"
				}
			}
			before := cleanPR("a1")
			d := watched(t, before, ignoreSetting)
			setup(&before, listed(time2))
			d.GitHub.PullRequest("ENG-1", before)
			// Page 2 is full of listed authors too, and the person's item is on page 3.
			d.GitHub.RespondOlder(kind, "page2", listed(time1), "page3")
			d.GitHub.RespondOlder(kind, "page3", []activity{human}, "")

			d.Tick()

			// Dispatch has not seen the person's item before, so it is new activity.
			article := map[string]string{"comments": "a comment", "reviews": "a review"}[kind]
			require.Len(t, d.Workers.Prompts(), 1)
			assert.Contains(t, d.Workers.Prompts()[0], fmt.Sprintf("new activity, %s by alice", article))
			assert.Len(t, d.GitHub.OlderReads(), 2)
		})
	}
}

func TestTickStopsReadingOlderPagesOnceAnUnlistedItemIsFound(t *testing.T) {
	pr := cleanPR("a1")
	d := watched(t, pr, ignoreSetting)
	pr.comments = []activity{{"", time2, listedBot, "", ""}, {"", time3, "alice", "", ""}}
	pr.commentsBefore = "page2"
	d.GitHub.PullRequest("ENG-1", pr)

	d.Tick()

	// An unset page 2 would fail the read, and no older call proves it was not requested.
	assert.Empty(t, d.GitHub.OlderReads())
}

func TestTickJudgesAPRWhoseNewestCommentsAreListedByOlderUnlistedOnes(t *testing.T) {
	d := newDispatch(t, dispatchOptions{RepoSettings: ignoreSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	pr := cleanPR("a1")
	d.GitHub.PullRequest("ENG-1", pr)
	d.Tick()
	d.Advance(time.Second)
	pr.comments = []activity{{"", time3, listedBot, "", ""}}
	pr.commentsBefore = "page2"
	d.GitHub.PullRequest("ENG-1", pr)
	d.GitHub.RespondOlder("comments", "page2", []activity{{"", time1, "alice", "", ""}}, "")
	d.Tick()
	d.Workers.SetStatus("ENG-1", "idle")

	d.Tick()

	require.Len(t, d.Workers.Prompts(), 1)
	assert.Contains(t, d.Workers.Prompts()[0], "a comment by alice")
}

func TestTickMatchesListedAuthorsWithoutRegardToCase(t *testing.T) {
	before := cleanPR("a1")
	d := watched(t, before, "ignore_comment_authors = [\"CodeOwners-BOT\"]\n")
	before.comments = []activity{{"", time2, "codeowners-bot", "", ""}}
	d.GitHub.PullRequest("ENG-1", before)

	d.Tick()

	assert.Empty(t, d.Workers.Prompts())
}

func TestTickReadyIgnoresListedCommentsReviewsAndThreads(t *testing.T) {
	d := newDispatch(t, dispatchOptions{RepoSettings: ignoreSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	pr := cleanPR("a1")
	pr.comments = []activity{{"", time2, listedBot, "", ""}}
	pr.reviews = []activity{{"COMMENTED", time3, "codeowners-bot", "", ""}}
	pr.threads = []activity{{"", time3, listedBot, "", ""}}
	d.GitHub.PullRequest("ENG-1", pr)

	d.Tick()

	assert.Equal(t, []string{"ENG-1 is ready for review: " + prURL1}, d.Alerts.Shown())
}

func TestTickReadsPastUnlistedReviewsThatAskForNothing(t *testing.T) {
	d := newDispatch(t, dispatchOptions{RepoSettings: ignoreSetting})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	pr := cleanPR("a1")
	d.GitHub.PullRequest("ENG-1", pr)
	d.Tick()
	d.Advance(time.Second)
	pr.reviews = []activity{{state: "DISMISSED", at: time3, author: "alice"}}
	pr.reviewsBefore = "page2"
	d.GitHub.PullRequest("ENG-1", pr)
	d.GitHub.RespondOlder("reviews", "page2", []activity{{state: "COMMENTED", at: time1, author: "alice"}}, "")
	d.Tick()
	d.Workers.SetStatus("ENG-1", "idle")

	d.Tick()

	require.Len(t, d.Workers.Prompts(), 1)
	assert.Contains(t, d.Workers.Prompts()[0], "a review by alice")
}

// commandsRule comments each time what a worker's commands read as changes, one comment for each reading.
const commandsRule = `
whenever({ name: "commands", each: "linear.ticket",
  if: (t) => t.status === "in_progress" || t.status === "in_review",
  then: (t, ctx) => {
    const w = ctx.herdr.worker(t);
    const body = known(() => w.commands)
      ? "commands " + w.commands.map((c) => c.name + "|" + c.args + "|" + c.cut + "|" + c.by).join(";") + " of " + w.commands.length
      : "commands unknown";
    return ctx.linear.queueComment(t, body, { once: ["commands", t.id, body, ctx.now] });
  } });`

const nudgeRule = `
whenever({ name: "nudge", each: "linear.ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.herdr.queueNudge(t, "/merge-it 42", { once: ["nudge", t.id] }) });`

const anyStatusRule = `
whenever({ name: "anystatus", each: "linear.ticket", if: () => true,
  then: (t, ctx) => {
    const w = ctx.herdr.worker(t);
    const body = known(() => w.commands) ? "known " + w.commands.length : "unknown";
    return ctx.linear.queueComment(t, body, { once: ["anystatus", t.id, body] });
  } });`

// lastReadingOf is the newest comment a rule posted on the ticket, without the ticket prefix, or
// empty before any rule has commented.
func lastReadingOf(d *Dispatch, ticket string) string {
	var last string
	for _, comment := range d.Linear.Comments() {
		if body, ok := strings.CutPrefix(comment, ticket+": "); ok {
			last = body
		}
	}
	return last
}

// lastReading is lastReadingOf ENG-1, which must have one.
func lastReading(t *testing.T, d *Dispatch) string {
	t.Helper()
	reading := lastReadingOf(d, "ENG-1")
	require.NotEmpty(t, reading)
	return reading
}

func TestRulesReadTheCommandsASessionTookWithTheirArgumentsOldestFirst(t *testing.T) {
	for _, test := range []struct {
		name    string
		prompts []string
		want    string
	}{
		{"NameAndArguments", []string{"/merge-it 42"}, "commands merge-it|42|false|person of 1"},
		{"NameOnly", []string{"/merge-it"}, "commands merge-it||false|person of 1"},
		{"ArgumentsHoldASecondSlash", []string{"/merge-it and /explain this"}, "commands merge-it|and /explain this|false|person of 1"},
		{"ArgumentsTrimmed", []string{"/merge-it \t 42 \n\n"}, "commands merge-it|42|false|person of 1"},
		{"MultiLineArguments", []string{"/merge-it 42\nand then\nstop"}, "commands merge-it|42\nand then\nstop|false|person of 1"},
		{"PluginAndSkillNames", []string{"/dispatch:build ENG-457"}, "commands dispatch:build|ENG-457|false|person of 1"},
		{"NameWithDotAndUnderscore", []string{"/a_b.c-d"}, "commands a_b.c-d||false|person of 1"},
		{"NameThenNewline", []string{"/merge-it\n42"}, "commands merge-it|42|false|person of 1"},
		{"UppercaseAndDigitName", []string{"/Merge2"}, "commands Merge2||false|person of 1"},
		{"NameFollowedByNonBreakingSpace", []string{"/merge-it 42"}, "commands merge-it|42|false|person of 1"},
		{"OldestFirst", []string{"/one", "/two", "/three"}, "commands one||false|person;two||false|person;three||false|person of 3"},
		{"SkipsMessagesThatAreNotCommands", []string{"/one", "when should I run /merge-it?", "/two"}, "commands one||false|person;two||false|person of 2"},
		{"MentionIsNotACommand", []string{"when should I run /merge-it?"}, "commands  of 0"},
		{"PathIsNotACommand", []string{"/Users/x"}, "commands  of 0"},
		{"LeadingSpaceIsNotACommand", []string{" /x"}, "commands  of 0"},
		{"LoneSlashIsNotACommand", []string{"/", "/ x", "//x", "/-x", "/_x"}, "commands  of 0"},
		{"NameRunningIntoPunctuationIsNotACommand", []string{"/merge-it?", "/x/y"}, "commands  of 0"},
		{"TaskNotificationIsNotACommand", []string{"<task-notification>done</task-notification>"}, "commands  of 0"},
		{"EmptyMessage", []string{""}, "commands  of 0"},
		{"PlainPromptIsNotACommand", []string{"please fix the build"}, "commands  of 0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Rules: rule(commandsRule)})
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
			d.Workers.CheckedOut("ENG-1", "working")
			for _, prompt := range test.prompts {
				d.Workers.Type("ENG-1", prompt)
			}

			d.Tick()

			assert.Equal(t, test.want, lastReading(t, d))
		})
	}
}

func TestCommandArgumentsAreCutAtOneKilobyteWithoutSplittingACharacter(t *testing.T) {
	for _, test := range []struct {
		name, args string
		wantCut    bool
		wantChars  int
	}{
		{"UnderTheLimit", strings.Repeat("a", 1024), false, 1024},
		{"OverTheLimit", strings.Repeat("a", 2000), true, 1024},
		{"TwoByteCharacters", strings.Repeat("é", 1000), true, 512},
		{"ThreeByteCharacterOnTheLimit", "ab" + strings.Repeat("€", 500), true, 2 + 340},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Rules: rule(`
whenever({ name: "cut", each: "linear.ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => {
    const c = ctx.herdr.worker(t).commands[0];
    const body = "cut " + c.cut + " characters " + c.args.length;
    return ctx.linear.queueComment(t, body, { once: ["cut", t.id] });
  } });`)})
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
			d.Workers.CheckedOut("ENG-1", "working")
			d.Workers.Type("ENG-1", "/long "+test.args)

			d.Tick()

			assert.Equal(t, fmt.Sprintf("cut %t characters %d", test.wantCut, test.wantChars), lastReading(t, d))
		})
	}
}

func TestCommandsAreReadForAWorkerInAnyStateOnAnInProgressOrInReviewTicket(t *testing.T) {
	for _, test := range []struct {
		name, status, worker string
	}{
		{"WorkingInProgress", "In Progress", "working"},
		{"BlockedInProgress", "In Progress", "blocked"},
		{"IdleInProgress", "In Progress", "idle"},
		{"IdleInReview", "In Review", "done"},
		{"WorkingInReview", "In Review", "working"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Rules: rule(commandsRule)})
			d.Linear.Tickets(ticket("ENG-1", test.status, "dispatch"))
			d.Workers.CheckedOut("ENG-1", test.worker)
			d.Workers.Type("ENG-1", "/merge-it 42")

			d.Tick()

			assert.Equal(t, "commands merge-it|42|false|person of 1", lastReading(t, d))
		})
	}
}

func TestCommandsAreUnknownWhenHerdrReportsNoSessionOrTheLogHasAHole(t *testing.T) {
	t.Run("NoSession", func(t *testing.T) {
		d := newDispatch(t, dispatchOptions{Rules: rule(commandsRule)})
		d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
		d.Workers.CheckedOut("ENG-1", "working")
		d.Workers.Type("ENG-1", "/merge-it 42")
		d.Workers.SetSession("ENG-1", "")

		d.Tick()

		assert.Equal(t, "commands unknown", lastReading(t, d))
	})
	// Unreachable from the CLI: only a writer that died mid-line leaves a hole in a session log.
	t.Run("BadLine", func(t *testing.T) {
		d := newDispatch(t, dispatchOptions{Rules: rule(commandsRule)})
		d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
		d.Workers.CheckedOut("ENG-1", "working")
		d.Workers.Type("ENG-1", "/one")
		d.CorruptSessionLog("not json\n")
		d.Workers.Type("ENG-1", "/two")

		d.Tick()

		assert.Equal(t, "commands unknown", lastReading(t, d))
	})
}

func TestCommandsOfASessionThatTookNoneReadAsEmpty(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(commandsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.CheckedOut("ENG-1", "working")

	d.Tick()

	assert.Equal(t, "commands  of 0", lastReading(t, d))
}

func TestAWorkerRelaunchedInANewSessionReadsNoCommandsEvenOnTheSameTree(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(commandsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.CheckedOut("ENG-1", "working")
	d.Workers.Type("ENG-1", "/merge-it 42")

	d.Workers.SetSession("ENG-1", "relaunched")
	d.Advance(time.Minute)
	d.Tick()

	assert.Equal(t, "commands  of 0", lastReading(t, d))
}

func TestCommandsOfAnotherSessionInTheTreeAreNotRead(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(commandsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	tree := d.Workers.CheckedOut("ENG-1", "working")
	d.Workers.Prompt(tree, "stranger", "/eval-run")
	d.Workers.Type("ENG-1", "/merge-it 42")

	d.Tick()

	assert.Equal(t, "commands merge-it|42|false|person of 1", lastReading(t, d))
}

func TestCommandsSurviveARestartAndAStateResetButNotOneTakenWhileDispatchWasDown(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(commandsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	tree := d.Workers.CheckedOut("ENG-1", "working")
	d.Workers.Type("ENG-1", "/one")
	d.Tick()
	d.Stop()
	require.NoError(t, d.Run("state", "reset").Err)
	d.Workers.Type("ENG-1", "/two")
	d.Stop()

	// A hook with no dispatch to tell drops its report.
	input, err := json.Marshal(map[string]string{"session_id": "worker", "cwd": tree, "prompt": "/lost"})
	require.NoError(t, err)
	require.NoError(t, d.HookWhileDown("wake", string(input)).Err)
	d.Restart()
	d.Advance(5 * time.Minute)
	d.Tick()

	assert.Equal(t, "commands one||false|person;two||false|person of 2", lastReading(t, d))
}

func TestACommandTakenWhileTheLoopRunsReachesRulesAtTheNextResync(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(commandsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.CheckedOut("ENG-1", "working")
	d.StartBeforeTheEvents()
	d.Workers.Type("ENG-1", "/merge-it 42")

	d.Advance(time.Hour)

	assert.Equal(t, "commands merge-it|42|false|person of 1", lastReading(t, d))
}

func TestReplayReproducesADecisionThatReadCommandsAndTraceShowsThem(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(commandsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.CheckedOut("ENG-1", "working")
	d.Workers.Type("ENG-1", "/merge-it 42")
	d.Tick()
	d.Stop()
	// Replay opens no log, so the tree's logs can go (unreachable from the CLI: no command deletes them).
	d.LoseSessionLogs()

	replay := d.Run("replay")
	trace := d.Run("trace", "eng-1")

	require.NoError(t, replay.Err)
	require.NoError(t, trace.Err)
	found := regexp.MustCompile(`^(\d+) cycles?, (\d+) match\n$`).FindStringSubmatch(replay.Stdout)
	require.NotNil(t, found)
	assert.NotEqual(t, "0", found[1])
	assert.Equal(t, found[1], found[2])
	assert.Regexp(t, `commands.*\{"name":"merge-it","args":"42","cut":false,"at":\d+,"by":"person"\}`, trace.Stdout)
}

func TestACommandDispatchKickedOffWithReadsAsSentByDispatchOnce(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Launcher: builtinLauncher, Rules: shippedRulesAnd(t, commandsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	tree := d.Trees.Pool()
	d.Tick()
	require.Len(t, d.Workers.Kicked(), 1)
	pane, kickoff, _ := strings.Cut(d.Workers.Kicked()[0], "|")
	d.Workers.SetPaneSession(pane, "launched")
	// Claude submits the kickoff dispatch typed, then a person submits the same text. Only the first
	// reads as dispatch's.
	d.Workers.Prompt(tree, "launched", kickoff)
	d.Workers.Prompt(tree, "launched", kickoff)

	d.Restart()
	d.Tick()

	assert.Equal(t, "commands dispatch:build|ENG-1|false|dispatch;dispatch:build|ENG-1|false|person of 2", lastReading(t, d))
}

func TestANudgeThatStartsWithASlashReadsAsSentByDispatchOnceWhetherOneLineOrMany(t *testing.T) {
	for _, test := range []struct {
		name, text, want string
	}{
		{"OneLine", "/merge-it 42", "commands merge-it|42|false|dispatch;merge-it|42|false|person of 2"},
		{"ManyLines", "/merge-it 42\nthen stop", "commands merge-it|42\nthen stop|false|dispatch;merge-it|42\nthen stop|false|person of 2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Rules: rule(commandsRule)})
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
			d.Workers.CheckedOut("ENG-1", "idle")
			require.NoError(t, d.Run("tell", "ENG-1", test.text).Err)

			d.Workers.Type("ENG-1", test.text)
			d.Workers.Type("ENG-1", test.text)
			d.Advance(5 * time.Minute)
			d.Tick()

			assert.Equal(t, test.want, lastReading(t, d))
		})
	}
}

func TestANudgeThatIsNotACommandLeavesAPersonsLaterCommandAPersons(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(commandsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.CheckedOut("ENG-1", "idle")
	require.NoError(t, d.Run("tell", "ENG-1", "keep going").Err)

	d.Workers.Type("ENG-1", "keep going")
	d.Workers.Type("ENG-1", "/keep going")
	d.Tick()

	assert.Equal(t, "commands keep|going|false|person of 1", lastReading(t, d))
}

func TestANudgeHerdrRefusedLeavesNoSendForAPersonsCommandToMatch(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(d *Dispatch)
	}{
		{"BlockedAfterSnapshot", func(d *Dispatch) { d.Workers.BlockAfterSnapshot("ENG-1") }},
		{"BlockedBeforeSnapshot", func(d *Dispatch) { d.Workers.SetStatus("ENG-1", "blocked") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Rules: rule(commandsRule)})
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
			d.Workers.CheckedOut("ENG-1", "idle")
			test.setup(d)
			require.Error(t, d.Run("tell", "ENG-1", "/merge-it 42").Err)
			require.Empty(t, d.Workers.Prompts())

			d.Workers.Type("ENG-1", "/merge-it 42")
			d.Tick()

			assert.Equal(t, "commands merge-it|42|false|person of 1", lastReading(t, d))
		})
	}
}

func TestANudgeThatTimedOutKeepsItsSendBecauseTheTextMayHaveBeenTyped(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(commandsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.CheckedOut("ENG-1", "idle")
	d.Workers.StallPrompts()
	told := later(func() Result {
		return d.RunWith(dispatch.Options{ActionTimeout: 100 * time.Millisecond}, "tell", "ENG-1", "/merge-it 42")
	})
	require.Eventually(t, func() bool { return len(d.tools.Calls("herdr", "prompt")) == 1 }, 20*time.Second, time.Millisecond)
	d.Expire("herdr.nudge ENG-1")
	require.NoError(t, told().Err)

	d.Workers.Type("ENG-1", "/merge-it 42")
	d.Tick()

	assert.Equal(t, "commands merge-it|42|false|dispatch of 1", lastReading(t, d))
}

func TestEachSendMatchesOneCommandWhenManyHooksRace(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(commandsRule)})
	tree := d.Workers.CheckedOut("ENG-1", "idle")
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	require.NoError(t, d.Run("tell", "ENG-1", "/merge-it 42").Err)
	input, err := json.Marshal(map[string]string{"session_id": "worker", "cwd": tree, "prompt": "/merge-it 42"})
	require.NoError(t, err)

	for _, res := range d.HookTogether(8, "wake", string(input)) {
		assert.NoError(t, res.Err)
	}

	// The hooks do not wait for the loop, so wait until it has read all eight.
	require.Eventually(t, func() bool { return strings.HasSuffix(lastReadingOf(d, "ENG-1"), " of 8") }, 10*time.Second, 5*time.Millisecond)
	read := lastReading(t, d)
	assert.Equal(t, 1, strings.Count(read, "|dispatch"))
	assert.Equal(t, 7, strings.Count(read, "|person"))
}

func TestClosingAWorkspaceDropsTheNotesOfWhatWasTypedIntoItsTree(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: shippedRulesAnd(t, commandsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.CheckedOut("ENG-1", "idle")
	d.Workers.StallPrompts()
	told := later(func() Result {
		return d.RunWith(dispatch.Options{ActionTimeout: 100 * time.Millisecond}, "tell", "ENG-1", "/merge-it 42")
	})
	require.Eventually(t, func() bool { return len(d.tools.Calls("herdr", "prompt")) == 1 }, 20*time.Second, time.Millisecond)
	d.Expire("herdr.nudge ENG-1")
	require.NoError(t, told().Err)
	d.Workers.Unconfirmed("ENG-1")
	d.Advance(6 * time.Minute)
	d.Tick()
	require.Equal(t, []string{"w-ENG-1"}, d.Workers.Closed())

	// A new Claude on the same tree must not inherit the note of the one that was closed.
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Type("ENG-1", "/merge-it 42")
	d.Restart()
	d.Tick()

	assert.Equal(t, "commands merge-it|42|false|person of 1", lastReading(t, d))
}

func TestReturningATreeDropsItsCommands(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: shippedRulesAnd(t, commandsRule)})
	d.Linear.Tickets(ticket("ENG-2", "In Progress", "dispatch"))
	d.Workers.CheckedOut("ENG-1", "idle")
	d.Workers.CheckedOut("ENG-2", "idle")
	d.Workers.Type("ENG-1", "/one")
	d.Workers.Type("ENG-2", "/two")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))
	d.Trees.Pushed("ENG-1")

	d.Tick()
	require.Len(t, d.Trees.Returned(), 1)

	// A new Claude on the returned tree must not inherit the commands of the one before it.
	d.Linear.Finished()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Advance(time.Minute)
	d.Tick()

	assert.Equal(t, "commands  of 0", lastReadingOf(d, "ENG-1"))
	assert.Equal(t, "commands two||false|person of 1", lastReadingOf(d, "ENG-2"))
}

func TestTheHookNeverBlocksOrAddsTextWhateverGoesWrong(t *testing.T) {
	for _, test := range []struct {
		name      string
		config    string
		breakIt   func(d *Dispatch)
		wantCalls int
	}{
		{"BadConfig", "[dispatch]\nnot_a_setting = 1\n", func(d *Dispatch) {}, 0},
		{"NoDispatchRunning", "", func(d *Dispatch) {}, 0},
		{"StuckDispatch", "", func(d *Dispatch) { d.BrokenLoop(neverAnswers) }, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Config: test.config})
			tree := d.Workers.CheckedOut("ENG-1", "idle")
			test.breakIt(d)
			input, err := json.Marshal(map[string]string{"session_id": "worker", "cwd": tree, "prompt": "/merge-it 42"})
			require.NoError(t, err)

			res := d.Hook("wake", string(input))

			require.NoError(t, res.Err)
			assert.Empty(t, res.Stdout)
			if test.wantCalls > 0 {
				assert.Eventually(t, func() bool { return d.LoopCalls() == test.wantCalls }, time.Second, 5*time.Millisecond)
			}
		})
	}
}

func TestTheHookReportsNothingOutsideAWorkerTree(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(commandsRule)})
	d.Linear.Tickets(
		ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"),
		ticket("ENG-3", "In Progress", "dispatch"), ticket("ENG-4", "In Progress", "dispatch"),
	)
	// A worker whose tree is the clone itself, another repo or a bare directory reads the commands
	// recorded for that path, so a hook that wrongly recorded one there shows up as a command.
	elsewhere := map[string]string{"ENG-2": d.Repo, "ENG-3": newHintRepo(t), "ENG-4": t.TempDir()}
	d.Workers.CheckedOut("ENG-1", "idle")
	d.Workers.Type("ENG-1", "/merge-it 42")
	for id, tree := range elsewhere {
		d.Workers.RunningIn(id, tree, "idle")
		prompt, err := json.Marshal(map[string]string{"hook_event_name": "UserPromptSubmit", "session_id": "worker", "cwd": tree, "prompt": "/merge-it 42"})
		require.NoError(t, err)
		require.NoError(t, d.Hook("wake", string(prompt)).Err)
	}
	require.NoError(t, d.Hook("wake", "not json").Err)

	d.Advance(time.Minute)
	d.Tick()

	assert.Equal(t, "commands merge-it|42|false|person of 1", lastReadingOf(d, "ENG-1"))
	for _, id := range []string{"ENG-2", "ENG-3", "ENG-4"} {
		assert.Equal(t, "commands  of 0", lastReadingOf(d, id))
	}
}

func TestRulesTestFixtureGivesAWorkerCommands(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	dir := rulesDir(t, map[string]string{
		"dispatch.ts": `import { whenever, known } from "dispatch";
whenever({ name: "person-merged", each: "linear.ticket", if: (t, ctx) => {
    const w = ctx.herdr.worker(t);
    return t.status === "in_progress" && known(() => w.commands) && w.commands.some((c) => c.name === "merge-it" && c.by === "person" && c.args === "42" && !c.cut && c.at === ctx.now + 1000);
  }, then: (t, ctx) => ctx.herdr.queueNudge(t, "hi", { once: ["merged", t.id] }) });
whenever({ name: "commands-unread", each: "linear.ticket", if: (t, ctx) => t.status === "in_progress" && !known(() => ctx.herdr.worker(t).commands),
  then: (t, ctx) => ctx.herdr.queueNudge(t, "hi", { once: ["unread", t.id] }) });`,
		"commands.test.ts": `import { scenario, ticket, worker, rulesFrom } from "dispatch/testing";
const rules = rulesFrom("./dispatch");
const mine = { status: "in_progress", optedIn: true, repo: "dispatch" };

scenario(rules, "a rule reads the commands the fixture gives", s => {
  s.given(ticket("ENG-1", mine), worker("ENG-1", "idle", { commands: [{ name: "merge-it", args: "42", at: 1000, by: "person" }] }));
  s.expect([["person-merged", "herdr.nudge", "ENG-1"]]);
});

scenario(rules, "a command sent by dispatch is not a person's", s => {
  s.given(ticket("ENG-1", mine), worker("ENG-1", "idle", { commands: [{ name: "merge-it", args: "42", at: 1000, by: "dispatch" }] }));
  s.expect([]);
});

scenario(rules, "a worker given no commands has taken none", s => {
  s.given(ticket("ENG-1", mine), worker("ENG-1", "idle"));
  s.expect([]);
});

scenario(rules, "sessionUnread makes the commands unknown", s => {
  s.given(ticket("ENG-1", mine), worker("ENG-1", "idle", { sessionUnread: "no session" }));
  s.expect([["commands-unread", "herdr.nudge", "ENG-1"]]);
});
`,
	})

	res := d.RunWith(dispatch.Options{RulesDir: dir}, "rules", "test")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "0 failed")
}

func TestACommandIsStampedWithTheTimeClaudeTookIt(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(`
whenever({ name: "when", each: "linear.ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => {
    const cmds = ctx.herdr.worker(t).commands;
    const body = "at " + cmds.map((c) => c.at).join(",");
    return ctx.linear.queueComment(t, body, { once: ["when", t.id, body] });
  } });`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.CheckedOut("ENG-1", "working")
	d.Advance(time.Minute)
	first := d.Clock.Now().UnixMilli()
	d.Workers.Type("ENG-1", "/one")
	d.Advance(time.Minute)
	second := d.Clock.Now().UnixMilli()
	d.Workers.Type("ENG-1", "/two")

	d.Tick()
	require.Eventually(t, func() bool {
		return lastReadingOf(d, "ENG-1") == fmt.Sprintf("at %d,%d", first, second)
	}, 5*time.Second, 5*time.Millisecond)
}

func TestAKickoffHerdrNeverConfirmedLeavesNoNoteForAPersonsCommandToMatch(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Launcher: builtinLauncher, Rules: shippedRulesAnd(t, commandsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	tree := d.Trees.Pool()
	d.Workers.FailKickoff()
	_ = d.Run("tick")

	d.Workers.Prompt(tree, "worker", "/dispatch:build ENG-1")

	d.Workers.RunningIn("ENG-1", tree, "working")
	d.Restart()
	d.Tick()
	assert.Equal(t, "commands dispatch:build|ENG-1|false|person of 1", lastReading(t, d))
}

func TestARuleNudgeThatStartsWithASlashReadsAsSentByDispatch(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(commandsRule + nudgeRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.CheckedOut("ENG-1", "idle")
	d.Tick()
	require.Equal(t, []string{"w-ENG-1:p1|/merge-it 42"}, d.Workers.Prompts())

	d.Workers.Type("ENG-1", "/merge-it 42")
	d.Advance(5 * time.Minute)
	d.Tick()

	assert.Equal(t, "commands merge-it|42|false|dispatch of 1", lastReading(t, d))
}

func TestTheLaunchSubcommandKickoffReadsAsSentByDispatch(t *testing.T) {
	d := newDispatch(t, dispatchOptions{
		Launcher: builtinLauncher, Rules: rule(commandsRule), Env: map[string]string{"DISPATCH_PLUGIN_ROOT": "/plugin"},
	})
	tree := d.Trees.Pool()
	d.StartBeforeTheEvents()
	require.NoError(t, d.RunIn(d.Repo, "launch", "ENG-1", "br-eng-1").Err)
	require.Len(t, d.Workers.Kicked(), 1)

	pane, _, _ := strings.Cut(d.Workers.Kicked()[0], "|")
	d.Workers.SetPaneSession(pane, "launched")
	d.Workers.Prompt(tree, "launched", "/dispatch:build ENG-1")
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Restart()
	d.Tick()

	assert.Equal(t, "commands dispatch:build|ENG-1|false|dispatch of 1", lastReading(t, d))
}

func TestANudgeMatchesThePromptWhateverTrailingWhiteSpaceItHas(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(commandsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.CheckedOut("ENG-1", "idle")
	require.NoError(t, d.Run("tell", "ENG-1", "/merge-it 42").Err)

	d.Workers.Type("ENG-1", "/merge-it 42\n")
	d.Tick()

	assert.Equal(t, "commands merge-it|42|false|dispatch of 1", lastReading(t, d))
}

func TestANudgeToOneWorkerIsNotAnsweredByAPersonsCommandInAnother(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(commandsRule)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	d.Workers.CheckedOut("ENG-1", "idle")
	d.Workers.CheckedOut("ENG-2", "idle")
	require.NoError(t, d.Run("tell", "ENG-1", "/merge-it 42").Err)

	d.Workers.Type("ENG-2", "/merge-it 42")
	d.Tick()

	assert.Contains(t, d.Linear.Comments(), "ENG-2: commands merge-it|42|false|person of 1")
	assert.NotContains(t, d.Linear.Comments(), "ENG-2: commands merge-it|42|false|dispatch of 1")
}

func TestCommandsAreUnknownForAWorkerOfATicketThatIsNotInProgressOrInReview(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(anyStatusRule)})
	d.Linear.Tickets(ticket("ENG-1", "Todo", "dispatch"))
	d.Workers.CheckedOut("ENG-1", "idle")
	d.Workers.Type("ENG-1", "/merge-it 42")

	d.Tick()

	assert.Equal(t, "unknown", lastReading(t, d))
}
