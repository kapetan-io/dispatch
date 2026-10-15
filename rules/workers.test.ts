import { scenario, ticket, worker, pool, pr, comment, rulesFrom } from "dispatch/testing";

const rules = rulesFrom("./dispatch");

// A scenario sets up the world, then each step is an event. s.see is a read that
// differs, s.land is an action's receipt, s.restart writes history, loads the world and
// the ledger back through the real read-back, and re-reads every source. s.expect checks
// the actions the cycle after it starts.

scenario(rules, "an unread pool holds the launch", s => {
  s.given(ticket("ENG-1", { status: "in_progress", optedIn: true, repo: "dispatch" }), worker("ENG-1", "working"));
  s.unread("herdr");
  s.resync();
  s.expect([]);
  s.expectHeld("launch-worker", "ENG-1", "herdr unread");
});

scenario(rules, "a crashed worker is relaunched", s => {
  s.given(ticket("ENG-1", { status: "in_progress", optedIn: true, repo: "dispatch" }), worker("ENG-1", "working"));
  s.see(worker("ENG-1", "none"));
  s.expect([["launch-worker", "herdr.launch", "ENG-1"]]);
  s.land("herdr.launch", "ENG-1");
  s.expect([]);                                  // one launch per stretch with no worker
});

scenario(rules, "a free slot goes to the oldest waiting ticket", s => {
  s.config({ maxInProgress: 1 });
  s.given(ticket("ENG-1", { status: "in_progress", optedIn: true, repo: "dispatch" }), worker("ENG-1", "working"),
          ticket("ENG-2", { status: "in_progress", optedIn: true, repo: "dispatch", createdAt: "09:00" }),
          ticket("ENG-3", { status: "in_progress", optedIn: true, repo: "dispatch", createdAt: "08:00" }));
  s.expect([]);
  s.see(worker("ENG-1", "none"), ticket("ENG-1", { status: "done" }));
  s.expect([["launch-worker", "herdr.launch", "ENG-3"]]);
});

scenario(rules, "a full pool is retried only after a tree comes back", s => {
  s.given(ticket("ENG-1", { status: "in_progress", optedIn: true, repo: "dispatch" }), pool("dispatch", { free: 0 }));
  s.expect([["launch-worker", "herdr.launch", "ENG-1"]]);
  s.land("herdr.launch", "ENG-1", { outcome: "refused", detail: "pool_exhausted" });
  s.resync();
  s.expect([]);                                  // same key, same full pool
  s.see(pool("dispatch", { free: 1, returns: 1 })); // a tree came back
  s.expect([["launch-worker", "herdr.launch", "ENG-1"]]);
});

scenario(rules, "an In Review relaunch into a full pool is retried only after a tree comes back", s => {
  s.given(ticket("ENG-1", { status: "in_review", optedIn: true, repo: "dispatch" }),
          pr("ENG-1", { head: "h1", problems: ["a failed check"] }), pool("dispatch", { free: 0 }));
  s.expect([["relaunch-for-pr", "herdr.launch", "ENG-1"]]);
  s.land("herdr.launch", "ENG-1", { outcome: "refused", detail: "pool_exhausted" });
  s.resync();
  s.expect([]);
  s.see(pool("dispatch", { free: 1, returns: 1 }));
  s.expect([["relaunch-for-pr", "herdr.launch", "ENG-1"]]);
});

scenario(rules, "a comment while blocked is delivered once unblocked", s => {
  s.given(ticket("ENG-1", { status: "in_progress" }), worker("ENG-1", "blocked"));
  s.see(comment("ENG-1", { body: "use the v2 API", at: "10:00" }));
  s.expect([["notify-blocked", "notify.send", "ENG-1"]]);   // a blocked worker is never typed into
  s.land("notify.send", "ENG-1");
  s.see(worker("ENG-1", "idle"));
  s.expect([["nudge-comment", "herdr.nudge", "ENG-1"]]);
  s.land("herdr.nudge", "ENG-1");
  s.restart();
  s.expect([]);                                  // once per comment time, across a restart
});
