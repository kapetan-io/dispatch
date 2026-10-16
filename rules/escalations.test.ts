import { scenario, ticket, worker, pr, rulesFrom } from "dispatch/testing";

const rules = rulesFrom("./dispatch");

const build = { status: "in_progress", optedIn: true, repo: "dispatch" };
const review = { status: "in_review", optedIn: true, repo: "dispatch" };

scenario(rules, "escalate-launch-failures escalates after launch_failure_limit failures", s => {
  s.given(ticket("ENG-1", build));
  s.land("herdr.launch", "ENG-1", { outcome: "failed", detail: "no claude" });
  s.expect([["launch-worker", "herdr.launch", "ENG-1"]]);
  s.land("herdr.launch", "ENG-1", { outcome: "failed", detail: "no claude" });
  s.expect([["escalate-launch-failures", "linear.escalate", "ENG-1"]]);
  s.land("linear.escalate", "ENG-1");
  s.see(ticket("ENG-1", { status: "needs_human" }));
  s.expect([["notify-needs-human", "notify.send", "ENG-1"]]);
});

scenario(rules, "escalate-launch-failures counts In Review relaunches too", s => {
  s.given(ticket("ENG-1", review), pr("ENG-1", { problems: ["a failed check"] }));
  s.land("herdr.launch", "ENG-1", { outcome: "failed", detail: "x" });
  s.land("herdr.launch", "ENG-1", { outcome: "failed", detail: "x" });
  s.expect([["escalate-launch-failures", "linear.escalate", "ENG-1"]]);
});

scenario(rules, "escalate-launch-failures starts over once the ticket is moved back", s => {
  s.given(ticket("ENG-1", build));
  s.land("herdr.launch", "ENG-1", { outcome: "failed", detail: "x" });
  s.land("herdr.launch", "ENG-1", { outcome: "failed", detail: "x" });
  s.land("linear.escalate", "ENG-1");
  s.see(ticket("ENG-1", { status: "needs_human" }));
  s.see(ticket("ENG-1", { status: "in_progress" }));
  s.expect([["launch-worker", "herdr.launch", "ENG-1"]]);
});

scenario(rules, "escalate-relaunch-limit escalates a PR relaunched nudge_limit times for one head", s => {
  s.given(ticket("ENG-1", review), pr("ENG-1", { head: "h1", problems: ["a failed check"] }));
  s.land("herdr.launch", "ENG-1");
  s.see(worker("ENG-1", "working"));
  s.see(worker("ENG-1", "none"));
  s.land("herdr.launch", "ENG-1");
  s.expect([["escalate-relaunch-limit", "linear.escalate", "ENG-1"]]);
  s.land("linear.escalate", "ENG-1");
  s.expect([]);
});

scenario(rules, "escalate-relaunch-limit stays quiet while the worker is alive", s => {
  s.given(ticket("ENG-1", review), pr("ENG-1", { problems: ["a failed check"] }), worker("ENG-1", "working"));
  s.expect([]);
});

scenario(rules, "escalate-relaunch-limit is held when the PR could not be read", s => {
  s.given(ticket("ENG-1", review), pr("ENG-1", { problems: ["a failed check"] }));
  s.unread("github:ENG-1");
  s.expect([]);
  s.expectHeld("escalate-relaunch-limit", "ENG-1");
});

scenario(rules, "escalate-truncated-labels escalates a ticket with more labels than are read", s => {
  s.given(ticket("ENG-1", { status: "in_progress", labelsTruncated: true }));
  s.expect([["escalate-truncated-labels", "linear.escalate", "ENG-1"]]);
  s.land("linear.escalate", "ENG-1");
  s.expect([]);
});

scenario(rules, "escalate-truncated-labels leaves a ticket whose labels were all read", s => {
  s.given(ticket("ENG-1", { status: "in_progress", optedIn: true, repo: "dispatch" }), worker("ENG-1", "working"));
  s.expect([]);
});

