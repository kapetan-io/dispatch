import { scenario, ticket, worker, pr, rulesFrom } from "dispatch/testing";

const rules = rulesFrom("./dispatch", "./stacking");

const stack = { parent: "ENG-2", forkPoint: "a".repeat(40) };
const build = { status: "in_progress", optedIn: true, repo: "dispatch" };
const review = { status: "in_review", optedIn: true, repo: "dispatch" };
const blockedBy = (id: string, status = "in_review", abandoned = false) => ({ blockers: [{ id, status, abandoned }] });

// ENG-2 is In Review with a ready PR, the bar for stacking on it.
const parentReady = [ticket("ENG-2", review), worker("ENG-2", "idle"), pr("ENG-2", { ready: true, head: "p1" })];

scenario(rules, "a ticket whose only open blocker is In Review with a ready PR launches stacked", s => {
  s.given(ticket("ENG-3", { ...build, ...blockedBy("ENG-2") }), ...parentReady);
  s.expect([["launch-stacked-worker", "launch", "ENG-3"], ["notify-ready-for-review", "notify", "ENG-2"]]);
});

scenario(rules, "a stacked launch is not repeated while its launch is done", s => {
  s.given(ticket("ENG-3", { ...build, ...blockedBy("ENG-2") }), ...parentReady);
  s.land("launch", "ENG-3");
  s.see(worker("ENG-3", "working"));
  s.expect([["notify-ready-for-review", "notify", "ENG-2"]]);
});

scenario(rules, "a ticket with two open blockers does not stack", s => {
  s.given(ticket("ENG-3", { ...build, blockers: [{ id: "ENG-2", status: "in_review" }, { id: "ENG-4", status: "in_review" }] }),
    ...parentReady, ticket("ENG-4", review), worker("ENG-4", "idle"), pr("ENG-4", { ready: true }));
  s.expect([["note-blocker-hold", "comment", "ENG-3"], ["notify-ready-for-review", "notify", "ENG-2"], ["notify-ready-for-review", "notify", "ENG-4"]]);
});

scenario(rules, "a blocker without a ready PR does not stack", s => {
  s.given(ticket("ENG-3", { ...build, ...blockedBy("ENG-2") }), ticket("ENG-2", review), worker("ENG-2", "idle"), pr("ENG-2"));
  s.expect([["note-blocker-hold", "comment", "ENG-3"]]);
});

scenario(rules, "a blocker that is not In Review does not stack", s => {
  s.given(ticket("ENG-3", { ...build, ...blockedBy("ENG-2", "in_progress") }), ticket("ENG-2", build), worker("ENG-2", "idle"));
  s.expect([["note-blocker-hold", "comment", "ENG-3"]]);
});

scenario(rules, "a blocker in another repo does not stack", s => {
  s.given(ticket("ENG-3", { ...build, ...blockedBy("ENG-2") }), ticket("ENG-2", { ...review, repo: "other" }),
    worker("ENG-2", "idle"), pr("ENG-2", { ready: true }));
  s.expect([["note-blocker-hold", "comment", "ENG-3"], ["notify-ready-for-review", "notify", "ENG-2"]]);
});

scenario(rules, "a blocker whose PR was not read this cycle holds the stacked launch", s => {
  s.given(ticket("ENG-3", { ...build, ...blockedBy("ENG-2") }), ticket("ENG-2", review), worker("ENG-2", "working"));
  s.expect([]);
  s.expectHeld("launch-stacked-worker", "ENG-3");
});

scenario(rules, "a stacked launch stops at the launch failure limit and escalates", s => {
  s.given(ticket("ENG-3", { ...build, ...blockedBy("ENG-2") }), ...parentReady);
  s.land("launch", "ENG-3", { outcome: "failed", detail: "checkout_failed" });
  s.expect([["launch-stacked-worker", "launch", "ENG-3"], ["notify-ready-for-review", "notify", "ENG-2"]]);
  s.land("launch", "ENG-3", { outcome: "failed", detail: "checkout_failed" });
  s.expect([["escalate-launch-failures", "escalate", "ENG-3"], ["notify-ready-for-review", "notify", "ENG-2"]]);
});

scenario(rules, "in A <- B <- C, C launches once B is In Review and ready", s => {
  s.given(
    ticket("ENG-1", { ...review, stack: undefined }), worker("ENG-1", "idle"), pr("ENG-1", { ready: true, head: "a1" }),
    ticket("ENG-2", { ...review, ...blockedBy("ENG-1"), stack: { parent: "ENG-1", forkPoint: "b".repeat(40) } }),
    worker("ENG-2", "idle"), pr("ENG-2", { ready: true, head: "b1" }),
    ticket("ENG-3", { ...build, ...blockedBy("ENG-2") }));
  s.expect([["launch-stacked-worker", "launch", "ENG-3"], ["notify-ready-for-review", "notify", "ENG-1"], ["notify-ready-for-review", "notify", "ENG-2"]]);
});

