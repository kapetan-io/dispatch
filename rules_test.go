package dispatch_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
	"github.com/kapetan-io/dispatch/internal/httpservicetest"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/rules"
)

// shippedRulesConfig is the [rules] table `rules init` asks the user to add.
const shippedRulesConfig = "[rules]\nworker_model = \"sonnet\"\ndesign_model = \"opus\"\nlaunch_failure_limit = 2\nnudge_limit = 2\nblocked_renotify_minutes = 15\n"

func TestRulesCheckPrintsWhatEachRuleWantsNow(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	d.Workers.Running("ENG-2", "working")

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `linear\.ticket +2 tracked tickets \(assigned to .*; teams ENG; statuses Todo, In Progress, In Review, Needs Human\)`, res.Stdout)
	assert.Regexp(t, `launch-worker +whenever +linear\.ticket +wants launch now for ENG-1\n`, res.Stdout)
	assert.Regexp(t, `nudge-resume +whenever +linear\.ticket +wants nothing now\n`, res.Stdout)
	assert.Contains(t, res.Stdout, "herdr.worker")
	assert.Contains(t, res.Stdout, "treehouse.tree")
}

func TestRulesCheckListsEveryShippedRule(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	for _, name := range []string{
		"launch-worker", "relaunch-for-pr", "stop-paused-worker", "teardown-clean-tree", "stop-worker-on-kept-tree",
		"comment-kept-tree", "close-parent", "promote-child", "escalate-launch-failures", "escalate-relaunch-limit",
		"escalate-truncated-labels", "escalate-missing-repo", "escalate-abandoned-blocker", "escalate-abandoned-blocker-child",
		"note-blocker-hold", "retry-escalation-comment", "nudge-resume", "nudge-pr-activity",
		"nudge-pr-needs-work", "notify-needs-human", "notify-blocked", "notify-ready-for-review",
		"notify-merge-failed", "auto-merge",
	} {
		assert.Regexp(t, `(?m)^`+name+` +whenever `, res.Stdout)
	}
}

