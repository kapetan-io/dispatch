package dispatch_test

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/httpservicetest"
)

// thread is one element of GitHub's notifications list.
func thread(reason string, unread bool, updated, repo, title string) map[string]any {
	return map[string]any{
		"reason": reason, "unread": unread, "updated_at": updated, "repository": map[string]any{"full_name": repo},
		"subject": map[string]any{"title": title, "url": "https://api.github.com/repos/" + repo + "/pulls/7", "type": "PullRequest"},
	}
}

const tellEveryItem = `whenever({ name: "tell", each: "inbox", if: (i) => true,
  then: (i, ctx) => ctx.http.queuePost("sentry", "/inbox", { id: i.id, reason: i.reason }, { once: ["tell", i.id, i.updatedAt] }) });`

func TestInboxScenarioGivesRulesTheNewest50Of60Items(t *testing.T) {
	t.Parallel()
	files := rule(tellEveryItem + `
whenever({ name: "old", each: "inbox", if: (i) => parseInt(i.id.slice(1)) <= 10, then: (i, ctx) => ctx.http.queuePost("sentry", "/old", {}, { once: ["old", i.id] }) });`)
	files["inbox.test.ts"] = `import { scenario, inbox, rulesFrom } from "dispatch/testing";
const rules = rulesFrom("./dispatch");
const pad = (n: number) => String(n).padStart(2, "0");

scenario(rules, "keeps the 50 most recently updated whatever order they are given in", s => {
  s.config({ http: { sentry: { baseUrl: "https://sentry.test/api/0" } } });
  for (let n = 60; n >= 1; n--) {
    s.given(inbox("t" + n, { updatedAt: "2026-10-01T" + pad(Math.floor(n / 60)) + ":" + pad(n % 60) + ":00Z" }));
  }
  s.start();
  s.expect(Array.from({ length: 50 }, () => ["tell", "post", "sentry"]));
});
`
	d := newDispatch(t, dispatchOptions{Rules: files})
	d.Services.Sentry()

	res := d.Run("rules", "test")

	require.NoError(t, res.Err, res.Stdout)
	assert.Contains(t, res.Stdout, "ok   inbox.test.ts: keeps the 50 most recently updated")
}

func TestInboxScenarioRunsATicketRuleBesideAFailedInboxRead(t *testing.T) {
	t.Parallel()
	files := rule(tellEveryItem + `
whenever({ name: "ask", each: "ticket", if: (t) => t.status === "in_progress", then: (t, ctx) => ctx.ticket.queueComment(t, "hi", { once: ["ask", t.id] }) });
whenever({ name: "stale", each: "ticket", if: (t, ctx) => ctx.dispatch.inboxReadAt !== undefined, then: (t, ctx) => ctx.ticket.queueComment(t, "read", { once: ["stale", t.id] }) });`)
	files["inbox.test.ts"] = `import { scenario, ticket, inbox, rulesFrom } from "dispatch/testing";
const rules = rulesFrom("./dispatch");
const build = { status: "in_progress", optedIn: true, repo: "dispatch" };

scenario(rules, "keeps the items given before the failed read and runs the ticket rules", s => {
  s.config({ http: { sentry: { baseUrl: "https://sentry.test/api/0" } } });
  s.given(ticket("ENG-1", build), inbox("t1"));
  s.unread("inbox");
  s.given(inbox("t2"));
  s.resync();
  s.expect([["tell", "post", "sentry"], ["ask", "comment", "ENG-1"], ["stale", "comment", "ENG-1"]]);
});
`
	d := newDispatch(t, dispatchOptions{Rules: files})
	d.Services.Sentry()

	res := d.Run("rules", "test")

	require.NoError(t, res.Err, res.Stdout)
}

func TestRulesCheckSaysItDidNotReadTheInboxWhenNoRuleRunsOverIt(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig})
	d.Tools().Inbox.Put("1", thread("mention", true, "2026-10-31T09:00:00Z", "acme/widgets", "Fix"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `inbox +not read \(no loaded rule runs over inbox\)`, res.Stdout)
	assert.Empty(t, d.Tools().Calls("inbox", "read"))
}

