package dispatch_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/linear"
)

const harnessTables = `
[harness.claude-sonnet]
kind = "claude-code"
model = "sonnet"

[harness.claude-opus]
kind = "claude-code"
model = "opus"
`

func TestConfigRefusesHarnessKeysAndTablesThatAreWrong(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		config string
		repo   string
		want   string
	}{
		{"RulesNamesMissingTable", harnessTables + "[rules]\nworker = \"harness.claude-snnet\"\n", "",
			`rules.worker names harness.claude-snnet, which has no [harness.claude-snnet] table`},
		{"RulesDesignNamesMissingTable", harnessTables + "[rules]\ndesign = \"harness.nope\"\n", "",
			`rules.design names harness.nope, which has no [harness.nope] table`},
		{"RulesKeyWithoutPrefix", harnessTables + "[rules]\nworker = \"opus\"\n", "",
			`rules.worker must be "harness.<name>", not "opus"`},
		{"RulesKeyNotAString", harnessTables + "[rules]\nworker = 3\n", "",
			`rules.worker must be a string`},
		{"RepoNamesMissingTable", harnessTables, "worker = \"harness.nope\"\n",
			`repos.dispatch.worker names harness.nope, which has no [harness.nope] table`},
		{"RepoKeyWithoutPrefix", harnessTables, "design = \"opus\"\n",
			`repos.dispatch.design must be "harness.<name>", not "opus"`},
		{"UnknownKind", "[harness.pi-flash]\nkind = \"pi\"\nmodel = \"flash\"\n", "",
			`harness.pi-flash: kind "pi" is not a harness this dispatch drives (claude-code)`},
		{"MissingKind", "[harness.bare]\nmodel = \"opus\"\n", "",
			`harness.bare: kind is missing`},
		{"MissingModel", "[harness.claude-fast]\nkind = \"claude-code\"\n", "",
			`harness.claude-fast: model is missing`},
		{"MisspelledKey", "[harness.claude-fast]\nkind = \"claude-code\"\nmodle = \"opus\"\n", "",
			`unknown setting harness.claude-fast.modle`},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Config: test.config, RepoSettings: test.repo})

			res := d.Run("tick")

			require.ErrorContains(t, res.Err, test.want)
		})
	}
}

func TestConfigTreatsAnEmptyRoleKeyAsUnset(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[rules]\nworker = \"\"\n", RepoSettings: "worker = \"\"\n"})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	d.Tick()

	assert.Equal(t, []string{d.Repo + " ENG-1 thrawn01/eng-1 --model sonnet"}, d.Workers.Launched())
}

func TestTickLaunchesBuildWorkersOnTheTableRulesWorkerNames(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: harnessTables + "[rules]\nworker = \"harness.claude-opus\"\n"})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	d.Tick()

	assert.Equal(t, []string{d.Repo + " ENG-1 thrawn01/eng-1 --model opus"}, d.Workers.Launched())
}

func TestTickLaunchesDesignWorkersOnTheTableRulesDesignNames(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: harnessTables + "[rules]\nworker = \"harness.claude-opus\"\ndesign = \"harness.claude-sonnet\"\n"})
	d.Linear.Tickets(designTicket("ENG-1", "In Progress"))

	d.Tick()

	assert.Equal(t, []string{d.Repo + " ENG-1 thrawn01/eng-1 --model sonnet --kickoff /dispatch:design ENG-1"}, d.Workers.Launched())
}

func TestTickLaunchesARepoOnItsOwnTableOverRules(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{
		Config:       harnessTables + "[rules]\nworker = \"harness.claude-sonnet\"\ndesign = \"harness.claude-sonnet\"\n",
		RepoSettings: "worker = \"harness.claude-opus\"\n",
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), designTicket("ENG-2", "In Progress"))

	d.Tick()

	assert.ElementsMatch(t, []string{
		d.Repo + " ENG-1 thrawn01/eng-1 --model opus",
		d.Repo + " ENG-2 thrawn01/eng-2 --model sonnet --kickoff /dispatch:design ENG-2",
	}, d.Workers.Launched())
}

func TestTickLaunchesADifferentRepoOnTheRulesTable(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{
		Config:       harnessTables + "[rules]\nworker = \"harness.claude-sonnet\"\n",
		RepoSettings: "worker = \"harness.claude-opus\"\n",
	})
	other := t.TempDir()
	d.addRepo("webapp", other)
	d.Linear.Tickets(labeledAs("ENG-1", linear.Label{Name: "ai"}, linear.Label{Name: "webapp", Group: "repo"}))

	d.Tick()

	assert.Equal(t, []string{other + " ENG-1 thrawn01/eng-1 --model sonnet"}, d.Workers.Launched())
}

func TestTickLaunchesADesignWorkerOnOpusWhenTheConfigLacksDesignModel(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: "[rules]\nworker_model = \"sonnet\"\nlaunch_failure_limit = 2\n"})
	d.ExactRules()
	d.Linear.Tickets(designTicket("ENG-1", "In Progress"))

	d.Tick()

	assert.Equal(t, []string{d.Repo + " ENG-1 thrawn01/eng-1 --model opus --kickoff /dispatch:design ENG-1"}, d.Workers.Launched())
}