func TestRulesCheckActsOnNothing(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "Needs Human", "dispatch"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Empty(t, d.Workers.Launched())
	assert.Empty(t, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
	assert.Empty(t, d.Alerts.Shown())
	assert.Empty(t, d.Workers.Prompts())
}

func TestRulesCheckHoldsWhatItCannotRead(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tools().Treehouse.FailTrees(errors.New("exit status 1"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `launch-worker +whenever +linear\.ticket +wants nothing now; held for ENG-1\n`, res.Stdout)
	assert.Empty(t, d.Workers.Launched())
}

func TestRulesCheckHoldsWhenHerdrCannotBeRead(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tools().Herdr.Fail(herdrtest.OpRead, errors.New("herdr is down"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `launch-worker +whenever +linear\.ticket +wants nothing now; held for ENG-1\n`, res.Stdout)
}

func TestRulesCheckRunsTheTicketsThatCanLaunchBesideTheOnesThatCannot(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", ""))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `launch-worker +whenever +linear\.ticket +wants launch now for ENG-1\n`, res.Stdout)
	assert.Regexp(t, `escalate-missing-repo +whenever +linear\.ticket +wants escalate now for ENG-2\n`, res.Stdout)
}

func TestRulesCheckFailsOnARuleReadingAMissingRulesKey(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: "[rules]\nworker_model = \"sonnet\"\n", ExactRules: true})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.ErrorContains(t, res.Err, "problems found")
	assert.Contains(t, res.Stdout, `finding: launch-worker threw in its if for ENG-1: Error: config.toml has no [rules] key "launch_failure_limit". Add launch_failure_limit = ... under [rules]`)
}

func TestRulesCheckFailsOnACustomRulesKeyTheFileLacks(t *testing.T) {
	d := newDispatch(t, dispatchOptions{
		Config: "[rules]\nbug_model = \"opus\"\n",
		Rules:  rule(`whenever({ name: "bug-model", each: "linear.ticket", if: (t, ctx) => ctx.config.rules.bug_model === "opus" && ctx.config.rules.review_model === "x", then: () => {} });`),
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.ErrorContains(t, res.Err, "1 problems found")
	assert.Contains(t, res.Stdout, `config.toml has no [rules] key "review_model"`)
}

func TestRulesCheckReadsAKeyTheFileHas(t *testing.T) {
	d := newDispatch(t, dispatchOptions{
		Config: "[rules]\nbug_model = \"opus\"\n",
		Rules: rule(`whenever({ name: "bug-model", each: "linear.ticket", if: (t, ctx) => ctx.config.rules.bug_model === "opus",
  then: (t, ctx) => ctx.herdr.queueNudge(t, "use " + ctx.config.rules.bug_model, { once: ["m", t.id] }) });`),
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `bug-model +whenever +linear\.ticket +wants nudge now for ENG-1\n`, res.Stdout)
}

func TestRulesCheckFindsEachProblemWhenLoading(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{"UnknownRecordKind", `whenever({ name: "a", each: "linear.tickets", if: () => true, then: () => {} });`, `there is no record kind "linear.tickets"`},
		{"UnknownEvent", `on({ name: "a", event: "linear.comment.posted2", if: () => true, then: () => {} });`, `there is no event "linear.comment.posted2"`},
		{"WheneverWithEvent", `whenever({ name: "a", event: "linear.comment.posted", if: () => true, then: () => {} } as any);`, `whenever takes each, not event`},
		{"OnWithEach", `on({ name: "a", each: "linear.ticket", if: () => true, then: () => {} } as any);`, `on takes event, not each`},
		{"DuplicateName", `whenever({ name: "a", each: "linear.ticket", if: () => false, then: () => {} });
whenever({ name: "a", each: "github.pr", if: () => false, then: () => {} });`, `rule "a" is defined twice`},
		{"NoName", `whenever({ name: "", each: "linear.ticket", if: () => false, then: () => {} });`, `a whenever rule with no name`},
		{"QueueWithoutOnce", `whenever({ name: "a", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.herdr.queueNudge(t, "hello", { }) });`, `queueNudge is queued with no once key`},
		{"AsyncThen", `whenever({ name: "a", each: "linear.ticket", if: () => true, then: async () => {} });`, `async functions are not allowed`},
		{"Generator", `whenever({ name: "a", each: "linear.ticket", if: () => true, then: function* () {} });`, `generators are not allowed`},
		{"Promise", `whenever({ name: "a", each: "linear.ticket", if: () => true, then: () => { new Promise(() => {}); } });`, `Promise is not available`},
		{"EvalCall", `whenever({ name: "a", each: "linear.ticket", if: () => true, then: () => { eval("1"); } });`, `eval is not available`},
		{"SyntaxError", `whenever({ name: "a", each: "linear.ticket", if: ( => true });`, `Unexpected`},
		{"MissingImport", `import "./nothing";`, `Could not resolve "./nothing"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: rule(test.body)})
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

			res := d.Run("rules", "check")

			require.Error(t, res.Err)
			assert.Contains(t, res.Stdout, "finding:")
			assert.Contains(t, res.Stdout, test.want)
			assert.Empty(t, d.Workers.Launched())
		})
	}
}

func TestRulesCheckRefusesAThenThatReturnsABoolean(t *testing.T) {
	d := newDispatch(t, dispatchOptions{
		Config: shippedRulesConfig,
		Rules:  rule(`whenever({ name: "swap", each: "linear.ticket", if: (t) => true, then: ((t: any) => t.status === "in_progress") as any });`),
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.ErrorContains(t, res.Err, "1 problems found")
	assert.Contains(t, res.Stdout, "finding: swap threw in its then for ENG-1: then returned a boolean")
}

func TestRulesCheckIsolatesAFailingRuleFromTheOthers(t *testing.T) {
	d := newDispatch(t, dispatchOptions{
		Config: shippedRulesConfig,
		Rules: rule(`
whenever({ name: "throws", each: "linear.ticket", if: () => true, then: (t, ctx) => { ctx.herdr.queueNudge(t, "discarded-text", { once: ["x"] }); throw new Error("boom"); } });
whenever({ name: "loops", each: "linear.ticket", if: () => { for (;;) {} }, then: () => {} });
whenever({ name: "recurses", each: "linear.ticket", if: () => { const f = (): number => f() + 1; return f() > 0; }, then: () => {} });
whenever({ name: "healthy", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.herdr.queueNudge(t, "hello", { once: ["healthy", t.id] }) });`),
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))

	res := d.RunWith(dispatch.Options{RuleTimeout: dispatch.RuleTimeout{If: 50 * time.Millisecond}}, "rules", "check")

	require.ErrorContains(t, res.Err, "problems found")
	assert.Regexp(t, `healthy +whenever +linear\.ticket +wants nudge now for ENG-1, ENG-2\n`, res.Stdout)
	assert.Regexp(t, `throws +whenever +linear\.ticket +wants nothing now\n`, res.Stdout)
	assert.Contains(t, res.Stdout, "finding: throws threw in its then for ENG-1: Error: boom")
	assert.Contains(t, res.Stdout, "finding: loops ran out of time in its if for ENG-1: took longer than its time budget and was stopped")
	assert.Contains(t, res.Stdout, "finding: recurses threw in its if for ENG-1: called itself more than 256 levels deep and was stopped")
	assert.NotContains(t, res.Stdout, "discarded-text")
}

func TestRulesCheckHoldsARuleThatCatchesAnUnknownRead(t *testing.T) {
	d := newDispatch(t, dispatchOptions{
		Config: shippedRulesConfig,
		Rules: rule(`
whenever({ name: "catches", each: "linear.ticket",
  if: (t, ctx) => { try { return ctx.herdr.worker(t).state === "none"; } catch (e) { return true; } },
  then: (t, ctx) => ctx.herdr.queueLaunch(t, { once: ["catches", t.id] }) });
whenever({ name: "asks", each: "linear.ticket",
  if: (t, ctx) => !known(() => ctx.herdr.worker(t).state),
  then: (t, ctx) => ctx.linear.queueComment(t, "herdr is unread", { once: ["asks", t.id] }) });`),
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tools().Herdr.Fail(herdrtest.OpRead, errors.New("herdr is down"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `catches +whenever +linear\.ticket +wants nothing now; held for ENG-1\n`, res.Stdout)
	assert.Regexp(t, `asks +whenever +linear\.ticket +wants comment now for ENG-1\n`, res.Stdout)
}

func TestRulesCheckPinsTimeAndRemovesWhatMakesACycleDepend(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{"Random", `Math.random()`, "Math.random is not available"},
		{"LocalTime", `new Date().getHours()`, "getHours"},
		{"GlobalEval", `(globalThis as any).eval("1")`, "finding: a threw in its then for ENG-1: TypeError"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{
				Config: shippedRulesConfig,
				Rules:  rule(`whenever({ name: "a", each: "linear.ticket", if: () => true, then: (t: any) => { ` + test.body + `; } });`),
			})
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

			res := d.Run("rules", "check")

			require.Error(t, res.Err)
			assert.Contains(t, res.Stdout, test.want)
		})
	}
}

func TestRulesCheckGivesDateTheTimeOfTheCycle(t *testing.T) {
	d := newDispatch(t, dispatchOptions{
		Config: shippedRulesConfig,
		Rules: rule(`
whenever({ name: "now", each: "linear.ticket", if: (t, ctx) => Date.now() === ctx.now && new Date().getTime() === ctx.now && new Date().toISOString() === "2026-10-09T12:00:00.000Z",
  then: (t, ctx) => ctx.linear.queueComment(t, "pinned", { once: ["now", t.id] }) });`),
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `now +whenever +linear\.ticket +wants comment now for ENG-1\n`, res.Stdout)
}

func TestRulesCheckRefusesAWriteToARecord(t *testing.T) {
	d := newDispatch(t, dispatchOptions{
		Config: shippedRulesConfig,
		Rules:  rule(`whenever({ name: "writes", each: "linear.ticket", if: (t: any) => { t.status = "done"; return true; }, then: () => {} });`),
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.Error(t, res.Err)
	assert.Contains(t, res.Stdout, "linear.ticket/ENG-1 is read-only")
}

func TestRulesCheckLoadsAShadowRuleAndReportsWhatItWouldDo(t *testing.T) {
	d := newDispatch(t, dispatchOptions{
		Config: shippedRulesConfig,
		Rules: rule(`
whenever({ name: "dry", each: "linear.ticket", shadow: true, if: () => true,
  then: (t, ctx) => ctx.linear.queueComment(t, "hi", { once: ["dry", t.id] }) });
on({ name: "check-failed", event: "github.check.completed", shadow: true, if: () => true, then: () => {} });`),
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `dry +whenever +linear\.ticket +shadow: would comment now for ENG-1\n`, res.Stdout)
	assert.Regexp(t, `check-failed +on +github\.check\.completed +shadow: reports only\n`, res.Stdout)
	assert.Empty(t, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
}

func TestRulesCheckFollowsTheDispatchRulesSetting(t *testing.T) {
	dir := oneRule(t, `whenever({ name: "mine", each: "linear.ticket", if: () => false, then: () => {} });`)
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nrules = " + `"` + dir + `"` + "\n"})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `mine +whenever +linear\.ticket +wants nothing now\n`, res.Stdout)
}

func TestRulesCheckFailsOnAnUnknownConfigKey(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progres = 4\n"})

	res := d.Run("rules", "check")

	require.ErrorContains(t, res.Err, "unknown setting dispatch.max_in_progres")
}

func TestRulesCheckListsTheOrderedStepsOfAMerge(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, RepoSettings: "auto_merge = true\n"})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", approvedPR("a1"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `auto-merge +whenever +github\.pr +wants merge now for ENG-1; waiting: notify for ENG-1 \(after the step before it lands\)\n`, res.Stdout)
}

func TestRulesInitWritesTheShippedRules(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	dir := filepath.Join(t.TempDir(), "rules")

	res := d.Run("rules", "init", dir)

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "wrote ")
	for _, name := range []string{
		"dispatch.ts", "shared.ts", "texts.ts", "workers.ts", "trees.ts", "parents.ts", "escalations.ts", "nudges.ts",
		"notices.ts", "merge.ts", "extras.ts", "commands.ts", "workers.test.ts", "config.example.toml", "tsconfig.json",
		"types/dispatch.d.ts", "types/testing.d.ts", "config.schema.json",
	} {
		assert.FileExists(t, filepath.Join(dir, name))
	}
	entry, err := os.ReadFile(filepath.Join(dir, "dispatch.ts"))
	require.NoError(t, err)
	for _, imported := range []string{"workers", "trees", "parents", "escalations", "nudges", "notices", "merge"} {
		assert.Contains(t, string(entry), `import "./`+imported+`";`)
	}
	assert.NotContains(t, string(entry), `import "./extras"`)
	assert.NotContains(t, string(entry), `import "./commands"`)
}

func TestRulesInitRunsTheRulesItWrote(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := filepath.Join(t.TempDir(), "rules")
	require.NoError(t, d.Run("rules", "init", dir).Err)

	check := d.RunWith(dispatch.Options{RulesDir: dir}, "rules", "check")
	require.NoError(t, check.Err)
	assert.Regexp(t, `launch-worker +whenever +linear\.ticket +wants launch now for ENG-1\n`, check.Stdout)

	test := d.RunWith(dispatch.Options{RulesDir: dir}, "rules", "test")
	require.NoError(t, test.Err)
	assert.Contains(t, test.Stdout, "0 failed")
}

func TestRulesInitRefusesToOverwriteAnyFile(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	dir := rulesDir(t, map[string]string{"shared.ts": "// mine\n", "extra-notes.md": "keep\n"})

	res := d.Run("rules", "init", dir)

	require.ErrorContains(t, res.Err, "already has shared.ts")
	shared, err := os.ReadFile(filepath.Join(dir, "shared.ts"))
	require.NoError(t, err)
	assert.Equal(t, "// mine\n", string(shared))
	assert.NoFileExists(t, filepath.Join(dir, "dispatch.ts"))
	assert.NoDirExists(t, filepath.Join(dir, "types"))
}

func TestRulesInitTwiceRefusesTheSecond(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})
	dir := filepath.Join(t.TempDir(), "rules")
	require.NoError(t, d.Run("rules", "init", dir).Err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "shared.ts"), []byte("// edited\n"), 0o644))

	res := d.Run("rules", "init", dir)

	require.ErrorContains(t, res.Err, "already has")
	edited, err := os.ReadFile(filepath.Join(dir, "shared.ts"))
	require.NoError(t, err)
	assert.Equal(t, "// edited\n", string(edited))
}

func TestRulesInitWritesToTheConfiguredDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "configured")
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nrules = " + `"` + dir + `"` + "\n"})

	res := d.Run("rules", "init")

	require.NoError(t, res.Err)
	assert.FileExists(t, filepath.Join(dir, "dispatch.ts"))
}

func TestRulesTestRunsTheShippedScenarios(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})

	res := d.Run("rules", "test")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "0 failed")
	assert.NotContains(t, res.Stdout, "FAIL")
}

func TestRulesTestReportsAFailingScenarioAndRunsTheRest(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: map[string]string{
		"dispatch.ts": `import { whenever } from "dispatch";
whenever({ name: "greet", each: "linear.ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.herdr.queueNudge(t, "hello", { once: ["greet", t.id] }) });`,
		"greet.test.ts": `import { scenario, ticket, rulesFrom } from "dispatch/testing";
const rules = rulesFrom("./dispatch");
scenario(rules, "wants a greeting", s => {
  s.given(ticket("ENG-1", { status: "in_progress" }));
  s.expect([["greet", "herdr.nudge", "ENG-1"]]);
});
scenario(rules, "expects the wrong thing", s => {
  s.given(ticket("ENG-1", { status: "in_progress" }));
  s.expect([]);
});
scenario(rules, "wants nothing for a ticket in review", s => {
  s.given(ticket("ENG-2", { status: "in_review" }));
  s.expect([]);
});`,
	}})

	res := d.Run("rules", "test")

	require.ErrorContains(t, res.Err, "1 of 3 scenarios failed")
	assert.Contains(t, res.Stdout, "ok   greet.test.ts: wants a greeting")
	assert.Contains(t, res.Stdout, "FAIL greet.test.ts: expects the wrong thing")
	assert.Contains(t, res.Stdout, "expected the cycle to want nothing but it wants [greet herdr.nudge ENG-1]")
	assert.Contains(t, res.Stdout, "ok   greet.test.ts: wants nothing for a ticket in review")
}

func TestRulesTestFailsWithNoScenarios(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: map[string]string{"dispatch.ts": ""}})

	res := d.Run("rules", "test")

	require.ErrorContains(t, res.Err, "no *.test.ts files")
}

func TestRulesCommandNeedsASubcommand(t *testing.T) {
	d := newDispatch(t, dispatchOptions{})

	bare := d.Run("rules")
	require.ErrorContains(t, bare.Err, "rules takes init, check or test")
	assert.Contains(t, bare.Stderr, "dispatch rules init [dir]")

	unknown := d.Run("rules", "frobnicate")
	require.ErrorContains(t, unknown.Err, `unknown rules command "frobnicate"`)
}

func TestRulesCheckStillReadsLinearThroughItsPortOnly(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	before := d.Tools().Linear.Calls()

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Positive(t, d.Tools().Linear.Calls()-before)
	assert.Empty(t, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
}

// generated is every file the repo commits that dispatch generates from its Go types.
func generated() map[string]string {
	return map[string]string{
		"rules/types/dispatch.d.ts": rules.Declarations(config.ShippedRules()),
		"rules/types/testing.d.ts":  rules.TestingDeclarations(),
		"rules/config.schema.json":  string(config.JSONSchema()),
	}
}

// TestGeneratedFilesMatchTheGoTypes is a repo check, not a behavior test: it compares committed
// files against what the code generates, and fails when a Go type, an action, an event or
// a config key changes without the committed declarations and schema. Run it
// with UPDATE_GENERATED=1 to rewrite them.
func TestGeneratedFilesMatchTheGoTypes(t *testing.T) {
	for path, want := range generated() {
		if os.Getenv("UPDATE_GENERATED") != "" {
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, []byte(want), 0o644))
		}
		got, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, want, string(got), "%s is stale: run UPDATE_GENERATED=1 go test -run TestGeneratedFilesMatchTheGoTypes .", path)
	}
}

