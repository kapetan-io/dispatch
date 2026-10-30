package dispatch_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdrtest"
	"github.com/kapetan-io/dispatch/internal/treehouse"
)

// greetRule comments "hi" once on every In Progress ticket, and a second rule comments "bye" on the same tickets.
const greetRule = `
import { exempt } from "dispatch";
whenever({ name: "greet", each: "ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.ticket.queueComment(t, "hi", { once: ["greet", t.id] }) });
whenever({ name: "farewell", each: "ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.ticket.queueComment(t, "bye", { once: ["farewell", t.id] }) });
`

func TestAnExemptionStopsTheRulesItNamesForTheRecordsItIsTrueOf(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: rule(greetRule + `
exempt({ name: "skip-eng-1", each: "ticket", rules: ["greet"], if: (t) => t.id === "ENG-1" });`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))

	d.Tick()

	assert.ElementsMatch(t, []string{"ENG-1: bye", "ENG-2: hi", "ENG-2: bye"}, d.Linear.Comments())
}

func TestAnExemptionNamingTwoRulesStopsBoth(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: rule(greetRule + `
exempt({ name: "skip-eng-1", each: "ticket", rules: ["greet", "farewell"], if: (t) => t.id === "ENG-1" });`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))

	d.Tick()

	assert.ElementsMatch(t, []string{"ENG-2: hi", "ENG-2: bye"}, d.Linear.Comments())
}

