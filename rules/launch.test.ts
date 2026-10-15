import { scenario, ticket, worker, pool, pr, tree, rulesFrom } from "dispatch/testing";

const rules = rulesFrom("./dispatch");

const build = { status: "in_progress", optedIn: true, repo: "dispatch" };

scenario(rules, "launch-worker starts a worker for an opted-in ticket with none", s => {
  s.given(ticket("ENG-1", build));
  s.expect([["launch-worker", "herdr.launch", "ENG-1"]]);
});

scenario(rules, "launch-worker leaves alone a ticket that is not opted in", s => {
  s.given(ticket("ENG-1", { status: "in_progress", repo: "dispatch" }));
  s.expect([]);
});

scenario(rules, "launch-worker leaves alone a ticket in a repo the config lacks", s => {
  s.given(ticket("ENG-1", { status: "in_progress", optedIn: true, repo: "elsewhere" }));
  s.expect([["escalate-missing-repo", "linear.escalate", "ENG-1"]]);
});

scenario(rules, "launch-worker leaves alone a parent", s => {
  s.given(ticket("ENG-1", build), ticket("ENG-2", { parent: "ENG-1", status: "todo" }));
  s.expect([]);
});

scenario(rules, "launch-worker waits while a blocker is open", s => {
  s.given(ticket("ENG-1", { ...build, blockers: [{ id: "ENG-9", status: "in_progress" }] }));
  s.expect([["note-blocker-hold", "linear.comment", "ENG-1"]]);
});

scenario(rules, "launch-worker starts once every blocker is done", s => {
  s.given(ticket("ENG-1", { ...build, blockers: [{ id: "ENG-9", status: "done" }] }));
  s.expect([["launch-worker", "herdr.launch", "ENG-1"]]);
});

scenario(rules, "launch-worker holds for a ticket whose blockers were cut short", s => {
  s.given(ticket("ENG-1", { ...build, blockersTruncated: true }));
  s.expect([["note-blocker-hold", "linear.comment", "ENG-1"]]);
  s.expectHeld("launch-worker", "ENG-1");
});

scenario(rules, "launch-worker holds for a ticket whose labels were cut short", s => {
  s.given(ticket("ENG-1", { ...build, labelsTruncated: true }));
  s.expect([["escalate-truncated-labels", "linear.escalate", "ENG-1"]]);
  s.expectHeld("launch-worker", "ENG-1");
});

scenario(rules, "launch-worker holds for an unread herdr and wants nothing from any rule", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "working"));
  s.unread("herdr");
  s.expect([]);
  s.expectHeld("launch-worker", "ENG-1", "herdr unread");
});

scenario(rules, "launch-worker holds only the ticket in the repo whose pool is unread", s => {
  s.given(pool("dispatch"), pool("other"),
          ticket("ENG-1", build),
          ticket("ENG-2", { status: "in_progress", optedIn: true, repo: "other" }));
  s.unread("treehouse:other");
  s.expect([["launch-worker", "herdr.launch", "ENG-1"]]);
  s.expectHeld("launch-worker", "ENG-2", "other unread");
});

scenario(rules, "launch-worker launches the next stretch with no worker", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "working"));
  s.see(worker("ENG-1", "none"));
  s.expect([["launch-worker", "herdr.launch", "ENG-1"]]);
  s.land("herdr.launch", "ENG-1");
  s.expect([]);
  s.see(worker("ENG-1", "working"));
  s.see(worker("ENG-1", "none"));
  s.expect([["launch-worker", "herdr.launch", "ENG-1"]]);
});

scenario(rules, "a failed launch is tried again", s => {
  s.given(ticket("ENG-1", build));
  s.expect([["launch-worker", "herdr.launch", "ENG-1"]]);
  s.land("herdr.launch", "ENG-1", { outcome: "failed", detail: "boom" });
  s.expect([["launch-worker", "herdr.launch", "ENG-1"]]);
});

scenario(rules, "a refused launch is not repeated for the same stretch", s => {
  s.given(ticket("ENG-1", build));
  s.expect([["launch-worker", "herdr.launch", "ENG-1"]]);
  s.land("herdr.launch", "ENG-1", { outcome: "refused", detail: "already_running" });
  s.expect([]);
});