// A repo check, not a behavior test: the committed schema must name every table the config loader reads.
func TestConfigSchemaNamesEveryTableTheLoaderReads(t *testing.T) {
	var schema struct {
		Properties map[string]struct {
			AdditionalProperties any `json:"additionalProperties"`
		} `json:"properties"`
		AdditionalProperties bool `json:"additionalProperties"`
	}
	require.NoError(t, json.Unmarshal(config.JSONSchema(), &schema))

	var tables []string
	for name := range schema.Properties {
		tables = append(tables, name)
	}
	assert.ElementsMatch(t, []string{"dispatch", "history", "repos", "plugins", "http", "rules"}, tables)
	assert.False(t, schema.AdditionalProperties)
	assert.Equal(t, true, schema.Properties["rules"].AdditionalProperties)
}

func TestRulesCheckClosesAParentWhoseChildrenAreFinished(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig})
	d.Linear.Tickets(parent("ENG-1", "In Progress"))
	d.Linear.Children(child("ENG-2", "ENG-1", "Done"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `close-parent +whenever +linear\.ticket +wants move now for ENG-1\n`, res.Stdout)
	assert.Empty(t, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
}

func TestRulesCheckHoldsAParentWhoseChildrenCannotBeRead(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig})
	d.Linear.Tickets(parent("ENG-1", "In Progress"))
	d.Linear.Children(child("ENG-2", "ENG-1", "Done"))
	d.Linear.FailChildren(true)

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `close-parent +whenever +linear\.ticket +wants nothing now; held for ENG-1\n`, res.Stdout)
	assert.Empty(t, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
}

func TestRulesCheckHoldsTheRulesOfAPRThatCannotBeRead(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, RepoSettings: "auto_merge = true\n"})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", approvedPR("a1"))
	d.Tools().PullRequests.FailRead(errors.New("exit status 1"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `auto-merge +whenever +github\.pr +wants nothing now; held for ENG-1\n`, res.Stdout)
	assert.Regexp(t, `notify-ready-for-review +whenever +github\.pr +wants nothing now; held for ENG-1\n`, res.Stdout)
	assert.Empty(t, d.Tools().PullRequests.Merged())
}

func TestRulesCheckHoldsTheTearDownOfATreeItCannotRead(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig})
	// The tree's directory is gone, so git can't say whether it held work.
	d.Workers.Running("ENG-1", "idle")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `teardown-clean-tree +whenever +treehouse\.tree +wants nothing now; held for ENG-1\n`, res.Stdout)
	assert.Regexp(t, `comment-kept-tree +whenever +treehouse\.tree +wants nothing now; held for ENG-1\n`, res.Stdout)
	assert.Empty(t, d.Trees.Returned())
	assert.Empty(t, d.Linear.Moves())
	assert.Empty(t, d.Linear.Comments())
}

