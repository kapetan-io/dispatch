package dispatch_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch"
	"github.com/kapetan-io/dispatch/internal/httpservicetest"
	"github.com/kapetan-io/dispatch/internal/ledger"
)

// commentBodies lists the text of the comments dispatch posted on Linear.
func (h *harness) commentBodies() []string {
	var out []string
	for _, m := range h.linear.mutations() {
		if m.Kind == "comment" {
			out = append(out, m.Body)
		}
	}
	return out
}

func TestAThenThatCallsGetActsOnTheResponse(t *testing.T) {
	h := newHarness(t)
	sentry := h.sentry()
	sentry.Respond("sentry", "GET", unresolved, 200, `[{"title":"KeyError"},{"title":"Timeout"}]`)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `whenever({
  name: "report-sentry", each: "linear.ticket",
  if: (t) => t.status === "in_progress",
  then: (t, ctx) => {
    const res = ctx.http.get("sentry", "/issues/?query=is:unresolved");
    return ctx.linear.queueComment(t, res.status + " " + (res.body as any[]).map((i) => i.title).join(","), { once: ["sentry", t.id] });
  },
});`)

	_, _, err := h.runWith(context.Background(), dispatch.Options{RulesDir: dir}, "tick")

	require.NoError(t, err)
	assert.Equal(t, []string{"200 KeyError,Timeout"}, h.commentBodies())
	reqs := sentry.Requests()
	require.NotEmpty(t, reqs)
	assert.Equal(t, "Bearer "+sentryToken, reqs[0].Authorization)
}

func TestAnIfThatCallsGetThrows(t *testing.T) {
	h := newHarness(t)
	sentry := h.sentry()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `whenever({ name: "fetch-in-if", each: "linear.ticket", if: (t, ctx) => ctx.http.get("sentry", "/me").status === 200, then: () => {} });`)

	out, err := h.rules(dir, "check")

	require.ErrorContains(t, err, "problem")
	assert.Contains(t, out, "fetch-in-if threw in its if for ENG-1: Error: ctx.http.get can only be called in then")
	assert.Empty(t, sentry.Requests())
}

func TestACallToAServiceConfigDoesNotNameThrowsAtTheCall(t *testing.T) {
	h := newHarness(t)
	sentry := h.sentry()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `whenever({ name: "wrong-service", each: "linear.ticket", if: () => true, then: (t, ctx) => { ctx.http.get("pagerduty", "/me"); } });`)

	out, err := h.rules(dir, "check")

	require.Error(t, err)
	assert.Contains(t, out, "wrong-service threw in its then for ENG-1: Error: ctx.http.get: no such service: [http.pagerduty] is not in config.toml")
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
			h := newHarness(t)
			sentry := h.sentry()
			h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
			dir := oneRule(t, `whenever({ name: "escape", each: "linear.ticket", if: () => true, then: (t, ctx) => { ctx.http.get("sentry", "`+test.path+`"); } });`)

			out, err := h.rules(dir, "check")

			require.Error(t, err)
			assert.Contains(t, out, "escape threw in its then for ENG-1: Error: ctx.http.get: bad path")
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
			h := newHarness(t)
			sentry := h.sentry()
			sentry.Respond("sentry", "GET", "/me", test.status, "boom "+sentryToken)
			h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
			dir := oneRule(t, `whenever({ name: "ask", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.linear.queueComment(t, "got " + ctx.http.get("sentry", "/me").status, { once: ["ask", t.id] }) });`)

			out, err := h.rules(dir, "check")

			require.NoError(t, err)
			assert.Regexp(t, `ask +whenever +linear\.ticket +wants nothing now; held for ENG-1\n`, out)
			assert.NotContains(t, out, sentryToken)
			assert.Empty(t, h.linear.mutations())
			require.Len(t, sentry.Requests(), 1)
			assert.Equal(t, "Bearer "+sentryToken, sentry.Requests()[0].Authorization)
		})
	}
}

