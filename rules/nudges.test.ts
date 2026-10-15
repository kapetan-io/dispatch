import { scenario, ticket, worker, comment, pr, rulesFrom } from "dispatch/testing";

const rules = rulesFrom("./dispatch");

const build = { status: "in_progress", optedIn: true, repo: "dispatch" };
const review = { status: "in_review", optedIn: true, repo: "dispatch" };

// A worker that stops again after a nudge: it works, then idles.
const stopsAgain = (s: { see: (...f: ReturnType<typeof worker>[]) => void }, id: string) => {
  s.see(worker(id, "working"));
  s.see(worker(id, "idle"));
};

scenario(rules, "nudge-stall nudges an idle worker once per stop", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "idle"));
  s.expect([["nudge-stall", "herdr.nudge", "ENG-1"]]);
  s.land("herdr.nudge", "ENG-1");
  s.expect([]);
});

scenario(rules, "nudge-stall nudges again after the worker stops again", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "idle"));
  s.land("herdr.nudge", "ENG-1");
  s.expect([]);
  stopsAgain(s, "ENG-1");
  s.expect([["nudge-stall", "herdr.nudge", "ENG-1"]]);
});

scenario(rules, "nudge-stall waits for a Stop after the last nudge", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "idle"));
  s.land("herdr.nudge", "ENG-1");
  s.advance(10 * 60 * 1000);
  s.expect([]);
});

scenario(rules, "nudge-stall does not nudge a working worker", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "working"));
  s.expect([]);
});

scenario(rules, "nudge-stall does not nudge a ticket in review", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"));
  s.expect([]);
});

scenario(rules, "nudge-stall does not nudge a design worker, which waits on a human", s => {
  s.given(ticket("ENG-1", { ...build, labels: ["design"] }), worker("ENG-1", "idle"));
  s.expect([]);
});

scenario(rules, "nudge-stall does not nudge a design-review worker", s => {
  s.given(ticket("ENG-1", { ...build, labels: ["design-review"] }), worker("ENG-1", "idle"));
  s.expect([]);
});

scenario(rules, "nudge-stall does not nudge a parent", s => {
  s.given(ticket("ENG-1", build), ticket("ENG-2", { parent: "ENG-1", status: "done" }), worker("ENG-1", "idle"));
  s.expect([["close-parent", "linear.move", "ENG-1"]]);
});

scenario(rules, "nudge-stall waits out a Stop with background tasks still running", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "idle", { lastStop: 0, busy: true }));
  s.expect([]);
  s.advance(61 * 60 * 1000);
  s.expect([["nudge-stall", "herdr.nudge", "ENG-1"]]);
});

scenario(rules, "nudge-stall is held when herdr could not be read", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "idle"));
  s.unread("herdr");
  s.expect([]);
  s.expectHeld("nudge-stall", "ENG-1", "herdr unread");
});

scenario(rules, "a refused stall nudge is not sent again for the same stop", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "idle"));
  s.land("herdr.nudge", "ENG-1", { outcome: "refused", detail: "worker asked a question" });
  s.expect([]);
});

scenario(rules, "a failed stall nudge is sent again", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "idle"));
  s.land("herdr.nudge", "ENG-1", { outcome: "failed", detail: "herdr down" });
  s.expect([["nudge-stall", "herdr.nudge", "ENG-1"]]);
});

scenario(rules, "nudge-comment goes before nudge-stall for the same worker", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "idle"));
  s.see(comment("ENG-1", { body: "use v2", at: "10:00" }));
  s.expect([["nudge-comment", "herdr.nudge", "ENG-1"]]);
  s.land("herdr.nudge", "ENG-1");
  s.expect([]);
  stopsAgain(s, "ENG-1");
  s.expect([["nudge-stall", "herdr.nudge", "ENG-1"]]);
});