func TestRulesCheckReturnsNothingButWantsTheTearDownOfACleanTree(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig})
	d.Trees.CleanLeased("ENG-1")
	d.Linear.Finished(ticket("ENG-1", "Done", "dispatch"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `teardown-clean-tree +whenever +treehouse\.tree +wants return now for ENG-1\n`, res.Stdout)
	assert.Empty(t, d.Trees.Returned())
}

func TestRulesCheckStopsAnIfAfterOneSecondByDefault(t *testing.T) {
	d := newDispatch(t, dispatchOptions{
		Config: shippedRulesConfig,
		Rules:  rule(`whenever({ name: "loops", each: "linear.ticket", if: () => { for (;;) {} }, then: () => {} });`),
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	start := time.Now()

	res := d.Run("rules", "check")

	require.ErrorContains(t, res.Err, "1 problems found")
	assert.Contains(t, res.Stdout, "finding: loops ran out of time in its if for ENG-1")
	assert.GreaterOrEqual(t, time.Since(start), time.Second)
	assert.Less(t, time.Since(start), 10*time.Second)
}

func TestRulesCheckStopsAThenThatRunsOutOfTimeAndKeepsTheOthers(t *testing.T) {
	d := newDispatch(t, dispatchOptions{
		Config: shippedRulesConfig,
		Rules: rule(`
whenever({ name: "slow", each: "linear.ticket", if: () => true, then: (t, ctx) => { ctx.herdr.queueNudge(t, "x", { once: ["slow"] }); for (;;) {} } });
whenever({ name: "fine", each: "linear.ticket", if: () => true, then: (t, ctx) => ctx.herdr.queueNudge(t, "y", { once: ["fine"] }) });`),
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.RunWith(dispatch.Options{RuleTimeout: dispatch.RuleTimeout{Then: 50 * time.Millisecond}}, "rules", "check")

	require.ErrorContains(t, res.Err, "1 problems found")
	assert.Contains(t, res.Stdout, "finding: slow ran out of time in its then for ENG-1")
	assert.Regexp(t, `slow +whenever +linear\.ticket +wants nothing now\n`, res.Stdout)
	assert.Regexp(t, `fine +whenever +linear\.ticket +wants nudge now for ENG-1\n`, res.Stdout)
}

func TestRulesCheckGivesEachRuleCallAFreshRuntime(t *testing.T) {
	d := newDispatch(t, dispatchOptions{
		Config: shippedRulesConfig,
		Rules: rule(`
let calls = 0;
whenever({ name: "counts", each: "linear.ticket", if: () => { calls++; return calls === 1; },
  then: (t, ctx) => ctx.linear.queueComment(t, "first", { once: ["counts", t.id] }) });`),
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `counts +whenever +linear\.ticket +wants comment now for ENG-1, ENG-2\n`, res.Stdout)
}

func TestRulesCheckHoldsARecordWhoseReadFailedEvenWhenTheRuleCatchesIt(t *testing.T) {
	d := newDispatch(t, dispatchOptions{
		Config: shippedRulesConfig,
		Rules: rule(`
whenever({ name: "catches", each: "linear.ticket", if: (t, ctx) => true,
  then: (t, ctx) => { try { ctx.treehouse.pool(t.repo); } catch (e) {} return ctx.linear.queueComment(t, "x", { once: ["c", t.id] }); } });`),
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tools().Treehouse.FailTrees(errors.New("exit status 1"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `catches +whenever +linear\.ticket +wants nothing now; held for ENG-1\n`, res.Stdout)
}

func TestRulesCheckHoldsALeaseQuestionAboutAChildWhenTheRepoLabelGroupIsCustom(t *testing.T) {
	d := newDispatch(t, dispatchOptions{
		Config: "[plugins.linear]\nrepo_label_group = \"project\"\n" + shippedRulesConfig,
		Rules: rule(`whenever({ name: "asks", each: "linear.ticket", if: (t, ctx) => t.status === "todo" && !ctx.treehouse.leased(t),
  then: (t, ctx) => ctx.linear.queueComment(t, "free", { once: ["asks", t.id] }) });`),
	})
	regroup := func(tk fakeTicket) fakeTicket {
		tk.Labels = []linear.Label{{Name: "ai"}, {Name: "dispatch", Group: "project"}}
		return tk
	}
	d.Linear.Tickets(regroup(parent("ENG-1", "In Progress")))
	d.Linear.Children(regroup(child("ENG-2", "ENG-1", "Todo")))
	d.Tools().Treehouse.FailTrees(errors.New("exit status 1"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `asks +whenever +linear\.ticket +wants nothing now; held for ENG-2\n`, res.Stdout)
}

func TestRulesCheckHoldsARuleWhoseKnownCallbackCatchesTheUnknownRead(t *testing.T) {
	d := newDispatch(t, dispatchOptions{
		Config: shippedRulesConfig,
		Rules: rule(`whenever({ name: "swallows", each: "linear.ticket",
  if: (t, ctx) => known(() => { try { return ctx.herdr.worker(t).state; } catch (e) { return 0; } }),
  then: (t, ctx) => ctx.linear.queueComment(t, "acted", { once: ["swallows", t.id] }) });`),
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tools().Herdr.Fail(herdrtest.OpRead, errors.New("herdr is down"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `swallows +whenever +linear\.ticket +wants nothing now`, res.Stdout)
}

func TestRulesCheckRecordsAForgedActionHandleAsAThrow(t *testing.T) {
	d := newDispatch(t, dispatchOptions{
		Config: shippedRulesConfig,
		Rules: rule(`whenever({ name: "forges", each: "linear.ticket", if: () => true, then: () => ({ queued: true, __id: 99 }) as any });
whenever({ name: "healthy", each: "linear.ticket", if: () => true, then: (t, ctx) => ctx.linear.queueComment(t, "ok", { once: ["h", t.id] }) });`),
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.ErrorContains(t, res.Err, "problems found")
	assert.Contains(t, res.Stdout, "finding: forges threw in its then for ENG-1: then returned something that isn't an action")
	assert.Regexp(t, `healthy +whenever +linear\.ticket +wants comment now for ENG-1`, res.Stdout)
}

func TestRulesCheckNamesAMissingOnceKeyThatTheLintCouldNotSee(t *testing.T) {
	d := newDispatch(t, dispatchOptions{
		Config: shippedRulesConfig,
		Rules: rule(`whenever({ name: "computed", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.linear.queueComment(t, "hi", (t.id === "ENG-1" ? {} : { once: ["x"] }) as any) });`),
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.ErrorContains(t, res.Err, "problems found")
	assert.Contains(t, res.Stdout, "once is missing. Every action needs once")
}

func TestRulesCheckStopsALoopAtTheTopOfARuleFile(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: rule(`for (;;) {}`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	start := time.Now()

	res := d.RunWith(dispatch.Options{RuleTimeout: dispatch.RuleTimeout{Then: 100 * time.Millisecond}}, "rules", "check")

	require.Error(t, res.Err)
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestRulesCheckCannotReachTheRealClockThroughDate(t *testing.T) {
	d := newDispatch(t, dispatchOptions{
		Config: shippedRulesConfig,
		Rules: rule(`whenever({ name: "clock", each: "linear.ticket",
  if: (t, ctx) => new (new Date().constructor as any)().getTime() === ctx.now,
  then: (t, ctx) => ctx.linear.queueComment(t, "pinned", { once: ["c", t.id] }) });`),
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `clock +whenever +linear\.ticket +wants comment now for ENG-1`, res.Stdout)
}

func TestRulesCheckRefusesAHandleThatIsNotAnActionOrIsReturnedTwice(t *testing.T) {
	for _, test := range []struct {
		name string
		then string
	}{
		{"NotANumber", `(t: any, ctx: any) => { const a = ctx.linear.queueComment(t, "a", { once: ["a"] }); return [a, { queued: true, __id: "x" }]; }`},
		{"NaN", `(t: any, ctx: any) => { const a = ctx.linear.queueComment(t, "a", { once: ["a"] }); return [{ queued: true, __id: NaN }, a]; }`},
		{"Twice", `(t: any, ctx: any) => { const a = ctx.linear.queueComment(t, "a", { once: ["a"] }); return [a, a]; }`},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{
				Config: shippedRulesConfig,
				Rules:  rule(`whenever({ name: "forges", each: "linear.ticket", if: () => true, then: ` + test.then + ` });`),
			})
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

			res := d.Run("rules", "check")

			require.ErrorContains(t, res.Err, "problems found")
			assert.Contains(t, res.Stdout, "finding: forges threw in its then for ENG-1: then returned something that isn't an action")
		})
	}
}

func TestRulesCheckStopsAGetterInAReturnedObjectWithinTheBudget(t *testing.T) {
	d := newDispatch(t, dispatchOptions{
		Config: shippedRulesConfig,
		Rules: rule(`whenever({ name: "getter", each: "linear.ticket", if: () => true,
  then: (t, ctx) => { ctx.linear.queueComment(t, "a", { once: ["a"] }); const returned: any[] = [null]; Object.defineProperty(returned, "0", { get() { for (;;) {} } }); return returned as any; } });
whenever({ name: "healthy", each: "linear.ticket", if: () => true, then: (t, ctx) => ctx.linear.queueComment(t, "ok", { once: ["h", t.id] }) });`),
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	start := time.Now()

	res := d.RunWith(dispatch.Options{RuleTimeout: dispatch.RuleTimeout{Then: 100 * time.Millisecond}}, "rules", "check")

	require.ErrorContains(t, res.Err, "problems found")
	assert.Less(t, time.Since(start), 5*time.Second)
	assert.Contains(t, res.Stdout, "finding: getter ran out of time in its then for ENG-1: took longer than its time budget and was stopped")
	assert.Regexp(t, `healthy +whenever +linear\.ticket +wants comment now for ENG-1`, res.Stdout)
}

func TestRulesCheckNamesAMissingKeyInANestedRulesTable(t *testing.T) {
	d := newDispatch(t, dispatchOptions{
		Config: "[rules.slack]\nchannel = \"#eng\"\n",
		Rules:  rule(`whenever({ name: "nested", each: "linear.ticket", if: (t, ctx) => ctx.config.rules.slack.channel === "#eng" && ctx.config.rules.slack.token === "x", then: () => {} });`),
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.ErrorContains(t, res.Err, "problems found")
	assert.Contains(t, res.Stdout, `config.toml has no [rules.slack] key "token"`)
}

func TestRulesCheckShowsWhatWaitsForAWorkerSlot(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: "[dispatch]\nmax_in_progress = 1\n" + shippedRulesConfig})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `launch-worker +whenever +linear\.ticket +wants launch now for ENG-\d; waiting: launch for ENG-\d \(0 of 1 in progress, 1st in line\)\n`, res.Stdout)
}

func TestRulesTestFailsAScenarioWhenARuleThrowsUnlessItExpectsTheThrow(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: map[string]string{
		"dispatch.ts": `import { whenever } from "dispatch";
whenever({ name: "broken", each: "linear.ticket", if: (t) => t.nothing.deeper === 1, then: () => {} });`,
		"broken.test.ts": `import { scenario, ticket, rulesFrom } from "dispatch/testing";
const rules = rulesFrom("./dispatch");
scenario(rules, "expects nothing from a broken rule", s => {
  s.given(ticket("ENG-1", { status: "in_progress" }));
  s.expect([]);
});
scenario(rules, "expects the break", s => {
  s.given(ticket("ENG-1", { status: "in_progress" }));
  s.expectThrew("broken");
  s.expect([]);
});`,
	}})

	res := d.Run("rules", "test")

	require.ErrorContains(t, res.Err, "1 of 2 scenarios failed")
	assert.Contains(t, res.Stdout, "FAIL broken.test.ts: expects nothing from a broken rule")
	assert.Contains(t, res.Stdout, "broken threw in its if for ENG-1")
	assert.Contains(t, res.Stdout, "ok   broken.test.ts: expects the break")
}

func TestRulesCheckRefusesAHugeArrayWithoutBuildingIt(t *testing.T) {
	d := newDispatch(t, dispatchOptions{
		Config: shippedRulesConfig,
		Rules: rule(`whenever({ name: "huge", each: "linear.ticket", if: () => true, then: () => new Array(1_000_000_000) as any });
whenever({ name: "huge-once", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.linear.queueComment(t, "x", { once: new Array(1_000_000_000) as any }) });`),
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.ErrorContains(t, res.Err, "problems found")
	assert.Contains(t, res.Stdout, "finding: huge threw in its then for ENG-1: then returned something that isn't an action")
	assert.Contains(t, res.Stdout, "finding: huge-once threw in its then for ENG-1: ")
	assert.Contains(t, res.Stdout, "once has more than 32 items")
}

func TestRulesCheckTypeChecksWithTscThroughAConfigOfItsOwn(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	record := fakeTSC(t, 0, "")

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	got, err := os.ReadFile(record)
	require.NoError(t, err)
	assert.Contains(t, string(got), "-p ")
	assert.Contains(t, string(got), `"moduleResolution":"bundler"`)
	assert.Contains(t, string(got), `"strict":true`)
	assert.Contains(t, string(got), "dispatch.d.ts")
	assert.Contains(t, string(got), "launch.test.ts")
	assert.NotContains(t, string(got), "node10")
}

func TestRulesCheckReportsWhatTscFinds(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	fakeTSC(t, 2, "shared.ts(6,57): error TS2322: Type 'unknown' is not assignable to type 'number'.")

	res := d.Run("rules", "check")

	require.ErrorContains(t, res.Err, "1 problems found")
	assert.Contains(t, res.Stdout, "finding: tsc: shared.ts(6,57): error TS2322")
}

func TestRulesTestAcknowledgesAThrowOnlyForTheCycleItWasMadeIn(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: map[string]string{
		"dispatch.ts": `import { whenever } from "dispatch";
whenever({ name: "broken", each: "linear.ticket", if: (t: any) => t.nothing.deeper === 1, then: () => {} });`,
		"broken.test.ts": `import { scenario, ticket, rulesFrom } from "dispatch/testing";
const rules = rulesFrom("./dispatch");
scenario(rules, "acknowledged in the cycle it happens", s => {
  s.given(ticket("ENG-1", { status: "in_progress" }));
  s.expectThrew("broken");
  s.expect([]);
});
scenario(rules, "not acknowledged in the next cycle", s => {
  s.given(ticket("ENG-1", { status: "in_progress" }));
  s.expectThrew("broken");
  s.resync();
  s.expect([]);
});`,
	}})

	res := d.Run("rules", "test")

	require.ErrorContains(t, res.Err, "1 of 2 scenarios failed")
	assert.Contains(t, res.Stdout, "ok   broken.test.ts: acknowledged in the cycle it happens")
	assert.Contains(t, res.Stdout, "FAIL broken.test.ts: not acknowledged in the next cycle")
}

// The rules guide is what readers copy, so these two checks run its examples. They read
// docs/rules.md, a repo check that the documented rules are not held for tickets they have no opinion on.

// The long-loop rule in docs/rules.md is what readers copy, so it must not hold for tickets it has no opinion on.
func TestTheTurnsExampleInTheRulesGuideIsNotHeldForOtherTickets(t *testing.T) {
	guide, err := os.ReadFile("docs/rules.md")
	require.NoError(t, err)
	example := regexp.MustCompile("(?s)```ts\nwhenever\\(\\{\n  name: \"long-loop-in-review\".*?\n\\}\\);\n```").Find(guide)
	require.NotEmpty(t, example)
	d := newDispatch(t, dispatchOptions{Rules: rule(string(example[len("```ts\n") : len(example)-len("\n```")]))})
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"), ticket("ENG-2", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Running("ENG-2", "idle")

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.NotRegexp(t, `held`, res.Stdout)
	assert.Regexp(t, `long-loop-in-review +whenever +linear\.ticket +wants nothing now`, res.Stdout)
}

// The person-took-over rule in docs/rules.md is what readers copy, so it must not hold for tickets it has no opinion on.
func TestTheCommandsExampleInTheRulesGuideIsNotHeldForOtherTickets(t *testing.T) {
	guide, err := os.ReadFile("docs/rules.md")
	require.NoError(t, err)
	example := regexp.MustCompile("(?s)```ts\nwhenever\\(\\{\n  name: \"person-took-over\".*?\n\\}\\);\n```").Find(guide)
	require.NotEmpty(t, example)
	d := newDispatch(t, dispatchOptions{Rules: rule(string(example[len("```ts\n") : len(example)-len("\n```")]))})
	d.Linear.Tickets(ticket("ENG-1", "Needs Human", "dispatch"), ticket("ENG-2", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Running("ENG-2", "idle")

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.NotRegexp(t, `held`, res.Stdout)
	assert.Regexp(t, `person-took-over +whenever +linear\.ticket +wants nothing now`, res.Stdout)
}

func TestAThenThatCallsGetActsOnTheResponse(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(`whenever({
  name: "report-sentry", each: "linear.ticket",
  if: (t) => t.status === "in_progress",
  then: (t, ctx) => {
    const res = ctx.http.get("sentry", "/issues/?query=is:unresolved");
    return ctx.linear.queueComment(t, res.status + " " + (res.body as any[]).map((i) => i.title).join(","), { once: ["sentry", t.id] });
  },
});`)})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "GET", unresolved, 200, `[{"title":"KeyError"},{"title":"Timeout"}]`)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	d.Tick()

	assert.Equal(t, []string{"ENG-1: 200 KeyError,Timeout"}, d.Linear.Comments())
	reqs := sentry.Requests()
	require.NotEmpty(t, reqs)
	assert.Equal(t, "Bearer "+sentryToken, reqs[0].Authorization)
}

func TestAnIfThatCallsGetThrows(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(`whenever({ name: "fetch-in-if", each: "linear.ticket", if: (t, ctx) => ctx.http.get("sentry", "/me").status === 200, then: () => {} });`)})
	sentry := d.Services.Sentry()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.ErrorContains(t, res.Err, "problem")
	assert.Contains(t, res.Stdout, "fetch-in-if threw in its if for ENG-1: Error: ctx.http.get can only be called in then")
	assert.Empty(t, sentry.Requests())
}

func TestACallToAServiceConfigDoesNotNameThrowsAtTheCall(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(`whenever({ name: "wrong-service", each: "linear.ticket", if: () => true, then: (t, ctx) => { ctx.http.get("pagerduty", "/me"); } });`)})
	sentry := d.Services.Sentry()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.Error(t, res.Err)
	assert.Contains(t, res.Stdout, "wrong-service threw in its then for ENG-1: Error: ctx.http.get: no such service: [http.pagerduty] is not in config.toml")
	assert.Empty(t, sentry.Requests())
}

func TestAPathThatLeavesTheServiceThrowsAtTheCall(t *testing.T) {
	for _, test := range []struct{ name, path string }{
		{"NoLeadingSlash", "issues"},
		{"OtherHost", "//evil.example/x"},
		{"AboveThePrefix", "/../../admin"},
		{"EncodedDots", "/%2e%2e/admin"},
		{"ParameterDots", "/..;/admin"},
		{"MidPathParameterDots", "/issues/..;/admin"},
		{"ParameterDotsWithValue", "/issues/..;x=1/admin"},
		{"EncodedSemicolon", "/issues/..%3b/admin"},
		{"EncodedDotsAndSemicolon", "/%2e%2e%3B/admin"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Rules: rule(`whenever({ name: "escape", each: "linear.ticket", if: () => true, then: (t, ctx) => { ctx.http.get("sentry", "` + test.path + `"); } });`)})
			sentry := d.Services.Sentry()
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

			res := d.Run("rules", "check")

			require.Error(t, res.Err)
			assert.Contains(t, res.Stdout, "escape threw in its then for ENG-1: Error: ctx.http.get: bad path")
			assert.Empty(t, sentry.Requests())
		})
	}
}

func TestAGetThatFailsHoldsTheRule(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		reason string
	}{
		{"ServerError", 503, "GET sentry /me: returned 503"},
		{"NotFound", 404, "GET sentry /me: returned 404"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Rules: rule(`whenever({ name: "ask", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.linear.queueComment(t, "got " + ctx.http.get("sentry", "/me").status, { once: ["ask", t.id] }) });`)})
			sentry := d.Services.Sentry()
			sentry.Respond("sentry", "GET", "/me", test.status, "boom "+sentryToken)
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

			res := d.Run("rules", "check")

			require.NoError(t, res.Err)
			assert.Regexp(t, `ask +whenever +linear\.ticket +wants nothing now; held for ENG-1\n`, res.Stdout)
			assert.NotContains(t, res.Stdout, sentryToken)
			assert.Empty(t, d.Linear.Comments())
			require.Len(t, sentry.Requests(), 1)
			assert.Equal(t, "Bearer "+sentryToken, sentry.Requests()[0].Authorization)
		})
	}
}

func TestAGetThatOutlastsTheThenBudgetHoldsTheRuleInsteadOfThrowing(t *testing.T) {
	d := newDispatch(t, dispatchOptions{
		RuleTimeout: dispatch.RuleTimeout{Then: 100 * time.Millisecond},
		Rules: rule(`whenever({ name: "slow", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.linear.queueComment(t, "got " + ctx.http.get("sentry", "/slow").status, { once: ["slow", t.id] }) });`),
	})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "GET", "/slow", 200, `{}`)
	sentry.Stall("sentry", "GET", "/slow", 10*time.Second)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	start := time.Now()
	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Less(t, time.Since(start), 5*time.Second)
	assert.Len(t, sentry.Requests(), 1)
	assert.Regexp(t, `slow +whenever +linear\.ticket +wants nothing now; held for ENG-1\n`, res.Stdout)
}

func TestLastReadsTheResponseAGetKeptUntilTheNextResync(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(`
// Fetches once per ticket, so only the first cycle calls the service.
whenever({ name: "fetch", each: "linear.ticket", if: (t, ctx) => !ctx.ledger.done(t, ["fetch", t.id]),
  then: (t, ctx) => { ctx.http.get("sentry", "/count"); return ctx.linear.queueComment(t, "fetched", { once: ["fetch", t.id] }); } });
// Reads the kept response from an if, with no request of its own.
whenever({ name: "use-kept", each: "linear.ticket", if: (t, ctx) => (ctx.http.last("sentry", "/count").body as any).open > 0,
  then: (t, ctx) => ctx.linear.queueComment(t, "kept " + (ctx.http.last("sentry", "/count").body as any).open, { once: ["kept", t.id] }) });
// True only while nothing is kept.
whenever({ name: "forgot", each: "linear.ticket", if: (t, ctx) => !known(() => ctx.http.last("sentry", "/count")) && ctx.ledger.done(t, ["fetch", t.id]),
  then: (t, ctx) => ctx.linear.queueComment(t, "forgot", { once: ["forgot", t.id] }) });`)})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "GET", "/count", 200, `{"open":3}`)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	d.Tick()

	assert.ElementsMatch(t, []string{"ENG-1: fetched", "ENG-1: kept 3"}, d.Linear.Comments())
	assert.Len(t, sentry.Requests(), 1)

	d.Advance(5 * time.Minute)
	d.Tick()

	assert.ElementsMatch(t, []string{"ENG-1: fetched", "ENG-1: kept 3", "ENG-1: forgot"}, d.Linear.Comments())
	assert.Len(t, sentry.Requests(), 1)
}

