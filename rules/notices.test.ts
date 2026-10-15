import { scenario, ticket, worker, pr, comment, rulesFrom } from "dispatch/testing";

const rules = rulesFrom("./dispatch");

const build = { status: "in_progress", optedIn: true, repo: "dispatch" };
const review = { status: "in_review", optedIn: true, repo: "dispatch" };
const hour = 60 * 60 * 1000;

// A worker that stops again: it works, then idles.
const stopsAgain = (s: { see: (...f: ReturnType<typeof worker>[]) => void }, id: string) => {
  s.see(worker(id, "working"));
  s.see(worker(id, "idle"));
};

scenario(rules, "notify-needs-human notifies once per stretch in Needs Human", s => {
  s.given(ticket("ENG-1", { status: "needs_human", optedIn: true, repo: "dispatch" }));
  s.expect([["notify-needs-human", "notify.send", "ENG-1"]]);
  s.land("notify.send", "ENG-1");
  s.expect([]);
  s.see(ticket("ENG-1", { status: "in_progress" }), worker("ENG-1", "working"));
  s.see(ticket("ENG-1", { status: "needs_human" }));
  s.expect([["notify-needs-human", "notify.send", "ENG-1"]]);
});

scenario(rules, "notify-needs-human is silent for a design ticket", s => {
  s.given(ticket("ENG-1", { status: "needs_human", labels: ["design"] }));
  s.expect([]);
});

scenario(rules, "notify-needs-human notifies for a design-review ticket", s => {
  s.given(ticket("ENG-1", { status: "needs_human", labels: ["design-review"] }));
  s.expect([["notify-needs-human", "notify.send", "ENG-1"]]);
});

scenario(rules, "notify-needs-human still notifies when the labels were cut short", s => {
  s.given(ticket("ENG-1", { status: "needs_human", labelsTruncated: true }));
  s.expect([["notify-needs-human", "notify.send", "ENG-1"]]);
});

scenario(rules, "notify-needs-human sends again after a failed notification", s => {
  s.given(ticket("ENG-1", { status: "needs_human" }));
  s.land("notify.send", "ENG-1", { outcome: "failed", detail: "osascript" });
  s.expect([["notify-needs-human", "notify.send", "ENG-1"]]);
});

scenario(rules, "notify-needs-human does not send again after a refused notification", s => {
  s.given(ticket("ENG-1", { status: "needs_human" }));
  s.land("notify.send", "ENG-1", { outcome: "refused", detail: "muted" });
  s.expect([]);
});

scenario(rules, "notify-blocked notifies for a blocked worker and again each period", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "blocked"));
  s.expect([["notify-blocked", "notify.send", "ENG-1"]]);
  s.land("notify.send", "ENG-1");
  s.expect([]);
  s.advance(14 * 60 * 1000);
  s.expect([]);
  s.advance(2 * 60 * 1000);
  s.expect([["notify-blocked", "notify.send", "ENG-1"]]);
});

scenario(rules, "notify-blocked takes its period from blocked_renotify_minutes", s => {
  s.config({ rules: { blocked_renotify_minutes: 5 } });
  s.given(ticket("ENG-1", build), worker("ENG-1", "blocked"));
  s.land("notify.send", "ENG-1");
  s.advance(6 * 60 * 1000);
  s.expect([["notify-blocked", "notify.send", "ENG-1"]]);
});

scenario(rules, "notify-blocked notifies for a blocked worker on a ticket in review", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "blocked"));
  s.expect([["notify-blocked", "notify.send", "ENG-1"]]);
});

scenario(rules, "notify-blocked is silent for a design worker", s => {
  s.given(ticket("ENG-1", { ...build, labels: ["design"] }), worker("ENG-1", "blocked"));
  s.expect([]);
});

scenario(rules, "notify-blocked notifies for a design-review worker only as a review", s => {
  s.given(ticket("ENG-1", { ...build, labels: ["design-review"] }), worker("ENG-1", "blocked"));
  s.expect([["notify-blocked", "notify.send", "ENG-1"]]);
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
  s.expect([["notify-ready-for-review", "notify.send", "ENG-1"]]);
  s.land("notify.send", "ENG-1");
  s.expect([]);
  s.see(pr("ENG-1", { head: "h2" }));
  s.expect([["notify-ready-for-review", "notify.send", "ENG-1"]]);
});

scenario(rules, "notify-ready-for-review waits for a PR that is not ready", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1"));
  s.expect([]);
});

scenario(rules, "notify-ready-for-review waits until the worker has heard of the activity", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true }));
  s.see(pr("ENG-1", { activityAt: "10:00" }));
  s.expect([["nudge-pr-activity", "herdr.nudge", "ENG-1"]]);
});

