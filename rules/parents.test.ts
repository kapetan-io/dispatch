import { scenario, ticket, worker, tree, rulesFrom } from "dispatch/testing";

const rules = rulesFrom("./dispatch");

const parent = { status: "in_progress", optedIn: true, repo: "dispatch" };
const child = { status: "todo", optedIn: true, repo: "dispatch", parent: "ENG-1" };

scenario(rules, "close-parent moves a parent whose children are all finished", s => {
  s.given(ticket("ENG-1", parent),
          ticket("ENG-2", { ...child, status: "done" }),
          ticket("ENG-3", { ...child, status: "canceled" }));
  s.expect([["close-parent", "linear.move", "ENG-1", "close_parent"]]);
  s.land("linear.move", "ENG-1");
  s.expect([]);
});

scenario(rules, "close-parent waits while one child is unfinished", s => {
  s.given(ticket("ENG-1", parent),
          ticket("ENG-2", { ...child, status: "done" }),
          ticket("ENG-3", { ...child, status: "in_progress" }), worker("ENG-3", "working"));
  s.expect([]);
  s.see(ticket("ENG-3", { status: "done" }));
  s.expect([["close-parent", "linear.move", "ENG-1"], ["teardown-clean-tree", "treehouse.return", "ENG-3"]]);
});

scenario(rules, "close-parent never closes a parent with no children", s => {
  s.given(ticket("ENG-1", { ...parent, hasChildren: true }));
  s.expect([]);
});

scenario(rules, "close-parent leaves a design parent to its designer", s => {
  s.given(ticket("ENG-1", { ...parent, labels: ["design"] }),
          ticket("ENG-2", { ...child, status: "done" }));
  s.expect([]);
});

scenario(rules, "close-parent does not move a parent that is not In Progress", s => {
  s.given(ticket("ENG-1", { ...parent, status: "in_review" }),
          ticket("ENG-2", { ...child, status: "done" }));
  s.expect([]);
});

scenario(rules, "a refused close is not tried again for the same stretch", s => {
  s.given(ticket("ENG-1", parent), ticket("ENG-2", { ...child, status: "done" }));
  s.land("linear.move", "ENG-1", { outcome: "refused", detail: "no" });
  s.expect([]);
});

scenario(rules, "a failed close is tried again", s => {
  s.given(ticket("ENG-1", parent), ticket("ENG-2", { ...child, status: "done" }));
  s.land("linear.move", "ENG-1", { outcome: "failed", detail: "network" });
  s.expect([["close-parent", "linear.move", "ENG-1"]]);
});

scenario(rules, "promote-child starts a Todo child of an In Progress parent", s => {
  s.given(ticket("ENG-1", parent), ticket("ENG-2", child));
  s.expect([["promote-child", "linear.move", "ENG-2", "in_progress"]]);
  s.land("linear.move", "ENG-2");
  s.expect([]);
});

scenario(rules, "promote-child waits for the blocker to be done", s => {
  s.given(ticket("ENG-1", parent),
          ticket("ENG-2", { ...child, blockers: [{ id: "ENG-8", status: "in_progress" }] }));
  s.expect([]);
  s.see(ticket("ENG-2", { blockers: [{ id: "ENG-8", status: "done" }] }));
  s.expect([["promote-child", "linear.move", "ENG-2"]]);
});

scenario(rules, "promote-child holds for a child whose blockers were cut short", s => {
  s.given(ticket("ENG-1", parent), ticket("ENG-2", { ...child, blockersTruncated: true }));
  s.expect([]);
  s.expectHeld("promote-child", "ENG-2");
});

scenario(rules, "promote-child holds for a child whose labels were cut short", s => {
  s.given(ticket("ENG-1", parent), ticket("ENG-2", { ...child, labelsTruncated: true }));
  s.expect([]);
  s.expectHeld("promote-child", "ENG-2");
});

scenario(rules, "promote-child skips a child not opted in", s => {
  s.given(ticket("ENG-1", parent), ticket("ENG-2", { ...child, optedIn: false }));
  s.expect([]);
});

scenario(rules, "promote-child skips a design child", s => {
  s.given(ticket("ENG-1", parent), ticket("ENG-2", { ...child, labels: ["design"] }));
  s.expect([]);
});

scenario(rules, "promote-child skips a child of a design parent", s => {
  s.given(ticket("ENG-1", { ...parent, labels: ["design"] }), ticket("ENG-2", child));
  s.expect([]);
});

scenario(rules, "promote-child skips a child of a parent that is not In Progress", s => {
  s.given(ticket("ENG-1", { ...parent, status: "todo" }), ticket("ENG-2", child));
  s.expect([]);
});

scenario(rules, "promote-child leaves a child that still holds a tree", s => {
  s.given(ticket("ENG-1", parent), ticket("ENG-2", child), tree("/trees/ENG-2", { holder: "ENG-2" }));
  s.expect([]);
});

scenario(rules, "promote-child holds for a child whose pool could not be read", s => {
  s.given(ticket("ENG-1", parent), ticket("ENG-2", child));
  s.unread("treehouse");
  s.expect([]);
  s.expectHeld("promote-child", "ENG-2");
});

scenario(rules, "escalate-abandoned-blocker-child escalates a child behind an abandoned blocker", s => {
  s.given(ticket("ENG-1", parent),
          ticket("ENG-2", { ...child, blockers: [{ id: "ENG-8", status: "canceled", abandoned: true }] }));
  s.expect([["escalate-abandoned-blocker-child", "linear.escalate", "ENG-2"]]);
  s.land("linear.escalate", "ENG-2");
  s.expect([]);
});

scenario(rules, "escalate-abandoned-blocker-child ignores a blocker that is merely Done", s => {
  s.given(ticket("ENG-1", parent),
          ticket("ENG-2", { ...child, blockers: [{ id: "ENG-8", status: "done" }] }));
  s.expect([["promote-child", "linear.move", "ENG-2"]]);
});

scenario(rules, "escalate-abandoned-blocker-child sees an abandoned blocker beside cut-short relations", s => {
  s.given(ticket("ENG-1", parent),
          ticket("ENG-2", { ...child, blockersTruncated: true, blockers: [{ id: "ENG-8", status: "canceled", abandoned: true }] }));
  s.expect([["escalate-abandoned-blocker-child", "linear.escalate", "ENG-2"]]);
});

scenario(rules, "a child started by promote-child gets a worker", s => {
  s.given(ticket("ENG-1", parent), ticket("ENG-2", child));
  s.land("linear.move", "ENG-2");
  s.see(ticket("ENG-2", { status: "in_progress" }));
  s.expect([["launch-worker", "herdr.launch", "ENG-2"]]);
});