func TestLastWithNothingKeptHoldsTheRule(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(`whenever({ name: "peek", each: "linear.ticket", if: (t, ctx) => (ctx.http.last("sentry", "/count").body as any).open > 0, then: () => {} });`)})
	d.Services.Sentry()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `peek +whenever +linear\.ticket +wants nothing now; held for ENG-1\n`, res.Stdout)
}

func TestAQueuedPostIsSentOnceWithTheServiceTokenAndRetriedWhenItFails(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(`whenever({ name: "resolve", each: "linear.ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.http.queuePost("sentry", "/issues/7/resolve", { status: "resolved", by: t.id }, { once: ["resolve", t.id] }) });`)})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "POST", "/issues/7/resolve", 503, "")
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	first := d.Run("tick")

	// A receipt for a service isn't keyed by a ticket, so trace can't show it: the fake's requests are the record.
	require.ErrorContains(t, first.Err, "POST sentry /issues/7/resolve: returned 503")
	assert.Equal(t, 1, countPosts(sentry))

	sentry.Respond("sentry", "POST", "/issues/7/resolve", 200, `{"ok":true}`)
	d.Advance(5 * time.Minute)
	d.Tick()
	d.Advance(5 * time.Minute)
	d.Tick()
	assert.Equal(t, 2, countPosts(sentry))

	var posts []httpservicetest.Request
	for _, r := range sentry.Requests() {
		if r.Method == "POST" && r.Authorization != "" {
			posts = append(posts, r)
		}
	}
	require.Greater(t, len(posts), 1)
	last := posts[len(posts)-1]
	assert.JSONEq(t, `{"status":"resolved","by":"ENG-1"}`, last.Body)
	assert.Equal(t, "Bearer "+sentryToken, last.Authorization)

	// The receipt covers the key, so a restart doesn't post again.
	d.Restart()
	d.Advance(5 * time.Minute)
	d.Tick()
	assert.Equal(t, len(posts), countPosts(sentry))
}