func TestAnExemptionDecidesEachRecordSharingASubject(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: rule(`
import { exempt } from "dispatch";
whenever({ name: "note", each: "tree", if: () => true,
  then: (tr, ctx) => ctx.ticket.queueComment(tr.holder, "tree " + tr.path, { once: ["note", tr.path] }) });
exempt({ name: "skip-a", each: "tree", rules: ["note"], if: (tr) => tr.path.endsWith("/a") });`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.tools.Treehouse.Clear(d.Repo)
	d.tools.Treehouse.Seed(d.Repo, treehouse.Tree{Path: d.dir + "/a", Status: "leased", LeaseHolder: "ENG-1"})
	d.tools.Treehouse.Seed(d.Repo, treehouse.Tree{Path: d.dir + "/b", Status: "leased", LeaseHolder: "ENG-1"})

	d.Tick()

	assert.Equal(t, []string{"ENG-1: tree " + d.dir + "/b"}, d.Linear.Comments())
}

func TestAnExemptionThatIsFalseLeavesTheRulesAsTheyWere(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: rule(greetRule + `
exempt({ name: "skip-nobody", each: "ticket", rules: ["greet", "farewell"], if: () => false });`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	d.Tick()

	assert.ElementsMatch(t, []string{"ENG-1: hi", "ENG-1: bye"}, d.Linear.Comments())
}

func TestAnExemptionReadingAnUnknownFactHoldsTheRulesItNames(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: rule(greetRule + `
exempt({ name: "skip-idle", each: "ticket", rules: ["greet"], if: (t, ctx) => ctx.worker.of(t).state === "idle" });`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tools().Herdr.Fail(herdrtest.OpRead, errors.New("herdr is down"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `greet +whenever +ticket +wants nothing now; held for ENG-1\n`, res.Stdout)
	assert.Regexp(t, `farewell +whenever +ticket +wants comment now for ENG-1\n`, res.Stdout)
}

func TestAHeldExemptionActsOnceItCanBeRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: rule(greetRule + `
exempt({ name: "skip-idle", each: "ticket", rules: ["greet"], if: (t, ctx) => ctx.worker.of(t).state === "idle" });`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tools().Herdr.Fail(herdrtest.OpRead, errors.New("herdr is down"))

	d.Tick()

	assert.Equal(t, []string{"ENG-1: bye"}, d.Linear.Comments())
	d.Tools().Herdr.Fail(herdrtest.OpRead, nil)
	d.Tick()
	assert.ElementsMatch(t, []string{"ENG-1: bye", "ENG-1: hi"}, d.Linear.Comments())
}

func TestAnExemptionThatThrowsStopsTheRulesItNames(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: rule(greetRule + `
exempt({ name: "broken", each: "ticket", rules: ["greet"], if: () => { throw new Error("boom"); } });`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	d.Tick()
	res := d.Run("rules", "check")

	assert.Equal(t, []string{"ENG-1: bye"}, d.Linear.Comments())
	require.Error(t, res.Err)
	assert.Contains(t, res.Stdout, "finding: broken threw in its if for ENG-1: Error: boom")
}

func TestARulesCheckNamesTheExemptionThatStoppedARule(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: rule(greetRule + `
exempt({ name: "skip-eng-1", each: "ticket", rules: ["greet"], if: (t) => t.id === "ENG-1" });`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `greet +whenever +ticket +wants comment now for ENG-2; exempt for ENG-1 by skip-eng-1\n`, res.Stdout)
	assert.Regexp(t, `farewell +whenever +ticket +wants comment now for ENG-1, ENG-2\n`, res.Stdout)
	assert.Regexp(t, `skip-eng-1 +exempt +ticket +exempts greet\n`, res.Stdout)
	assert.Empty(t, d.Linear.Comments())
}

func TestAShadowRuleAnExemptionStopsReportsNothingForThatRecord(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: rule(`
import { exempt } from "dispatch";
whenever({ name: "dry", each: "ticket", shadow: true, if: () => true,
  then: (t, ctx) => ctx.ticket.queueComment(t, "hi", { once: ["dry", t.id] }) });
exempt({ name: "skip-eng-1", each: "ticket", rules: ["dry"], if: (t) => t.id === "ENG-1" });`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `dry +whenever +ticket +shadow: would comment now for ENG-2; exempt for ENG-1 by skip-eng-1\n`, res.Stdout)
}

func TestExplainNamesTheExemptionThatStoppedARule(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: rule(greetRule + `
exempt({ name: "skip-eng-1", each: "ticket", rules: ["greet"], if: (t) => t.id === "ENG-1" });`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()

	res := d.Run("explain", "ENG-1")
	other := d.Run("explain", "ENG-2")

	require.NoError(t, res.Err)
	assert.Regexp(t, `greet  exempt: stopped by skip-eng-1`, res.Stdout)
	assert.Regexp(t, `farewell  wants comment`, res.Stdout)
	require.NoError(t, other.Err)
	assert.Regexp(t, `greet  wants comment`, other.Stdout)
	assert.NotContains(t, other.Stdout, "exempt")
}

func TestDispatchCycleListsWhatAnExemptionStopped(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: rule(greetRule + `
exempt({ name: "skip-eng-1", each: "ticket", rules: ["greet"], if: (t) => t.id === "ENG-1" });`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()

	res := d.Run("cycle")

	require.NoError(t, res.Err)
	assert.Regexp(t, `exempt +ENG-1 +- +greet +by skip-eng-1`, res.Stdout)
	assert.NotRegexp(t, `exempt +ENG-2`, res.Stdout)
}

func TestReplayOfACycleWithAnExemptionShowsNoDifferences(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: rule(greetRule + `
exempt({ name: "skip-eng-1", each: "ticket", rules: ["greet"], if: (t) => t.id === "ENG-1" });
exempt({ name: "skip-idle", each: "ticket", rules: ["farewell"], if: (t, ctx) => ctx.worker.of(t).state === "idle" });`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	d.Tools().Herdr.Fail(herdrtest.OpRead, errors.New("herdr is down"))
	d.Tick()
	d.Stop()

	cycles, matched := replayOf(t, d)

	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}

func TestRulesCheckFindsEachExemptionProblemWhenLoading(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{"UnknownRule", `exempt({ name: "x", each: "ticket", rules: ["nothing"], if: () => true });`,
			`exemption "x" names rule "nothing", which no file defines`},
		{"OnRule", `on({ name: "listen", event: "comment.posted", if: () => true, then: () => {} });
exempt({ name: "x", each: "ticket", rules: ["listen"], if: () => true });`,
			`exemption "x" names rule "listen", an on rule`},
		{"OtherRecordKind", `whenever({ name: "prs", each: "pr", if: () => false, then: () => {} });
exempt({ name: "x", each: "ticket", rules: ["prs"], if: () => true });`,
			`exemption "x" is each: "ticket" but names rule "prs", which is each: "pr"`},
		{"NameOfARule", `whenever({ name: "a", each: "ticket", if: () => false, then: () => {} });
exempt({ name: "a", each: "ticket", rules: ["a"], if: () => true });`,
			`exemption "a" reuses a name already taken`},
		{"NameOfARuleDefinedAfter", `exempt({ name: "a", each: "ticket", rules: ["a"], if: () => true });
whenever({ name: "a", each: "ticket", if: () => false, then: () => {} });`,
			`rule "a" reuses the name of an exemption`},
		{"NameOfAnExemption", `whenever({ name: "a", each: "ticket", if: () => false, then: () => {} });
exempt({ name: "x", each: "ticket", rules: ["a"], if: () => true });
exempt({ name: "x", each: "ticket", rules: ["a"], if: () => true });`,
			`exemption "x" reuses a name already taken`},
		{"NoRules", `exempt({ name: "x", each: "ticket", rules: [], if: () => true });`, `exemption "x": rules is empty`},
		{"UnknownRecordKind", `exempt({ name: "x", each: "linear.tickets" as any, rules: ["a"], if: () => true });`,
			`there is no record kind "linear.tickets"`},
		{"NoRulesKey", `exempt({ name: "x", each: "ticket", if: () => true } as any);`,
			`exemption "x": rules must be a list of rule names`},
		{"WithEvent", `exempt({ name: "x", event: "comment.posted", rules: ["a"], if: () => true } as any);`,
			`exempt takes each, not event`},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: rule(`import { exempt } from "dispatch";
` + test.body)})
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

			res := d.Run("rules", "check")

			require.Error(t, res.Err)
			assert.Contains(t, res.Stdout, "finding:")
			assert.Contains(t, res.Stdout, test.want)
			assert.Empty(t, d.Workers.Launched())
		})
	}
}

func TestAPRReadsConflictWhenGitHubReportsAConflictingMergeState(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		mergeable string
		want      string
	}{
		{"Conflicting", "CONFLICTING", "conflict true needsWork true"},
		{"Mergeable", "MERGEABLE", "conflict false needsWork false"},
		{"Unknown", "UNKNOWN", "conflict false needsWork false"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: rule(`
whenever({ name: "report", each: "pr", if: () => true,
  then: (pr, ctx) => {
    const body = "conflict " + pr.conflict + " needsWork " + pr.needsWork;
    return ctx.ticket.queueComment(pr.ticket, body, { once: ["report", body] });
  } });`)})
			d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
			d.Workers.Running("ENG-1", "idle")
			d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "SUCCESS", mergeable: test.mergeable})

			d.Tick()

			assert.Equal(t, test.want, lastReadingOf(d, "ENG-1"))
		})
	}
}

