package dispatch_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/lineartest"
	tkt "github.com/kapetan-io/dispatch/internal/ticket"
)

func linearDispatch(t *testing.T, config string) *Dispatch {
	t.Helper()
	d := newDispatch(t, dispatchOptions{Config: config})
	d.StartBeforeTheEvents()
	return d
}

func TestTicketCreateLinearFilesATicketDispatchManages(t *testing.T) {
	t.Parallel()
	d := linearDispatch(t, "")

	out := d.ticketOut("create", "linear", "--repo", "dispatch", "--title", "Retry webhooks on 502", "--label", "feature", "--attr", "model=opus", "--description", "Webhooks fail.")

	assert.Equal(t, "ENG-901\n", out)
	issue := d.Linear.Issue("ENG-901")
	assert.Equal(t, "Retry webhooks on 502", issue.Title)
	assert.Equal(t, "Todo", issue.Status)
	assert.Equal(t, viewerID, issue.Assignee)
	assert.Equal(t, "Webhooks fail.\n\n## Attributes\nmodel: opus\n", issue.Description)
	assert.Equal(t, []tkt.Label{{Name: "ai"}, {Name: "dispatch", Group: "repo"}, {Name: "Feature"}}, issue.Labels)
	assert.Equal(t, map[string]string{"model": "opus"}, issue.Attributes)
}

func TestTicketCreateLinearWaitsInThePausedStatusItIsGiven(t *testing.T) {
	t.Parallel()
	d := linearDispatch(t, "")

	d.ticketOut("create", "linear", "--repo", "dispatch", "--title", "Idea", "--status", "backlog")
	res := d.Run("ticket", "create", "linear", "--repo", "dispatch", "--title", "Eager", "--status", "In Progress")

	assert.Equal(t, "Backlog", d.Linear.Issue("ENG-901").Status)
	require.ErrorContains(t, res.Err, `status "In Progress" is not a paused status`)
	assert.Len(t, d.tools.Linear.Issues(), 1)
}

func TestTicketCreateNeedsASystemAndNamesTheOnesItReads(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		config string
		args   []string
		want   string
	}{
		{name: "NoSystem", config: localOn, args: []string{"--repo", "dispatch", "--title", "x"}, want: "create takes a ticket system first: linear or local"},
		{name: "NoArguments", config: localOn, want: "create takes a ticket system first: linear or local"},
		{name: "UnknownSystem", config: localOn, args: []string{"jira", "--repo", "dispatch", "--title", "x"}, want: "create takes a ticket system first: linear or local"},
		{name: "LocalOffNamesLinearOnly", args: []string{"--repo", "dispatch", "--title", "x"}, want: "create takes a ticket system first: linear"},
		{name: "LocalOff", args: []string{"local", "--repo", "dispatch", "--title", "x"}, want: "local tickets are off"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := linearDispatch(t, test.config)

			res := d.Run(append([]string{"ticket", "create"}, test.args...)...)

			require.ErrorContains(t, res.Err, test.want)
			assert.Equal(t, 1, res.Code)
			assert.Empty(t, d.tools.Linear.Issues())
		})
	}
}

func TestTicketCreateLinearRefusesWhatItCannotFileAndFilesNothing(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "MissingLabel", args: []string{"--label", "Nope"}, want: `has no "Nope" label`},
		{name: "UnknownRepo", args: []string{"--repo", "elsewhere"}, want: `"elsewhere" is not a [repos.<label>] table`},
		{name: "Parent", args: []string{"--parent", "ENG-1"}, want: "create linear does not take --parent yet"},
		{name: "BlockedBy", args: []string{"--blocked-by", "ENG-1"}, want: "create linear does not take --blocked-by yet"},
		{name: "StartPR", args: []string{"--start-pr", "7"}, want: "create linear does not take --start-pr yet"},
		{name: "BadAttribute", args: []string{"--attr", "a:b=c"}, want: "must be a plain word"},
		{name: "UnknownTeam", args: []string{"--team", "OPS"}, want: "team OPS is not one dispatch reads; the configured teams are ENG"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := linearDispatch(t, localOn)
			args := append([]string{"ticket", "create", "linear", "--repo", "dispatch", "--title", "x"}, test.args...)

			res := d.Run(args...)

			require.ErrorContains(t, res.Err, test.want)
			assert.Empty(t, d.tools.Linear.Issues())
		})
	}
}