scenario(rules, "nudge-comment waits for a Stop after the last nudge before telling of a second comment", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "idle"));
  s.see(comment("ENG-1", { at: "10:00" }));
  s.land("herdr.nudge", "ENG-1");
  s.see(comment("ENG-1", { at: "11:00" }));
  s.expect([]);
  stopsAgain(s, "ENG-1");
  s.expect([["nudge-comment", "herdr.nudge", "ENG-1"]]);
  s.land("herdr.nudge", "ENG-1");
  s.expect([]);
});

scenario(rules, "nudge-comment nudges a working worker", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "working"));
  s.see(comment("ENG-1", { at: "10:00" }));
  s.expect([["nudge-comment", "herdr.nudge", "ENG-1"]]);
});

scenario(rules, "nudge-comment never types into a blocked worker", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "blocked"));
  s.see(comment("ENG-1", { at: "10:00" }));
  s.expect([["notify-blocked", "notify.send", "ENG-1"]]);
});

scenario(rules, "nudge-comment is once per comment time across a restart", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "idle"));
  s.see(comment("ENG-1", { at: "10:00" }));
  s.land("herdr.nudge", "ENG-1");
  s.restart();
  stopsAgain(s, "ENG-1");
  s.expect([["nudge-stall", "herdr.nudge", "ENG-1"]]);
});

scenario(rules, "nudge-comment does not tell a worker of a comment it started out knowing", s => {
  s.given(ticket("ENG-1", build), comment("ENG-1", { at: "09:00" }), worker("ENG-1", "working"));
  s.expect([]);
});

scenario(rules, "nudge-comment does not nudge a design ticket", s => {
  s.given(ticket("ENG-1", { ...build, labels: ["design"] }), worker("ENG-1", "idle"));
  s.see(comment("ENG-1", { at: "10:00" }));
  s.expect([]);
});

scenario(rules, "nudge-comment does not nudge a ticket in Needs Human", s => {
  s.given(ticket("ENG-1", { status: "needs_human", optedIn: true, repo: "dispatch" }), worker("ENG-1", "idle"));
  s.see(comment("ENG-1", { at: "10:00" }));
  s.expect([["notify-needs-human", "notify.send", "ENG-1"]]);
});

scenario(rules, "nudge-comment is held when herdr could not be read", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "idle"));
  s.see(comment("ENG-1", { at: "10:00" }));
  s.unread("herdr");
  s.expect([]);
  s.expectHeld("nudge-comment", "ENG-1", "herdr unread");
});

scenario(rules, "nudge-resume tells an idle worker the human answered, instead of the comment and stall nudges", s => {
  s.given(ticket("ENG-1", { status: "needs_human", optedIn: true, repo: "dispatch" }), worker("ENG-1", "idle"));
  s.see(comment("ENG-1", { at: "10:00" }));
  s.see(ticket("ENG-1", { status: "in_progress" }));
  s.expect([["nudge-resume", "herdr.nudge", "ENG-1"]]);
  s.land("herdr.nudge", "ENG-1");
  s.expect([]);
  stopsAgain(s, "ENG-1");
  s.expect([["nudge-stall", "herdr.nudge", "ENG-1"]]);
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
  s.expect([["nudge-stall", "herdr.nudge", "ENG-1"]]);
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

scenario(rules, "nudge-pr-activity lets an owed comment go first", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1"));
  s.see(pr("ENG-1", { activityAt: "10:00" }));
  s.see(comment("ENG-1", { at: "10:30" }));
  s.expect([["nudge-comment", "herdr.nudge", "ENG-1"]]);
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

scenario(rules, "a refused resume nudge is not owed again, so the stall nudge is not held behind it", s => {
  s.given(ticket("ENG-1", { status: "in_progress", previousStatus: "needs_human" }), worker("ENG-1", "idle"));
  s.expect([["nudge-resume", "herdr.nudge", "ENG-1"]]);
  s.land("herdr.nudge", "ENG-1", { outcome: "refused", detail: "blocked" });
  s.see(worker("ENG-1", "working"));
  s.see(worker("ENG-1", "idle"));
  s.expect([["nudge-stall", "herdr.nudge", "ENG-1"]]);
});