scenario(rules, "a feature piece is promoted on the same condition", s => {
  s.given(ticket("ENG-9", { ...build, hasChildren: true }),
    ticket("ENG-3", { status: "todo", optedIn: true, repo: "dispatch", parent: "ENG-9", ...blockedBy("ENG-2") }), ...parentReady);
  s.expect([["promote-stacked-child", "move", "ENG-3", "in_progress"], ["notify-ready-for-review", "notify", "ENG-2"]]);
});

scenario(rules, "a feature piece whose blocker is not ready is not promoted", s => {
  s.given(ticket("ENG-9", { ...build, hasChildren: true }),
    ticket("ENG-3", { status: "todo", optedIn: true, repo: "dispatch", parent: "ENG-9", ...blockedBy("ENG-2") }),
    ticket("ENG-2", review), worker("ENG-2", "idle"), pr("ENG-2"));
  s.expect([]);
});

// The stack parent ENG-2 is Done, so ENG-3 has no blocker left.
const restackOwed = { ...review, stack };

scenario(rules, "when the stack parent is Done an idle worker is told to restack, once", s => {
  s.given(ticket("ENG-3", restackOwed), worker("ENG-3", "idle"), pr("ENG-3", { head: "h1" }));
  s.expect([["nudge-restack", "nudge", "ENG-3"]]);
  s.land("nudge", "ENG-3");
  s.see(worker("ENG-3", "idle"));
  s.expect([]);
});

scenario(rules, "a stacked ticket owed a restack gets nothing else from the PR rules", s => {
  s.given(ticket("ENG-3", restackOwed), worker("ENG-3", "idle"), pr("ENG-3", { head: "h1", problems: ["a failed check"], activityAt: "10:00" }));
  s.expect([["nudge-restack", "nudge", "ENG-3"]]);
});

scenario(rules, "a stacked ticket with no worker owed a restack is relaunched by one rule", s => {
  s.given(ticket("ENG-3", { ...restackOwed, status: "in_progress" }), worker("ENG-3", "none"));
  s.expect([["relaunch-for-restack", "launch", "ENG-3"]]);
});

scenario(rules, "a stacked In Review ticket with no worker is relaunched once for the restack, not for its PR", s => {
  s.given(ticket("ENG-3", restackOwed), worker("ENG-3", "none"), pr("ENG-3", { head: "h1", problems: ["a failed check"] }));
  s.expect([["relaunch-for-restack", "launch", "ENG-3"]]);
});

scenario(rules, "the restack is told again after a new stretch in In Review", s => {
  s.given(ticket("ENG-3", restackOwed), worker("ENG-3", "idle"), pr("ENG-3", { head: "h1" }));
  s.land("nudge", "ENG-3");
  s.see(ticket("ENG-3", { ...restackOwed, status: "in_progress" }), worker("ENG-3", "idle", { lastStop: 60000 }));
  s.see(ticket("ENG-3", restackOwed), worker("ENG-3", "idle", { lastStop: 120000 }));
  s.expect([["nudge-restack", "nudge", "ENG-3"]]);
});

scenario(rules, "once the stack record is gone the ticket is ordinary", s => {
  s.given(ticket("ENG-3", { ...review, stack: undefined }), worker("ENG-3", "idle"), pr("ENG-3", { head: "h1", problems: ["a failed check"] }));
  s.expect([["nudge-pr-needs-work", "nudge", "ENG-3"]]);
});

scenario(rules, "a push to the stack parent's PR sends nothing to the stacked ticket", s => {
  s.given(ticket("ENG-3", { ...review, ...blockedBy("ENG-2"), stack }), worker("ENG-3", "idle"), pr("ENG-3", { ready: true, head: "h1" }), ...parentReady);
  s.land("notify", "ENG-3");
  s.land("notify", "ENG-2");
  s.see(pr("ENG-2", { head: "p2" }));
  s.expect([["notify-ready-for-review", "notify", "ENG-2"]]);
});

scenario(rules, "auto-merge never merges a stacked PR", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-3", { ...review, ...blockedBy("ENG-2"), stack }), worker("ENG-3", "idle"),
    pr("ENG-3", { ready: true, approved: true, head: "h1" }), ...parentReady);
  s.expect([["notify-stacked-ready", "notify", "ENG-3"], ["notify-ready-for-review", "notify", "ENG-2"]]);
});

scenario(rules, "auto-merge merges a PR whose ticket has no stack record", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", { ...review, stack: undefined }), worker("ENG-1", "idle"), pr("ENG-1", { ready: true, approved: true, head: "h1" }));
  s.expect([["auto-merge", "merge", "ENG-1"]]);
});