func TestAGetThatOutlastsTheThenBudgetHoldsTheRuleInsteadOfThrowing(t *testing.T) {
	h := newHarness(t)
	sentry := h.sentry()
	sentry.Respond("sentry", "GET", "/slow", 200, `{}`)
	sentry.Stall("sentry", "GET", "/slow", 10*time.Second)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `whenever({ name: "slow", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.linear.queueComment(t, "got " + ctx.http.get("sentry", "/slow").status, { once: ["slow", t.id] }) });`)

	start := time.Now()
	stdout, _, err := h.runWith(context.Background(), dispatch.Options{RulesDir: dir, RuleTimeout: dispatch.RuleTimeout{Then: 100 * time.Millisecond}}, "rules", "check")

	require.NoError(t, err)
	assert.Less(t, time.Since(start), 5*time.Second)
	assert.Len(t, sentry.Requests(), 1)
	assert.Regexp(t, `slow +whenever +linear\.ticket +wants nothing now; held for ENG-1\n`, stdout)
}

func TestLastReadsTheResponseAGetKeptUntilTheNextResync(t *testing.T) {
	h := newHarness(t)
	sentry := h.sentry()
	sentry.Respond("sentry", "GET", "/count", 200, `{"open":3}`)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `
// Fetches once per ticket, so only the first cycle calls the service.
whenever({ name: "fetch", each: "linear.ticket", if: (t, ctx) => !ctx.ledger.done(t, ["fetch", t.id]),
  then: (t, ctx) => { ctx.http.get("sentry", "/count"); return ctx.linear.queueComment(t, "fetched", { once: ["fetch", t.id] }); } });
// Reads the kept response from an if, with no request of its own.
whenever({ name: "use-kept", each: "linear.ticket", if: (t, ctx) => (ctx.http.last("sentry", "/count").body as any).open > 0,
  then: (t, ctx) => ctx.linear.queueComment(t, "kept " + (ctx.http.last("sentry", "/count").body as any).open, { once: ["kept", t.id] }) });
// True only while nothing is kept.
whenever({ name: "forgot", each: "linear.ticket", if: (t, ctx) => !known(() => ctx.http.last("sentry", "/count")) && ctx.ledger.done(t, ["fetch", t.id]),
  then: (t, ctx) => ctx.linear.queueComment(t, "forgot", { once: ["forgot", t.id] }) });`)

	_, _, err := h.runWith(context.Background(), dispatch.Options{RulesDir: dir}, "tick")
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"fetched", "kept 3"}, h.commentBodies())
	assert.Len(t, sentry.Requests(), 1)

	h.advance(5 * time.Minute)
	h.tick()

	assert.ElementsMatch(t, []string{"fetched", "kept 3", "forgot"}, h.commentBodies())
	assert.Len(t, sentry.Requests(), 1)
}

func TestLastWithNothingKeptHoldsTheRule(t *testing.T) {
	h := newHarness(t)
	h.sentry()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `whenever({ name: "peek", each: "linear.ticket", if: (t, ctx) => (ctx.http.last("sentry", "/count").body as any).open > 0, then: () => {} });`)

	out, err := h.rules(dir, "check")

	require.NoError(t, err)
	assert.Regexp(t, `peek +whenever +linear\.ticket +wants nothing now; held for ENG-1\n`, out)
}

func TestAQueuedPostIsSentOnceWithTheServiceTokenAndRetriedWhenItFails(t *testing.T) {
	h := newHarness(t)
	sentry := h.sentry()
	sentry.Respond("sentry", "POST", "/issues/7/resolve", 503, "")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `whenever({ name: "resolve", each: "linear.ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.http.queuePost("sentry", "/issues/7/resolve", { status: "resolved", by: t.id }, { once: ["resolve", t.id] }) });`)

	_, _, _ = h.runWith(context.Background(), dispatch.Options{RulesDir: dir}, "tick")

	failed := h.receipts("post", "sentry")
	require.NotEmpty(t, failed)
	assert.Equal(t, ledger.Failed, failed[len(failed)-1].Outcome)
	assert.Contains(t, failed[len(failed)-1].Detail, "returned 503")

	sentry.Respond("sentry", "POST", "/issues/7/resolve", 200, `{"ok":true}`)
	h.advance(5 * time.Minute)
	h.tick()
	h.advance(5 * time.Minute)
	h.tick()

	done := h.receipts("post", "sentry")
	require.NotEmpty(t, done)
	assert.Equal(t, ledger.Done, done[len(done)-1].Outcome)
	var posts []httpservicetest.Request
	for _, r := range sentry.Requests() {
		if r.Method == "POST" && r.Authorization != "" {
			posts = append(posts, r)
		}
	}
	require.NotEmpty(t, posts)
	last := posts[len(posts)-1]
	assert.JSONEq(t, `{"status":"resolved","by":"ENG-1"}`, last.Body)
	assert.Equal(t, "Bearer "+sentryToken, last.Authorization)

	// The receipt covers the key, so a restart doesn't post again.
	sent := len(posts)
	h.restart()
	h.advance(5 * time.Minute)
	h.tick()
	var after int
	for _, r := range sentry.Requests() {
		if r.Method == "POST" {
			after++
		}
	}
	assert.Equal(t, sent, after)
}