func TestRulesCheckCountsTheInboxItems(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(tellEveryItem)})
	d.Services.Sentry()
	d.Tools().Inbox.Put("1", thread("mention", true, "2026-10-31T09:00:00Z", "acme/widgets", "Fix"))
	d.Tools().Inbox.Put("2", thread("author", false, "2026-10-31T08:00:00Z", "acme/gadgets", "Add"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `inbox +2 inbox items \(newest 50 of the GitHub inbox\), 1 unread, 0 in configured repos`, res.Stdout)
	assert.Regexp(t, `tell +whenever +inbox +wants post now for sentry`, res.Stdout)
}

func TestRulesCheckSaysWhyItDidNotReadTheInbox(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(tellEveryItem)})
	d.Services.Sentry()
	d.Tools().Inbox.FailRead(errors.New("gh: HTTP 401"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `inbox +not read: gh: HTTP 401`, res.Stdout)
	assert.Contains(t, res.Stderr, "dispatch: while reading the inbox: gh: HTTP 401")
}

func TestATickReadsTheInboxAndAFailedReadKeepsTheLastGoodItems(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(tellEveryItem)})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "POST", "/inbox", 200, "{}")
	d.Tools().Inbox.Put("1", thread("mention", true, "2026-10-31T09:00:00Z", "acme/widgets", "Fix"))
	d.Tick()
	require.Equal(t, 1, countPosts(sentry))

	reads := len(d.Tools().Calls("inbox", "read"))
	d.Tools().Inbox.FailRead(errors.New("gh: HTTP 502"))
	d.Tools().Inbox.Put("2", thread("mention", true, "2026-10-31T09:05:00Z", "acme/widgets", "Other"))
	d.Advance(5 * time.Minute)
	d.Tick()

	assert.Equal(t, 1, countPosts(sentry))
	failed := len(d.Tools().Calls("inbox", "read")) - reads
	assert.Positive(t, failed)
	assert.Equal(t, failed, strings.Count(d.loopErr(), "dispatch: while reading the inbox: gh: HTTP 502\n"))

	d.Tools().Inbox.FailRead(nil)
	d.Advance(5 * time.Minute)
	d.Tick()

	assert.Equal(t, 2, countPosts(sentry))
}

const readTime = `whenever({ name: "when", each: "ticket", if: (t) => t.status === "in_progress",
  then: (t, ctx) => ctx.http.queuePost("sentry", "/when", { at: ctx.dispatch.inboxReadAt === undefined ? "never" : ctx.dispatch.inboxReadAt }, { once: ["when", t.id, String(ctx.dispatch.inboxReadAt)] }) });`

func postBodies(sentry *httpservicetest.Fake, path string) []string {
	var bodies []string
	for _, r := range sentry.Requests() {
		if r.Method == "POST" && r.Path == path {
			bodies = append(bodies, r.Body)
		}
	}
	return bodies
}

func TestAFailedInboxReadAtStartLeavesNoItemsAndNoReadTime(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(tellEveryItem + readTime)})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "POST", "/inbox", 200, "{}")
	sentry.Respond("sentry", "POST", "/when", 200, "{}")
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tools().Inbox.Put("1", thread("mention", true, "2026-10-31T09:00:00Z", "acme/widgets", "Fix"))
	d.Tools().Inbox.FailRead(errors.New("gh: HTTP 401"))

	d.Tick()

	assert.Zero(t, countPostsTo(sentry, "/inbox"))
	assert.Equal(t, []string{`{"at":"never"}`}, postBodies(sentry, "/when"))
	assert.Contains(t, d.loopErr(), "dispatch: while reading the inbox: gh: HTTP 401")
}

func TestARuleOverATicketReadsTheTimeOfTheLastGoodInboxRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(tellEveryItem + readTime)})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "POST", "/inbox", 200, "{}")
	sentry.Respond("sentry", "POST", "/when", 200, "{}")
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tools().Inbox.Put("1", thread("mention", true, "2026-10-31T09:00:00Z", "acme/widgets", "Fix"))

	d.Tick()
	good := d.Clock.Now()
	d.Tools().Inbox.FailRead(errors.New("gh: HTTP 502"))
	d.Advance(5 * time.Minute)
	d.Tick()

	// The failed read leaves the time of the good one, so the once key finds nothing new.
	assert.Equal(t, []string{fmt.Sprintf(`{"at":%d}`, good.UnixMilli())}, postBodies(sentry, "/when"))
}

