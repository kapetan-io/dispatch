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