func TestAPostToAServiceConfigDoesNotNameThrowsWhenQueued(t *testing.T) {
	h := newHarness(t)
	sentry := h.sentry()
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `whenever({ name: "wrong-post", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.http.queuePost("pagerduty", "/x", {}, { once: ["x", t.id] }) });`)

	out, err := h.rules(dir, "check")

	require.Error(t, err)
	assert.Contains(t, out, "wrong-post threw in its then for ENG-1: Error: ctx.http.queuePost: no such service")
	assert.Empty(t, sentry.Requests())
}

func TestTheServiceTokenNeverReachesARuleHistoryOrOutput(t *testing.T) {
	h := newHarness(t)
	sentry := h.sentry()
	sentry.Respond("sentry", "GET", "/echo", 200, `{"you":"`+sentryToken+`"}`)
	sentry.Respond("sentry", "GET", "/boom", 500, "token "+sentryToken)
	sentry.Respond("sentry", "POST", "/hook", 502, sentryToken)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `
whenever({ name: "echo", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.linear.queueComment(t, JSON.stringify(ctx.http.get("sentry", "/echo").body) + " " + JSON.stringify(ctx.config.http), { once: ["echo", t.id] }) });
whenever({ name: "boom", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.linear.queueComment(t, "boom " + ctx.http.get("sentry", "/boom").status, { once: ["boom", t.id] }) });
whenever({ name: "hook", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.http.queuePost("sentry", "/hook", { hello: "world" }, { once: ["hook", t.id] }) });`)

	stdout, _, firstErr := h.runWith(context.Background(), dispatch.Options{RulesDir: dir}, "tick")
	require.ErrorContains(t, firstErr, "POST sentry /hook: returned 502")
	h.advance(5 * time.Minute)
	secondOut, secondErr := h.run("tick")
	require.ErrorContains(t, secondErr, "POST sentry /hook: returned 502")
	checkOut, _ := h.rules(dir, "check")

	bodies := h.commentBodies()
	require.Len(t, bodies, 1)
	assert.Equal(t, `{"you":"[redacted]"} {"sentry":{"base_url":"https://sentry.test/api/0"}}`, bodies[0])
	var seen []string
	require.NoError(t, filepath.Walk(h.state, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || strings.HasSuffix(path, ".sock") {
			return err
		}
		body, rerr := os.ReadFile(path)
		require.NoError(t, rerr)
		seen = append(seen, string(body))
		return nil
	}))
	require.NotEmpty(t, seen)
	for _, body := range append(seen, stdout, secondOut, firstErr.Error(), secondErr.Error(), h.loopOut(), h.loopErr(), checkOut, strings.Join(h.notifications(), "\n"), strings.Join(h.commentBodies(), "\n")) {
		assert.NotContains(t, body, sentryToken)
	}
}