scenario(rules, "launch-worker stops after launch_failure_limit failures and escalate-launch-failures takes over", s => {
  s.given(ticket("ENG-1", build));
  s.land("herdr.launch", "ENG-1", { outcome: "failed", detail: "boom 1" });
  s.land("herdr.launch", "ENG-1", { outcome: "failed", detail: "boom 2" });
  s.expect([["escalate-launch-failures", "linear.escalate", "ENG-1"]]);
});

scenario(rules, "launch_failure_limit comes from the config", s => {
  s.config({ rules: { launch_failure_limit: 3 } });
  s.given(ticket("ENG-1", build));
  s.land("herdr.launch", "ENG-1", { outcome: "failed", detail: "boom 1" });
  s.land("herdr.launch", "ENG-1", { outcome: "failed", detail: "boom 2" });
  s.expect([["launch-worker", "herdr.launch", "ENG-1"]]);
  s.land("herdr.launch", "ENG-1", { outcome: "failed", detail: "boom 3" });
  s.expect([["escalate-launch-failures", "linear.escalate", "ENG-1"]]);
});

scenario(rules, "a launch that found the pool full resets the failure count", s => {
  s.given(ticket("ENG-1", build), pool("dispatch", { free: 0 }));
  s.land("herdr.launch", "ENG-1", { outcome: "failed", detail: "boom" });
  s.land("herdr.launch", "ENG-1", { outcome: "refused", detail: "pool_exhausted" });
  s.expect([]);
  s.see(pool("dispatch", { returns: 1 }));
  s.land("herdr.launch", "ENG-1", { outcome: "failed", detail: "boom" });
  s.expect([["launch-worker", "herdr.launch", "ENG-1"]]);
});

scenario(rules, "relaunch-for-pr brings a worker back for a PR that needs work", s => {
  s.given(ticket("ENG-1", { status: "in_review", optedIn: true, repo: "dispatch" }),
          pr("ENG-1", { problems: ["a failed check"] }));
  s.expect([["relaunch-for-pr", "herdr.launch", "ENG-1"]]);
  s.land("herdr.launch", "ENG-1");
  s.expect([]);
});

scenario(rules, "relaunch-for-pr brings a worker back for a review thread it has not seen", s => {
  s.given(ticket("ENG-1", { status: "in_review", optedIn: true, repo: "dispatch" }),
          pr("ENG-1", { threadAt: "10:00" }));
  s.expect([["relaunch-for-pr", "herdr.launch", "ENG-1"]]);
});

scenario(rules, "relaunch-for-pr leaves a clean PR alone", s => {
  s.given(ticket("ENG-1", { status: "in_review", optedIn: true, repo: "dispatch" }), pr("ENG-1", { ready: true }));
  s.expect([]);
});

scenario(rules, "relaunch-for-pr stops at nudge_limit relaunches for one head and the ticket is escalated", s => {
  s.given(ticket("ENG-1", { status: "in_review", optedIn: true, repo: "dispatch" }),
          pr("ENG-1", { head: "h1", problems: ["a failed check"] }));
  s.expect([["relaunch-for-pr", "herdr.launch", "ENG-1"]]);
  s.land("herdr.launch", "ENG-1");
  s.see(worker("ENG-1", "working"));
  s.see(worker("ENG-1", "none"));
  s.expect([["relaunch-for-pr", "herdr.launch", "ENG-1"]]);
  s.land("herdr.launch", "ENG-1");
  s.expect([["escalate-relaunch-limit", "linear.escalate", "ENG-1"]]);
});

scenario(rules, "relaunch-for-pr starts fresh for a new head", s => {
  s.given(ticket("ENG-1", { status: "in_review", optedIn: true, repo: "dispatch" }),
          pr("ENG-1", { head: "h1", problems: ["a failed check"] }));
  s.land("herdr.launch", "ENG-1");
  s.see(worker("ENG-1", "working"));
  s.see(worker("ENG-1", "none"));
  s.land("herdr.launch", "ENG-1");
  s.see(pr("ENG-1", { head: "h2" }));
  s.expect([["relaunch-for-pr", "herdr.launch", "ENG-1"]]);
});

scenario(rules, "relaunch-for-pr is held when the PR could not be read", s => {
  s.given(ticket("ENG-1", { status: "in_review", optedIn: true, repo: "dispatch" }), pr("ENG-1", { problems: ["a failed check"] }));
  s.unread("github:ENG-1");
  s.expect([]);
  s.expectHeld("relaunch-for-pr", "ENG-1");
});

