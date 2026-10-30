package dispatch_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
)

const importStacking = `import "./stacking";`

func TestTheStackingExampleLaunchesATicketOnItsBlockersReadyPR(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: shippedRulesAnd(t, importStacking)})
	head := stackedPair(t, d)
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", prState{head: head, checks: "SUCCESS", mergeable: "MERGEABLE"})

	d.Tick()

	assert.Equal(t, []string{"w2:p2|/dispatch:build ENG-2 --base thrawn01/eng-1"}, d.fakes().Herdr.Kicked())
	assert.Equal(t, "ENG-1", gitConfig(t, d.Repo, "branch.thrawn01/eng-2.dispatchStackParent"))
	assert.Equal(t, head, gitConfig(t, d.Repo, "branch.thrawn01/eng-2.dispatchStackForkPoint"))
	assert.Empty(t, d.Linear.Comments())
}

func TestTheStackingExampleLeavesABlockerWithoutAReadyPRWaiting(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: shippedRulesAnd(t, importStacking)})
	head := stackedPair(t, d)
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", prState{head: head, checks: "FAILURE", mergeable: "MERGEABLE"})

	d.Tick()

	assert.Empty(t, d.fakes().Herdr.Kicked())
	assert.Empty(t, gitConfig(t, d.Repo, "branch.thrawn01/eng-2.dispatchStackParent"))
}

func TestTheStackingExampleRelaunchesATicketThatOwesARestackWithOneLaunch(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Rules: shippedRulesAnd(t, importStacking)})
	d.Trees.Pooled()
	const fork = "0123456789abcdef0123456789abcdef01234567"
	hintGit(t, d.Repo, "branch", "thrawn01/eng-2", "main")
	hintGit(t, d.Repo, "config", "branch.thrawn01/eng-2.dispatchStackParent", "ENG-1")
	hintGit(t, d.Repo, "config", "branch.thrawn01/eng-2.dispatchStackForkPoint", fork)
	// ENG-1 is Done, so ENG-2 has no open blocker and launch-worker would start a plain build.
	d.Linear.Tickets(ticket("ENG-2", "In Progress", "dispatch"))

	d.Tick()

	require.Len(t, d.receiptsOfKind("ENG-2", "launch"), 1)
	kicked := d.fakes().Herdr.Kicked()
	require.Len(t, kicked, 1)
	assert.Contains(t, kicked[0], "git rebase --onto origin/<default> "+fork)
	assert.Equal(t, fork, gitConfig(t, d.Repo, "branch.thrawn01/eng-2.dispatchStackForkPoint"))
}

func TestRulesTestAcceptsSeveralEntryFilesInRulesFrom(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	dir := filepath.Join(t.TempDir(), "rules")
	require.NoError(t, d.Run("rules", "init", dir).Err)
	tests, err := filepath.Glob(filepath.Join(dir, "*.test.ts"))
	require.NoError(t, err)
	for _, path := range tests {
		require.NoError(t, os.Remove(path))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "both.test.ts"), []byte(`
import { scenario, ticket, worker, pr, rulesFrom } from "dispatch/testing";

const rules = rulesFrom("./dispatch", "./stacking");

scenario(rules, "the entries load together", s => {
  s.given(ticket("ENG-1", { status: "in_review", optedIn: true, repo: "dispatch" }), worker("ENG-1", "idle"), pr("ENG-1", { problems: ["a failed check"] }));
  s.expect([["nudge-pr-needs-work", "nudge", "ENG-1"]]);
});
`), 0o644))

	res := d.RunWith(dispatch.Options{RulesDir: dir}, "rules", "test")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "1 scenarios, 1 passed, 0 failed")
}

func TestRulesTestReportsAnEntryFileThatDoesNotExist(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	dir := filepath.Join(t.TempDir(), "rules")
	require.NoError(t, d.Run("rules", "init", dir).Err)
	tests, err := filepath.Glob(filepath.Join(dir, "*.test.ts"))
	require.NoError(t, err)
	for _, path := range tests {
		require.NoError(t, os.Remove(path))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "missing.test.ts"), []byte(`
import { scenario, rulesFrom } from "dispatch/testing";

scenario(rulesFrom("./dispatch", "./nothing"), "never runs", s => {});
`), 0o644))

	res := d.RunWith(dispatch.Options{RulesDir: dir}, "rules", "test")

	require.ErrorContains(t, res.Err, "nothing")
}