func TestTicketCreateLinearPicksTheTeamWithTeam(t *testing.T) {
	t.Parallel()
	d := linearDispatch(t, "[plugins.linear]\nteams = [\"ENG\", \"OPS\"]\n")

	res := d.Run("ticket", "create", "linear", "--repo", "dispatch", "--title", "x")
	assert.ErrorContains(t, res.Err, "create linear needs --team, one of ENG, OPS")
	assert.Equal(t, "ENG-901\n", d.ticketOut("create", "linear", "--repo", "dispatch", "--title", "x", "--team", "ENG"))
}

func TestTicketCreateLocalRefusesTeam(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)

	res := d.Run("ticket", "create", "local", "--repo", "dispatch", "--title", "x", "--team", "ENG")

	require.ErrorContains(t, res.Err, "--team applies to create linear only")
}

func TestTicketEditSetsAndRemovesLinearAttributesAndKeepsTheRestOfTheText(t *testing.T) {
	t.Parallel()
	d := linearDispatch(t, "")
	text := "Intro line.\n\n## Attributes\nmodel: sonnet\nowner: ada\n\n## Notes\nkeep this.\n"
	ticket := ticket("ENG-1", "In Progress", "dispatch")
	ticket.Description = text
	d.Linear.Tickets(ticket)

	out := d.ticketOut("edit", "eng-1", "--attr", "model=opus", "--attr", "effort=high", "--unset-attr", "owner")

	assert.Equal(t, "ENG-1\n", out)
	want := "Intro line.\n\n## Attributes\nmodel: opus\neffort: high\n\n## Notes\nkeep this.\n"
	assert.Equal(t, want, d.Linear.Issue("ENG-1").Description)
	assert.Equal(t, []string{"ENG-1: " + want}, d.Linear.Descriptions())
}

func TestTicketEditAddsTheAttributesSectionToATicketWithout(t *testing.T) {
	t.Parallel()
	d := linearDispatch(t, "")
	ticket := ticket("ENG-1", "Todo", "dispatch")
	ticket.Description = "Just text."
	d.Linear.Finished(ticket)

	d.ticketOut("edit", "ENG-1", "--attr", "model=opus")

	assert.Equal(t, "Just text.\n\n## Attributes\nmodel: opus\n", d.Linear.Issue("ENG-1").Description)
}

func TestTicketEditWritesNothingWhenNothingChanges(t *testing.T) {
	t.Parallel()
	d := linearDispatch(t, "")
	ticket := ticket("ENG-1", "In Progress", "dispatch")
	ticket.Description = "Text.\n\n## Attributes\nmodel: opus\n"
	d.Linear.Tickets(ticket)

	d.ticketOut("edit", "ENG-1", "--attr", "model=opus", "--unset-attr", "absent")

	assert.Empty(t, d.Linear.Descriptions())
}

func TestTicketEditRefusesALinearTicketDispatchDoesNotManage(t *testing.T) {
	t.Parallel()
	d := linearDispatch(t, "")
	stranger := ticket("ENG-1", "In Progress", "dispatch")
	stranger.Assignee = "someone-else"
	unlabeled := ticket("ENG-2", "In Progress", "dispatch")
	unlabeled.Labels = nil
	d.Linear.Tickets(stranger, unlabeled)

	for _, id := range []string{"ENG-1", "ENG-2", "ENG-404"} {
		res := d.Run("ticket", "edit", id, "--attr", "model=opus")

		require.ErrorContains(t, res.Err, "dispatch doesn't manage this ticket")
	}
	assert.Empty(t, d.Linear.Descriptions())
}

func TestTicketEditOfALinearTicketChangesOnlyAttributes(t *testing.T) {
	t.Parallel()
	d := linearDispatch(t, "")
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	for _, flag := range [][]string{{"--title", "new"}, {"--description", "new"}, {"--add-label", "x"}, {"--parent", ""}} {
		res := d.Run(append([]string{"ticket", "edit", "ENG-1"}, flag...)...)

		require.ErrorContains(t, res.Err, "edit changes only attributes")
	}
	assert.Empty(t, d.Linear.Descriptions())
}

func TestTicketShowMoveAndCommentStillRefuseALinearTicket(t *testing.T) {
	t.Parallel()
	d := linearDispatch(t, localOn)

	for _, args := range [][]string{{"show", "ENG-1"}, {"move", "ENG-1", "Done"}, {"comment", "ENG-1", "hi"}} {
		res := d.Run(append([]string{"ticket"}, args...)...)

		require.EqualError(t, res.Err, "ENG-1 is a Linear ticket; use the Linear tools")
	}
}

