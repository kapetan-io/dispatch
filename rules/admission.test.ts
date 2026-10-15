import { scenario, ticket, worker, pool, rulesFrom } from "dispatch/testing";

const rules = rulesFrom("./dispatch");

const waiting = { status: "in_progress", optedIn: true, repo: "dispatch" };

scenario(rules, "the cap lets the oldest ticket launch first and the rest wait", s => {
  s.config({ maxInProgress: 1 });
  s.given(ticket("ENG-1", { ...waiting, createdAt: "09:00" }),
          ticket("ENG-2", { ...waiting, createdAt: "08:00" }));
  s.expect([["launch-worker", "herdr.launch", "ENG-2"]]);
  s.expectWaiting("launch-worker", "ENG-1");
});

scenario(rules, "the cap counts each ticket with a live worker", s => {
  s.config({ maxInProgress: 2 });
  s.given(ticket("ENG-1", waiting), worker("ENG-1", "working"),
          ticket("ENG-2", { ...waiting, createdAt: "08:00" }),
          ticket("ENG-3", { ...waiting, createdAt: "09:00" }));
  s.expect([["launch-worker", "herdr.launch", "ENG-2"]]);
  s.expectWaiting("launch-worker", "ENG-3");
});

scenario(rules, "the cap counts an idle and a blocked worker", s => {
  s.config({ maxInProgress: 2 });
  s.given(ticket("ENG-1", waiting), worker("ENG-1", "idle"),
          ticket("ENG-2", waiting), worker("ENG-2", "blocked"),
          ticket("ENG-3", waiting));
  s.expect([["notify-blocked", "notify.send", "ENG-2"], ["nudge-stall", "herdr.nudge", "ENG-1"]]);
  s.expectWaiting("launch-worker", "ENG-3");
});

scenario(rules, "a full cap launches nothing at all", s => {
  s.config({ maxInProgress: 0 });
  s.given(ticket("ENG-1", waiting));
  s.expect([]);
  s.expectWaiting("launch-worker", "ENG-1");
});

scenario(rules, "a ticket with an unknown worker takes a slot", s => {
  s.config({ maxInProgress: 1 });
  s.given(pool("dispatch"), pool("other"),
          ticket("ENG-1", { status: "in_progress", optedIn: true, repo: "other" }),
          ticket("ENG-2", waiting));
  s.unread("treehouse:other");
  s.expect([]);
  s.expectHeld("launch-worker", "ENG-1");
  s.expectWaiting("launch-worker", "ENG-2");
});

scenario(rules, "a waiting launch goes once a slot frees", s => {
  s.config({ maxInProgress: 1 });
  s.given(ticket("ENG-1", waiting), worker("ENG-1", "working"), ticket("ENG-2", waiting));
  s.expect([]);
  s.expectWaiting("launch-worker", "ENG-2");
  s.see(ticket("ENG-1", { status: "done" }), worker("ENG-1", "none"));
  s.expect([["launch-worker", "herdr.launch", "ENG-2"]]);
});

scenario(rules, "promote-child waits for a slot like a launch", s => {
  s.config({ maxInProgress: 1 });
  s.given(ticket("ENG-9", waiting), worker("ENG-9", "working"),
          ticket("ENG-1", { status: "in_progress", optedIn: true, repo: "dispatch" }),
          ticket("ENG-2", { status: "todo", optedIn: true, repo: "dispatch", parent: "ENG-1" }));
  s.expect([]);
  s.expectWaiting("promote-child", "ENG-2");
  s.see(ticket("ENG-9", { status: "done" }), worker("ENG-9", "none"));
  s.expect([["promote-child", "linear.move", "ENG-2"]]);
});

scenario(rules, "promote-child shares the cap with launches and the oldest ticket goes first", s => {
  s.config({ maxInProgress: 1 });
  s.given(ticket("ENG-1", { status: "in_progress", optedIn: true, repo: "dispatch", createdAt: "07:00" }), worker("ENG-1", "none"),
          ticket("ENG-2", { status: "todo", optedIn: true, repo: "dispatch", parent: "ENG-1", createdAt: "08:00" }),
          ticket("ENG-3", { ...waiting, createdAt: "09:00" }));
  s.expect([["promote-child", "linear.move", "ENG-2"]]);
  s.expectWaiting("launch-worker", "ENG-3");
});

scenario(rules, "a child that is itself a parent takes no slot", s => {
  s.config({ maxInProgress: 0 });
  s.given(ticket("ENG-1", waiting),
          ticket("ENG-2", { status: "todo", optedIn: true, repo: "dispatch", parent: "ENG-1", hasChildren: true }));
  s.expect([["promote-child", "linear.move", "ENG-2"]]);
});

scenario(rules, "escalations and notices are not held by the cap", s => {
  s.config({ maxInProgress: 0 });
  s.given(ticket("ENG-1", { status: "in_progress", optedIn: true, repo: "gone" }),
          ticket("ENG-2", { status: "needs_human" }));
  s.expect([["escalate-missing-repo", "linear.escalate", "ENG-1"], ["notify-needs-human", "notify.send", "ENG-2"]]);
});
