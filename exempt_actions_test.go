package dispatch_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/herdrtest"
)

// pokeRule nudges each In Progress ticket's worker, then notifies its followers once the nudge lands.
const pokeRule = `
whenever({ name: "poke", each: "ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => [
    ctx.worker.queueNudge(t, "poke", { once: ["poke", t.id] }),
    ctx.notify.queueSend(t, t.id + " was poked", { once: ["poked", t.id] }),
  ] });
`

// holdFile is a second rule file whose exemption names actions for ENG-1, so it covers rules it never names.
func holdFile(actions string) map[string]string {
	return map[string]string{
		"dispatch.ts": ruleHeader + "import \"./hold\";\n" + pokeRule,
		"hold.ts": `import { exempt } from "dispatch";
exempt({ name: "hold-eng-1", each: "ticket", actions: ` + actions + `, if: (t) => t.id === "ENG-1" });`,
	}
}

func TestAnExemptionByActionTakesAwayAWantAndTheStepThatWaitsOnIt(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: holdFile(`["nudge"]`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Running("ENG-2", "idle")

	d.Tick()
	d.Tick()

	assert.Empty(t, d.Workers.Received("ENG-1"))
	assert.Equal(t, []string{"poke"}, d.Workers.Received("ENG-2"))
	for _, told := range d.Followed.Told() {
		assert.NotContains(t, told, "ENG-1 was poked")
	}
}

func TestRulesCheckReportsAWantAnExemptionByActionTookAwayAsExemptNotWaiting(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: holdFile(`["nudge"]`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Workers.Running("ENG-2", "idle")

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `poke +whenever +ticket +wants nudge now for ENG-2; waiting: notify for ENG-2 \(after the step before it lands\); exempt for ENG-1 by hold-eng-1\n`, res.Stdout)
	assert.NotContains(t, res.Stdout, "for ENG-1 (after")
	assert.Regexp(t, `hold-eng-1 +exempt +ticket +exempts nudge\n`, res.Stdout)
}

func TestAnExemptionByActionWithATopicTakesAwayOnlyThatTopic(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: map[string]string{
		"dispatch.ts": ruleHeader + `import "./hold";
whenever({ name: "tell", each: "ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => {
    ctx.notify.queueSend(t, t.id + " one", { once: ["one", t.id] });
    ctx.notify.queueSend(t, t.id + " two", { once: ["two", t.id] });
  } });`,
		"hold.ts": `import { exempt } from "dispatch";
exempt({ name: "hold-one", each: "ticket", actions: [{ kind: "notify", topic: "one" }], if: () => true });`,
	}})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	d.Tick()
	res := d.Run("rules", "check")

	require.Len(t, d.Followed.Told(), 1)
	assert.Contains(t, d.Followed.Told()[0], "ENG-1 two")
	require.NoError(t, res.Err)
	assert.Regexp(t, `hold-one +exempt +ticket +exempts notify \(one\)\n`, res.Stdout)
}

func TestAnExemptionByActionHoldsAShippedMergeAndTheNotificationAfterIt(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{RepoSettings: autoMergeSetting, Rules: shippedRulesAnd(t, `
import { exempt } from "dispatch";
exempt({ name: "no-merge", each: "ticket", actions: ["merge"], if: (t) => t.id === "ENG-1" });`)})
	d.Linear.Tickets(ticket("ENG-1", "In Review", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.GitHub.PullRequest("ENG-1", approvedPR("a1"))

	d.Tick()
	res := d.Run("rules", "check")

	assert.Empty(t, d.GitHub.Merged())
	assert.Empty(t, d.Followed.Told())
	require.NoError(t, res.Err)
	assert.Regexp(t, `auto-merge +whenever +pr +wants nothing now; exempt for ENG-1 by no-merge\n`, res.Stdout)
}

func TestAnExemptionByActionThatReadsAnUnknownHoldsTheWant(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: map[string]string{
		"dispatch.ts": ruleHeader + `import "./hold";
whenever({ name: "greet", each: "ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.ticket.queueComment(t, "hi", { once: ["greet", t.id] }) });`,
		"hold.ts": `import { exempt } from "dispatch";
exempt({ name: "hold-idle", each: "ticket", actions: ["comment"], if: (t, ctx) => ctx.worker.of(t).state === "idle" });`,
	}})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tools().Multiplexer.Fail(herdrtest.OpRead, errors.New("herdr is down"))

	d.Tick()
	res := d.Run("rules", "check")

	assert.Empty(t, d.Linear.Comments())
	require.NoError(t, res.Err)
	assert.Regexp(t, `greet +whenever +ticket +wants nothing now; held for ENG-1\n`, res.Stdout)

	d.Tools().Multiplexer.Fail(herdrtest.OpRead, nil)
	d.Tick()
	assert.Equal(t, []string{"ENG-1: hi"}, d.Linear.Comments())
}