scenario(rules, "auto-merge is held for every PR of a repo whose stack record can't be read", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", { ...review, stack: undefined }), worker("ENG-1", "idle"), pr("ENG-1", { ready: true, approved: true, head: "h1" }));
  s.unread("stack");
  s.expect([]);
  s.expectHeld("auto-merge", "ENG-1");
});

scenario(rules, "a stacked PR that conflicts with its base starts no needs-work nudge", s => {
  s.given(ticket("ENG-3", { ...review, ...blockedBy("ENG-2"), stack }), worker("ENG-3", "idle"), pr("ENG-3", { head: "h1", conflict: true, problems: ["a merge conflict"] }),
    ...parentReady);
  s.expect([["notify-ready-for-review", "notify", "ENG-2"]]);
});

scenario(rules, "a stacked PR that conflicts with its base is not relaunched or escalated", s => {
  s.given(ticket("ENG-3", { ...review, ...blockedBy("ENG-2"), stack }), worker("ENG-3", "none"), pr("ENG-3", { head: "h1", conflict: true, problems: ["a merge conflict"] }),
    ...parentReady);
  for (let i = 0; i < 4; i++) {
    s.expect([["notify-ready-for-review", "notify", "ENG-2"]]);
    s.see(worker("ENG-3", "none"));
  }
});

scenario(rules, "a stacked PR with another problem than the conflict is still nudged for it", s => {
  s.given(ticket("ENG-3", { ...review, ...blockedBy("ENG-2"), stack }), worker("ENG-3", "idle"), pr("ENG-3", { head: "h1", problems: ["a failed check"] }),
    ...parentReady);
  s.expect([["nudge-pr-needs-work", "nudge", "ENG-3"], ["notify-ready-for-review", "notify", "ENG-2"]]);
});

scenario(rules, "review activity on a conflicting stacked PR reaches an idle worker", s => {
  s.given(ticket("ENG-3", { ...review, ...blockedBy("ENG-2"), stack }), worker("ENG-3", "idle"),
    pr("ENG-3", { head: "h1", conflict: true, problems: ["a merge conflict"] }), ...parentReady);
  s.see(pr("ENG-3", { activityAt: "10:00" }));
  s.expect([["nudge-stacked-activity", "nudge", "ENG-3"], ["notify-ready-for-review", "notify", "ENG-2"]]);
});

scenario(rules, "a stacked ticket whose stack parent is Canceled moves to Needs Human once", s => {
  s.given(ticket("ENG-3", { ...review, ...blockedBy("ENG-2", "canceled", true), stack }), worker("ENG-3", "idle"), pr("ENG-3", { head: "h1" }),
    ticket("ENG-2", { status: "canceled", repo: "dispatch" }));
  s.expect([["escalate-abandoned-stack-parent", "escalate", "ENG-3"]]);
  s.land("escalate", "ENG-3");
  s.expect([]);
});

scenario(rules, "a stacked ticket whose stack parent is a Duplicate moves to Needs Human once", s => {
  s.given(ticket("ENG-3", { ...build, ...blockedBy("ENG-2", "duplicate", true), stack }), worker("ENG-3", "none"),
    ticket("ENG-2", { status: "duplicate", repo: "dispatch" }));
  s.expect([["escalate-abandoned-stack-parent", "escalate", "ENG-3"]]);
});

// The two rules below copy shipped rules, so each scenario lands the shipped rule's receipt and
// expects the copy to find its once key done, and the other way round.
scenario(rules, "notify-stacked-ready and notify-ready-for-review share a once key", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-3", { ...review, ...blockedBy("ENG-2"), stack }), worker("ENG-3", "idle"),
    pr("ENG-3", { ready: true, approved: true, head: "h1" }), ...parentReady);
  s.land("notify", "ENG-3");
  s.land("notify", "ENG-2");
  s.config({ repos: { dispatch: { autoMerge: false } } });
  s.see(pr("ENG-3", { head: "h1" }));
  s.expect([]);
});

scenario(rules, "nudge-stacked-activity and nudge-pr-activity share a once key", s => {
  s.given(ticket("ENG-3", { ...review, ...blockedBy("ENG-2"), stack }), worker("ENG-3", "idle"),
    pr("ENG-3", { head: "h1", conflict: true, problems: ["a merge conflict"] }), ...parentReady);
  s.land("notify", "ENG-2");
  s.see(pr("ENG-3", { activityAt: "10:00" }));
  s.expect([["nudge-stacked-activity", "nudge", "ENG-3"]]);
  s.land("nudge", "ENG-3");
  s.see(pr("ENG-3", { head: "h1", conflict: false, problems: [] }));
  s.see(worker("ENG-3", "idle"));
  s.expect([]);
});