func TestWithNoRuleOverTheInboxDispatchMakesNoInboxCallAndHistoryHoldsNoItems(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tools().Inbox.Put("1", thread("mention", true, "2026-10-31T09:00:00Z", "acme/widgets", "Fix"))

	d.Tick()
	d.Advance(5 * time.Minute)
	d.Tick()
	d.Stop()

	assert.Empty(t, d.Tools().Calls("inbox", "read"))
	for _, body := range d.StateFiles() {
		// The assertions avoid assert.NotContains, which prints the whole segment on failure.
		assert.False(t, strings.Contains(body, `"inbox":`) || strings.Contains(body, `"inboxErr"`) || strings.Contains(body, `"inboxReadAt":"`) || strings.Contains(body, `"field":"inboxReadAt"`))
		assert.False(t, strings.Contains(body, `"record":"inbox/`) || strings.Contains(body, "Fix"))
	}
}

func TestStateResetQuietRefusesWhileTheInboxCannotBeRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(tellEveryItem)})
	d.Services.Sentry()
	d.Tick()
	d.Stop()
	d.Tools().Inbox.FailRead(errors.New("gh: HTTP 502"))

	res := d.Run("state", "reset", "--quiet")

	require.ErrorContains(t, res.Err, "gh: HTTP 502")
}

func TestReplayOfARunThatReadInboxItemsReportsEveryCycleTheSameAsLive(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(tellEveryItem + readTime)})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "POST", "/inbox", 200, "{}")
	sentry.Respond("sentry", "POST", "/when", 200, "{}")
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tools().Inbox.Put("1", thread("mention", true, "2026-10-31T09:00:00Z", "acme/widgets", "Fix"))
	d.Tick()
	d.Tools().Inbox.FailRead(errors.New("gh: HTTP 502"))
	d.Advance(5 * time.Minute)
	d.Tick()
	d.Tools().Inbox.FailRead(nil)
	d.Tools().Inbox.Put("2", thread("review_requested", true, "2026-10-31T09:10:00Z", "acme/widgets", "Other"))
	d.Advance(5 * time.Minute)
	d.Tick()
	d.Stop()

	cycles, matched := replayOf(t, d)

	assert.Greater(t, cycles, 2)
	assert.Equal(t, cycles, matched)
}

func countPostsTo(sentry *httpservicetest.Fake, path string) int {
	return len(postBodies(sentry, path))
}

const tellEveryChange = `whenever({ name: "change", each: "inbox", if: (i) => true,
  then: (i, ctx) => ctx.http.queuePost("sentry", "/change", { id: i.id, reason: i.reason, unread: i.unread }, { once: ["change", i.id, i.reason, String(i.unread)] }) });`

func TestAnItemThatAppearsOnGitHubIsARecordAfterTheNextResyncAndTheNextTick(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(tellEveryItem)})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "POST", "/inbox", 200, "{}")
	d.Tools().Inbox.Put("1", thread("mention", true, "2026-10-31T09:00:00Z", "acme/widgets", "Fix"))
	d.Tick()
	require.Equal(t, 1, countPostsTo(sentry, "/inbox"))

	d.Tools().Inbox.Put("2", thread("mention", true, "2026-10-31T09:05:00Z", "acme/widgets", "Other"))
	d.Advance(5 * time.Minute)

	assert.Equal(t, 2, countPostsTo(sentry, "/inbox"))

	d.Tools().Inbox.Put("3", thread("mention", true, "2026-10-31T09:10:00Z", "acme/widgets", "Third"))
	d.Tick()

	assert.Equal(t, 3, countPostsTo(sentry, "/inbox"))
}

