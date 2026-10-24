import { scenario, ticket, worker, pr, comment, rulesFrom } from "dispatch/testing";

const rules = rulesFrom("./dispatch");

const build = { status: "in_progress", optedIn: true, repo: "dispatch" };
const review = { status: "in_review", optedIn: true, repo: "dispatch" };

scenario(rules, "notify-needs-human notifies once per stretch in Needs Human", s => {
  s.given(ticket("ENG-1", { status: "needs_human", optedIn: true, repo: "dispatch" }));
  s.expect([["notify-needs-human", "notify", "ENG-1"]]);
  s.land("notify", "ENG-1");
  s.expect([]);
  s.see(ticket("ENG-1", { status: "in_progress" }), worker("ENG-1", "working"));
  s.see(ticket("ENG-1", { status: "needs_human" }));
  s.expect([["notify-needs-human", "notify", "ENG-1"]]);
});

scenario(rules, "notify-needs-human is silent for a design ticket", s => {
  s.given(ticket("ENG-1", { status: "needs_human", labels: ["design"] }));
  s.expect([]);
});

scenario(rules, "notify-needs-human notifies for a design-review ticket", s => {
  s.given(ticket("ENG-1", { status: "needs_human", labels: ["design-review"] }));
  s.expect([["notify-needs-human", "notify", "ENG-1"]]);
});

scenario(rules, "notify-needs-human still notifies when the labels were cut short", s => {
  s.given(ticket("ENG-1", { status: "needs_human", labelsTruncated: true }));
  s.expect([["notify-needs-human", "notify", "ENG-1"]]);
});

scenario(rules, "notify-needs-human sends again after a failed notification", s => {
  s.given(ticket("ENG-1", { status: "needs_human" }));
  s.land("notify", "ENG-1", { outcome: "failed", detail: "osascript" });
  s.expect([["notify-needs-human", "notify", "ENG-1"]]);
});

scenario(rules, "notify-needs-human does not send again after a refused notification", s => {
  s.given(ticket("ENG-1", { status: "needs_human" }));
  s.land("notify", "ENG-1", { outcome: "refused", detail: "muted" });
  s.expect([]);
});

scenario(rules, "notify-blocked notifies for a blocked worker and again each period", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "blocked"));
  s.expect([["notify-blocked", "notify", "ENG-1"]]);
  s.land("notify", "ENG-1");
  s.expect([]);
  s.advance(14 * 60 * 1000);
  s.expect([]);
  s.advance(2 * 60 * 1000);
  s.expect([["notify-blocked", "notify", "ENG-1"]]);
});

scenario(rules, "notify-blocked takes its period from blocked_renotify_minutes", s => {
  s.config({ rules: { blocked_renotify_minutes: 5 } });
  s.given(ticket("ENG-1", build), worker("ENG-1", "blocked"));
  s.land("notify", "ENG-1");
  s.advance(6 * 60 * 1000);
  s.expect([["notify-blocked", "notify", "ENG-1"]]);
});

scenario(rules, "notify-blocked notifies for a blocked worker on a ticket in review", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "blocked"));
  s.expect([["notify-blocked", "notify", "ENG-1"]]);
});

scenario(rules, "notify-blocked is silent for a design worker", s => {
  s.given(ticket("ENG-1", { ...build, labels: ["design"] }), worker("ENG-1", "blocked"));
  s.expect([]);
});

scenario(rules, "notify-blocked notifies for a design-review worker only as a review", s => {
  s.given(ticket("ENG-1", { ...build, labels: ["design-review"] }), worker("ENG-1", "blocked"));
  s.expect([["notify-blocked", "notify", "ENG-1"]]);
});

scenario(rules, "notify-blocked is silent for a working worker", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "working"));
  s.expect([]);
});

scenario(rules, "notify-blocked is held when herdr could not be read", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "blocked"));
  s.unread("herdr");
  s.expect([]);
  s.expectHeld("notify-blocked", "ENG-1", "herdr unread");
});

scenario(rules, "notify-ready-for-review notifies once per head", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true, head: "h1" }));
  s.expect([["notify-ready-for-review", "notify", "ENG-1"]]);
  s.land("notify", "ENG-1");
  s.expect([]);
  s.see(pr("ENG-1", { head: "h2" }));
  s.expect([["notify-ready-for-review", "notify", "ENG-1"]]);
});

scenario(rules, "notify-ready-for-review waits for a PR that is not ready", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1"));
  s.expect([]);
});

scenario(rules, "notify-ready-for-review waits until the worker has heard of the activity", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true }));
  s.see(pr("ENG-1", { activityAt: "10:00" }));
  s.expect([["nudge-pr-activity", "nudge", "ENG-1"]]);
});

scenario(rules, "notify-ready-for-review is not held back by a ticket comment", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true, head: "h1" }));
  s.land("notify", "ENG-1");
  s.see(comment("ENG-1", { at: "10:00" }), pr("ENG-1", { head: "h2" }));
  s.expect([["notify-ready-for-review", "notify", "ENG-1"]]);
});