func TestAPostToAServiceConfigDoesNotNameThrowsWhenQueued(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(`whenever({ name: "wrong-post", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.http.queuePost("pagerduty", "/x", {}, { once: ["x", t.id] }) });`)})
	sentry := d.Services.Sentry()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.Error(t, res.Err)
	assert.Contains(t, res.Stdout, "wrong-post threw in its then for ENG-1: Error: ctx.http.queuePost: no such service")
	assert.Empty(t, sentry.Requests())
}

func TestTheServiceTokenNeverReachesARuleHistoryOrOutput(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(`
whenever({ name: "echo", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.linear.queueComment(t, JSON.stringify(ctx.http.get("sentry", "/echo").body) + " " + JSON.stringify(ctx.config.http), { once: ["echo", t.id] }) });
whenever({ name: "boom", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.linear.queueComment(t, "boom " + ctx.http.get("sentry", "/boom").status, { once: ["boom", t.id] }) });
whenever({ name: "hook", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.http.queuePost("sentry", "/hook", { hello: "world" }, { once: ["hook", t.id] }) });`)})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "GET", "/echo", 200, `{"you":"`+sentryToken+`"}`)
	sentry.Respond("sentry", "GET", "/boom", 500, "token "+sentryToken)
	sentry.Respond("sentry", "POST", "/hook", 502, sentryToken)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	first := d.Run("tick")
	require.ErrorContains(t, first.Err, "POST sentry /hook: returned 502")
	d.Advance(5 * time.Minute)
	second := d.Run("tick")
	require.ErrorContains(t, second.Err, "POST sentry /hook: returned 502")
	check := d.Run("rules", "check")
	trace := d.Run("trace", "ENG-1")

	comments := d.Linear.Comments()
	require.Len(t, comments, 1)
	assert.Equal(t, `ENG-1: {"you":"[redacted]"} {"sentry":{"base_url":"https://sentry.test/api/0"}}`, comments[0])
	var seen []string
	// The token must not reach any file dispatch keeps, and no command prints them whole.
	require.NoError(t, filepath.Walk(d.h.state, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || strings.HasSuffix(path, ".sock") {
			return err
		}
		body, rerr := os.ReadFile(path)
		require.NoError(t, rerr)
		seen = append(seen, string(body))
		return nil
	}))
	require.NotEmpty(t, seen)
	for _, body := range append(seen, first.Stdout, second.Stdout, first.Err.Error(), second.Err.Error(), check.Stdout, trace.Stdout, d.h.loopOut(), d.h.loopErr(), strings.Join(d.Alerts.Shown(), "\n"), strings.Join(comments, "\n")) {
		assert.NotContains(t, body, sentryToken)
	}
}