func TestAnItemWhoseReasonOrUnreadChangesOnGitHubReadsTheNewValueAfterTheNextRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(tellEveryChange)})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "POST", "/change", 200, "{}")
	d.Tools().Inbox.Put("1", thread("mention", true, "2026-10-31T09:00:00Z", "acme/widgets", "Fix"))
	d.Tick()
	require.Equal(t, []string{`{"id":"1","reason":"mention","unread":true}`}, postBodies(sentry, "/change"))

	// Reason and unread change while id and updated_at stay as they were.
	d.Tools().Inbox.Put("1", thread("review_requested", true, "2026-10-31T09:00:00Z", "acme/widgets", "Fix"))
	d.Tick()
	d.Tools().Inbox.Put("1", thread("review_requested", false, "2026-10-31T09:00:00Z", "acme/widgets", "Fix"))
	d.Advance(5 * time.Minute)

	assert.Equal(t, []string{
		`{"id":"1","reason":"mention","unread":true}`,
		`{"id":"1","reason":"review_requested","unread":true}`,
		`{"id":"1","reason":"review_requested","unread":false}`,
	}, postBodies(sentry, "/change"))
}

func TestAnInboxItemChangeInAScenarioReadsTheNewValueAfterTheNextResync(t *testing.T) {
	t.Parallel()
	files := rule(tellEveryChange)
	files["inbox.test.ts"] = `import { scenario, inbox, rulesFrom } from "dispatch/testing";
const rules = rulesFrom("./dispatch");

scenario(rules, "reads the reason and unread GitHub has now", s => {
  s.config({ http: { sentry: { baseUrl: "https://sentry.test/api/0" } } });
  s.given(inbox("t1", { reason: "mention", unread: true }));
  s.start();
  s.expect([["change", "post", "sentry"]]);
  s.given(inbox("t1", { reason: "author", unread: false }));
  s.resync();
  s.expect([["change", "post", "sentry"]]);
});
`
	d := newDispatch(t, dispatchOptions{Rules: files})
	d.Services.Sentry()

	res := d.Run("rules", "test")

	require.NoError(t, res.Err, res.Stdout)
	assert.Contains(t, res.Stdout, "ok   inbox.test.ts: reads the reason and unread GitHub has now")
}

func TestARuleOverATicketFiresWhileTheInboxReadFails(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(tellEveryItem + `
whenever({ name: "ask", each: "ticket", if: (t) => t.status === "in_progress", then: (t, ctx) => ctx.ticket.queueComment(t, "hi", { once: ["ask", t.id] }) });`)})
	d.Services.Sentry()
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))
	d.Tools().Inbox.FailRead(errors.New("gh: HTTP 401"))

	d.Tick()

	require.Len(t, d.Receipts("ENG-1"), 1)
	assert.Equal(t, "done", d.Receipts("ENG-1")[0].State)
}

func TestAnIdleWorkerAnActionsRereadAndWakeMakeNoInboxCallOnceTheLoopSettles(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: shippedRulesAnd(t, tellEveryItem), Config: shippedRulesConfig})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "POST", "/inbox", 200, "{}")
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"), ticket("ENG-2", "In Progress", "dispatch"))
	tree := d.Workers.CheckedOut("ENG-2", "working")
	d.Tools().Inbox.Put("1", thread("mention", true, "2026-10-31T09:00:00Z", "acme/widgets", "Fix"))
	d.Tick()
	require.NotEmpty(t, d.Workers.Launched())
	prompt := fmt.Sprintf(`{"hook_event_name":"UserPromptSubmit","session_id":"worker","cwd":%q,"prompt":"hello"}`, tree)
	// A wake runs a read of herdr and the pools, so it makes the loop take the reads the launch asked for before the count.
	require.NoError(t, d.HookAndWait("wake", prompt).Err)
	reads := len(d.Tools().Calls("inbox", "read"))

	d.Workers.SetStatus("ENG-2", "idle")
	require.NoError(t, d.HookAndWait("wake", prompt).Err)
	d.Workers.SetStatus("ENG-2", "working")
	require.NoError(t, d.HookAndWait("wake", prompt).Err)
	d.Workers.SetStatus("ENG-2", "idle")
	d.Workers.Stop("ENG-2", stopTasks{})
	require.NoError(t, d.HookAndWait("wake", prompt).Err)

	assert.Equal(t, reads, len(d.Tools().Calls("inbox", "read")))
}

const tellStaleness = `whenever({ name: "stale", each: "inbox", if: (i) => true,
  then: (i, ctx) => ctx.http.queuePost("sentry", "/stale", { id: i.id, age: ctx.now - ctx.dispatch.inboxReadAt }, { once: ["stale", i.id, String(ctx.now)] }) });`