scenario(rules, "notify-ready-for-review is not sent for a PR that auto-merge is about to merge", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true, approved: true }));
  s.expect([["auto-merge", "merge", "ENG-1"]]);
});

scenario(rules, "notify-ready-for-review is sent for an approved PR when auto-merge is off", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true, approved: true }));
  s.expect([["notify-ready-for-review", "notify", "ENG-1"]]);
});

scenario(rules, "notify-ready-for-review notifies a ready draft and stays silent once the user opens it", s => {
  s.config({ repos: { dispatch: { openPrAs: "draft" } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true, draft: true, head: "h1" }));
  s.expect([["notify-ready-for-review", "notify", "ENG-1"]]);
  s.land("notify", "ENG-1");
  s.expect([]);
  s.see(pr("ENG-1", { draft: false }));
  s.expect([]);
});

scenario(rules, "notify-ready-for-review is silent for an opened PR waiting on approvers", s => {
  s.config({ repos: { dispatch: { openPrAs: "draft" } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true, draft: false, approved: false }));
  s.expect([]);
});

scenario(rules, "notify-ready-for-review still notifies a ready PR in a repo that opens PRs as open", s => {
  s.config({ repos: { dispatch: { openPrAs: "open" } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true, approved: false }));
  s.expect([["notify-ready-for-review", "notify", "ENG-1"]]);
});

scenario(rules, "notify-ready-for-review notifies once per head for an approved PR dispatch won't merge", s => {
  s.config({ repos: { dispatch: { openPrAs: "draft" } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true, draft: false, approved: true, head: "h1" }));
  s.expect([["notify-ready-for-review", "notify", "ENG-1"]]);
  s.land("notify", "ENG-1");
  s.expect([]);
  s.see(pr("ENG-1", { head: "h2" }));
  s.expect([["notify-ready-for-review", "notify", "ENG-1"]]);
});

scenario(rules, "notify-ready-for-review is silent when auto-merge will merge the PR", s => {
  s.config({ repos: { dispatch: { openPrAs: "draft", autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true, draft: false, approved: true }));
  s.expect([["auto-merge", "merge", "ENG-1"]]);
});

scenario(rules, "notify-ready-for-review is silent for a gone worker and for a PR that waits on approvers", s => {
  s.config({ repos: { dispatch: { openPrAs: "draft" } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "none"), pr("ENG-1", { ready: true, draft: false, approved: true }));
  s.expect([]);
  s.see(worker("ENG-1", "idle"), pr("ENG-1", { approved: false }));
  s.expect([]);
});

scenario(rules, "notify-ready-for-review is held when the PR could not be read", s => {
  s.config({ repos: { dispatch: { openPrAs: "draft" } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true, approved: true }));
  s.unread("github:ENG-1");
  s.expect([]);
  s.expectHeld("notify-ready-for-review", "ENG-1");
});

scenario(rules, "notify-ready-for-review is held when the PR could not be read", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true }));
  s.unread("github:ENG-1");
  s.expect([]);
  s.expectHeld("notify-ready-for-review", "ENG-1");
});

scenario(rules, "notify-ready-for-review is silent for a working worker", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "working"), pr("ENG-1", { ready: true }));
  s.expect([]);
});

scenario(rules, "notify-merge-failed tells the followers once per head after a refused merge", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true, approved: true, head: "h1" }));
  s.expect([["auto-merge", "merge", "ENG-1"]]);
  s.land("merge", "ENG-1", { outcome: "refused", detail: "base branch policy" });
  s.expect([["notify-merge-failed", "notify", "ENG-1"]]);
  s.land("notify", "ENG-1");
  s.expect([]);
  s.see(pr("ENG-1", { head: "h2" }));
  s.expect([["auto-merge", "merge", "ENG-1"]]);
});

scenario(rules, "notify-merge-failed stays silent while a base-moved refusal is still being retried", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true, approved: true }));
  s.land("merge", "ENG-1", { outcome: "refused", detail: "Base branch was modified. Review and try the merge again." });
  s.expect([]);
});

scenario(rules, "notify-merge-failed is silent after a merge that failed to run, which is tried again", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true, approved: true }));
  s.land("merge", "ENG-1", { outcome: "failed", detail: "network" });
  s.expect([["auto-merge", "merge", "ENG-1"]]);
});

scenario(rules, "notify-merge-failed is held when the PR could not be read", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true, approved: true }));
  s.unread("github:ENG-1");
  s.expect([]);
  s.expectHeld("notify-merge-failed", "ENG-1");
});

scenario(rules, "notify-blocked with blocked_renotify_minutes = 0 notifies in every cycle", s => {
  s.config({ rules: { blocked_renotify_minutes: 0 } });
  s.given(ticket("ENG-1", { status: "in_progress" }), worker("ENG-1", "blocked"));
  s.expect([["notify-blocked", "notify", "ENG-1"]]);
  s.land("notify", "ENG-1");
  s.resync();
  s.expect([["notify-blocked", "notify", "ENG-1"]]);
});
