import { scenario, ticket, worker, pr, comment, rulesFrom } from "dispatch/testing";

const rules = rulesFrom("./dispatch");

const review = { status: "in_review", optedIn: true, repo: "dispatch" };
const approved = { ready: true, approved: true, head: "h1" };

scenario(rules, "auto-merge merges an approved ready PR, then tells the followers once the merge is done", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", approved));
  s.expect([["auto-merge", "merge", "ENG-1"]]);
  s.expectWaiting("auto-merge", "ENG-1");
  s.land("merge", "ENG-1");
  s.expect([["auto-merge", "notify", "ENG-1"]]);
  s.land("notify", "ENG-1");
  s.expect([]);
});

scenario(rules, "auto-merge does not tell the followers of a merge that was refused", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", approved));
  s.land("merge", "ENG-1", { outcome: "refused", detail: "head moved" });
  s.expect([["notify-merge-failed", "notify", "ENG-1"]]);
  s.expectWaiting("auto-merge", "ENG-1");
});

scenario(rules, "auto-merge does not tell the followers of a merge that failed to run, and tries the merge again", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", approved));
  s.land("merge", "ENG-1", { outcome: "failed", detail: "network" });
  s.expect([["auto-merge", "merge", "ENG-1"]]);
});

scenario(rules, "auto-merge sends a notice again that failed, without merging twice", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", approved));
  s.land("merge", "ENG-1");
  s.land("notify", "ENG-1", { outcome: "failed", detail: "osascript" });
  s.expect([["auto-merge", "notify", "ENG-1"]]);
});

scenario(rules, "auto-merge merges once per head", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", approved));
  s.land("merge", "ENG-1", { outcome: "refused", detail: "head moved" });
  s.land("notify", "ENG-1");
  s.expect([]);
  s.see(pr("ENG-1", { head: "h2" }));
  s.expect([["auto-merge", "merge", "ENG-1"]]);
});

const baseMoved = "GraphQL: Base branch was modified. Review and try the merge again. (mergePullRequest)";

scenario(rules, "auto-merge tries a merge again a minute after GitHub refused it because the base branch moved", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", approved));
  s.land("merge", "ENG-1", { outcome: "refused", detail: baseMoved });
  s.expect([]);
  s.advance(30_000);
  s.expect([]);
  s.advance(31_000);
  s.expect([["auto-merge", "merge", "ENG-1"]]);
  s.land("merge", "ENG-1");
  s.expect([["auto-merge", "notify", "ENG-1"]]);
});

scenario(rules, "auto-merge stops retrying a base-moved refusal after five retries", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", approved));
  for (let i = 0; i < 5; i++) {
    s.land("merge", "ENG-1", { outcome: "refused", detail: baseMoved });
    s.advance(61_000);
    s.expect([["auto-merge", "merge", "ENG-1"]]);
  }
  s.land("merge", "ENG-1", { outcome: "refused", detail: baseMoved });
  s.advance(61_000);
  s.expect([["notify-merge-failed", "notify", "ENG-1"]]);
  s.land("notify", "ENG-1");
  s.advance(61_000);
  s.expect([]);
});

scenario(rules, "auto-merge does not retry another refusal after a base-moved one", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", approved));
  s.land("merge", "ENG-1", { outcome: "refused", detail: baseMoved });
  s.advance(61_000);
  s.land("merge", "ENG-1", { outcome: "refused", detail: "head moved" });
  s.advance(61_000);
  s.expect([["notify-merge-failed", "notify", "ENG-1"]]);
  s.land("notify", "ENG-1");
  s.expect([]);
});

scenario(rules, "auto-merge starts its retries over for a new head", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", approved));
  s.land("merge", "ENG-1", { outcome: "refused", detail: baseMoved });
  s.see(pr("ENG-1", { head: "h2" }));
  s.expect([["auto-merge", "merge", "ENG-1"]]);
});

scenario(rules, "auto-merge does not retry once approval is withdrawn", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", approved));
  s.land("merge", "ENG-1", { outcome: "refused", detail: baseMoved });
  s.see(pr("ENG-1", { approved: false }));
  s.advance(61_000);
  s.expect([["notify-ready-for-review", "notify", "ENG-1"]]);
});

scenario(rules, "auto-merge needs the repo to turn it on", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", approved));
  s.expect([["notify-ready-for-review", "notify", "ENG-1"]]);
});

scenario(rules, "auto-merge never merges a draft", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ...approved, draft: true }));
  s.expect([["notify-ready-for-review", "notify", "ENG-1"]]);
});

scenario(rules, "auto-merge waits for approval", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true }));
  s.expect([["notify-ready-for-review", "notify", "ENG-1"]]);
});

scenario(rules, "auto-merge waits for a ready PR", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { approved: true }));
  s.expect([]);
});

scenario(rules, "auto-merge waits while the worker is still working", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "working"), pr("ENG-1", approved));
  s.expect([]);
});

scenario(rules, "auto-merge waits until the worker has heard of the latest review", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", approved));
  s.see(pr("ENG-1", { activityAt: "10:00" }));
  s.expect([["nudge-pr-activity", "nudge", "ENG-1"]]);
  s.land("nudge", "ENG-1");
  s.expect([["auto-merge", "merge", "ENG-1"]]);
});

scenario(rules, "auto-merge is not held back by a ticket comment", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", approved));
  s.see(comment("ENG-1", { body: "don't merge yet", at: "10:00" }));
  s.expect([["auto-merge", "merge", "ENG-1"]]);
});

scenario(rules, "auto-merge leaves a ticket not in review", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", { ...review, status: "in_progress" }), worker("ENG-1", "idle"), pr("ENG-1", approved));
  s.expect([]);
});

scenario(rules, "auto-merge leaves a design ticket", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", { ...review, labels: ["design"] }), worker("ENG-1", "idle"), pr("ENG-1", approved));
  s.expect([]);
});

scenario(rules, "auto-merge is held when the PR could not be read", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", approved));
  s.unread("github:ENG-1");
  s.expect([]);
  s.expectHeld("auto-merge", "ENG-1");
});

scenario(rules, "auto-merge is held when herdr could not be read", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", approved));
  s.unread("herdr");
  s.expect([]);
  s.expectHeld("auto-merge", "ENG-1", "herdr unread");
});