func TestAnExemptionByActionThatThrowsTakesAwayTheWant(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: map[string]string{
		"dispatch.ts": ruleHeader + `import "./hold";
whenever({ name: "greet", each: "ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.ticket.queueComment(t, "hi", { once: ["greet", t.id] }) });`,
		"hold.ts": `import { exempt } from "dispatch";
exempt({ name: "broken", each: "ticket", actions: ["comment"], if: () => { throw new Error("boom"); } });`,
	}})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	d.Tick()
	res := d.Run("rules", "check")

	assert.Empty(t, d.Linear.Comments())
	require.Error(t, res.Err)
	assert.Contains(t, res.Stdout, "finding: broken threw in its if for ENG-1: Error: boom")
}

func TestAnExemptionByActionThatIsFalseLeavesTheWants(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: map[string]string{
		"dispatch.ts": ruleHeader + "import \"./hold\";\n" + pokeRule,
		"hold.ts": `import { exempt } from "dispatch";
exempt({ name: "hold-nobody", each: "ticket", actions: ["nudge", "notify"], if: () => false });`,
	}})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")

	d.Tick()
	d.Tick()

	assert.Equal(t, []string{"poke"}, d.Workers.Received("ENG-1"))
	require.Len(t, d.Followed.Told(), 1)
	assert.Contains(t, d.Followed.Told()[0], "ENG-1 was poked")
}

func TestDispatchCycleExplainAndReplayReportAWantAnExemptionByActionTookAway(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: holdFile(`["nudge"]`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Workers.Running("ENG-1", "idle")
	d.Tick()
	d.Stop()

	cycle := d.Run("cycle")
	explain := d.Run("explain", "ENG-1")
	cycles, matched := replayOf(t, d)

	require.NoError(t, cycle.Err)
	assert.Regexp(t, `exempt +ENG-1 +nudge +poke +by hold-eng-1`, cycle.Stdout)
	assert.Regexp(t, `exempt +ENG-1 +notify +poke +by hold-eng-1`, cycle.Stdout)
	assert.NotRegexp(t, `waiting +ENG-1`, cycle.Stdout)
	require.NoError(t, explain.Err)
	assert.Regexp(t, `poke  exempt: stopped by hold-eng-1`, explain.Stdout)
	assert.Positive(t, cycles)
	assert.Equal(t, cycles, matched)
}

func TestRulesTestAssertsAWantWasExemptAndByWhichExemption(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: map[string]string{
		"dispatch.ts": ruleHeader + "import \"./hold\";\n" + pokeRule + `
on({ name: "answer", event: "comment.posted", if: () => true,
  then: (c, ctx) => ctx.worker.queueNudge(c.ticket, "answer", { once: ["answer", c.id] }) });`,
		"hold.ts": `import { exempt } from "dispatch";
exempt({ name: "hold-eng-1", each: "ticket", actions: ["nudge"], if: (t) => t.id === "ENG-1" });`,
		"hold.test.ts": `import { scenario, ticket, worker, comment, rulesFrom } from "dispatch/testing";
const rules = rulesFrom("./dispatch");
scenario(rules, "poke", s => {
  s.given(ticket("ENG-1", { status: "in_progress" }), worker("ENG-1", "idle"));
  s.expect([]);
  s.expectExempt("poke", "ENG-1", "hold-eng-1", "nudge");
  s.expectExempt("poke", "ENG-1", "hold-eng-1", "notify");
});
scenario(rules, "answer", s => {
  s.given(ticket("ENG-1", { status: "in_review" }), worker("ENG-1", "idle"));
  s.see(comment("ENG-1", { body: "go on", author: "Derrick" }));
  s.expect([]);
  s.expectExempt("answer", "ENG-1", "hold-eng-1");
});
scenario(rules, "wrong exemption", s => {
  s.given(ticket("ENG-1", { status: "in_progress" }), worker("ENG-1", "idle"));
  s.expectExempt("poke", "ENG-1", "someone-else");
});`,
	}})

	res := d.Run("rules", "test")

	require.Error(t, res.Err)
	assert.Contains(t, res.Stdout, "ok   hold.test.ts: poke")
	assert.Contains(t, res.Stdout, "ok   hold.test.ts: answer")
	assert.Contains(t, res.Stdout, "expected poke to be exempt for ENG-1 by someone-else, but the cycle exempted poke for ENG-1 by hold-eng-1 (nudge), poke for ENG-1 by hold-eng-1 (notify)")
}

