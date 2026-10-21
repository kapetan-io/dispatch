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
  s.expect([["nudge-pr-needs-work", "herdr.nudge", "ENG-1"]]);
  s.land("herdr.nudge", "ENG-1");
  s.expect([]);
});

scenario(rules, "a nudge goes out again after the worker stops again", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", failing));
  s.land("herdr.nudge", "ENG-1");
  s.expect([]);
  stopsAgain(s, "ENG-1");
  s.expect([["nudge-pr-needs-work", "herdr.nudge", "ENG-1"]]);
});

scenario(rules, "no nudge goes out until a Stop after the last nudge", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", failing));
  s.land("herdr.nudge", "ENG-1");
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
  s.land("herdr.nudge", "ENG-1", { outcome: "refused", detail: "worker asked a question" });
  s.expect([]);
});

scenario(rules, "a failed nudge is sent again", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", failing));
  s.land("herdr.nudge", "ENG-1", { outcome: "failed", detail: "herdr down" });
  s.expect([["nudge-pr-needs-work", "herdr.nudge", "ENG-1"]]);
});

scenario(rules, "a ticket comment sends a working worker no nudge", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "working"));
  s.see(comment("ENG-1", { at: "10:00" }));
  s.expect([]);
});

scenario(rules, "a ticket comment does not hold back a nudge", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", failing));
  s.see(comment("ENG-1", { body: "use v2", at: "10:00" }));
  s.expect([["nudge-pr-needs-work", "herdr.nudge", "ENG-1"]]);
});

scenario(rules, "a ticket comment on a blocked worker only notifies the user", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "blocked"));
  s.see(comment("ENG-1", { at: "10:00" }));
  s.expect([["notify-blocked", "notify.send", "ENG-1"]]);
});

scenario(rules, "nudge-resume tells an idle worker the human answered, ", s => {
  s.given(ticket("ENG-1", { status: "needs_human", optedIn: true, repo: "dispatch" }), worker("ENG-1", "idle"));
  s.see(comment("ENG-1", { at: "10:00" }));
  s.see(ticket("ENG-1", { status: "in_progress" }));
  s.expect([["nudge-resume", "herdr.nudge", "ENG-1"]]);
  s.land("herdr.nudge", "ENG-1");
  s.expect([]);
  stopsAgain(s, "ENG-1");
  s.expect([]);
});

scenario(rules, "nudge-resume waits for a working worker to idle", s => {
  s.given(ticket("ENG-1", { status: "needs_human", optedIn: true, repo: "dispatch" }), worker("ENG-1", "working"));
  s.see(ticket("ENG-1", { status: "in_progress" }));
  s.expect([]);
  s.see(worker("ENG-1", "idle"));
  s.expect([["nudge-resume", "herdr.nudge", "ENG-1"]]);
});

scenario(rules, "nudge-resume is once per move back from Needs Human", s => {
  s.given(ticket("ENG-1", { status: "needs_human", optedIn: true, repo: "dispatch" }), worker("ENG-1", "idle"));
  s.see(ticket("ENG-1", { status: "in_progress" }));
  s.land("herdr.nudge", "ENG-1");
  stopsAgain(s, "ENG-1");
  s.see(ticket("ENG-1", { status: "needs_human" }));
  s.see(ticket("ENG-1", { status: "in_progress" }));
  s.expect([["nudge-resume", "herdr.nudge", "ENG-1"]]);
});

scenario(rules, "nudge-resume does not follow a move from another status", s => {
  s.given(ticket("ENG-1", { status: "in_review", optedIn: true, repo: "dispatch" }), worker("ENG-1", "idle"));
  s.see(ticket("ENG-1", { status: "in_progress" }));
  s.expect([]);
});

scenario(rules, "nudge-pr-activity tells an idle worker of new PR activity", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1"));
  s.expect([]);
  s.see(pr("ENG-1", { activityAt: "10:00", activitySummary: "a review comment" }));
  s.expect([["nudge-pr-activity", "herdr.nudge", "ENG-1"]]);
  s.land("herdr.nudge", "ENG-1");
  s.expect([]);
});