scenario(rules, "notify-ready-for-review waits behind an owed comment nudge, even while that nudge waits for a Stop", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true, head: "h1" }));
  s.land("notify.send", "ENG-1");
  s.see(comment("ENG-1", { at: "10:00" }));
  s.expect([["nudge-comment", "herdr.nudge", "ENG-1"]]);
  s.land("herdr.nudge", "ENG-1");
  s.see(pr("ENG-1", { head: "h2" }));
  s.expect([["notify-ready-for-review", "notify.send", "ENG-1"]]);
  s.see(comment("ENG-1", { at: "11:00" }));
  s.expect([]);
  stopsAgain(s, "ENG-1");
  s.expect([["nudge-comment", "herdr.nudge", "ENG-1"]]);
  s.land("herdr.nudge", "ENG-1");
  s.expect([["notify-ready-for-review", "notify.send", "ENG-1"]]);
});

scenario(rules, "notify-ready-for-review is not sent for a PR that auto-merge is about to merge", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true, approved: true }));
  s.expect([["auto-merge", "github.merge", "ENG-1"]]);
});

scenario(rules, "notify-ready-for-review is sent for an approved PR when auto-merge is off", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true, approved: true }));
  s.expect([["notify-ready-for-review", "notify.send", "ENG-1"]]);
});

scenario(rules, "notify-ready-for-review sends two notices for a draft, one when ready and one when opened", s => {
  s.config({ repos: { dispatch: { openPrAs: "draft" } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true, draft: true, head: "h1" }));
  s.expect([["notify-ready-for-review", "notify.send", "ENG-1"]]);
  s.land("notify.send", "ENG-1");
  s.expect([]);
  s.see(pr("ENG-1", { draft: false }));
  s.expect([["notify-ready-for-review", "notify.send", "ENG-1"]]);
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

scenario(rules, "notify-stall-count notifies at every sixth stall nudge", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "idle"));
  for (let i = 1; i <= 5; i++) {
    s.expect([["nudge-stall", "herdr.nudge", "ENG-1"]]);
    s.land("herdr.nudge", "ENG-1");
    stopsAgain(s, "ENG-1");
  }
  s.expect([["nudge-stall", "herdr.nudge", "ENG-1"]]);
  s.land("herdr.nudge", "ENG-1");
  s.expect([["notify-stall-count", "notify.send", "ENG-1"]]);
  s.land("notify.send", "ENG-1");
  s.expect([]);
});

scenario(rules, "notify-stall-count is silent before the sixth nudge", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "idle"));
  s.land("herdr.nudge", "ENG-1");
  stopsAgain(s, "ENG-1");
  s.land("herdr.nudge", "ENG-1");
  stopsAgain(s, "ENG-1");
  s.expect([["nudge-stall", "herdr.nudge", "ENG-1"]]);
});

scenario(rules, "notify-unresponsive notifies an hour after a stall nudge nobody answered", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "idle"));
  s.land("herdr.nudge", "ENG-1");
  s.advance(59 * 60 * 1000);
  s.expect([]);
  s.advance(2 * 60 * 1000);
  s.expect([["notify-unresponsive", "notify.send", "ENG-1"]]);
  s.land("notify.send", "ENG-1");
  s.expect([]);
});

scenario(rules, "notify-unresponsive is silent once the worker stops again", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "idle"));
  s.land("herdr.nudge", "ENG-1");
  s.advance(2 * hour);
  stopsAgain(s, "ENG-1");
  s.expect([["nudge-stall", "herdr.nudge", "ENG-1"]]);
});

scenario(rules, "notify-unresponsive does not follow a comment nudge", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "idle"));
  s.see(comment("ENG-1", { at: "10:00" }));
  s.land("herdr.nudge", "ENG-1");
  s.advance(2 * hour);
  s.expect([]);
});

scenario(rules, "notify-merge-failed tells the followers once per head after a refused merge", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true, approved: true, head: "h1" }));
  s.expect([["auto-merge", "github.merge", "ENG-1"]]);
  s.land("github.merge", "ENG-1", { outcome: "refused", detail: "base branch policy" });
  s.expect([["notify-merge-failed", "notify.send", "ENG-1"]]);
  s.land("notify.send", "ENG-1");
  s.expect([]);
  s.see(pr("ENG-1", { head: "h2" }));
  s.expect([["auto-merge", "github.merge", "ENG-1"]]);
});

scenario(rules, "notify-merge-failed is silent after a merge that failed to run, which is tried again", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true, approved: true }));
  s.land("github.merge", "ENG-1", { outcome: "failed", detail: "network" });
  s.expect([["auto-merge", "github.merge", "ENG-1"]]);
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
  s.expect([["notify-blocked", "notify.send", "ENG-1"]]);
  s.land("notify.send", "ENG-1");
  s.resync();
  s.expect([["notify-blocked", "notify.send", "ENG-1"]]);
});
