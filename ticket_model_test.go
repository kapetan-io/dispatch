package dispatch_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTickLaunchesALocalTicketOnItsModelAttribute(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.fileLocal("In Progress", "--attr", "model=opus")
	d.fileLocal("In Progress")

	d.Tick()

	assert.ElementsMatch(t, []string{
		d.Repo + " LOC-1 loc-1-work --model opus",
		d.Repo + " LOC-2 loc-2-work --model sonnet",
	}, d.Workers.Launched())
}

func TestTickIgnoresABlankModelAttributeAndTrimsAPaddedOne(t *testing.T) {
	t.Parallel()
	d := localDispatch(t)
	d.fileLocal("In Progress", "--attr", "model= ")
	d.fileLocal("In Progress", "--attr", "model= opus")

	d.Tick()

	assert.ElementsMatch(t, []string{
		d.Repo + " LOC-1 loc-1-work --model sonnet",
		d.Repo + " LOC-2 loc-2-work --model opus",
	}, d.Workers.Launched())
}

func TestTickRelaunchesAGoneWorkerOnItsModelAttribute(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	withModel := ticket("ENG-1", "In Review", "dispatch")
	withModel.Attributes = map[string]string{"model": "opus"}
	d.Linear.Tickets(withModel)
	d.Workers.Running("ENG-1", "idle")
	pr := cleanPR("a1")
	d.GitHub.PullRequest("ENG-1", pr)
	d.Tick()
	d.Advance(time.Second)
	d.Workers.Gone("ENG-1")
	pr.threads = []activity{{"", time2, "alice", "", ""}}
	d.GitHub.PullRequest("ENG-1", pr)

	d.Tick()

	assert.Equal(t, []string{d.Repo + " ENG-1 thrawn01/eng-1 --model opus"}, d.Workers.Launched())
}

func TestTickLaunchesALinearTicketOnItsModelAttributeInsteadOfTheRepoTable(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{
		Config:       harnessTables + "[rules]\nworker = \"harness.claude-sonnet\"\n",
		RepoSettings: "worker = \"harness.claude-sonnet\"\n",
	})
	withModel := ticket("ENG-1", "In Progress", "dispatch")
	withModel.Attributes = map[string]string{"model": "opus"}
	d.Linear.Tickets(withModel, ticket("ENG-2", "In Progress", "dispatch"))

	d.Tick()

	assert.ElementsMatch(t, []string{
		d.Repo + " ENG-1 thrawn01/eng-1 --model opus",
		d.Repo + " ENG-2 thrawn01/eng-2 --model sonnet",
	}, d.Workers.Launched())
}

func TestTickLaunchesADesignTicketOnItsModelAttribute(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	design := designTicket("ENG-1", "In Progress")
	design.Attributes = map[string]string{"model": "sonnet"}
	d.Linear.Tickets(design)

	d.Tick()

	assert.Equal(t, []string{d.Repo + " ENG-1 thrawn01/eng-1 --model sonnet --kickoff /dispatch:design ENG-1"}, d.Workers.Launched())
}

func TestExplainShowsTheModelALaunchWanted(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	withModel := ticket("ENG-1", "In Progress", "dispatch")
	withModel.Attributes = map[string]string{"model": "opus"}
	d.Linear.Tickets(withModel)
	d.Tick()
	d.Stop()

	text := d.Run("explain", "ENG-1")
	asJSON := d.Run("explain", "ENG-1", "--json")

	require.NoError(t, text.Err)
	require.NoError(t, asJSON.Err)
	assert.Regexp(t, `launch-worker  wants launch once \[[^\]]*\] on model opus`, text.Stdout)
	var found bool
	for _, line := range traceJSON(t, asJSON.Stdout) {
		if line["decision"] == "wants" && line["kind"] == "launch" {
			assert.Equal(t, "opus", line["model"])
			found = true
		}
	}
	assert.True(t, found)
}

func TestExplainShowsTheHarnessTableALaunchWanted(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: harnessTables + "[rules]\nworker = \"harness.claude-opus\"\n"})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()

	res := d.Run("explain", "ENG-1")

	require.NoError(t, res.Err)
	assert.Regexp(t, `launch-worker  wants launch once \[[^\]]*\] on harness.claude-opus`, res.Stdout)
}