func TestEachResponseAThenWaitedOnIsRecordedInTheCycleLine(t *testing.T) {
	h := newHarness(t)
	sentry := h.sentry()
	sentry.Respond("sentry", "GET", "/count", 200, `{"open":3}`)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `whenever({ name: "count", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.linear.queueComment(t, "open " + (ctx.http.get("sentry", "/count").body as any).open, { once: ["count", t.id] }) });`)

	_, _, err := h.runWith(context.Background(), dispatch.Options{RulesDir: dir}, "tick")
	require.NoError(t, err)

	var lines []string
	files, err := filepath.Glob(filepath.Join(h.state, "events", "*.jsonl"))
	require.NoError(t, err)
	for _, f := range files {
		body, err := os.ReadFile(f)
		require.NoError(t, err)
		lines = append(lines, strings.Split(string(body), "\n")...)
	}
	var recorded int
	for _, line := range lines {
		if strings.Contains(line, `"line":"cycle"`) && strings.Contains(line, `"http":[{"service":"sentry","path":"/count","rule":"count","ticket":"ENG-1","status":200,"body":{"open":3}}]`) {
			recorded++
		}
	}
	assert.Positive(t, recorded)
}

func TestABaseURLThatIsNotAnHTTPURLStopsTheStart(t *testing.T) {
	for _, base := range []string{
		"ftp://sentry.test", "sentry.test/api", "https://", "https://user:pw@sentry.test",
		"https://sentry.test/api?org=1", "https://sentry.test/api?", "https://sentry.test/api#top",
	} {
		t.Run(base, func(t *testing.T) {
			h := newHarness(t)
			h.config("\n[http.sentry]\nbase_url = \"" + base + "\"\n")

			_, err := h.run("plan")

			require.ErrorContains(t, err, "http.sentry base_url must be an http or https URL")
		})
	}
}

func TestRulesTestRunsHTTPScenariosAgainstTheFakeServices(t *testing.T) {
	h := newHarness(t)
	h.sentry()
	dir := rulesDir(t, map[string]string{
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
	})

	out, err := h.rules(dir, "test")

	require.NoError(t, err, out)
	assert.Contains(t, out, "ok   sentry.test.ts: posts when sentry has nothing open")
	assert.Contains(t, out, "ok   sentry.test.ts: reads the response a get kept from another rule's if")
	assert.Contains(t, out, "ok   sentry.test.ts: holds while the service has no answer")
}

func TestAPostThatTimesOutCountsAsSentAndIsNotPostedAgain(t *testing.T) {
	h := newHarness(t)
	sentry := h.sentry()
	sentry.Respond("sentry", "POST", "/slow", 200, `{}`)
	sentry.Stall("sentry", "POST", "/slow", 10*time.Second)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `whenever({ name: "slow-post", each: "linear.ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.http.queuePost("sentry", "/slow", { id: t.id }, { once: ["slow-post", t.id] }) });`)
	opts := dispatch.Options{RulesDir: dir, ActionTimeout: 100 * time.Millisecond}

	_, _, err := h.runWith(context.Background(), opts, "tick")

	require.NoError(t, err)
	sent := h.receipts("post", "sentry")
	require.Len(t, sent, 1)
	assert.Equal(t, ledger.Done, sent[0].Outcome)
	assert.Equal(t, "timeout", sent[0].Detail)

	h.advance(5 * time.Minute)
	_, _, err = h.runWith(context.Background(), opts, "tick")

	require.NoError(t, err)
	var posts int
	for _, r := range sentry.Requests() {
		if r.Method == "POST" {
			posts++
		}
	}
	assert.Equal(t, 1, posts)
}

