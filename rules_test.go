package dispatch_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/config"
	"github.com/kapetan-io/dispatch/internal/herdrtest"
	"github.com/kapetan-io/dispatch/internal/linear"
	"github.com/kapetan-io/dispatch/internal/lineartest"
	"github.com/kapetan-io/dispatch/internal/rules"
)

// shippedRulesConfig is the [rules] table `rules init` asks the user to add.
const shippedRulesConfig = "[rules]\nworker_model = \"sonnet\"\ndesign_model = \"opus\"\nlaunch_failure_limit = 2\nnudge_limit = 2\nblocked_renotify_minutes = 15\n"

// repoRules is the repo's own rules/ directory, which the tests run.
const repoRules = "rules"

// rulesDir writes the files into a new directory and returns it.
func rulesDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	return dir
}

// oneRule is a rules directory holding dispatch.ts with just this body.
func oneRule(t *testing.T, body string) string {
	t.Helper()
	return rulesDir(t, map[string]string{"dispatch.ts": "import { whenever, on, known, minutes, hours } from \"dispatch\";\n" + body})
}

func (h *harness) rules(dir string, args ...string) (string, error) {
	h.t.Helper()
	stdout, _, err := h.runWith(context.Background(), dispatch.Options{RulesDir: dir}, append([]string{"rules"}, args...)...)
	return stdout, err
}

func TestRulesCheckPrintsWhatEachRuleWantsNow(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	h.worker("ENG-2", "working")

	out, err := h.rules(repoRules, "check")

	require.NoError(t, err)
	assert.Regexp(t, `linear\.ticket +2 tracked tickets \(assigned to .*; teams ENG; statuses Todo, In Progress, In Review, Needs Human\)`, out)
	assert.Regexp(t, `launch-worker +whenever +linear\.ticket +wants launch now for ENG-1\n`, out)
	assert.Regexp(t, `nudge-stall +whenever +linear\.ticket +wants nothing now\n`, out)
	assert.Contains(t, out, "herdr.worker")
	assert.Contains(t, out, "treehouse.tree")
}

func TestRulesCheckListsEveryShippedRule(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	out, err := h.rules(repoRules, "check")

	require.NoError(t, err)
	for _, name := range []string{
		"launch-worker", "relaunch-for-pr", "stop-paused-worker", "teardown-clean-tree", "stop-worker-on-kept-tree",
		"comment-kept-tree", "close-parent", "promote-child", "escalate-launch-failures", "escalate-relaunch-limit",
		"escalate-truncated-labels", "escalate-missing-repo", "escalate-abandoned-blocker", "escalate-abandoned-blocker-child",
		"note-blocker-hold", "retry-escalation-comment", "nudge-resume", "nudge-stall", "nudge-pr-activity",
		"nudge-pr-needs-work", "notify-needs-human", "notify-blocked", "notify-ready-for-review", "notify-stall-count",
		"notify-unresponsive", "notify-merge-failed", "auto-merge",
	} {
		assert.Regexp(t, `(?m)^`+name+` +whenever `, out)
	}
}

func TestRulesCheckActsOnNothing(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "Needs Human", "dispatch"))

	_, err := h.rules(repoRules, "check")

	require.NoError(t, err)
	assert.Empty(t, h.launches())
	assert.Empty(t, h.linear.mutations())
	assert.Empty(t, h.notifications())
	assert.Empty(t, h.prompts())
}

func TestRulesCheckHoldsWhatItCannotRead(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.tools.Treehouse.FailTrees(errors.New("exit status 1"))

	out, err := h.rules(repoRules, "check")

	require.NoError(t, err)
	assert.Regexp(t, `launch-worker +whenever +linear\.ticket +wants nothing now; held for ENG-1\n`, out)
	assert.Empty(t, h.launches())
}

func TestRulesCheckHoldsWhenHerdrCannotBeRead(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.tools.Herdr.Fail(herdrtest.OpRead, errors.New("herdr is down"))

	out, err := h.rules(repoRules, "check")

	require.NoError(t, err)
	assert.Regexp(t, `launch-worker +whenever +linear\.ticket +wants nothing now; held for ENG-1\n`, out)
}