scenario(rules, "escalate-truncated-labels waits while a worker is running", s => {
  s.given(ticket("ENG-1", { status: "in_progress", labelsTruncated: true }), worker("ENG-1", "working"));
  s.expect([]);
});

scenario(rules, "escalate-truncated-labels holds for an unread herdr", s => {
  s.given(ticket("ENG-1", { status: "in_progress", labelsTruncated: true }));
  s.unread("herdr");
  s.expect([]);
  s.expectHeld("escalate-truncated-labels", "ENG-1", "herdr unread");
});

scenario(rules, "escalate-missing-repo escalates a ticket with no repo label", s => {
  s.given(ticket("ENG-1", { status: "in_progress", optedIn: true }));
  s.expect([["escalate-missing-repo", "linear.escalate", "ENG-1"]]);
});

scenario(rules, "escalate-missing-repo escalates a ticket whose repo is not configured", s => {
  s.given(ticket("ENG-1", { status: "in_progress", optedIn: true, repo: "nowhere" }));
  s.expect([["escalate-missing-repo", "linear.escalate", "ENG-1"]]);
  s.land("linear.escalate", "ENG-1");
  s.expect([]);
});

scenario(rules, "escalate-missing-repo leaves a ticket that is not opted in", s => {
  s.given(ticket("ENG-1", { status: "in_progress", repo: "nowhere" }));
  s.expect([]);
});

scenario(rules, "escalate-missing-repo escalates again after a move back", s => {
  s.given(ticket("ENG-1", { status: "in_progress", optedIn: true }));
  s.land("linear.escalate", "ENG-1");
  s.see(ticket("ENG-1", { status: "needs_human" }));
  s.see(ticket("ENG-1", { status: "in_progress" }));
  s.expect([["escalate-missing-repo", "linear.escalate", "ENG-1"]]);
});

scenario(rules, "escalate-missing-repo does not hold a refused escalation to run again", s => {
  s.given(ticket("ENG-1", { status: "in_progress", optedIn: true }));
  s.land("linear.escalate", "ENG-1", { outcome: "refused", detail: "linear said no" });
  s.expect([]);
});

scenario(rules, "escalate-missing-repo retries a failed escalation", s => {
  s.given(ticket("ENG-1", { status: "in_progress", optedIn: true }));
  s.land("linear.escalate", "ENG-1", { outcome: "failed", detail: "network" });
  s.expect([["escalate-missing-repo", "linear.escalate", "ENG-1"]]);
});

scenario(rules, "escalate-abandoned-blocker escalates a ticket behind a canceled blocker", s => {
  s.given(ticket("ENG-1", { ...build, blockers: [{ id: "ENG-8", status: "canceled", abandoned: true }] }));
  s.expect([["escalate-abandoned-blocker", "linear.escalate", "ENG-1"]]);
  s.land("linear.escalate", "ENG-1");
  s.expect([]);
});

scenario(rules, "escalate-abandoned-blocker sees the abandoned blocker beside truncated relations", s => {
  s.given(ticket("ENG-1", { ...build, blockersTruncated: true, blockers: [{ id: "ENG-8", status: "canceled", abandoned: true }] }));
  s.expect([["escalate-abandoned-blocker", "linear.escalate", "ENG-1"]]);
});

scenario(rules, "escalate-abandoned-blocker leaves a ticket with a Done blocker", s => {
  s.given(ticket("ENG-1", { ...build, blockers: [{ id: "ENG-8", status: "done" }] }));
  s.expect([["launch-worker", "herdr.launch", "ENG-1"]]);
});

scenario(rules, "escalate-abandoned-blocker keys on the blocker so a second abandoned one escalates again", s => {
  s.given(ticket("ENG-1", { ...build, blockers: [{ id: "ENG-8", status: "canceled", abandoned: true }] }));
  s.land("linear.escalate", "ENG-1");
  s.see(ticket("ENG-1", { blockers: [{ id: "ENG-7", status: "canceled", abandoned: true }, { id: "ENG-8", status: "canceled", abandoned: true }] }));
  s.expect([["escalate-abandoned-blocker", "linear.escalate", "ENG-1"]]);
});

