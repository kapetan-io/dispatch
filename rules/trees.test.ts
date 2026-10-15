import { scenario, ticket, worker, tree, rulesFrom } from "dispatch/testing";

const rules = rulesFrom("./dispatch");

const finished = { status: "done", optedIn: true, repo: "dispatch" };

scenario(rules, "teardown-clean-tree returns the tree of a finished ticket once its work is pushed", s => {
  s.given(ticket("ENG-1", finished), tree("/trees/ENG-1", { holder: "ENG-1" }));
  s.expect([["teardown-clean-tree", "treehouse.return", "ENG-1"]]);
  s.land("treehouse.return", "ENG-1");
  s.expect([]);
});

scenario(rules, "teardown-clean-tree leaves the tree of a ticket still being worked", s => {
  s.given(ticket("ENG-1", { status: "in_progress", optedIn: true, repo: "dispatch" }), worker("ENG-1", "working"));
  s.expect([]);
});

scenario(rules, "teardown-clean-tree keeps a tree with unpushed commits", s => {
  s.given(ticket("ENG-1", finished), tree("/trees/ENG-1", { holder: "ENG-1", pushed: false }));
  s.expect([["comment-kept-tree", "linear.comment", "ENG-1"]]);
});

scenario(rules, "teardown-clean-tree keeps a tree with changes to tracked files", s => {
  s.given(ticket("ENG-1", finished), tree("/trees/ENG-1", { holder: "ENG-1", trackedChanges: true }));
  s.expect([["comment-kept-tree", "linear.comment", "ENG-1"]]);
});

scenario(rules, "teardown-clean-tree returns a kept tree once the work is pushed", s => {
  s.given(ticket("ENG-1", finished), tree("/trees/ENG-1", { holder: "ENG-1", pushed: false }));
  s.expect([["comment-kept-tree", "linear.comment", "ENG-1"]]);
  s.see(tree("/trees/ENG-1", { pushed: true }));
  s.expect([["teardown-clean-tree", "treehouse.return", "ENG-1"]]);
});

scenario(rules, "teardown-clean-tree treats a canceled ticket as finished", s => {
  s.given(ticket("ENG-1", { status: "canceled" }), tree("/trees/ENG-1", { holder: "ENG-1" }));
  s.expect([["teardown-clean-tree", "treehouse.return", "ENG-1"]]);
});

scenario(rules, "teardown-clean-tree leaves a tree no one holds", s => {
  s.given(tree("/trees/free"));
  s.expect([]);
});

scenario(rules, "teardown-clean-tree is covered by a failed return only until it is tried again", s => {
  s.given(ticket("ENG-1", finished), tree("/trees/ENG-1", { holder: "ENG-1" }));
  s.land("treehouse.return", "ENG-1", { outcome: "failed", detail: "busy" });
  s.expect([["teardown-clean-tree", "treehouse.return", "ENG-1"]]);
  s.land("treehouse.return", "ENG-1", { outcome: "refused", detail: "work found" });
  s.expect([]);
});

scenario(rules, "stop-worker-on-kept-tree closes the worker of a kept tree once", s => {
  s.given(ticket("ENG-1", { status: "in_progress", optedIn: true, repo: "dispatch" }), worker("ENG-1", "working"));
  s.see(ticket("ENG-1", { status: "done" }), tree("/trees/ENG-1", { holder: "ENG-1", pushed: false }));
  s.expect([["stop-worker-on-kept-tree", "herdr.close", "ENG-1"], ["comment-kept-tree", "linear.comment", "ENG-1"]]);
  s.land("herdr.close", "ENG-1");
  s.expect([["comment-kept-tree", "linear.comment", "ENG-1"]]);
});

scenario(rules, "stop-worker-on-kept-tree leaves a clean tree to teardown", s => {
  s.given(ticket("ENG-1", { status: "in_progress", optedIn: true, repo: "dispatch" }), worker("ENG-1", "working"));
  s.see(ticket("ENG-1", { status: "done" }));
  s.expect([["teardown-clean-tree", "treehouse.return", "ENG-1"]]);
});

scenario(rules, "stop-worker-on-kept-tree has nothing to close when no worker is there", s => {
  s.given(ticket("ENG-1", finished), tree("/trees/ENG-1", { holder: "ENG-1", pushed: false }));
  s.expect([["comment-kept-tree", "linear.comment", "ENG-1"]]);
});

scenario(rules, "comment-kept-tree comments once per lease", s => {
  s.given(ticket("ENG-1", finished), tree("/trees/ENG-1", { holder: "ENG-1", pushed: false }));
  s.expect([["comment-kept-tree", "linear.comment", "ENG-1"]]);
  s.land("linear.comment", "ENG-1");
  s.resync();
  s.expect([]);
});

scenario(rules, "comment-kept-tree comments again after a failed comment", s => {
  s.given(ticket("ENG-1", finished), tree("/trees/ENG-1", { holder: "ENG-1", pushed: false }));
  s.land("linear.comment", "ENG-1", { outcome: "failed", detail: "network" });
  s.expect([["comment-kept-tree", "linear.comment", "ENG-1"]]);
});

scenario(rules, "comment-kept-tree stays silent for a ticket still in progress", s => {
  s.given(ticket("ENG-1", { status: "in_progress", optedIn: true, repo: "dispatch" }), worker("ENG-1", "working"),
          tree("/trees/ENG-1", { holder: "ENG-1", pushed: false }));
  s.expect([]);
});

scenario(rules, "teardown-clean-tree returns every tree a finished ticket holds, not only the first", s => {
  s.given(ticket("ENG-1", { status: "done" }),
          tree("/trees/a", { holder: "ENG-1", pushed: true }), tree("/trees/b", { holder: "ENG-1", pushed: true }));
  s.expect([["teardown-clean-tree", "treehouse.return", "ENG-1"], ["teardown-clean-tree", "treehouse.return", "ENG-1"]]);
  s.land("treehouse.return", "ENG-1");
  s.expect([["teardown-clean-tree", "treehouse.return", "ENG-1"]]);
});

scenario(rules, "a tree nobody holds is left alone and a rule asking about its holder is not thrown off", s => {
  s.given(tree("/trees/free", {}));
  s.expect([]);
});
