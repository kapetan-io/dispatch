import { scenario, ticket, worker, comment, pr, rulesFrom } from "dispatch/testing";

const rules = rulesFrom("./dispatch");

const build = { status: "in_progress", optedIn: true, repo: "dispatch" };
const review = { status: "in_review", optedIn: true, repo: "dispatch" };

// A worker that stops again after a nudge: it works, then idles.
const stopsAgain = (s: { see: (...f: ReturnType<typeof worker>[]) => void }, id: string) => {
  s.see(worker(id, "working"));
  s.see(worker(id, "idle"));
};

const failing = { problems: ["a failed check"] };

scenario(rules, "a nudge goes out once per Stop", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", failing));
  s.expect([["nudge-pr-needs-work", "nudge", "ENG-1"]]);
  s.land("nudge", "ENG-1");
  s.expect([]);
});

scenario(rules, "a nudge goes out again after the worker stops again", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", failing));
  s.land("nudge", "ENG-1");
  s.expect([]);
  stopsAgain(s, "ENG-1");
  s.expect([["nudge-pr-needs-work", "nudge", "ENG-1"]]);
});

scenario(rules, "no nudge goes out until a Stop after the last nudge", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", failing));
  s.land("nudge", "ENG-1");
  s.advance(10 * 60 * 1000);
  s.expect([]);
});

scenario(rules, "a nudge is held when herdr could not be read", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", failing));
  s.unread("herdr");
  s.expect([]);
  s.expectHeld("nudge-pr-needs-work", "ENG-1", "herdr unread");
});

scenario(rules, "a refused nudge is not sent again for the same Stop", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", failing));
  s.land("nudge", "ENG-1", { outcome: "refused", detail: "worker asked a question" });
  s.expect([]);
});

scenario(rules, "a failed nudge is sent again", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", failing));
  s.land("nudge", "ENG-1", { outcome: "failed", detail: "herdr down" });
  s.expect([["nudge-pr-needs-work", "nudge", "ENG-1"]]);
});

scenario(rules, "a ticket comment sends a working worker no nudge", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "working"));
  s.see(comment("ENG-1", { at: "10:00" }));
  s.expect([]);
});

scenario(rules, "a ticket comment does not hold back a nudge", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", failing));
  s.see(comment("ENG-1", { body: "use v2", at: "10:00" }));
  s.expect([["nudge-pr-needs-work", "nudge", "ENG-1"]]);
});

scenario(rules, "a ticket comment on a blocked worker only notifies the user", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "blocked"));
  s.see(comment("ENG-1", { at: "10:00" }));
  s.expect([["notify-blocked", "notify", "ENG-1"]]);
});

scenario(rules, "nudge-pr-activity tells an idle worker of new PR activity", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1"));
  s.expect([]);
  s.see(pr("ENG-1", { activityAt: "10:00", activitySummary: "a review comment" }));
  s.expect([["nudge-pr-activity", "nudge", "ENG-1"]]);
  s.land("nudge", "ENG-1");
  s.expect([]);
});

scenario(rules, "nudge-pr-activity tells of a later activity only after the worker stops again", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1"));
  s.see(pr("ENG-1", { activityAt: "10:00" }));
  s.land("nudge", "ENG-1");
  s.see(pr("ENG-1", { activityAt: "11:00" }));
  s.expect([]);
  stopsAgain(s, "ENG-1");
  s.expect([["nudge-pr-activity", "nudge", "ENG-1"]]);
});

scenario(rules, "nudge-pr-activity leaves a worker that already knew of the activity", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { activityAt: "09:00" }));
  s.expect([]);
});

scenario(rules, "nudge-pr-activity leaves a PR that needs work to nudge-pr-needs-work", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1"));
  s.see(pr("ENG-1", { activityAt: "10:00", problems: ["a failed check"] }));
  s.expect([["nudge-pr-needs-work", "nudge", "ENG-1"]]);
});

scenario(rules, "nudge-pr-activity is not held back by a ticket comment", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1"));
  s.see(pr("ENG-1", { activityAt: "10:00" }));
  s.see(comment("ENG-1", { at: "10:30" }));
  s.expect([["nudge-pr-activity", "nudge", "ENG-1"]]);
});

scenario(rules, "nudge-pr-activity is held when the PR could not be read", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1"));
  s.unread("github:ENG-1");
  s.expect([]);
  s.expectHeld("nudge-pr-activity", "ENG-1");
});

scenario(rules, "nudge-pr-needs-work nudges an idle worker whose PR needs work", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { problems: ["a failed check"] }));
  s.expect([["nudge-pr-needs-work", "nudge", "ENG-1"]]);
  s.land("nudge", "ENG-1");
  s.expect([]);
});