func TestRulesCheckFindsEachMalformedExemptionByAction(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{"Neither", `exempt({ name: "x", each: "ticket", if: () => true } as any);`,
			`exemption "x": rules must be a list of rule names, or actions a list of action names`},
		{"Both", `whenever({ name: "a", each: "ticket", if: () => false, then: () => {} });
exempt({ name: "x", each: "ticket", rules: ["a"], actions: ["nudge"], if: () => true } as any);`,
			`exemption "x": exempt takes rules or actions, not both`},
		{"EmptyActions", `exempt({ name: "x", each: "ticket", actions: [], if: () => true });`,
			`exemption "x": actions is empty`},
		{"UnknownAction", `exempt({ name: "x", each: "ticket", actions: ["poke" as any], if: () => true });`,
			`exemption "x": there is no action "poke"`},
		{"TopicWithoutKind", `exempt({ name: "x", each: "ticket", actions: [{ topic: "idle" } as any], if: () => true });`,
			`exemption "x": actions must be a list of action names`},
		{"UnknownKindWithTopic", `exempt({ name: "x", each: "ticket", actions: [{ kind: "poke", topic: "idle" } as any], if: () => true });`,
			`exemption "x": there is no action "poke"`},
		{"NotAList", `exempt({ name: "x", each: "ticket", actions: "nudge" as any, if: () => true });`,
			`exemption "x": actions must be a list of action names`},
		{"EachPR", `exempt({ name: "x", each: "pr", actions: ["nudge"], if: () => true } as any);`,
			`exemption "x": exempt with actions takes each: "ticket"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: rule(`import { exempt } from "dispatch";
` + test.body)})
			d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

			res := d.Run("rules", "check")

			require.Error(t, res.Err)
			assert.Contains(t, res.Stdout, "finding:")
			assert.Contains(t, res.Stdout, test.want)
			assert.Equal(t, 1, strings.Count(res.Stdout, "finding:"))
		})
	}
}

func TestRulesCheckListsEveryActionAnExemptionByActionNames(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig, Rules: holdFile(`["nudge", "merge", { kind: "escalate", topic: "pr-ignored" }]`)})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `hold-eng-1 +exempt +ticket +exempts nudge, merge, escalate \(pr-ignored\)\n`, res.Stdout)
}

func TestAnExemptionByActionLetsTheRulesThenRunSoItsReadsAndHTTPCallShow(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: map[string]string{
		"dispatch.ts": ruleHeader + "import \"./hold\";\n" + `
whenever({ name: "count", each: "ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.ticket.queueComment(t, "open " + (ctx.http.get("sentry", "/count").body as any).open, { once: ["count", t.id] }) });`,
		"hold.ts": `import { exempt } from "dispatch";
exempt({ name: "hold-eng-1", each: "ticket", actions: ["comment"], if: (t) => t.id === "ENG-1" });`,
	}})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "GET", "/count", 200, `{"open":3}`)
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tick()
	d.Stop()

	res := d.Run("explain", "ENG-1")

	assert.Empty(t, d.Linear.Comments())
	require.Len(t, sentry.Requests(), 1)
	assert.Equal(t, "/count", sentry.Requests()[0].Path)
	require.NoError(t, res.Err)
	assert.Regexp(t, `count  exempt: stopped by hold-eng-1`, res.Stdout)
	assert.Contains(t, res.Stdout, "http.get sentry /count=200")
	assert.Contains(t, res.Stdout, "http  count  GET sentry /count")
}