scenario(rules, "escalate-abandoned-blocker waits while a worker is running", s => {
  s.given(ticket("ENG-1", { ...build, blockers: [{ id: "ENG-8", status: "canceled", abandoned: true }] }), worker("ENG-1", "working"));
  s.expect([]);
});

scenario(rules, "note-blocker-hold comments once for the open blockers", s => {
  s.given(ticket("ENG-1", { ...build, blockers: [{ id: "ENG-8", status: "in_progress" }] }));
  s.expect([["note-blocker-hold", "linear.comment", "ENG-1"]]);
  s.land("linear.comment", "ENG-1");
  s.resync();
  s.expect([]);
});

scenario(rules, "note-blocker-hold comments again when the open blockers change", s => {
  s.given(ticket("ENG-1", { ...build, blockers: [{ id: "ENG-8", status: "in_progress" }] }));
  s.land("linear.comment", "ENG-1");
  s.see(ticket("ENG-1", { blockers: [{ id: "ENG-8", status: "in_progress" }, { id: "ENG-9", status: "todo" }] }));
  s.expect([["note-blocker-hold", "linear.comment", "ENG-1"]]);
});

scenario(rules, "note-blocker-hold comments again when a ticket is blocked anew after waiting", s => {
  s.given(ticket("ENG-1", { ...build, blockers: [{ id: "ENG-8", status: "in_progress" }] }));
  s.land("linear.comment", "ENG-1");
  s.see(ticket("ENG-1", { blockers: [{ id: "ENG-8", status: "done" }] }));
  s.land("herdr.launch", "ENG-1");
  s.see(worker("ENG-1", "working"));
  s.see(worker("ENG-1", "none"));
  s.see(ticket("ENG-1", { blockers: [{ id: "ENG-8", status: "in_progress" }] }));
  s.expect([["note-blocker-hold", "linear.comment", "ENG-1"]]);
});

scenario(rules, "note-blocker-hold says relations were not read when blockers are cut short", s => {
  s.given(ticket("ENG-1", { ...build, blockersTruncated: true }));
  s.expect([["note-blocker-hold", "linear.comment", "ENG-1"]]);
  s.expectHeld("launch-worker", "ENG-1");
});

scenario(rules, "note-blocker-hold stays quiet for an abandoned blocker, which escalates", s => {
  s.given(ticket("ENG-1", { ...build, blockers: [{ id: "ENG-8", status: "canceled", abandoned: true }] }));
  s.expect([["escalate-abandoned-blocker", "linear.escalate", "ENG-1"]]);
});

scenario(rules, "note-blocker-hold stays quiet once the worker is running", s => {
  s.given(ticket("ENG-1", { ...build, blockers: [{ id: "ENG-8", status: "in_progress" }] }), worker("ENG-1", "working"));
  s.expect([]);
});

scenario(rules, "escalate-truncated-labels leaves a parent alone, because a parent never gets a worker", s => {
  s.given(ticket("ENG-1", { status: "in_progress", optedIn: true, repo: "dispatch", labelsTruncated: true }),
          ticket("ENG-2", { parent: "ENG-1", status: "todo" }));
  s.expect([]);
});

scenario(rules, "escalating a design ticket announces it, because notify-needs-human leaves design tickets alone", s => {
  s.given(ticket("ENG-1", { ...build, labels: ["design"] }));
  s.land("herdr.launch", "ENG-1", { outcome: "failed", detail: "x" });
  s.land("herdr.launch", "ENG-1", { outcome: "failed", detail: "x" });
  s.expect([["escalate-launch-failures", "linear.escalate", "ENG-1"]]);   // the notice waits for the move
  s.land("linear.escalate", "ENG-1");
  s.see(ticket("ENG-1", { status: "needs_human" }));
  s.expect([]);   // the loop runs the notice from the escalate receipt, so the rule wants nothing more
});
