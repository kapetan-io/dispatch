import { scenario, ticket, worker, tree, rulesFrom } from "dispatch/testing";

const rules = rulesFrom("./dispatch");

const finished = { status: "done", optedIn: true, repo: "dispatch" };
const working = { status: "in_progress", optedIn: true, repo: "dispatch" };

scenario(rules, "teardown-clean-tree returns the tree of a finished ticket", s => {
  s.given(ticket("ENG-1", finished), tree("/trees/ENG-1", { holder: "ENG-1" }));
  s.expect([["teardown-clean-tree", "return", "ENG-1"]]);
  s.land("return", "ENG-1");
  s.expect([]);
});

scenario(rules, "teardown-clean-tree treats a canceled ticket as finished", s => {
  s.given(ticket("ENG-1", { status: "canceled" }), tree("/trees/ENG-1", { holder: "ENG-1" }));
  s.expect([["teardown-clean-tree", "return", "ENG-1"]]);
});

scenario(rules, "teardown-clean-tree leaves a tree no one holds", s => {
  s.given(tree("/trees/free"));
  s.expect([]);
});

scenario(rules, "teardown-clean-tree returns every tree a finished ticket holds, not only the first", s => {
  s.given(ticket("ENG-1", { status: "done" }), tree("/trees/a", { holder: "ENG-1" }), tree("/trees/b", { holder: "ENG-1" }));
  s.expect([["teardown-clean-tree", "return", "ENG-1"], ["teardown-clean-tree", "return", "ENG-1"]]);
  s.land("return", "ENG-1");
  s.expect([["teardown-clean-tree", "return", "ENG-1"]]);
});

scenario(rules, "teardown-clean-tree leaves the tree of a ticket still being worked", s => {
  s.given(ticket("ENG-1", working), worker("ENG-1", "working"), tree("/trees/ENG-1", { holder: "ENG-1" }));
  s.expect([]);
});

scenario(rules, "teardown-clean-tree keeps the tree of an In Progress ticket whose worker is gone, for the relaunch", s => {
  s.given(ticket("ENG-1", { ...working, blockers: undefined }), tree("/trees/ENG-1", { holder: "ENG-1" }));
  s.expect([["launch-worker", "launch", "ENG-1"]]);
});

scenario(rules, "teardown-clean-tree keeps the tree of an In Review ticket whose worker is gone", s => {
  s.given(ticket("ENG-1", { status: "in_review", optedIn: true, repo: "dispatch" }), tree("/trees/ENG-1", { holder: "ENG-1", trackedChanges: true }));
  s.expect([]);
});

scenario(rules, "teardown-clean-tree returns the tree of a Backlog ticket", s => {
  s.given(ticket("ENG-1", { status: "backlog", optedIn: true, repo: "dispatch" }), tree("/trees/ENG-1", { holder: "ENG-1" }));
  s.expect([["teardown-clean-tree", "return", "ENG-1"]]);
});

scenario(rules, "teardown-clean-tree returns the tree of a Todo ticket that has no parent", s => {
  s.given(ticket("ENG-1", { status: "todo", optedIn: true, repo: "dispatch" }), tree("/trees/ENG-1", { holder: "ENG-1" }));
  s.expect([["teardown-clean-tree", "return", "ENG-1"]]);
});

scenario(rules, "teardown-clean-tree keeps the tree of a Todo child of an In Progress parent, which marks it paused on purpose", s => {
  s.given(ticket("ENG-1", { ...working, hasChildren: true }),
          ticket("ENG-2", { status: "todo", optedIn: true, repo: "dispatch", parent: "ENG-1" }),
          tree("/trees/ENG-2", { holder: "ENG-2" }));
  s.expect([]);
});

scenario(rules, "teardown-clean-tree returns the tree of a Todo child once its parent is no longer In Progress", s => {
  s.given(ticket("ENG-1", { status: "backlog", hasChildren: true }),
          ticket("ENG-2", { status: "todo", optedIn: true, repo: "dispatch", parent: "ENG-1" }),
          tree("/trees/ENG-2", { holder: "ENG-2" }));
  s.expect([["teardown-clean-tree", "return", "ENG-2"]]);
});

scenario(rules, "teardown-clean-tree returns the tree of a Needs Human ticket whose worker is gone", s => {
  s.given(ticket("ENG-1", { status: "needs_human", optedIn: true, repo: "dispatch" }), tree("/trees/ENG-1", { holder: "ENG-1" }));
  s.expect([["notify-needs-human", "notify", "ENG-1"], ["teardown-clean-tree", "return", "ENG-1"]]);
});

scenario(rules, "teardown-clean-tree keeps the tree of a Needs Human ticket while its worker waits on you", s => {
  s.given(ticket("ENG-1", { status: "needs_human", optedIn: true, repo: "dispatch" }), worker("ENG-1", "blocked"),
          tree("/trees/ENG-1", { holder: "ENG-1", trackedChanges: true }));
  s.advance(5 * 60 * 1000);
  s.expect([["notify-needs-human", "notify", "ENG-1"]]);
});

scenario(rules, "teardown-clean-tree returns an unpushed tree without a comment, because the branch keeps the commits", s => {
  s.given(ticket("ENG-1", finished), tree("/trees/ENG-1", { holder: "ENG-1", pushed: false }));
  s.expect([["teardown-clean-tree", "return", "ENG-1"]]);
});