func TestTickLaunchesALinearTicketOnTheModelAnEditSet(t *testing.T) {
	t.Parallel()
	d := linearDispatch(t, "")
	d.Linear.Tickets(ticket("ENG-1", "Todo", "dispatch"))
	d.ticketOut("edit", "ENG-1", "--attr", "model=opus")
	d.Linear.Tickets(func() fakeTicket { t := d.Linear.Issue("ENG-1"); t.Status = "In Progress"; return t }())

	d.Tick()

	assert.Equal(t, []string{d.Repo + " ENG-1 thrawn01/eng-1 --model opus"}, d.Workers.Launched())
}

func TestTicketCreateLinearAndEditMakeTheLoopReadLinearAgain(t *testing.T) {
	t.Parallel()
	d := linearDispatch(t, "")
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.settleLoop()

	before := d.tools.Linear.CallsTo(lineartest.OnManaged)
	d.ticketOut("create", "linear", "--repo", "dispatch", "--title", "x")
	d.settleLoop()
	afterCreate := d.tools.Linear.CallsTo(lineartest.OnManaged)
	d.ticketOut("edit", "ENG-1", "--attr", "model=opus")
	d.settleLoop()

	assert.Greater(t, afterCreate, before)
	assert.Greater(t, d.tools.Linear.CallsTo(lineartest.OnManaged), afterCreate)
}

func TestTicketCreateLinearNeedsTodoInPausedWhenNoStatusIsGiven(t *testing.T) {
	t.Parallel()
	d := linearDispatch(t, "[plugins.linear.statuses]\npaused = [\"Backlog\"]\n")

	res := d.Run("ticket", "create", "linear", "--repo", "dispatch", "--title", "x")

	require.ErrorContains(t, res.Err, `status "Todo" is not a paused status`)
	assert.Empty(t, d.tools.Linear.Issues())
}

func TestTicketCreateLinearMatchesRepoAndTeamIgnoringCase(t *testing.T) {
	t.Parallel()
	d := linearDispatch(t, "")

	assert.Equal(t, "ENG-901\n", d.ticketOut("create", "linear", "--repo", "Dispatch", "--team", "eng", "--title", "x"))
	assert.Contains(t, d.Linear.Issue("ENG-901").Labels, tkt.Label{Name: "dispatch", Group: "repo"})
}

func TestTicketCreateLinearTakesTheSystemInAnyCase(t *testing.T) {
	t.Parallel()
	d := linearDispatch(t, "")

	assert.Equal(t, "ENG-901\n", d.ticketOut("create", "Linear", "--repo", "dispatch", "--title", "x"))
}

func TestTicketEditOfALinearTicketNeedsAnAttributeToChange(t *testing.T) {
	t.Parallel()
	d := linearDispatch(t, "")
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("ticket", "edit", "ENG-1")

	require.ErrorContains(t, res.Err, "edit needs --attr or --unset-attr")
}

// within waits up to ten seconds for a command started with later, and fails the test when it is
// still running, so a loop that stalls fails the test instead of hanging it.
func within[T any](t *testing.T, wait func() T) T {
	t.Helper()
	done := make(chan T, 1)
	go func() { done <- wait() }()
	select {
	case v := <-done:
		return v
	case <-time.After(10 * time.Second):
		require.FailNow(t, "the command did not return in 10 seconds")
		var zero T
		return zero
	}
}

func TestTickIsAnsweredWhileACreateLinearWaitsOnLinear(t *testing.T) {
	t.Parallel()
	d := linearDispatch(t, "")
	release := d.tools.Linear.Hold(lineartest.OnCreate)
	defer release()
	create := later(func() Result { return d.Run("ticket", "create", "linear", "--repo", "dispatch", "--title", "x") })
	require.Eventually(t, func() bool { return d.tools.Linear.Held(lineartest.OnCreate) == 1 }, 10*time.Second, time.Millisecond)

	tick := within(t, later(func() Result { return d.Run("tick") }))

	require.NoError(t, tick.Err)
	assert.Equal(t, 1, d.tools.Linear.Held(lineartest.OnCreate))
	release()
	res := within(t, create)
	require.NoError(t, res.Err)
	assert.Equal(t, "ENG-901\n", res.Stdout)
}