func TestAFailedInboxReadKeepsTheLastGoodItemsAndReadTimeForRulesToAct(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(tellStaleness)})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "POST", "/stale", 200, "{}")
	d.Tools().Inbox.Put("1", thread("mention", true, "2026-10-31T09:00:00Z", "acme/widgets", "Fix"))
	d.Tick()
	d.Tools().Inbox.FailRead(errors.New("gh: HTTP 502"))
	d.Tools().Inbox.Remove("1")
	d.Advance(5 * time.Minute)

	bodies := postBodies(sentry, "/stale")

	require.NotEmpty(t, bodies)
	assert.Equal(t, `{"age":300000,"id":"1"}`, bodies[len(bodies)-1])
}

func TestAnInboxOf60ItemsGivesALiveRuleTheNewest50(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(tellEveryItem)})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "POST", "/inbox", 200, "{}")
	// GitHub lists them oldest first here, so the newest 50 are the last 50 listed.
	for n := 1; n <= 60; n++ {
		d.Tools().Inbox.Put(fmt.Sprintf("t%02d", n), thread("mention", true, fmt.Sprintf("2026-10-31T09:%02d:00Z", n-1), "acme/widgets", "Fix"))
	}

	d.Tick()

	bodies := strings.Join(postBodies(sentry, "/inbox"), "")
	assert.Equal(t, 50, countPostsTo(sentry, "/inbox"))
	for n := 1; n <= 10; n++ {
		assert.False(t, strings.Contains(bodies, fmt.Sprintf(`"id":"t%02d"`, n)))
	}
	assert.True(t, strings.Contains(bodies, `"id":"t60"`))
}

const tellRepo = `whenever({ name: "repo", each: "inbox", if: (i) => true,
  then: (i, ctx) => ctx.http.queuePost("sentry", "/repo", { id: i.id, repo: i.repo === undefined ? "none" : i.repo }, { once: ["repo", i.id, String(i.repo)] }) });`

// cloneAt makes dir a git clone with the remotes named, so the dispatch under test reads real ones.
func cloneAt(t *testing.T, dir string, remotes map[string]string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	hintGit(t, dir, "init", "-q", "--initial-branch=main")
	for _, name := range slices.Sorted(maps.Keys(remotes)) {
		hintGit(t, dir, "remote", "add", name, remotes[name])
	}
}

// breakGitConfig leaves the clone's directory in place, so only the remote read fails.
func breakGitConfig(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("[remote\n"), 0o644))
}

func TestAnInboxItemCarriesTheLabelOfTheCloneWhoseOriginNamesItsRepo(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(tellRepo)})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "POST", "/repo", 200, "{}")
	cloneAt(t, d.Repo, map[string]string{"origin": "git@github.com:acme/widgets.git"})
	d.Tools().Inbox.Put("1", thread("review_requested", true, "2026-10-31T09:00:00Z", "acme/widgets", "Fix"))
	d.Tools().Inbox.Put("2", thread("mention", true, "2026-10-31T08:00:00Z", "stranger/thing", "Other"))

	d.Tick()

	assert.ElementsMatch(t, []string{`{"id":"1","repo":"dispatch"}`, `{"id":"2","repo":"none"}`}, postBodies(sentry, "/repo"))
}