scenario(rules, "nudge-pr-needs-work escalates after nudge_limit nudges for one head", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { head: "h1", problems: ["a failed check"] }));
  s.land("nudge", "ENG-1");
  stopsAgain(s, "ENG-1");
  s.expect([["nudge-pr-needs-work", "nudge", "ENG-1"]]);
  s.land("nudge", "ENG-1");
  stopsAgain(s, "ENG-1");
  s.expect([["nudge-pr-needs-work", "escalate", "ENG-1"]]);
  s.land("escalate", "ENG-1");
  s.expect([]);
});

scenario(rules, "nudge-pr-needs-work starts the count over for a new head", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { head: "h1", problems: ["a failed check"] }));
  s.land("nudge", "ENG-1");
  stopsAgain(s, "ENG-1");
  s.land("nudge", "ENG-1");
  stopsAgain(s, "ENG-1");
  s.see(pr("ENG-1", { head: "h2" }));
  s.expect([["nudge-pr-needs-work", "nudge", "ENG-1"]]);
});

scenario(rules, "nudge-pr-needs-work leaves a working worker alone", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "working"));
  s.expect([]);
});

scenario(rules, "nudge-pr-needs-work leaves a PR that does not need work", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true }));
  s.expect([["notify-ready-for-review", "notify", "ENG-1"]]);
});

scenario(rules, "nudge-pr-needs-work is held when the PR could not be read", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { problems: ["a failed check"] }));
  s.unread("github:ENG-1");
  s.expect([]);
  s.expectHeld("nudge-pr-needs-work", "ENG-1");
});

scenario(rules, "a worker still on the message it took gets no PR nudge", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle", { messages: [{ at: -1000 }] }), pr("ENG-1", failing));
  s.expect([]);
  s.expectExempt("nudge-pr-needs-work", "ENG-1", "a-worker-finishes-its-message-first", "nudge");
});

scenario(rules, "a worker still on a command it took gets no PR nudge", s => {
  s.given(ticket("ENG-1", review),
          worker("ENG-1", "idle", { commands: [{ name: "dispatch:pr-respond", at: -1000, by: "dispatch" }] }), pr("ENG-1", failing));
  s.expect([]);
  s.expectExempt("nudge-pr-needs-work", "ENG-1", "a-worker-finishes-its-message-first", "nudge");
});

scenario(rules, "a worker still on the message it took gets no activity nudge", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle", { messages: [{ at: -1000 }] }), pr("ENG-1"));
  s.see(pr("ENG-1", { activityAt: "10:00", activitySummary: "a review comment" }));
  s.expect([]);
  s.expectExempt("nudge-pr-activity", "ENG-1", "a-worker-finishes-its-message-first", "nudge");
});

scenario(rules, "a worker is nudged once the turn its message started ends", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle", { messages: [{ at: -2000 }], turns: [{ at: -1000, running: false }] }),
          pr("ENG-1", failing));
  s.expect([["nudge-pr-needs-work", "nudge", "ENG-1"]]);
});

scenario(rules, "a worker whose turn end is lost is nudged 15 minutes after its message", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle", { messages: [{ at: -1000 }] }), pr("ENG-1", failing));
  s.expect([]);
  s.advance(15 * 60 * 1000 - 1001);
  s.expect([]);
  s.advance(1);
  s.expect([["nudge-pr-needs-work", "nudge", "ENG-1"]]);
});

scenario(rules, "a task notice does not hold back nudges the way a message does", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle", { messages: [{ at: -1000, task: true }] }), pr("ENG-1", failing));
  s.expect([["nudge-pr-needs-work", "nudge", "ENG-1"]]);
});

scenario(rules, "a worker whose session log can't be read is nudged", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle", { messages: [{ at: -1000 }], sessionUnread: "broken" }), pr("ENG-1", failing));
  s.expect([["nudge-pr-needs-work", "nudge", "ENG-1"]]);
});

const needsHuman = { status: "needs_human", optedIn: true, repo: "dispatch" };

// The Needs Human notice is landed first, so each scenario sees only what the PR adds. A worker
// seen no later than an item counts as having heard it, so the PR's activity comes in a later read.
const needsHumanWith = (s, ...facts) => {
  s.given(ticket("ENG-1", needsHuman), ...facts);
  s.land("notify", "ENG-1");
  s.advance(1000);
};

