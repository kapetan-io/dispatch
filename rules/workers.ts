import { whenever } from "dispatch";
import { openBlockers, abandonedBlocker, nudgeable, launchAs, heard, launchFailures, relaunches,
         launchFailureLimit, nudgeLimit } from "./shared";

// A worker exists while an opted-in ticket is In Progress, unblocked, and has none.
// Once per stretch with no worker. After a full pool, once more each time a tree comes
// back to that repo's pool. Another ticket's lease doesn't count, so a full pool is
// never retried as it stands.
// If herdr or the repo's pool couldn't be read, the worker is unknown and this holds,
// so dispatch never starts a second worker beside one it can't see.
whenever({
  name: "launch-worker",
  each: "linear.ticket",
  if:   (t, ctx) => t.status === "in_progress" && t.optedIn && !t.hasChildren
           && ctx.config.hasRepo(t.repo)
           && openBlockers(t, ctx).length === 0 && !abandonedBlocker(t, ctx)
           && launchFailures(t, ctx) < launchFailureLimit(ctx)
           && ctx.herdr.worker(t).state === "none",
  then: (t, ctx) => ctx.herdr.queueLaunch(t, {
           ...launchAs(t, ctx),
           once: ["launch", ctx.herdr.worker(t).goneSince, ctx.treehouse.pool(t.repo).returns],
         }),  // waits for a slot under max_in_progress, oldest ticket first
});

// An In Review PR with no worker gets one back when the PR needs work or has a review
// thread the worker hasn't seen. In Review never waits for a slot. Only a relaunch for
// a PR that needs work counts toward the relaunch limit, so its key's topic says which
// it is. Failed relaunches count toward launchFailures like any launch.
whenever({
  name: "relaunch-for-pr",
  each: "github.pr",
  if:   (pr, ctx) => !pr.closed && pr.ticket.status === "in_review" && nudgeable(pr.ticket)
           && ctx.herdr.worker(pr.ticket).state === "none"
           && (pr.needsWork || !heard(pr.ticket, "activity", pr.lastThread, ctx))
           && relaunches(pr.ticket, pr.head, ctx) < nudgeLimit(ctx)
           && launchFailures(pr.ticket, ctx) < launchFailureLimit(ctx),
  then: (pr, ctx) => ctx.herdr.queueLaunch(pr.ticket, {
           ...launchAs(pr.ticket, ctx), uncapped: true,
           once: [pr.needsWork ? "relaunch" : "relaunch-thread", pr.head, ctx.herdr.worker(pr.ticket).goneSince],
         }),
});

// A paused ticket's worker is closed, once for each worker dispatch sees. Its tree
// stays leased for when you resume it.
whenever({
  name: "stop-paused-worker",
  each: "herdr.worker",
  if:   (w) => w.state !== "none" && w.ticket.paused,
  then: (w, ctx) => ctx.herdr.queueClose(w, { once: ["close", w.seenAt] }),
});