scenario(rules, "a stack parent that is Done tells the worker to restack, and an open one does not", s => {
  s.given(ticket("ENG-3", { ...review, ...blockedBy("ENG-2", "done"), stack }), worker("ENG-3", "idle"), pr("ENG-3", { head: "h1" }));
  s.expect([["nudge-restack", "nudge", "ENG-3"]]);
  s.see(ticket("ENG-3", { ...review, ...blockedBy("ENG-2", "in_review"), stack }));
  s.expect([]);
});

scenario(rules, "in A <- B <- C, when A is Done only B is told to restack", s => {
  s.given(
    ticket("ENG-2", { ...review, ...blockedBy("ENG-1", "done"), stack: { parent: "ENG-1", forkPoint: "b".repeat(40) } }),
    worker("ENG-2", "idle"), pr("ENG-2", { head: "b1" }),
    ticket("ENG-3", { ...review, ...blockedBy("ENG-2"), stack }), worker("ENG-3", "idle"), pr("ENG-3", { head: "c1" }));
  s.expect([["nudge-restack", "nudge", "ENG-2"]]);
});

scenario(rules, "notify-ready-for-review's receipt satisfies notify-stacked-ready", s => {
  s.given(ticket("ENG-3", { ...review, ...blockedBy("ENG-2"), stack }), worker("ENG-3", "idle"),
    pr("ENG-3", { ready: true, approved: true, head: "h1" }), ...parentReady);
  s.expect([["notify-ready-for-review", "notify", "ENG-3"], ["notify-ready-for-review", "notify", "ENG-2"]]);
  s.land("notify", "ENG-3");
  s.land("notify", "ENG-2");
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.see(pr("ENG-3", { head: "h1" }));
  s.expect([]);
});

scenario(rules, "nudge-pr-activity's receipt satisfies nudge-stacked-activity", s => {
  s.given(ticket("ENG-3", { ...review, ...blockedBy("ENG-2"), stack }), worker("ENG-3", "idle"), pr("ENG-3", { head: "h1" }), ...parentReady);
  s.land("notify", "ENG-2");
  s.see(pr("ENG-3", { activityAt: "10:00" }));
  s.expect([["nudge-pr-activity", "nudge", "ENG-3"]]);
  s.land("nudge", "ENG-3");
  s.see(pr("ENG-3", { conflict: true, problems: ["a merge conflict"] }), worker("ENG-3", "idle"));
  s.expect([]);
});

scenario(rules, "a stacked ticket in Needs Human is not told to restack", s => {
  s.given(ticket("ENG-3", { ...restackOwed, status: "needs_human" }), worker("ENG-3", "idle"));
  s.expect([["notify-needs-human", "notify", "ENG-3"]]);
});

scenario(rules, "a stacked ticket with another abandoned blocker still escalates", s => {
  s.given(ticket("ENG-3", { ...build, stack, blockers: [{ id: "ENG-2", status: "in_review" }, { id: "ENG-5", status: "canceled", abandoned: true }] }),
    ...parentReady);
  s.expect([["escalate-abandoned-blocker", "escalate", "ENG-3"], ["notify-ready-for-review", "notify", "ENG-2"]]);
});

scenario(rules, "a ticket that is not opted in is not relaunched for a restack", s => {
  s.given(ticket("ENG-3", { ...restackOwed, status: "in_progress", optedIn: false }), worker("ENG-3", "none"));
  s.expect([]);
});

scenario(rules, "a ticket that is not opted in keeps its blocker hold comment", s => {
  s.given(ticket("ENG-3", { ...build, optedIn: false, ...blockedBy("ENG-2") }), ...parentReady);
  s.expect([["note-blocker-hold", "comment", "ENG-3"], ["notify-ready-for-review", "notify", "ENG-2"]]);
});

scenario(rules, "a restack owed beats a stacked launch on another blocker", s => {
  s.given(ticket("ENG-3", { ...build, stack, blockers: [{ id: "ENG-2", status: "done" }, { id: "ENG-4", status: "in_review" }] }),
    ticket("ENG-4", review), worker("ENG-4", "idle"), pr("ENG-4", { ready: true }));
  s.expect([["relaunch-for-restack", "launch", "ENG-3"], ["notify-ready-for-review", "notify", "ENG-4"]]);
});

scenario(rules, "a restack owed leaves no blocker hold comment", s => {
  s.given(ticket("ENG-3", { ...build, stack, blockers: [{ id: "ENG-2", status: "done" }, { id: "ENG-4", status: "in_progress" }] }), ticket("ENG-4", build), worker("ENG-4", "working"));
  s.expect([["relaunch-for-restack", "launch", "ENG-3"]]);
});