// A Needs Human worker's lastStop and busy come from its Stop record, so stopping again after a
// nudge needs a Stop after it that lists no running task. Each read and landing moves the clock a
// second, so the Stop goes 30 seconds in, well after the nudge, and the clock past it.
const stopsAgainAfterTheNudge = (s, id: string) => {
  s.advance(60 * 1000);
  s.see(worker(id, "working"));
  s.see(worker(id, "idle", { lastStop: 30 * 1000, busy: false }));
};
scenario(rules, "nudge-pr-activity nudges a Needs Human worker about a new review and each later one", s => {
  needsHumanWith(s, worker("ENG-1", "idle", { lastStop: 0 }), pr("ENG-1"));
  s.see(pr("ENG-1", { activityAt: "10:00", activitySummary: "a review by alice" }));
  s.expect([["nudge-pr-activity", "nudge", "ENG-1"]]);
  s.land("nudge", "ENG-1");
  s.expect([]);
  stopsAgainAfterTheNudge(s, "ENG-1");
  s.advance(1000);
  s.see(pr("ENG-1", { activityAt: "11:00", activitySummary: "a comment by bob" }));
  s.expect([["nudge-pr-activity", "nudge", "ENG-1"]]);
});

scenario(rules, "nudge-pr-activity nudges a Needs Human worker that is asking", s => {
  needsHumanWith(s, worker("ENG-1", "idle", { lastStop: 0, asked: -1000 }), pr("ENG-1"));
  s.see(pr("ENG-1", { activityAt: "10:00" }));
  s.expect([["nudge-pr-activity", "nudge", "ENG-1"]]);
});

scenario(rules, "nudge-pr-activity still leaves an asking worker alone in In Review", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle", { lastStop: 0, asked: -1000 }), pr("ENG-1"));
  s.land("notify", "ENG-1");
  s.advance(1000);
  s.see(pr("ENG-1", { activityAt: "10:00" }));
  s.expectExempt("nudge-pr-activity", "ENG-1", "an-asking-worker-is-left-alone");
});

scenario(rules, "nudge-pr-activity nudges about activity on a Needs Human PR that needs work, and nothing else nudges", s => {
  needsHumanWith(s, worker("ENG-1", "idle", { lastStop: 0 }), pr("ENG-1", failing));
  s.expect([]);
  s.see(pr("ENG-1", { activityAt: "10:00", ...failing }));
  s.expect([["nudge-pr-activity", "nudge", "ENG-1"]]);
  s.land("nudge", "ENG-1");
  stopsAgainAfterTheNudge(s, "ENG-1");
  s.advance(60 * 60 * 1000);
  s.expect([]);
});

scenario(rules, "nudge-pr-activity waits while a Needs Human worker is working", s => {
  needsHumanWith(s, worker("ENG-1", "idle", { lastStop: 0 }), pr("ENG-1"));
  s.see(worker("ENG-1", "working"), pr("ENG-1", { activityAt: "10:00" }));
  s.expect([]);
  s.see(worker("ENG-1", "idle", { lastStop: 1000 }));
  s.expect([["nudge-pr-activity", "nudge", "ENG-1"]]);
});

scenario(rules, "nudge-pr-activity nudges no design worker in Needs Human", s => {
  s.given(ticket("ENG-1", { ...needsHuman, labels: ["design"] }), worker("ENG-1", "idle", { lastStop: 0 }));
  s.land("notify", "ENG-1");
  s.advance(1000);
  s.see(pr("ENG-1", { activityAt: "10:00" }));
  s.expect([]);
});

scenario(rules, "nudge-pr-activity does not repeat in Needs Human a review the worker heard of in In Review", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1"));
  s.advance(1000);
  s.see(pr("ENG-1", { activityAt: "10:00" }));
  s.land("nudge", "ENG-1");
  s.advance(1000);
  s.see(ticket("ENG-1", needsHuman));
  stopsAgainAfterTheNudge(s, "ENG-1");
  s.land("notify", "ENG-1");
  s.expect([]);
});

scenario(rules, "nudge-pr-activity stays silent when the newest item goes away and an older one becomes the newest", s => {
  needsHumanWith(s, worker("ENG-1", "idle", { lastStop: 0 }), pr("ENG-1"));
  s.see(pr("ENG-1", { activityAt: "10:00" }));
  s.advance(1000);
  s.see(pr("ENG-1", { activityAt: "11:00" }));
  s.land("nudge", "ENG-1");
  stopsAgainAfterTheNudge(s, "ENG-1");
  s.advance(1000);
  s.see(pr("ENG-1", { activityAt: "10:00" }));
  s.expect([]);
  s.see(pr("ENG-1", { activityAt: "12:00" }));
  s.expect([["nudge-pr-activity", "nudge", "ENG-1"]]);
});

scenario(rules, "nudge-pr-activity does not repeat in In Review a comment the worker heard of in Needs Human", s => {
  needsHumanWith(s, worker("ENG-1", "idle", { lastStop: 0 }), pr("ENG-1"));
  s.see(pr("ENG-1", { activityAt: "10:00" }));
  s.land("nudge", "ENG-1");
  stopsAgainAfterTheNudge(s, "ENG-1");
  s.see(ticket("ENG-1", review));
  s.expect([]);
});