func TestEachResponseAThenWaitedOnIsShownInTheTrace(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(`whenever({ name: "count", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.linear.queueComment(t, "open " + (ctx.http.get("sentry", "/count").body as any).open, { once: ["count", t.id] }) });`)})
	d.Services.Sentry().Respond("sentry", "GET", "/count", 200, `{"open":3}`)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()

	res := d.Run("trace", "ENG-1")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, `http  count  GET sentry /count  200 {"open":3}`)
}

func TestABaseURLThatIsNotAnHTTPURLStopsTheStart(t *testing.T) {
	for _, base := range []string{
		"ftp://sentry.test", "sentry.test/api", "https://", "https://user:pw@sentry.test",
		"https://sentry.test/api?org=1", "https://sentry.test/api?", "https://sentry.test/api#top",
	} {
		t.Run(base, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Config: "\n[http.sentry]\nbase_url = \"" + base + "\"\n"})

			res := d.Run("plan")

			require.ErrorContains(t, res.Err, "http.sentry base_url must be an http or https URL")
		})
	}
}

func TestRulesTestRunsHTTPScenariosAgainstTheFakeServices(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: map[string]string{
		"dispatch.ts": `import { whenever, known } from "dispatch";
whenever({
  name: "resolve-quiet", each: "linear.ticket",
  if: (t, ctx) => t.status === "in_progress",
  then: (t, ctx) => {
    const issues = ctx.http.get("sentry", "/issues/?query=is:unresolved").body as any[];
    if (issues.length === 0) return ctx.http.queuePost("sentry", "/hooks/quiet", { ticket: t.id }, { once: ["quiet", t.id] });
  },
});
whenever({
  name: "note-open", each: "linear.ticket",
  if: (t, ctx) => known(() => ctx.http.last("sentry", "/issues/?query=is:unresolved")) && (ctx.http.last("sentry", "/issues/?query=is:unresolved").body as any[]).length > 0,
  then: (t, ctx) => ctx.linear.queueComment(t, "open errors", { once: ["open", t.id] }),
});
`,
		"sentry.test.ts": `import { scenario, ticket, rulesFrom } from "dispatch/testing";
const rules = rulesFrom("./dispatch");
const build = { status: "in_progress", optedIn: true, repo: "dispatch" };

scenario(rules, "posts when sentry has nothing open", s => {
  s.config({ http: { sentry: { baseUrl: "https://sentry.test/api/0" } } });
  s.http("sentry", "/issues/?query=is:unresolved", []);
  s.given(ticket("ENG-1", build));
  s.expect([["resolve-quiet", "http.post", "sentry"]]);
});

scenario(rules, "reads the response a get kept from another rule's if", s => {
  s.config({ http: { sentry: { baseUrl: "https://sentry.test/api/0" } } });
  s.http("sentry", "/issues/?query=is:unresolved", [{ id: "1" }]);
  s.given(ticket("ENG-1", build));
  s.expect([["note-open", "linear.comment", "ENG-1"]]);
  s.resync();
  s.expect([["note-open", "linear.comment", "ENG-1"]]);
});

scenario(rules, "holds while the service has no answer", s => {
  s.config({ http: { sentry: { baseUrl: "https://sentry.test/api/0" } } });
  s.given(ticket("ENG-1", build));
  s.expect([]);
  s.expectHeld("resolve-quiet", "ENG-1");
});
`,
	}})
	d.Services.Sentry()

	res := d.Run("rules", "test")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "ok   sentry.test.ts: posts when sentry has nothing open")
	assert.Contains(t, res.Stdout, "ok   sentry.test.ts: reads the response a get kept from another rule's if")
	assert.Contains(t, res.Stdout, "ok   sentry.test.ts: holds while the service has no answer")
}

