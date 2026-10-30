import { scenario, ticket, worker, pr, rulesFrom } from "dispatch/testing";

const rules = rulesFrom("./dispatch");

const stack = { parent: "ENG-2", forkPoint: "a".repeat(40) };
const review = { status: "in_review", optedIn: true, repo: "dispatch", stack };
const build = { status: "in_progress", optedIn: true, repo: "dispatch", stack };

// Nothing in the shipped rules reads a stack record, so a stacked ticket behaves as any other,
// and a record dispatch can't read holds none of them.

scenario(rules, "auto-merge merges a PR whose ticket has a stack record", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true, approved: true, head: "h1" }));
  s.expect([["auto-merge", "merge", "ENG-1"]]);
});

scenario(rules, "auto-merge still merges when the stack records can't be read", s => {
  s.config({ repos: { dispatch: { autoMerge: true } } });
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { ready: true, approved: true, head: "h1" }));
  s.unread("stack");
  s.expect([["auto-merge", "merge", "ENG-1"]]);
});

scenario(rules, "nudge-pr-needs-work still nudges when the stack records can't be read", s => {
  s.given(ticket("ENG-1", review), worker("ENG-1", "idle"), pr("ENG-1", { problems: ["a failed check"] }));
  s.unread("stack");
  s.expect([["nudge-pr-needs-work", "nudge", "ENG-1"]]);
});

scenario(rules, "launch-worker still launches when the stack records can't be read", s => {
  s.given(ticket("ENG-1", { ...build, stack: undefined }));
  s.unread("stack");
  s.expect([["launch-worker", "launch", "ENG-1"]]);
});

scenario(rules, "a ticket with any open blocker gets no worker", s => {
  s.given(ticket("ENG-1", { ...build, blockers: [{ id: "ENG-9", status: "in_review" }] }), ticket("ENG-9", { status: "in_review", optedIn: true, repo: "dispatch" }), worker("ENG-9", "idle"), pr("ENG-9", { ready: true }));
  s.expect([["note-blocker-hold", "comment", "ENG-1"], ["notify-ready-for-review", "notify", "ENG-9"]]);
});