scenario(rules, "relaunch-for-pr is not held back by the worker cap", s => {
  s.config({ maxInProgress: 1 });
  s.given(ticket("ENG-1", build), worker("ENG-1", "working"),
          ticket("ENG-2", { status: "in_review", optedIn: true, repo: "dispatch" }),
          pr("ENG-2", { problems: ["a failed check"] }));
  s.expect([["relaunch-for-pr", "herdr.launch", "ENG-2"]]);
});

scenario(rules, "stop-paused-worker closes the worker of a paused ticket once", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "working"));
  s.expect([]);
  s.see(ticket("ENG-1", { status: "backlog" }));
  s.expect([["stop-paused-worker", "herdr.close", "ENG-1"]]);
  s.land("herdr.close", "ENG-1");
  s.expect([]);
});

scenario(rules, "stop-paused-worker leaves a running ticket alone", s => {
  s.given(ticket("ENG-1", build), worker("ENG-1", "working"));
  s.expect([]);
});

scenario(rules, "stop-paused-worker closes a worker that comes back after a close", s => {
  s.given(ticket("ENG-1", { status: "todo", optedIn: true, repo: "dispatch" }), worker("ENG-1", "idle"));
  s.see(ticket("ENG-1", { status: "backlog" }));
  s.land("herdr.close", "ENG-1");
  s.see(worker("ENG-1", "none"));
  s.see(worker("ENG-1", "working"));
  s.expect([["stop-paused-worker", "herdr.close", "ENG-1"]]);
});

scenario(rules, "stop-paused-worker is held when herdr is unread", s => {
  s.given(ticket("ENG-1", { status: "backlog", optedIn: true, repo: "dispatch" }), worker("ENG-1", "working"));
  s.unread("herdr");
  s.expect([]);
});

scenario(rules, "relaunch-for-pr still answers a review thread after nudge_limit relaunches for the head", s => {
  s.given(ticket("ENG-1", { status: "in_review", optedIn: true, repo: "dispatch" }),
          pr("ENG-1", { head: "h1", problems: ["a failed check"] }));
  s.land("herdr.launch", "ENG-1");
  s.see(worker("ENG-1", "working"));
  s.see(worker("ENG-1", "none"));
  s.land("herdr.launch", "ENG-1");
  s.see(worker("ENG-1", "working"));
  s.see(worker("ENG-1", "none"), pr("ENG-1", { problems: [], threadAt: "11:00" }));
  s.expect([["relaunch-for-pr", "herdr.launch", "ENG-1"]]);
});

scenario(rules, "relaunch-for-pr does not start a worker in a repo the config lacks", s => {
  s.given(ticket("ENG-1", { status: "in_review", optedIn: true, repo: "elsewhere" }),
          pr("ENG-1", { problems: ["a failed check"] }));
  s.expect([]);
});

scenario(rules, "a launch that landed done resets the failure count, whatever its detail says", s => {
  s.given(ticket("ENG-1", build));
  s.land("herdr.launch", "ENG-1", { outcome: "failed", detail: "boom" });
  s.see(worker("ENG-1", "working"));
  s.see(worker("ENG-1", "none"));
  s.land("herdr.launch", "ENG-1");
  s.see(worker("ENG-1", "working"));
  s.see(worker("ENG-1", "none"));
  s.land("herdr.launch", "ENG-1", { outcome: "failed", detail: "boom" });
  s.see(worker("ENG-1", "working"));
  s.see(worker("ENG-1", "none"));
  s.expect([["launch-worker", "herdr.launch", "ENG-1"]]);
});

scenario(rules, "a refused launch that found the worker already running resets the failure count", s => {
  s.given(ticket("ENG-1", build));
  s.land("herdr.launch", "ENG-1", { outcome: "failed", detail: "boom" });
  s.see(worker("ENG-1", "working"));
  s.see(worker("ENG-1", "none"));
  s.land("herdr.launch", "ENG-1", { outcome: "refused", detail: "already_running" });
  s.see(worker("ENG-1", "working"));
  s.see(worker("ENG-1", "none"));
  s.land("herdr.launch", "ENG-1", { outcome: "failed", detail: "boom" });
  s.see(worker("ENG-1", "working"));
  s.see(worker("ENG-1", "none"));
  s.expect([["launch-worker", "herdr.launch", "ENG-1"]]);
});