func TestTickRefusesALaunchThatPassesBothHarnessAndModel(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{
		Config: harnessTables,
		Rules: rule(`whenever({ name: "both", each: "ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.worker.queueLaunch(t, { harness: "harness.claude-opus", model: "opus", once: ["both"] }) });`),
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("tick")

	require.ErrorContains(t, res.Err, `ENG-1 launch refused: launch of ENG-1 passes both harness "harness.claude-opus" and model "opus"`)
	assert.Empty(t, d.Workers.Launched())
}

func TestTickRefusesALaunchOnAHarnessWithNoTable(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{
		Config: harnessTables,
		Rules: rule(`whenever({ name: "missing", each: "ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.worker.queueLaunch(t, { harness: "harness.claude-snnet", once: ["missing"] }) });`),
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("tick")

	require.ErrorContains(t, res.Err, `ENG-1 launch refused: launch of ENG-1 passes harness "harness.claude-snnet": harness.claude-snnet has no [harness.claude-snnet] table`)
	assert.Empty(t, d.Workers.Launched())
}

func TestTickLaunchesARuleWhoseHarnessNamesATable(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{
		Config: harnessTables,
		Rules: rule(`whenever({ name: "named", each: "ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.worker.queueLaunch(t, { harness: "harness.claude-opus", once: ["named"] }) });`),
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	d.Tick()

	assert.Equal(t, []string{d.Repo + " ENG-1 thrawn01/eng-1 --model opus"}, d.Workers.Launched())
}

func TestRulesCannotReadTheHarnessTables(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{
		Config: harnessTables,
		Rules:  rule(`whenever({ name: "peek", each: "ticket", if: (t, ctx) => (ctx.config as any).harness !== undefined, then: () => {} });`),
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `peek +whenever +ticket +wants nothing now`, res.Stdout)
}

func TestLaunchStartsTheWorkerOnTheModelOfTheHarnessTable(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Config: harnessTables})
	d.Trees.Pooled()

	out := d.launch("ENG-1", "thrawn01/eng-1", "--harness", "harness.claude-opus")

	assert.Equal(t, "ok", out.result["status"])
	require.Len(t, d.fakes().Herdr.Started(), 1)
	assert.Equal(t, "opus", d.fakes().Herdr.Started()[0].Model)
}

func TestLaunchRefusesHarnessAndModelTogether(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Config: harnessTables})
	d.Trees.Pooled()

	out := d.launch("ENG-1", "thrawn01/eng-1", "--harness", "harness.claude-opus", "--model", "opus")

	assert.Equal(t, "usage_error", out.result["status"])
	assert.Contains(t, out.result["detail"], "--harness and --model together")
	assert.Empty(t, d.fakes().Herdr.Started())
}

func TestLaunchRefusesAHarnessThatNamesNoTable(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, harness, want string
	}{
		{"MissingTable", "harness.nope", "harness.nope has no [harness.nope] table"},
		{"NoPrefix", "opus", `"opus" must be "harness.<name>"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Env: launchEnv, Launcher: builtinLauncher, Config: harnessTables})
			d.Trees.Pooled()

			out := d.launch("ENG-1", "thrawn01/eng-1", "--harness", test.harness)

			assert.Equal(t, "usage_error", out.result["status"])
			assert.Contains(t, out.result["detail"], test.want)
			assert.Empty(t, d.fakes().Herdr.Started())
		})
	}
}

func TestRepoSettingsPrintsNothingForAnUnsetWorkerAndDesign(t *testing.T) {
	t.Parallel()
	clone := newHintRepo(t)
	d := newDispatch(t, dispatchOptions{OwnRepos: true, Config: harnessTables + "[repos.dispatch]\npath = \"" + clone + "\"\n"})

	res := d.RunIn(clone, "repo-settings")

	require.NoError(t, res.Err)
	assert.NotContains(t, res.Stdout, "worker")
	assert.NotContains(t, res.Stdout, "design")
}

func TestRepoSettingsPrintsASetWorkerAndLoadsBack(t *testing.T) {
	t.Parallel()
	clone := newHintRepo(t)
	original := newDispatch(t, dispatchOptions{OwnRepos: true, Config: harnessTables +
		"[repos.dispatch]\npath = \"" + clone + "\"\nworker = \"harness.claude-opus\"\n"})
	printed := original.RunIn(clone, "repo-settings")
	require.NoError(t, printed.Err)
	assert.Contains(t, printed.Stdout, "worker = \"harness.claude-opus\"\n")
	loaded := newDispatch(t, dispatchOptions{OwnRepos: true, Config: harnessTables + printed.Stdout})

	res := loaded.RunIn(clone, "repo-settings")

	require.NoError(t, res.Err)
	assert.Equal(t, printed.Stdout, res.Stdout)
}