func TestAConflictingPRStillNudgesItsWorkerWithoutAnExemption(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "SUCCESS", mergeable: "CONFLICTING"})

	d.Tick()

	assert.Equal(t, []string{prPrompt("a merge conflict")}, d.Workers.Prompts())
}

func TestAnExemptionOnConflictKeepsTheShippedNudgeOffAConflictingPR(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: shippedRulesAnd(t, `
import { exempt } from "dispatch";
exempt({ name: "conflict-is-not-work", each: "pr", rules: ["nudge-pr-needs-work"], if: (pr) => pr.conflict });`)})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", prState{head: "a1", checks: "SUCCESS", mergeable: "CONFLICTING"})

	d.Tick()

	assert.Empty(t, d.Workers.Prompts())
}

func TestExplainShowsWhatTheExemptionReadForTheRecordItStopped(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: rule(`
import { exempt } from "dispatch";
whenever({ name: "note", each: "tree", if: () => true,
  then: (tr, ctx) => ctx.ticket.queueComment(tr.holder, "tree " + tr.path, { once: ["note", tr.path] }) });
exempt({ name: "skip-a", each: "tree", rules: ["note"], if: (tr) => tr.path.endsWith("/a") });`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.tools.Treehouse.Clear(d.Repo)
	d.tools.Treehouse.Seed(d.Repo, treehouse.Tree{Path: d.dir + "/a", Status: "leased", LeaseHolder: "ENG-1"})
	d.tools.Treehouse.Seed(d.Repo, treehouse.Tree{Path: d.dir + "/b", Status: "leased", LeaseHolder: "ENG-1"})
	d.Tick()
	d.Stop()

	res := d.Run("explain", "ENG-1")

	require.NoError(t, res.Err)
	assert.Regexp(t, `note  exempt: stopped by skip-a  read path=\S*/a\b`, res.Stdout)
}