scenario(rules, "teardown-clean-tree waits for the discard comment before it returns a tree with uncommitted changes", s => {
  s.given(ticket("ENG-1", finished), tree("/trees/ENG-1", { holder: "ENG-1", trackedChanges: true }));
  s.expect([["comment-discarded-work", "comment", "ENG-1"]]);
  s.land("comment", "ENG-1");
  s.expect([["teardown-clean-tree", "return", "ENG-1"]]);
  s.land("return", "ENG-1");
  s.expect([]);
});

scenario(rules, "comment-discarded-work comments for untracked files too", s => {
  s.given(ticket("ENG-1", finished), tree("/trees/ENG-1", { holder: "ENG-1", untracked: ["notes.txt"] }));
  s.expect([["comment-discarded-work", "comment", "ENG-1"]]);
  s.land("comment", "ENG-1");
  s.expect([["teardown-clean-tree", "return", "ENG-1"]]);
});

scenario(rules, "comment-discarded-work comments on a Backlog ticket's dirty tree", s => {
  s.given(ticket("ENG-1", { status: "backlog", optedIn: true, repo: "dispatch" }), tree("/trees/ENG-1", { holder: "ENG-1", trackedChanges: true }));
  s.expect([["comment-discarded-work", "comment", "ENG-1"]]);
});

scenario(rules, "comment-discarded-work tries again after a failed comment and the tree stays", s => {
  s.given(ticket("ENG-1", finished), tree("/trees/ENG-1", { holder: "ENG-1", trackedChanges: true }));
  s.land("comment", "ENG-1", { outcome: "failed", detail: "network" });
  s.expect([["comment-discarded-work", "comment", "ENG-1"]]);
});

scenario(rules, "comment-discarded-work comments again about work that arrived after the comment", s => {
  s.given(ticket("ENG-1", finished), tree("/trees/ENG-1", { holder: "ENG-1", untracked: ["a.txt"] }));
  s.land("comment", "ENG-1");
  s.see(tree("/trees/ENG-1", { untracked: ["a.txt", "b.txt"] }));
  s.expect([["comment-discarded-work", "comment", "ENG-1"]]);
});

scenario(rules, "comment-discarded-work stays silent for a tree with nothing to discard", s => {
  s.given(ticket("ENG-1", finished), tree("/trees/ENG-1", { holder: "ENG-1" }));
  s.expect([["teardown-clean-tree", "return", "ENG-1"]]);
});

scenario(rules, "comment-discarded-work stays silent while the ticket is still being worked", s => {
  s.given(ticket("ENG-1", working), worker("ENG-1", "working"), tree("/trees/ENG-1", { holder: "ENG-1", trackedChanges: true }));
  s.expect([]);
});

scenario(rules, "stop-worker-on-kept-tree closes the worker of a finished ticket that left work, once", s => {
  s.given(ticket("ENG-1", working), worker("ENG-1", "working"));
  s.see(ticket("ENG-1", { status: "done" }), tree("/trees/ENG-1", { holder: "ENG-1", trackedChanges: true }));
  s.expect([["stop-worker-on-kept-tree", "close", "ENG-1"]]);
  s.land("close", "ENG-1");
  s.expect([]);
});

scenario(rules, "teardown-clean-tree returns a finished ticket's clean tree while its worker still runs", s => {
  s.given(ticket("ENG-1", working), worker("ENG-1", "working"));
  s.see(ticket("ENG-1", { status: "done" }), tree("/trees/ENG-1", { holder: "ENG-1" }));
  s.expect([["teardown-clean-tree", "return", "ENG-1"]]);
});

scenario(rules, "comment-kept-tree says why a return was refused and comments once per lease", s => {
  s.given(ticket("ENG-1", finished), tree("/trees/ENG-1", { holder: "ENG-1" }));
  s.land("return", "ENG-1", { outcome: "refused", detail: "untracked nested/ in /trees/ENG-1 survived git clean" });
  s.expect([["comment-kept-tree", "comment", "ENG-1"]]);
  s.land("comment", "ENG-1");
  s.resync();
  s.expect([]);
});

scenario(rules, "teardown-clean-tree tries a refused return again once the nested repository is gone", s => {
  s.given(ticket("ENG-1", finished), tree("/trees/ENG-1", { holder: "ENG-1", untracked: ["nested/"] }));
  s.land("comment", "ENG-1");
  s.land("return", "ENG-1", { outcome: "refused", detail: "untracked nested/ in /trees/ENG-1 survived git clean" });
  s.land("comment", "ENG-1");
  s.expect([]);
  s.see(tree("/trees/ENG-1", { untracked: [] }));
  s.expect([["teardown-clean-tree", "return", "ENG-1"]]);
});

scenario(rules, "teardown-clean-tree is covered by a failed return only until it is tried again", s => {
  s.given(ticket("ENG-1", finished), tree("/trees/ENG-1", { holder: "ENG-1" }));
  s.land("return", "ENG-1", { outcome: "failed", detail: "busy" });
  s.expect([["teardown-clean-tree", "return", "ENG-1"]]);
});