func TestAPostThatTimesOutCountsAsSentAndIsNotPostedAgain(t *testing.T) {
	d := newDispatch(t, dispatchOptions{
		ActionTimeout: 100 * time.Millisecond,
		Rules: rule(`whenever({ name: "slow-post", each: "linear.ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.http.queuePost("sentry", "/slow", { id: t.id }, { once: ["slow-post", t.id] }) });`),
	})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "POST", "/slow", 200, `{}`)
	sentry.Stall("sentry", "POST", "/slow", 10*time.Second)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	d.Tick()

	assert.Equal(t, 1, countPosts(sentry))

	d.Advance(5 * time.Minute)
	d.Tick()

	assert.Equal(t, 1, countPosts(sentry))
}

func TestAGetThatFailsIsShownInTheTraceWithItsReasonAndNoToken(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: rule(`whenever({ name: "down", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.linear.queueComment(t, "got " + ctx.http.get("sentry", "/down").status, { once: ["down", t.id] }) });`)})
	d.Services.Sentry().Respond("sentry", "GET", "/down", 503, "oops "+sentryToken)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()

	res := d.Run("trace", "ENG-1")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "GET sentry /down: returned 503")
	assert.NotContains(t, res.Stdout, sentryToken)
	assert.Empty(t, d.Linear.Comments())
}

func TestRulesTestHTTPScenarioErrors(t *testing.T) {
	const rulesTS = `import { whenever } from "dispatch";
whenever({ name: "ask", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.linear.queueComment(t, "got " + ctx.http.get("sentry", "/me").status, { once: ["ask", t.id] }) });
`
	for _, test := range []struct{ name, body, want string }{
		{"ServiceNotInScenarioConfig", `s.http("sentry", "/me", {}); s.given(ticket("ENG-1", build)); s.expect([]);`, "the scenario has no such service"},
		{"ServiceWithoutBaseURL", `s.config({ http: { sentry: {} } });`, "needs a baseUrl"},
		{"ServiceFailingWith503HoldsTheRule", `s.config({ http: { sentry: { baseUrl: "https://sentry.test" } } }); s.http("sentry", "/me", {}, { status: 503 }); s.given(ticket("ENG-1", build)); s.expect([["ask", "linear.comment", "ENG-1"]]);`, "FAIL"},
		{"ABodyForAnotherPathIsNotAnAnswer", `s.config({ http: { sentry: { baseUrl: "https://sentry.test" } } }); s.http("sentry", "/other", {}); s.given(ticket("ENG-1", build)); s.expect([["ask", "linear.comment", "ENG-1"]]);`, "FAIL"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Rules: map[string]string{
				"dispatch.ts": rulesTS,
				"a.test.ts": `import { scenario, ticket, rulesFrom } from "dispatch/testing";
const rules = rulesFrom("./dispatch");
const build = { status: "in_progress", optedIn: true, repo: "dispatch" };
scenario(rules, "case", s => { ` + test.body + ` });
`,
			}})

			res := d.Run("rules", "test")

			require.Error(t, res.Err)
			assert.Contains(t, res.Stdout, test.want)
		})
	}
}

func TestRulesTestHTTPScenarioAnswersAMethodAndStatus(t *testing.T) {
	d := newDispatch(t, dispatchOptions{Rules: map[string]string{
		"dispatch.ts": `import { whenever } from "dispatch";
whenever({ name: "ask", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.linear.queueComment(t, "got " + ctx.http.get("sentry", "/me").status, { once: ["ask", t.id] }) });
`,
		"a.test.ts": `import { scenario, ticket, rulesFrom } from "dispatch/testing";
const rules = rulesFrom("./dispatch");
const build = { status: "in_progress", optedIn: true, repo: "dispatch" };
scenario(rules, "a 201 reads as a response", s => {
  s.config({ http: { sentry: { baseUrl: "https://sentry.test" } } });
  s.http("sentry", "/me", {}, { status: 201 });
  s.given(ticket("ENG-1", build));
  s.expect([["ask", "linear.comment", "ENG-1"]]);
});
scenario(rules, "an answer for POST is not an answer for GET", s => {
  s.config({ http: { sentry: { baseUrl: "https://sentry.test" } } });
  s.http("sentry", "/me", {}, { method: "POST" });
  s.given(ticket("ENG-1", build));
  s.expect([]);
  s.expectHeld("ask", "ENG-1");
});
scenario(rules, "a failing status holds the rule", s => {
  s.config({ http: { sentry: { baseUrl: "https://sentry.test" } } });
  s.http("sentry", "/me", {}, { status: 503 });
  s.given(ticket("ENG-1", build));
  s.expect([]);
  s.expectHeld("ask", "ENG-1");
});
`,
	}})

	res := d.Run("rules", "test")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "ok   a.test.ts: a 201 reads as a response")
	assert.Contains(t, res.Stdout, "ok   a.test.ts: an answer for POST is not an answer for GET")
	assert.Contains(t, res.Stdout, "ok   a.test.ts: a failing status holds the rule")
}

func TestAPostIsNotSentAgainAfterHistoryRotates(t *testing.T) {
	d := newDispatch(t, dispatchOptions{
		Config: "\n[history]\nrotate = \"1m\"\nkeep = \"1d\"\n",
		Rules: rule(`whenever({ name: "hook", each: "linear.ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.http.queuePost("sentry", "/hook", { id: t.id }, { once: ["hook", t.id] }) });`),
	})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "POST", "/hook", 200, `{}`)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	d.Tick()
	for range 4 {
		d.Advance(6 * time.Minute)
		d.Tick()
	}

	assert.Equal(t, 1, countPosts(sentry))
}

func TestAPostWhoseTokenCommandOutlastsTheDeadlineSentNothingAndIsTriedAgain(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	d := newDispatch(t, dispatchOptions{
		Config:        "\n[http.sentry]\nbase_url = \"" + srv.URL + "\"\ntoken_command = \"sleep 5; echo tok\"\n",
		ActionTimeout: 300 * time.Millisecond,
		Rules: rule(`whenever({ name: "hook", each: "linear.ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.http.queuePost("sentry", "/hook", {}, { once: ["hook", t.id] }) });`),
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("tick")

	require.Error(t, res.Err)
	assert.Zero(t, hits.Load())

	d.Advance(5 * time.Minute)
	retry := d.Run("tick")

	require.Error(t, retry.Err)
	assert.Zero(t, hits.Load())
}

func TestRulesCheckCallsNoServiceUnlessGivenOne(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "token-command-ran")
	d := newDispatch(t, dispatchOptions{
		Config: "\n[http.sentry]\nbase_url = \"https://sentry.test/api/0\"\ntoken_command = \"touch " + marker + "; echo tok\"\n",
		Rules: rule(`whenever({ name: "ask", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.linear.queueComment(t, "got " + ctx.http.get("sentry", "/me").status, { once: ["ask", t.id] }) });`),
	})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `ask +whenever +linear\.ticket +wants nothing now; held for ENG-1\n`, res.Stdout)
	assert.NoFileExists(t, marker)
}

func countPosts(sentry *httpservicetest.Fake) int {
	var posts int
	for _, r := range sentry.Requests() {
		if r.Method == "POST" {
			posts++
		}
	}
	return posts
}