func TestRulesCheckRunsTheTicketsThatCanLaunchBesideTheOnesThatCannot(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	noRepo := ticket("ENG-2", "In Progress", "")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"), noRepo)

	out, err := h.rules(repoRules, "check")

	require.NoError(t, err)
	assert.Regexp(t, `launch-worker +whenever +linear\.ticket +wants launch now for ENG-1\n`, out)
	assert.Regexp(t, `escalate-missing-repo +whenever +linear\.ticket +wants escalate now for ENG-2\n`, out)
}

func TestRulesCheckFailsOnARuleReadingAMissingRulesKey(t *testing.T) {
	h := newHarness(t)
	h.exactRules = true
	h.config("[rules]\nworker_model = \"sonnet\"\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	out, err := h.rules(repoRules, "check")

	require.ErrorContains(t, err, "problems found")
	assert.Contains(t, out, `finding: launch-worker threw in its if for ENG-1: Error: config.toml has no [rules] key "launch_failure_limit". Add launch_failure_limit = ... under [rules]`)
}

func TestRulesCheckFailsOnACustomRulesKeyTheFileLacks(t *testing.T) {
	h := newHarness(t)
	h.config("[rules]\nbug_model = \"opus\"\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `whenever({ name: "bug-model", each: "linear.ticket", if: (t, ctx) => ctx.config.rules.bug_model === "opus" && ctx.config.rules.review_model === "x", then: () => {} });`)

	out, err := h.rules(dir, "check")

	require.ErrorContains(t, err, "1 problems found")
	assert.Contains(t, out, `config.toml has no [rules] key "review_model"`)
}

func TestRulesCheckReadsAKeyTheFileHas(t *testing.T) {
	h := newHarness(t)
	h.config("[rules]\nbug_model = \"opus\"\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `whenever({ name: "bug-model", each: "linear.ticket", if: (t, ctx) => ctx.config.rules.bug_model === "opus",
  then: (t, ctx) => ctx.herdr.queueNudge(t, "use " + ctx.config.rules.bug_model, { once: ["m", t.id] }) });`)

	out, err := h.rules(dir, "check")

	require.NoError(t, err)
	assert.Regexp(t, `bug-model +whenever +linear\.ticket +wants nudge now for ENG-1\n`, out)
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
			h := newHarness(t)
			h.config(shippedRulesConfig)
			h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

			out, err := h.rules(oneRule(t, test.body), "check")

			require.Error(t, err)
			assert.Contains(t, out, "finding:")
			assert.Contains(t, out, test.want)
			assert.Empty(t, h.launches())
		})
	}
}

func TestRulesCheckRefusesAThenThatReturnsABoolean(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `whenever({ name: "swap", each: "linear.ticket", if: (t) => true, then: ((t: any) => t.status === "in_progress") as any });`)

	out, err := h.rules(dir, "check")

	require.ErrorContains(t, err, "1 problems found")
	assert.Contains(t, out, "finding: swap threw in its then for ENG-1: then returned a boolean")
}

func TestRulesCheckIsolatesAFailingRuleFromTheOthers(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	dir := oneRule(t, `
whenever({ name: "throws", each: "linear.ticket", if: () => true, then: (t, ctx) => { ctx.herdr.queueNudge(t, "discarded-text", { once: ["x"] }); throw new Error("boom"); } });
whenever({ name: "loops", each: "linear.ticket", if: () => { for (;;) {} }, then: () => {} });
whenever({ name: "recurses", each: "linear.ticket", if: () => { const f = (): number => f() + 1; return f() > 0; }, then: () => {} });
whenever({ name: "healthy", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.herdr.queueNudge(t, "hello", { once: ["healthy", t.id] }) });`)

	out, _, err := h.runWith(context.Background(), dispatch.Options{RulesDir: dir, RuleTimeout: dispatch.RuleTimeout{If: 50 * time.Millisecond}}, "rules", "check")

	require.ErrorContains(t, err, "problems found")
	assert.Regexp(t, `healthy +whenever +linear\.ticket +wants nudge now for ENG-1, ENG-2\n`, out)
	assert.Regexp(t, `throws +whenever +linear\.ticket +wants nothing now\n`, out)
	assert.Contains(t, out, "finding: throws threw in its then for ENG-1: Error: boom")
	assert.Contains(t, out, "finding: loops ran out of time in its if for ENG-1: took longer than its time budget and was stopped")
	assert.Contains(t, out, "finding: recurses threw in its if for ENG-1: called itself more than 256 levels deep and was stopped")
	assert.NotContains(t, out, "discarded-text")
}

func TestRulesCheckHoldsARuleThatCatchesAnUnknownRead(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.tools.Herdr.Fail(herdrtest.OpRead, errors.New("herdr is down"))
	dir := oneRule(t, `
whenever({ name: "catches", each: "linear.ticket",
  if: (t, ctx) => { try { return ctx.herdr.worker(t).state === "none"; } catch (e) { return true; } },
  then: (t, ctx) => ctx.herdr.queueLaunch(t, { once: ["catches", t.id] }) });
whenever({ name: "asks", each: "linear.ticket",
  if: (t, ctx) => !known(() => ctx.herdr.worker(t).state),
  then: (t, ctx) => ctx.linear.queueComment(t, "herdr is unread", { once: ["asks", t.id] }) });`)

	out, err := h.rules(dir, "check")

	require.NoError(t, err)
	assert.Regexp(t, `catches +whenever +linear\.ticket +wants nothing now; held for ENG-1\n`, out)
	assert.Regexp(t, `asks +whenever +linear\.ticket +wants comment now for ENG-1\n`, out)
}

func TestRulesCheckPinsTimeAndRemovesWhatMakesACycleDepend(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{"Random", `Math.random()`, "Math.random is not available"},
		{"LocalTime", `new Date().getHours()`, "getHours"},
		{"Eval", `(globalThis as any).eval("1")`, ""},
	} {
		dir := oneRule(t, `whenever({ name: "a", each: "linear.ticket", if: () => true, then: (t: any) => { `+test.body+`; } });`)

		out, err := h.rules(dir, "check")

		if test.want == "" {
			continue
		}
		require.Error(t, err, test.name)
		assert.Contains(t, out, test.want, test.name)
	}
}

func TestRulesCheckGivesDateTheTimeOfTheCycle(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `
whenever({ name: "now", each: "linear.ticket", if: (t, ctx) => Date.now() === ctx.now && new Date().getTime() === ctx.now && new Date().toISOString() === "2026-10-09T12:00:00.000Z",
  then: (t, ctx) => ctx.linear.queueComment(t, "pinned", { once: ["now", t.id] }) });`)

	out, err := h.rules(dir, "check")

	require.NoError(t, err)
	assert.Regexp(t, `now +whenever +linear\.ticket +wants comment now for ENG-1\n`, out)
}

func TestRulesCheckRefusesAWriteToARecord(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `whenever({ name: "writes", each: "linear.ticket", if: (t: any) => { t.status = "done"; return true; }, then: () => {} });`)

	out, err := h.rules(dir, "check")

	require.Error(t, err)
	assert.Contains(t, out, "linear.ticket/ENG-1 is read-only")
}

func TestRulesCheckLoadsAShadowRuleAndReportsWhatItWouldDo(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `
whenever({ name: "dry", each: "linear.ticket", shadow: true, if: () => true,
  then: (t, ctx) => ctx.linear.queueComment(t, "hi", { once: ["dry", t.id] }) });
on({ name: "check-failed", event: "github.check.completed", shadow: true, if: () => true, then: () => {} });`)

	out, err := h.rules(dir, "check")

	require.NoError(t, err)
	assert.Regexp(t, `dry +whenever +linear\.ticket +shadow: would comment now for ENG-1\n`, out)
	assert.Regexp(t, `check-failed +on +github\.check\.completed +shadow: reports only\n`, out)
	assert.Empty(t, h.linear.mutations())
}

func TestRulesCheckFollowsTheDispatchRulesSetting(t *testing.T) {
	h := newHarness(t)
	dir := oneRule(t, `whenever({ name: "mine", each: "linear.ticket", if: () => false, then: () => {} });`)
	h.config("[dispatch]\nrules = " + `"` + dir + `"` + "\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))

	out, err := h.run("rules", "check")

	require.NoError(t, err)
	assert.Regexp(t, `mine +whenever +linear\.ticket +wants nothing now\n`, out)
}

func TestRulesCheckFailsOnAnUnknownConfigKey(t *testing.T) {
	h := newHarness(t)
	h.config("[dispatch]\nmax_in_progres = 4\n")

	_, err := h.rules(repoRules, "check")

	require.ErrorContains(t, err, "unknown setting dispatch.max_in_progres")
}

func TestRulesCheckListsTheOrderedStepsOfAMerge(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.repoSettings("dispatch", "auto_merge = true\n")
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", approvedPR("a1"))

	out, err := h.rules(repoRules, "check")

	require.NoError(t, err)
	assert.Regexp(t, `auto-merge +whenever +github\.pr +wants merge now for ENG-1; waiting: notify for ENG-1 \(after the step before it lands\)\n`, out)
}

func TestRulesInitWritesTheShippedRules(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(t.TempDir(), "rules")

	out, err := h.rules("", "init", dir)

	require.NoError(t, err)
	assert.Contains(t, out, "wrote ")
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
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := filepath.Join(t.TempDir(), "rules")
	_, err := h.rules("", "init", dir)
	require.NoError(t, err)

	out, err := h.rules(dir, "check")
	require.NoError(t, err)
	assert.Regexp(t, `launch-worker +whenever +linear\.ticket +wants launch now for ENG-1\n`, out)

	out, err = h.rules(dir, "test")
	require.NoError(t, err)
	assert.Contains(t, out, "0 failed")
}

func TestRulesInitRefusesToOverwriteAnyFile(t *testing.T) {
	h := newHarness(t)
	dir := rulesDir(t, map[string]string{"shared.ts": "// mine\n", "extra-notes.md": "keep\n"})

	_, err := h.rules("", "init", dir)

	require.ErrorContains(t, err, "already has shared.ts")
	shared, readErr := os.ReadFile(filepath.Join(dir, "shared.ts"))
	require.NoError(t, readErr)
	assert.Equal(t, "// mine\n", string(shared))
	assert.NoFileExists(t, filepath.Join(dir, "dispatch.ts"))
	assert.NoDirExists(t, filepath.Join(dir, "types"))
}

func TestRulesInitTwiceRefusesTheSecond(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(t.TempDir(), "rules")
	_, err := h.rules("", "init", dir)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "shared.ts"), []byte("// edited\n"), 0o644))

	_, err = h.rules("", "init", dir)

	require.ErrorContains(t, err, "already has")
	edited, readErr := os.ReadFile(filepath.Join(dir, "shared.ts"))
	require.NoError(t, readErr)
	assert.Equal(t, "// edited\n", string(edited))
}

func TestRulesInitWritesToTheConfiguredDirectory(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(t.TempDir(), "configured")
	h.config("[dispatch]\nrules = " + `"` + dir + `"` + "\n")

	_, err := h.run("rules", "init")

	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(dir, "dispatch.ts"))
}

func TestRulesTestRunsTheShippedScenarios(t *testing.T) {
	h := newHarness(t)

	out, err := h.rules(repoRules, "test")

	require.NoError(t, err)
	assert.Contains(t, out, "0 failed")
	assert.NotContains(t, out, "FAIL")
}

func TestRulesTestReportsAFailingScenarioAndRunsTheRest(t *testing.T) {
	h := newHarness(t)
	dir := rulesDir(t, map[string]string{
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
	})

	out, err := h.rules(dir, "test")

	require.ErrorContains(t, err, "1 of 3 scenarios failed")
	assert.Contains(t, out, "ok   greet.test.ts: wants a greeting")
	assert.Contains(t, out, "FAIL greet.test.ts: expects the wrong thing")
	assert.Contains(t, out, "expected the cycle to want nothing but it wants [greet herdr.nudge ENG-1]")
	assert.Contains(t, out, "ok   greet.test.ts: wants nothing for a ticket in review")
}

func TestRulesTestFailsWithNoScenarios(t *testing.T) {
	h := newHarness(t)

	_, err := h.rules(rulesDir(t, map[string]string{"dispatch.ts": ""}), "test")

	require.ErrorContains(t, err, "no *.test.ts files")
}

func TestRulesCommandNeedsASubcommand(t *testing.T) {
	h := newHarness(t)

	_, stderr, err := h.runContext(context.Background(), "rules")
	require.ErrorContains(t, err, "rules takes init, check or test")
	assert.Contains(t, stderr, "dispatch rules init [dir]")

	_, err = h.run("rules", "frobnicate")
	require.ErrorContains(t, err, `unknown rules command "frobnicate"`)
}

func TestRulesCheckStillReadsLinearThroughItsPortOnly(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	before := h.linear.calls()

	_, err := h.rules(repoRules, "check")

	require.NoError(t, err)
	assert.Positive(t, h.linear.calls()-before)
	assert.Empty(t, h.linear.mutations())
}

// generated is every file the repo commits that dispatch generates from its Go types.
func generated() map[string]string {
	return map[string]string{
		"rules/types/dispatch.d.ts": rules.Declarations(config.ShippedRules()),
		"rules/types/testing.d.ts":  rules.TestingDeclarations(),
		"rules/config.schema.json":  string(config.JSONSchema()),
	}
}

// TestGeneratedFilesMatchTheGoTypes fails when a Go type, an action, an event or
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
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(parent("ENG-1", "In Progress"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Done"))

	out, err := h.rules(repoRules, "check")

	require.NoError(t, err)
	assert.Regexp(t, `close-parent +whenever +linear\.ticket +wants move now for ENG-1\n`, out)
	assert.Empty(t, h.linear.mutations())
}

func TestRulesCheckHoldsAParentWhoseChildrenCannotBeRead(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(parent("ENG-1", "In Progress"))
	h.linear.setChildren(child("ENG-2", "ENG-1", "Done"))
	h.linear.FailOn(lineartest.OnChildren, true)

	out, err := h.rules(repoRules, "check")

	require.NoError(t, err)
	assert.Regexp(t, `close-parent +whenever +linear\.ticket +wants nothing now; held for ENG-1\n`, out)
	assert.Empty(t, h.linear.mutations())
}

func TestRulesCheckHoldsTheRulesOfAPRThatCannotBeRead(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.repoSettings("dispatch", "auto_merge = true\n")
	h.tickets(ticket("ENG-1", "In Review", "dispatch"))
	h.worker("ENG-1", "idle")
	h.pr("ENG-1", approvedPR("a1"))
	h.tools.PullRequests.FailRead(errors.New("exit status 1"))

	out, err := h.rules(repoRules, "check")

	require.NoError(t, err)
	assert.Regexp(t, `auto-merge +whenever +github\.pr +wants nothing now; held for ENG-1\n`, out)
	assert.Regexp(t, `notify-ready-for-review +whenever +github\.pr +wants nothing now; held for ENG-1\n`, out)
	assert.Empty(t, h.merges())
}

func TestRulesCheckHoldsTheTearDownOfATreeItCannotRead(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	// The tree's directory is gone, so git can't say whether it held work.
	h.worker("ENG-1", "idle")
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))

	out, err := h.rules(repoRules, "check")

	require.NoError(t, err)
	assert.Regexp(t, `teardown-clean-tree +whenever +treehouse\.tree +wants nothing now; held for ENG-1\n`, out)
	assert.Regexp(t, `comment-kept-tree +whenever +treehouse\.tree +wants nothing now; held for ENG-1\n`, out)
	assert.Empty(t, h.returns())
	assert.Empty(t, h.linear.mutations())
}

func TestRulesCheckReturnsNothingButWantsTheTearDownOfACleanTree(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	path := h.gitTree("ENG-1")
	h.lease("ENG-1", path)
	h.linear.setElsewhere(ticket("ENG-1", "Done", "dispatch"))

	out, err := h.rules(repoRules, "check")

	require.NoError(t, err)
	assert.Regexp(t, `teardown-clean-tree +whenever +treehouse\.tree +wants return now for ENG-1\n`, out)
	assert.Empty(t, h.returns())
}

func TestRulesCheckStopsAnIfAfterOneSecondByDefault(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `whenever({ name: "loops", each: "linear.ticket", if: () => { for (;;) {} }, then: () => {} });`)
	start := time.Now()

	out, err := h.rules(dir, "check")

	require.ErrorContains(t, err, "1 problems found")
	assert.Contains(t, out, "finding: loops ran out of time in its if for ENG-1")
	assert.GreaterOrEqual(t, time.Since(start), time.Second)
	assert.Less(t, time.Since(start), 10*time.Second)
}

func TestRulesCheckStopsAThenThatRunsOutOfTimeAndKeepsTheOthers(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `
whenever({ name: "slow", each: "linear.ticket", if: () => true, then: (t, ctx) => { ctx.herdr.queueNudge(t, "x", { once: ["slow"] }); for (;;) {} } });
whenever({ name: "fine", each: "linear.ticket", if: () => true, then: (t, ctx) => ctx.herdr.queueNudge(t, "y", { once: ["fine"] }) });`)

	out, _, err := h.runWith(context.Background(), dispatch.Options{RulesDir: dir, RuleTimeout: dispatch.RuleTimeout{Then: 50 * time.Millisecond}}, "rules", "check")

	require.ErrorContains(t, err, "1 problems found")
	assert.Contains(t, out, "finding: slow ran out of time in its then for ENG-1")
	assert.Regexp(t, `slow +whenever +linear\.ticket +wants nothing now\n`, out)
	assert.Regexp(t, `fine +whenever +linear\.ticket +wants nudge now for ENG-1\n`, out)
}

func TestRulesCheckGivesEachRuleCallAFreshRuntime(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	dir := oneRule(t, `
let calls = 0;
whenever({ name: "counts", each: "linear.ticket", if: () => { calls++; return calls === 1; },
  then: (t, ctx) => ctx.linear.queueComment(t, "first", { once: ["counts", t.id] }) });`)

	out, err := h.rules(dir, "check")

	require.NoError(t, err)
	assert.Regexp(t, `counts +whenever +linear\.ticket +wants comment now for ENG-1, ENG-2\n`, out)
}

func TestRulesCheckHoldsARecordWhoseReadFailedEvenWhenTheRuleCatchesIt(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.tools.Treehouse.FailTrees(errors.New("exit status 1"))
	dir := oneRule(t, `
whenever({ name: "catches", each: "linear.ticket", if: (t, ctx) => true,
  then: (t, ctx) => { try { ctx.treehouse.pool(t.repo); } catch (e) {} return ctx.linear.queueComment(t, "x", { once: ["c", t.id] }); } });`)

	out, err := h.rules(dir, "check")

	require.NoError(t, err)
	assert.Regexp(t, `catches +whenever +linear\.ticket +wants nothing now; held for ENG-1\n`, out)
}

func TestRulesCheckHoldsALeaseQuestionAboutAChildWhenTheRepoLabelGroupIsCustom(t *testing.T) {
	h := newHarness(t)
	h.config("[plugins.linear]\nrepo_label_group = \"project\"\n" + shippedRulesConfig)
	regroup := func(tk fakeTicket) fakeTicket {
		tk.Labels = []linear.Label{{Name: "ai"}, {Name: "dispatch", Group: "project"}}
		return tk
	}
	h.tickets(regroup(parent("ENG-1", "In Progress")))
	h.linear.setChildren(regroup(child("ENG-2", "ENG-1", "Todo")))
	h.tools.Treehouse.FailTrees(errors.New("exit status 1"))
	dir := oneRule(t, `whenever({ name: "asks", each: "linear.ticket", if: (t, ctx) => t.status === "todo" && !ctx.treehouse.leased(t),
  then: (t, ctx) => ctx.linear.queueComment(t, "free", { once: ["asks", t.id] }) });`)

	out, err := h.rules(dir, "check")

	require.NoError(t, err)
	assert.Regexp(t, `asks +whenever +linear\.ticket +wants nothing now; held for ENG-2\n`, out)
}

func TestRulesCheckHoldsARuleWhoseKnownCallbackCatchesTheUnknownRead(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	h.tools.Herdr.Fail(herdrtest.OpRead, errors.New("herdr is down"))
	dir := oneRule(t, `whenever({ name: "swallows", each: "linear.ticket",
  if: (t, ctx) => known(() => { try { return ctx.herdr.worker(t).state; } catch (e) { return 0; } }),
  then: (t, ctx) => ctx.linear.queueComment(t, "acted", { once: ["swallows", t.id] }) });`)

	out, err := h.rules(dir, "check")

	require.NoError(t, err)
	assert.Regexp(t, `swallows +whenever +linear\.ticket +wants nothing now`, out)
}

func TestRulesCheckRecordsAForgedActionHandleAsAThrow(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `whenever({ name: "forges", each: "linear.ticket", if: () => true, then: () => ({ queued: true, __id: 99 }) as any });
whenever({ name: "healthy", each: "linear.ticket", if: () => true, then: (t, ctx) => ctx.linear.queueComment(t, "ok", { once: ["h", t.id] }) });`)

	out, err := h.rules(dir, "check")

	require.ErrorContains(t, err, "problems found")
	assert.Contains(t, out, "finding: forges threw in its then for ENG-1: then returned something that isn't an action")
	assert.Regexp(t, `healthy +whenever +linear\.ticket +wants comment now for ENG-1`, out)
}

func TestRulesCheckNamesAMissingOnceKeyThatTheLintCouldNotSee(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `whenever({ name: "computed", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.linear.queueComment(t, "hi", (t.id === "ENG-1" ? {} : { once: ["x"] }) as any) });`)

	out, err := h.rules(dir, "check")

	require.ErrorContains(t, err, "problems found")
	assert.Contains(t, out, "once is missing. Every action needs once")
}

func TestRulesCheckStopsALoopAtTheTopOfARuleFile(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `for (;;) {}`)

	start := time.Now()
	_, _, err := h.runWith(context.Background(), dispatch.Options{RulesDir: dir, RuleTimeout: dispatch.RuleTimeout{Then: 100 * time.Millisecond}}, "rules", "check")

	require.Error(t, err)
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestRulesCheckCannotReachTheRealClockThroughDate(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `whenever({ name: "clock", each: "linear.ticket",
  if: (t, ctx) => new (new Date().constructor as any)().getTime() === ctx.now,
  then: (t, ctx) => ctx.linear.queueComment(t, "pinned", { once: ["c", t.id] }) });`)

	out, err := h.rules(dir, "check")

	require.NoError(t, err)
	assert.Regexp(t, `clock +whenever +linear\.ticket +wants comment now for ENG-1`, out)
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
			h := newHarness(t)
			h.config(shippedRulesConfig)
			h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
			dir := oneRule(t, `whenever({ name: "forges", each: "linear.ticket", if: () => true, then: `+test.then+` });`)

			out, err := h.rules(dir, "check")

			require.ErrorContains(t, err, "problems found")
			assert.Contains(t, out, "finding: forges threw in its then for ENG-1: then returned something that isn't an action")
		})
	}
}

func TestRulesCheckStopsAGetterInAReturnedObjectWithinTheBudget(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `whenever({ name: "getter", each: "linear.ticket", if: () => true,
  then: (t, ctx) => { ctx.linear.queueComment(t, "a", { once: ["a"] }); const returned: any[] = [null]; Object.defineProperty(returned, "0", { get() { for (;;) {} } }); return returned as any; } });
whenever({ name: "healthy", each: "linear.ticket", if: () => true, then: (t, ctx) => ctx.linear.queueComment(t, "ok", { once: ["h", t.id] }) });`)

	start := time.Now()
	out, _, err := h.runWith(context.Background(), dispatch.Options{RulesDir: dir, RuleTimeout: dispatch.RuleTimeout{Then: 100 * time.Millisecond}}, "rules", "check")

	require.ErrorContains(t, err, "problems found")
	assert.Less(t, time.Since(start), 5*time.Second)
	assert.Contains(t, out, "finding: getter ran out of time in its then for ENG-1: took longer than its time budget and was stopped")
	assert.Regexp(t, `healthy +whenever +linear\.ticket +wants comment now for ENG-1`, out)
}

func TestRulesCheckNamesAMissingKeyInANestedRulesTable(t *testing.T) {
	h := newHarness(t)
	h.config("[rules.slack]\nchannel = \"#eng\"\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `whenever({ name: "nested", each: "linear.ticket", if: (t, ctx) => ctx.config.rules.slack.channel === "#eng" && ctx.config.rules.slack.token === "x", then: () => {} });`)

	out, err := h.rules(dir, "check")

	require.ErrorContains(t, err, "problems found")
	assert.Contains(t, out, `config.toml has no [rules.slack] key "token"`)
}

func TestRulesCheckShowsWhatWaitsForAWorkerSlot(t *testing.T) {
	h := newHarness(t)
	h.config("[dispatch]\nmax_in_progress = 1\n" + shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))

	out, err := h.rules(repoRules, "check")

	require.NoError(t, err)
	assert.Regexp(t, `launch-worker +whenever +linear\.ticket +wants launch now for ENG-\d; waiting: launch for ENG-\d \(0 of 1 in progress, 1st in line\)\n`, out)
}

func TestRulesTestFailsAScenarioWhenARuleThrowsUnlessItExpectsTheThrow(t *testing.T) {
	h := newHarness(t)
	dir := rulesDir(t, map[string]string{
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
	})

	out, err := h.rules(dir, "test")

	require.ErrorContains(t, err, "1 of 2 scenarios failed")
	assert.Contains(t, out, "FAIL broken.test.ts: expects nothing from a broken rule")
	assert.Contains(t, out, "broken threw in its if for ENG-1")
	assert.Contains(t, out, "ok   broken.test.ts: expects the break")
}

func TestRulesCheckRefusesAHugeArrayWithoutBuildingIt(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `whenever({ name: "huge", each: "linear.ticket", if: () => true, then: () => new Array(1_000_000_000) as any });
whenever({ name: "huge-once", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.linear.queueComment(t, "x", { once: new Array(1_000_000_000) as any }) });`)

	out, err := h.rules(dir, "check")

	require.ErrorContains(t, err, "problems found")
	assert.Contains(t, out, "finding: huge threw in its then for ENG-1: then returned something that isn't an action")
	assert.Contains(t, out, "finding: huge-once threw in its then for ENG-1: ")
	assert.Contains(t, out, "once has more than 32 items")
}

// The test binary doubles as the tsc these tests put on PATH: a symlink named tsc
// points at it, and TestMain runs fakeTSCMain when it is started under that name.
func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == "tsc" {
		os.Exit(fakeTSCMain(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakeTSCMain records its arguments and the config named by -p, prints what the
// test asked for, and exits with the code the test asked for.
func fakeTSCMain(args []string) int {
	record := os.Getenv("FAKE_TSC_RECORD")
	out := strings.Join(args, " ") + "\n"
	if len(args) == 2 {
		if conf, err := os.ReadFile(args[1]); err == nil {
			out += string(conf)
		}
	}
	if err := os.WriteFile(record, []byte(out), 0o644); err != nil {
		return 99
	}
	fmt.Print(os.Getenv("FAKE_TSC_OUTPUT"))
	code, _ := strconv.Atoi(os.Getenv("FAKE_TSC_EXIT"))
	return code
}

// fakeTSC puts a tsc on PATH and returns the file it records its arguments in.
func fakeTSC(t *testing.T, exit int, output string) (record string) {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	bin := t.TempDir()
	require.NoError(t, os.Symlink(exe, filepath.Join(bin, "tsc")))
	record = filepath.Join(t.TempDir(), "args")
	t.Setenv("FAKE_TSC_RECORD", record)
	t.Setenv("FAKE_TSC_OUTPUT", output)
	t.Setenv("FAKE_TSC_EXIT", strconv.Itoa(exit))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return record
}

func TestRulesCheckTypeChecksWithTscThroughAConfigOfItsOwn(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	record := fakeTSC(t, 0, "")

	_, err := h.rules(repoRules, "check")

	require.NoError(t, err)
	got, readErr := os.ReadFile(record)
	require.NoError(t, readErr)
	assert.Contains(t, string(got), "-p ")
	assert.Contains(t, string(got), `"moduleResolution":"bundler"`)
	assert.Contains(t, string(got), `"strict":true`)
	assert.Contains(t, string(got), "dispatch.d.ts")
	assert.Contains(t, string(got), "launch.test.ts")
	assert.NotContains(t, string(got), "node10")
}

func TestRulesCheckReportsWhatTscFinds(t *testing.T) {
	h := newHarness(t)
	h.config(shippedRulesConfig)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	fakeTSC(t, 2, "shared.ts(6,57): error TS2322: Type 'unknown' is not assignable to type 'number'.")

	out, err := h.rules(repoRules, "check")

	require.ErrorContains(t, err, "1 problems found")
	assert.Contains(t, out, "finding: tsc: shared.ts(6,57): error TS2322")
}

func TestRulesTestAcknowledgesAThrowOnlyForTheCycleItWasMadeIn(t *testing.T) {
	h := newHarness(t)
	dir := rulesDir(t, map[string]string{
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
	})

	out, err := h.rules(dir, "test")

	require.ErrorContains(t, err, "1 of 2 scenarios failed")
	assert.Contains(t, out, "ok   broken.test.ts: acknowledged in the cycle it happens")
	assert.Contains(t, out, "FAIL broken.test.ts: not acknowledged in the next cycle")
}