func TestAnExemptionByActionTakesAwayOnlyTheLaterStepsOfItsOwnThen(t *testing.T) {
	t.Parallel()
	// One rule runs over two trees of ENG-1, and both thens queue a comment then a notify with the same once keys.
	// Only the first tree's then starts with the nudge the exemption takes away. A second rule's notify waits on
	// a nudge that landed before its ticket became exempt.
	d := newDispatch(t, dispatchOptions{Rules: map[string]string{
		"dispatch.ts": `import { whenever, exempt } from "dispatch";

whenever({ name: "chain", each: "tree", if: (tr) => tr.holder !== undefined && tr.holder.id === "ENG-1",
  then: (tr, ctx) => {
    const t = tr.holder!;
    if (tr.path === "/trees/a") {
      return [
        ctx.worker.queueNudge(t, "a", { once: ["a"] }),
        ctx.ticket.queueComment(t, "c", { once: ["c"] }),
        ctx.notify.queueSend(t, "n", { once: ["n"] }),
      ];
    }
    return [
      ctx.ticket.queueComment(t, "c", { once: ["c"] }),
      ctx.notify.queueSend(t, "n", { once: ["n"] }),
    ];
  } });

whenever({ name: "pair", each: "ticket", if: (t) => t.id === "ENG-2" && t.status === "in_progress",
  then: (t, ctx) => [
    ctx.worker.queueNudge(t, "p", { once: ["p"] }),
    ctx.notify.queueSend(t, "e", { once: ["e"] }),
  ] });

exempt({ name: "hold-a", each: "ticket", actions: [{ kind: "nudge", topic: "a" }], if: (t) => !t.hasLabel("open") });
exempt({ name: "hold-p", each: "ticket", actions: [{ kind: "nudge", topic: "p" }], if: (t) => t.hasLabel("hold") });
`,
		"chain.test.ts": `import { scenario, ticket, tree, worker, rulesFrom } from "dispatch/testing";
const rules = rulesFrom("./dispatch");
const working = { status: "in_progress", optedIn: true, repo: "dispatch" };

scenario(rules, "chain b's notify runs after chain b's comment lands", s => {
  s.given(ticket("ENG-1", working), worker("ENG-1", "idle"), tree("/trees/a", { holder: "ENG-1" }), tree("/trees/b", { holder: "ENG-1" }));
  s.expect([["chain", "comment", "ENG-1"]]);
  s.land("comment", "ENG-1");
  s.expect([["chain", "notify", "ENG-1"]]);
});

scenario(rules, "a later step whose step before it landed stays when the exemption turns true", s => {
  s.given(ticket("ENG-2", working), worker("ENG-2", "idle"));
  s.expect([["pair", "nudge", "ENG-2"]]);
  s.land("nudge", "ENG-2");
  s.see(ticket("ENG-2", { ...working, labels: ["hold"] }));
  s.expect([["pair", "notify", "ENG-2"]]);
});

scenario(rules, "control: without the exemption chain b's notify runs after the comment lands", s => {
  s.given(ticket("ENG-1", { ...working, labels: ["open"] }), worker("ENG-1", "idle"), tree("/trees/a", { holder: "ENG-1" }), tree("/trees/b", { holder: "ENG-1" }));
  s.expect([["chain", "nudge", "ENG-1"]]);
  s.land("nudge", "ENG-1");
  s.land("comment", "ENG-1");
  s.expect([["chain", "notify", "ENG-1"]]);
});
`,
	}})

	res := d.Run("rules", "test")

	require.NoError(t, res.Err)
	assert.Contains(t, res.Stdout, "ok   chain.test.ts: chain b's notify runs after chain b's comment lands")
	assert.Contains(t, res.Stdout, "ok   chain.test.ts: a later step whose step before it landed stays when the exemption turns true")
	assert.Contains(t, res.Stdout, "ok   chain.test.ts: control: without the exemption chain b's notify runs after the comment lands")
}
