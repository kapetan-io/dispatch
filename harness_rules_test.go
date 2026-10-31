package dispatch_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/linear"
)

func TestTickLaunchesATicketOnTheTableOfARepoWhoseLabelDiffersInCase(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{
		Config:       harnessTables + "[rules]\nworker = \"harness.claude-sonnet\"\n",
		RepoSettings: "worker = \"harness.claude-opus\"\n",
	})
	d.Linear.Tickets(labeledAs("ENG-1", linear.Label{Name: "ai"}, linear.Label{Name: "Dispatch", Group: "repo"}))

	d.Tick()

	assert.Equal(t, []string{d.Repo + " ENG-1 thrawn01/eng-1 --model opus"}, d.Workers.Launched())
}

func TestTheStackingExampleLaunchesTheStackedTicketOnTheRepoTable(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{
		Env: launchEnv, Launcher: builtinLauncher, Rules: shippedRulesAnd(t, importStacking),
		Config: harnessTables + "[rules]\nworker = \"harness.claude-sonnet\"\n", RepoSettings: "worker = \"harness.claude-opus\"\n",
	})
	head := stackedPair(t, d)
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", prState{head: head, checks: "SUCCESS", mergeable: "MERGEABLE"})

	d.Tick()

	require.Len(t, d.fakes().Herdr.Started(), 1)
	assert.Equal(t, "opus", d.fakes().Herdr.Started()[0].Model)
	assert.Equal(t, []string{"w2:p2|/dispatch:build ENG-2 --base thrawn01/eng-1"}, d.fakes().Herdr.Kicked())
}

func TestTheStackingExampleRelaunchesARestackOnTheRepoTable(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{
		Env: launchEnv, Launcher: builtinLauncher, Rules: shippedRulesAnd(t, importStacking),
		Config: harnessTables + "[rules]\nworker = \"harness.claude-sonnet\"\n", RepoSettings: "worker = \"harness.claude-opus\"\n",
	})
	d.Trees.Pooled()
	const fork = "0123456789abcdef0123456789abcdef01234567"
	hintGit(t, d.Repo, "branch", "thrawn01/eng-2", "main")
	hintGit(t, d.Repo, "config", "branch.thrawn01/eng-2.dispatchStackParent", "ENG-1")
	hintGit(t, d.Repo, "config", "branch.thrawn01/eng-2.dispatchStackForkPoint", fork)
	d.Linear.Tickets(ticket("ENG-2", "In Progress", "dispatch"))

	d.Tick()

	require.Len(t, d.fakes().Herdr.Started(), 1)
	assert.Equal(t, "opus", d.fakes().Herdr.Started()[0].Model)
	require.Len(t, d.receiptsOfKind("ENG-2", "launch"), 1)
}
