import { scenario, ticket, worker, pr, rulesFrom } from "dispatch/testing";

const rules = rulesFrom("./dispatch");

const build = { status: "in_progress", optedIn: true, repo: "dispatch" };
const review = { status: "in_review", optedIn: true, repo: "dispatch" };

scenario(rules, "notify-asked tells followers once per ask, in place of the idle notice", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "idle", { lastStop: 0, asked: -1000 }));
  s.expect([["notify-asked", "notify", "ENG-1"]]);
  s.expectExempt("notify-idle", "ENG-1", "an-asking-worker-hears-one-notice", "notify");
  s.land("notify", "ENG-1");
  s.expect([]);
});

scenario(rules, "notify-asked tells followers again for a second ask after an answer", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "idle", { lastStop: 0, asked: -1000 }));
  s.expect([["notify-asked", "notify", "ENG-1"]]);
  s.land("notify", "ENG-1");
  s.see(worker("ENG-1", "working", { asked: -1000, messages: [{ at: 1000 }] }));
  s.advance(60 * 1000);
  s.see(worker("ENG-1", "idle", { lastStop: 0, asked: 30 * 1000, messages: [{ at: 1000 }] }));
  s.expect([["notify-asked", "notify", "ENG-1"]]);
});

scenario(rules, "notify-asked is silent while the worker that asked is still in its turn", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "working", { asked: -1000 }));
  s.expect([]);
});

scenario(rules, "notify-asked is silent once the session took a message after the ask", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "idle", { asked: -2000, messages: [{ at: -1000 }] }));
  s.expect([]);
});

scenario(rules, "notify-asked is silent once the session took a command after the ask", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "idle", {
    asked: -2000, commands: [{ name: "dispatch:pr-respond", at: -1000, by: "dispatch" }],
  }));
  s.expect([]);
});

scenario(rules, "notify-asked is silent when the session's record can't be read", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "idle", { asked: -1000, sessionUnread: "broken" }));
  s.expect([]);
});

scenario(rules, "notify-asked is silent for a Needs Human ticket, which notify-needs-human covers", s => {
  s.given(ticket("ENG-1", { status: "needs_human", optedIn: true, repo: "dispatch" }), worker("ENG-1", "idle", { lastStop: 0, asked: -1000 }));
  s.expect([["notify-needs-human", "notify", "ENG-1"]]);
});

scenario(rules, "notify-asked tells followers of an In Review worker, and the ready PR notice waits", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle", { asked: -1000 }), pr("ENG-1", { ready: true, head: "h1" }));
  s.expect([["notify-asked", "notify", "ENG-1"]]);
  s.expectExempt("notify-ready-for-review", "ENG-1", "an-asking-worker-hears-one-notice", "notify");
  s.land("notify", "ENG-1");
  s.see(worker("ENG-1", "idle", { asked: -1000, messages: [{ at: 1000 }] }));
  s.expect([["notify-ready-for-review", "notify", "ENG-1"]]);
});