scenario(rules, "nudge-pr-activity tells of a later activity only after the worker stops again", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1"));
  s.see(pr("ENG-1", { activityAt: "10:00" }));
  s.land("herdr.nudge", "ENG-1");
  s.see(pr("ENG-1", { activityAt: "11:00" }));
  s.expect([]);
  stopsAgain(s, "ENG-1");
  s.expect([["nudge-pr-activity", "herdr.nudge", "ENG-1"]]);
});

scenario(rules, "nudge-pr-activity leaves a worker that already knew of the activity", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { activityAt: "09:00" }));
  s.expect([]);
});

scenario(rules, "nudge-pr-activity leaves a PR that needs work to nudge-pr-needs-work", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1"));
  s.see(pr("ENG-1", { activityAt: "10:00", problems: ["a failed check"] }));
  s.expect([["nudge-pr-needs-work", "herdr.nudge", "ENG-1"]]);
});

scenario(rules, "nudge-pr-activity is not held back by a ticket comment", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1"));
  s.see(pr("ENG-1", { activityAt: "10:00" }));
  s.see(comment("ENG-1", { at: "10:30" }));
  s.expect([["nudge-pr-activity", "herdr.nudge", "ENG-1"]]);
});

scenario(rules, "nudge-pr-activity is held when the PR could not be read", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1"));
  s.unread("github:ENG-1");
  s.expect([]);
  s.expectHeld("nudge-pr-activity", "ENG-1");
});

scenario(rules, "nudge-pr-needs-work nudges an idle worker whose PR needs work", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { problems: ["a failed check"] }));
  s.expect([["nudge-pr-needs-work", "herdr.nudge", "ENG-1"]]);
  s.land("herdr.nudge", "ENG-1");
  s.expect([]);
});

scenario(rules, "nudge-pr-needs-work escalates after nudge_limit nudges for one head", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { head: "h1", problems: ["a failed check"] }));
  s.land("herdr.nudge", "ENG-1");
  stopsAgain(s, "ENG-1");
  s.expect([["nudge-pr-needs-work", "herdr.nudge", "ENG-1"]]);
  s.land("herdr.nudge", "ENG-1");
  stopsAgain(s, "ENG-1");
  s.expect([["nudge-pr-needs-work", "linear.escalate", "ENG-1"]]);
  s.land("linear.escalate", "ENG-1");
  s.expect([]);
});

scenario(rules, "nudge-pr-needs-work starts the count over for a new head", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { head: "h1", problems: ["a failed check"] }));
  s.land("herdr.nudge", "ENG-1");
  stopsAgain(s, "ENG-1");
  s.land("herdr.nudge", "ENG-1");
  stopsAgain(s, "ENG-1");
  s.see(pr("ENG-1", { head: "h2" }));
  s.expect([["nudge-pr-needs-work", "herdr.nudge", "ENG-1"]]);
});

scenario(rules, "nudge-pr-needs-work leaves a working worker alone", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "working"));
  s.expect([]);
});

scenario(rules, "nudge-pr-needs-work leaves a PR that does not need work", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true }));
  s.expect([["notify-ready-for-review", "notify.send", "ENG-1"]]);
});

scenario(rules, "nudge-pr-needs-work is held when the PR could not be read", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { problems: ["a failed check"] }));
  s.unread("github:ENG-1");
  s.expect([]);
  s.expectHeld("nudge-pr-needs-work", "ENG-1");
});

scenario(rules, "a refused resume nudge is not owed again, so a later nudge is not held behind it", s => {
  s.given(ticket("ENG-1", { status: "in_progress", previousStatus: "needs_human" }), worker("ENG-1", "idle"));
  s.expect([["nudge-resume", "herdr.nudge", "ENG-1"]]);
  s.land("herdr.nudge", "ENG-1", { outcome: "refused", detail: "blocked" });
  s.see(worker("ENG-1", "working"));
  s.see(worker("ENG-1", "idle"));
  s.expect([]);
  s.see(ticket("ENG-1", review));
  s.see(pr("ENG-1", failing));
  s.expect([["nudge-pr-needs-work", "herdr.nudge", "ENG-1"]]);
});