func TestAGetThatFailsIsRecordedInTheCycleLineWithItsReasonAndNoToken(t *testing.T) {
	h := newHarness(t)
	sentry := h.sentry()
	sentry.Respond("sentry", "GET", "/down", 503, "oops "+sentryToken)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `whenever({ name: "down", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.linear.queueComment(t, "got " + ctx.http.get("sentry", "/down").status, { once: ["down", t.id] }) });`)

	_, _, err := h.runWith(context.Background(), dispatch.Options{RulesDir: dir}, "tick")
	require.NoError(t, err)

	var recorded int
	files, err := filepath.Glob(filepath.Join(h.state, "events", "*.jsonl"))
	require.NoError(t, err)
	for _, f := range files {
		body, err := os.ReadFile(f)
		require.NoError(t, err)
		for _, line := range strings.Split(string(body), "\n") {
			if strings.Contains(line, `"line":"cycle"`) && strings.Contains(line, `"http":[{"service":"sentry","path":"/down","rule":"down","ticket":"ENG-1","unknown":"GET sentry /down: returned 503"}]`) {
				recorded++
			}
		}
	}
	assert.Positive(t, recorded)
	assert.Empty(t, h.commentBodies())
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
			h := newHarness(t)
			dir := rulesDir(t, map[string]string{
				"dispatch.ts": rulesTS,
				"a.test.ts": `import { scenario, ticket, rulesFrom } from "dispatch/testing";
const rules = rulesFrom("./dispatch");
const build = { status: "in_progress", optedIn: true, repo: "dispatch" };
scenario(rules, "case", s => { ` + test.body + ` });
`,
			})

			out, err := h.rules(dir, "test")

			require.Error(t, err)
			assert.Contains(t, out, test.want)
		})
	}
}

func TestRulesTestHTTPScenarioAnswersAMethodAndStatus(t *testing.T) {
	h := newHarness(t)
	dir := rulesDir(t, map[string]string{
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
	})

	out, err := h.rules(dir, "test")

	require.NoError(t, err, out)
	assert.Contains(t, out, "ok   a.test.ts: a 201 reads as a response")
	assert.Contains(t, out, "ok   a.test.ts: an answer for POST is not an answer for GET")
	assert.Contains(t, out, "ok   a.test.ts: a failing status holds the rule")
}

func TestAPostIsNotSentAgainAfterHistoryRotates(t *testing.T) {
	h := newHarness(t)
	sentry := h.sentry()
	h.config(sentryConfig + "\n[history]\nrotate = \"1m\"\nkeep = \"1d\"\n")
	sentry.Respond("sentry", "POST", "/hook", 200, `{}`)
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `whenever({ name: "hook", each: "linear.ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.http.queuePost("sentry", "/hook", { id: t.id }, { once: ["hook", t.id] }) });`)

	_, _, err := h.runWith(context.Background(), dispatch.Options{RulesDir: dir}, "tick")
	require.NoError(t, err)
	for range 4 {
		h.advance(6 * time.Minute)
		h.tick()
	}

	var posts int
	for _, r := range sentry.Requests() {
		if r.Method == "POST" {
			posts++
		}
	}
	assert.Equal(t, 1, posts)
}

func TestAPostWhoseTokenCommandOutlastsTheDeadlineSentNothingAndIsTriedAgain(t *testing.T) {
	h := newHarness(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	h.config("\n[http.sentry]\nbase_url = \"" + srv.URL + "\"\ntoken_command = \"sleep 5; echo tok\"\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `whenever({ name: "hook", each: "linear.ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.http.queuePost("sentry", "/hook", {}, { once: ["hook", t.id] }) });`)

	_, _, err := h.runWith(context.Background(), dispatch.Options{RulesDir: dir, ActionTimeout: 300 * time.Millisecond}, "tick")

	require.Error(t, err)
	receipts := h.receipts("post", "sentry")
	require.NotEmpty(t, receipts)
	assert.Equal(t, ledger.Failed, receipts[len(receipts)-1].Outcome)
	assert.Zero(t, hits.Load())
}

func TestRulesCheckCallsNoServiceUnlessGivenOne(t *testing.T) {
	h := newHarness(t)
	marker := filepath.Join(t.TempDir(), "token-command-ran")
	h.config("\n[http.sentry]\nbase_url = \"https://sentry.test/api/0\"\ntoken_command = \"touch " + marker + "; echo tok\"\n")
	h.tickets(ticket("ENG-1", "In Progress", "dispatch"))
	dir := oneRule(t, `whenever({ name: "ask", each: "linear.ticket", if: () => true,
  then: (t, ctx) => ctx.linear.queueComment(t, "got " + ctx.http.get("sentry", "/me").status, { once: ["ask", t.id] }) });`)

	out, err := h.rules(dir, "check")

	require.NoError(t, err)
	assert.Regexp(t, `ask +whenever +linear\.ticket +wants nothing now; held for ENG-1\n`, out)
	assert.NoFileExists(t, marker)
}