scenario(rules, "notify-asked stands in for the ready notice of a draft", s => {
  s.config({ repos: { dispatch: { openPrAs: "draft" } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle", { asked: -1000 }), pr("ENG-1", { ready: true, draft: true, head: "h1" }));
  s.expect([["notify-asked", "notify", "ENG-1"]]);
  s.expectExempt("notify-ready-for-review", "ENG-1", "an-asking-worker-hears-one-notice", "notify");
});

scenario(rules, "notify-asked stands in for the ready notice of an approved PR dispatch won't merge", s => {
  s.config({ repos: { dispatch: { openPrAs: "draft" } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle", { asked: -1000 }),
          pr("ENG-1", { ready: true, draft: false, approved: true, head: "h1" }));
  s.expect([["notify-asked", "notify", "ENG-1"]]);
  s.expectExempt("notify-ready-for-review", "ENG-1", "an-asking-worker-hears-one-notice", "notify");
});

const failing = { problems: ["a failed check"] };
const approved = { ready: true, approved: true, head: "h1" };

scenario(rules, "an asking worker gets no PR nudge for a PR that needs work", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle", { asked: -1000 }), pr("ENG-1", failing));
  s.expect([["notify-asked", "notify", "ENG-1"]]);
  s.expectExempt("nudge-pr-needs-work", "ENG-1", "an-asking-worker-is-left-alone", "nudge");
});

scenario(rules, "an asking worker gets no nudge for new PR activity", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle", { asked: -1000 }), pr("ENG-1"));
  s.land("notify", "ENG-1");
  s.see(pr("ENG-1", { activityAt: "10:00", activitySummary: "a review comment" }));
  s.expect([]);
  s.expectExempt("nudge-pr-activity", "ENG-1", "an-asking-worker-is-left-alone", "nudge");
});

scenario(rules, "an asking worker is not escalated for ignoring PR nudges", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { head: "h1", ...failing }));
  s.land("nudge", "ENG-1");
  s.see(worker("ENG-1", "working"));
  s.see(worker("ENG-1", "idle"));
  s.land("nudge", "ENG-1");
  s.see(worker("ENG-1", "working"));
  s.advance(1000);
  s.see(worker("ENG-1", "idle", { asked: 0 }));
  s.expect([["notify-asked", "notify", "ENG-1"]]);
  s.expectExempt("nudge-pr-needs-work", "ENG-1", "an-asking-worker-is-left-alone", "escalate");
});

scenario(rules, "an asking worker's approved PR is not merged, and the approved-and-merged notice goes with the merge", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle", { asked: -1000 }), pr("ENG-1", approved));
  s.expect([["notify-asked", "notify", "ENG-1"]]);
  s.expectExempt("auto-merge", "ENG-1", "an-asking-worker-is-left-alone", "merge");
  s.expectExempt("auto-merge", "ENG-1", "an-asking-worker-is-left-alone", "notify");
});

scenario(rules, "an asking worker's PR merges once a message ends the ask and the worker's turn ends", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle", { asked: -2000 }), pr("ENG-1", approved));
  s.land("notify", "ENG-1");
  s.expect([]);
  s.see(worker("ENG-1", "idle", { asked: -2000, messages: [{ at: -1000 }], turns: [{ at: 0, running: false }] }));
  s.expect([["auto-merge", "merge", "ENG-1"]]);
});

scenario(rules, "a task notice leaves the ask open: no second notice, no nudge, no merge", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle", { asked: -1000 }), pr("ENG-1", approved));
  s.land("notify", "ENG-1");
  s.see(worker("ENG-1", "working", { asked: -1000, messages: [{ at: 1000, task: true }] }));
  s.advance(60 * 1000);
  s.see(worker("ENG-1", "idle", { asked: -1000, messages: [{ at: 1000, task: true }], turns: [{ at: 30 * 1000, running: false }] }));
  s.expect([]);
  s.expectExempt("auto-merge", "ENG-1", "an-asking-worker-is-left-alone", "merge");
});

scenario(rules, "an asking worker whose session log can't be read is nudged, as the log holds no ask then", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle", { asked: -1000, sessionUnread: "broken" }), pr("ENG-1", failing));
  s.expect([["nudge-pr-needs-work", "nudge", "ENG-1"]]);
});

scenario(rules, "an asking worker whose session log can't be read has its approved PR merged", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle", { asked: -1000, sessionUnread: "broken" }), pr("ENG-1", approved));
  s.expect([["auto-merge", "merge", "ENG-1"]]);
});

scenario(rules, "notify-asked tells followers of a question asked after the ticket moved to Needs Human", s => {
  s.given(ticket("ENG-1", { status: "needs_human", optedIn: true, repo: "dispatch" }), worker("ENG-1", "idle", { lastStop: 0 }));
  s.land("notify", "ENG-1");
  s.advance(60 * 1000);
  s.see(worker("ENG-1", "idle", { lastStop: 0, asked: 30 * 1000 }));
  s.expect([["notify-asked", "notify", "ENG-1"]]);
});