func TestACloneMatchesOnlyItsChosenRemote(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		settings string
		want     string
	}{
		{name: "originByDefault", settings: "", want: `{"id":"1","repo":"none"}`},
		{name: "githubRemoteUpstream", settings: "github_remote = \"upstream\"\n", want: `{"id":"1","repo":"dispatch"}`},
		{name: "githubRemoteMissing", settings: "github_remote = \"nowhere\"\n", want: `{"id":"1","repo":"none"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			d := newDispatch(t, dispatchOptions{Rules: rule(tellRepo), RepoSettings: test.settings})
			sentry := d.Services.Sentry()
			sentry.Respond("sentry", "POST", "/repo", 200, "{}")
			// A fork clone: origin is the owner's fork and the items come from the upstream repo.
			cloneAt(t, d.Repo, map[string]string{"origin": "git@github.com:me/widgets.git", "upstream": "git@github.com:acme/widgets.git"})
			d.Tools().Inbox.Put("1", thread("review_requested", true, "2026-10-31T09:00:00Z", "acme/widgets", "Fix"))

			d.Tick()

			assert.Equal(t, []string{test.want}, postBodies(sentry, "/repo"))
		})
	}
}

func TestAnItemTwoClonesNameHasNoKnownRepoAndHoldsTheRuleForThatItem(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(tellRepo)})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "POST", "/repo", 200, "{}")
	cloneAt(t, d.Repo, map[string]string{"origin": "git@github.com:acme/widgets.git"})
	second := filepath.Join(d.dir, "second")
	cloneAt(t, second, map[string]string{"origin": "https://github.com/acme/widgets"})
	d.addRepo("second", second)
	d.Tools().Inbox.Put("1", thread("review_requested", true, "2026-10-31T09:00:00Z", "acme/widgets", "Fix"))
	d.Tools().Inbox.Put("2", thread("mention", true, "2026-10-31T08:00:00Z", "stranger/thing", "Other"))

	d.Tick()

	assert.Equal(t, []string{`{"id":"2","repo":"none"}`}, postBodies(sentry, "/repo"))
}

func TestAFailedRemoteReadKeepsTheCloneLastGoodRepo(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(tellRepo)})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "POST", "/repo", 200, "{}")
	cloneAt(t, d.Repo, map[string]string{"origin": "git@github.com:acme/widgets.git"})
	d.Tools().Inbox.Put("1", thread("review_requested", true, "2026-10-31T09:00:00Z", "acme/widgets", "Fix"))
	d.Tick()
	require.Equal(t, []string{`{"id":"1","repo":"dispatch"}`}, postBodies(sentry, "/repo"))

	// A config git cannot parse fails the read, which is not the same as a missing remote.
	breakGitConfig(t, d.Repo)
	d.Tools().Inbox.Put("2", thread("mention", true, "2026-10-31T09:05:00Z", "acme/widgets", "Other"))
	d.Advance(5 * time.Minute)
	d.Tick()

	assert.Equal(t, []string{`{"id":"1","repo":"dispatch"}`, `{"id":"2","repo":"dispatch"}`}, postBodies(sentry, "/repo"))
	assert.Contains(t, d.loopErr(), "dispatch: while reading the origin remote of the dispatch clone:")
}

func TestAFailedRemoteReadWithNoEarlierReadMatchesNothing(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(tellRepo)})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "POST", "/repo", 200, "{}")
	cloneAt(t, d.Repo, map[string]string{"origin": "git@github.com:acme/widgets.git"})
	breakGitConfig(t, d.Repo)
	d.Tools().Inbox.Put("1", thread("review_requested", true, "2026-10-31T09:00:00Z", "acme/widgets", "Fix"))

	d.Tick()

	assert.Equal(t, []string{`{"id":"1","repo":"none"}`}, postBodies(sentry, "/repo"))
}

func TestStateResetQuietRefusesWhileACloneRemoteCannotBeRead(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(tellRepo)})
	d.Services.Sentry()
	cloneAt(t, d.Repo, map[string]string{"origin": "git@github.com:acme/widgets.git"})
	d.Tick()
	d.Stop()
	breakGitConfig(t, d.Repo)

	res := d.Run("state", "reset", "--quiet")

	require.ErrorContains(t, res.Err, "the remote of the dispatch clone")
}

func TestRulesCheckCountsTheItemsInConfiguredRepos(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(tellRepo)})
	d.Services.Sentry()
	cloneAt(t, d.Repo, map[string]string{"origin": "git@github.com:acme/widgets.git"})
	d.Tools().Inbox.Put("1", thread("mention", true, "2026-10-31T09:00:00Z", "acme/widgets", "Fix"))
	d.Tools().Inbox.Put("2", thread("author", false, "2026-10-31T08:00:00Z", "acme/gadgets", "Add"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `inbox +2 inbox items \(newest 50 of the GitHub inbox\), 1 unread, 1 in configured repos`, res.Stdout)
}

func TestWithNoRuleOverTheInboxHistoryHoldsNoCloneRemotes(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Config: shippedRulesConfig})
	cloneAt(t, d.Repo, map[string]string{"origin": "git@github.com:acme/widgets.git"})
	d.Linear.Tickets(ticket("ENG-1", "In Progress", "dispatch"))

	d.Tick()
	d.Stop()

	for _, body := range d.StateFiles() {
		// NotContains would print the whole segment on failure.
		assert.False(t, strings.Contains(body, `"remotes":`))
		assert.False(t, strings.Contains(body, `"remoteErrs"`))
	}
}

func TestReplayOfARunThatReadCloneRemotesReportsEveryCycleTheSameAsLive(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(tellRepo)})
	sentry := d.Services.Sentry()
	sentry.Respond("sentry", "POST", "/repo", 200, "{}")
	cloneAt(t, d.Repo, map[string]string{"origin": "git@github.com:acme/widgets.git"})
	d.Tools().Inbox.Put("1", thread("review_requested", true, "2026-10-31T09:00:00Z", "acme/widgets", "Fix"))
	d.Tick()
	breakGitConfig(t, d.Repo)
	d.Advance(5 * time.Minute)
	d.Tick()
	d.Stop()

	cycles, matched := replayOf(t, d)

	assert.Greater(t, cycles, 1)
	assert.Equal(t, cycles, matched)
}

func TestInboxScenarioLabelsItemsByTheCloneGitHubRepo(t *testing.T) {
	t.Parallel()
	files := rule(tellRepo)
	files["inbox.test.ts"] = `import { scenario, pool, inbox, rulesFrom } from "dispatch/testing";
const rules = rulesFrom("./dispatch");

scenario(rules, "gives an item in a configured repo its label and any other repo none", s => {
  s.config({ http: { sentry: { baseUrl: "https://sentry.test/api/0" } } });
  s.given(pool("dispatch", { github: "acme/widgets" }), inbox("t1", { repoName: "acme/widgets" }), inbox("t2", { repoName: "stranger/thing" }));
  s.start();
  s.expect([["repo", "post", "sentry"], ["repo", "post", "sentry"]]);
});

scenario(rules, "holds the rule for an item two labels name", s => {
  s.config({ http: { sentry: { baseUrl: "https://sentry.test/api/0" } } });
  s.given(pool("dispatch", { github: "acme/widgets" }), pool("second", { github: "acme/widgets" }), inbox("t1", { repoName: "acme/widgets" }), inbox("t2", { repoName: "stranger/thing" }));
  s.start();
  s.expect([["repo", "post", "sentry"]]);
});

scenario(rules, "keeps the repo given before a failed remote read", s => {
  s.config({ http: { sentry: { baseUrl: "https://sentry.test/api/0" } } });
  s.given(pool("dispatch", { github: "acme/widgets" }), inbox("t1", { repoName: "acme/widgets" }));
  s.unread("remotes:dispatch");
  s.given(pool("second", { github: "acme/gadgets" }), inbox("t2", { repoName: "acme/gadgets" }));
  s.start();
  s.expect([["repo", "post", "sentry"], ["repo", "post", "sentry"]]);
});
`
	d := newDispatch(t, dispatchOptions{Rules: files})
	d.Services.Sentry()

	res := d.Run("rules", "test")

	require.NoError(t, res.Err, res.Stdout)
	assert.Contains(t, res.Stdout, "ok   inbox.test.ts: gives an item in a configured repo its label and any other repo none")
	assert.Contains(t, res.Stdout, "ok   inbox.test.ts: holds the rule for an item two labels name")
	assert.Contains(t, res.Stdout, "ok   inbox.test.ts: keeps the repo given before a failed remote read")
}

func TestRulesCheckCountsAnItemTwoClonesNameAsInAConfiguredRepo(t *testing.T) {
	t.Parallel()
	d := newDispatch(t, dispatchOptions{Rules: rule(tellRepo)})
	d.Services.Sentry()
	cloneAt(t, d.Repo, map[string]string{"origin": "git@github.com:acme/widgets.git"})
	second := filepath.Join(d.dir, "second")
	cloneAt(t, second, map[string]string{"origin": "https://github.com/acme/widgets"})
	d.addRepo("second", second)
	d.Tools().Inbox.Put("1", thread("mention", true, "2026-10-31T09:00:00Z", "acme/widgets", "Fix"))

	res := d.Run("rules", "check")

	require.NoError(t, res.Err)
	assert.Regexp(t, `1 unread, 1 in configured repos`, res.Stdout)
}