func TestAWorkerEventIsActedOnWhileAnEditWaitsOnLinear(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[rules]\nnudge_limit = 10\n"})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "working")
	d.Workers.Running("ENG-2", "working")
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "FAILURE", mergeable: "MERGEABLE"})
	d.StartBeforeTheEvents()
	d.settleLoop()
	release := d.tools.Linear.Hold(lineartest.OnDescribe)
	defer release()
	edit := later(func() Result { return d.Run("ticket", "edit", "ENG-2", "--attr", "model=opus") })
	require.Eventually(t, func() bool { return d.tools.Linear.Held(lineartest.OnDescribe) == 1 }, 10*time.Second, time.Millisecond)

	d.Workers.SetStatus("ENG-1", "idle")
	d.Workers.Stop("ENG-1", stopTasks{})
	require.NoError(t, d.Hook("wake", d.Workers.StopInput("ENG-1", stopTasks{})).Err)

	require.Eventually(t, func() bool { return len(d.Workers.Received("ENG-1")) == 1 }, 10*time.Second, time.Millisecond)
	assert.Equal(t, 1, d.tools.Linear.Held(lineartest.OnDescribe))
	release()
	require.NoError(t, within(t, edit).Err)
}

func TestTwoEditsOfOneLinearTicketAtOnceBothLand(t *testing.T) {
	t.Parallel()
	d := linearDispatch(t, "")
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	release := d.tools.Linear.Hold(lineartest.OnDescribe)
	defer release()
	first := later(func() Result { return d.Run("ticket", "edit", "ENG-1", "--attr", "model=opus") })
	second := later(func() Result { return d.Run("ticket", "edit", "ENG-1", "--attr", "effort=high") })
	require.Eventually(t, func() bool { return d.tools.Linear.Held(lineartest.OnDescribe) == 1 }, 10*time.Second, time.Millisecond)

	// The second edit reads the description only after the first has written it.
	assert.Never(t, func() bool { return d.tools.Linear.Held(lineartest.OnDescribe) > 1 }, 200*time.Millisecond, time.Millisecond)
	release()

	require.NoError(t, within(t, first).Err)
	require.NoError(t, within(t, second).Err)
	assert.Equal(t, map[string]string{"model": "opus", "effort": "high"}, d.Linear.Issue("ENG-1").Attributes)
}

func TestStoppingDispatchAnswersTheLinearWritesItWasWaitingOn(t *testing.T) {
	t.Parallel()
	d := linearDispatch(t, "")
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	releaseEdits := d.tools.Linear.Hold(lineartest.OnDescribe)
	defer releaseEdits()
	releaseCreates := d.tools.Linear.Hold(lineartest.OnCreate)
	defer releaseCreates()
	create := later(func() Result { return d.Run("ticket", "create", "linear", "--repo", "dispatch", "--title", "x") })
	first := later(func() Result { return d.Run("ticket", "edit", "ENG-1", "--attr", "model=opus") })
	second := later(func() Result { return d.Run("ticket", "edit", "ENG-1", "--attr", "effort=high") })
	require.Eventually(t, func() bool {
		return d.tools.Linear.Held(lineartest.OnCreate) == 1 && d.tools.Linear.Held(lineartest.OnDescribe) == 1
	}, 10*time.Second, time.Millisecond)
	assert.Never(t, func() bool { return d.tools.Linear.Held(lineartest.OnDescribe) > 1 }, 200*time.Millisecond, time.Millisecond)

	d.Stop()

	// Each answer is Linear's own, so dispatch waited for the writes before it stopped.
	for _, wait := range []func() Result{create, first, second} {
		assert.ErrorContains(t, within(t, wait).Err, "context canceled")
	}
	assert.Empty(t, d.tools.Linear.Changes())
}

func TestAFailedLinearWriteIsReportedAndLinearIsNotReadAgain(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		operation string
		args      []string
	}{
		{name: "Create", operation: lineartest.OnCreate, args: []string{"create", "linear", "--repo", "dispatch", "--title", "x"}},
		{name: "Edit", operation: lineartest.OnSetText, args: []string{"edit", "ENG-1", "--attr", "model=opus"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := linearDispatch(t, "")
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
			d.settleLoop()
			d.tools.Linear.FailOn(test.operation, true)
			before := d.tools.Linear.CallsTo(lineartest.OnManaged)

			res := d.Run(append([]string{"ticket"}, test.args...)...)
			d.settleLoop()

			require.ErrorContains(t, res.Err, "HTTP 500")
			assert.Equal(t, before, d.tools.Linear.CallsTo(lineartest.OnManaged))
		})
	}
}
