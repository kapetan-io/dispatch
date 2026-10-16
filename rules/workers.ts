import { whenever, minutes } from "dispatch";
import { openBlockers, abandonedBlocker, nudgeable, launchAs, heard, launchFailures, relaunches,
         launchFailureLimit, nudgeLimit, UNCONFIRMED_CLOSE } from "./shared";

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
// it is. Failed relaunches count toward launchFailures like any launch. The key carries the
// pool's returns, as launch-worker's does, so a full pool is retried only after a tree comes back.
whenever({
  name: "relaunch-for-pr",
  each: "github.pr",
  if:   (pr, ctx) => !pr.closed && pr.ticket.status === "in_review" && nudgeable(pr.ticket)
           && ctx.config.hasRepo(pr.ticket.repo)
           && ctx.herdr.worker(pr.ticket).state === "none"
           && (pr.needsWork || !heard(pr.ticket, "activity", pr.lastThread, ctx))
           && (!pr.needsWork || relaunches(pr.ticket, pr.head, ctx) < nudgeLimit(ctx))
           && launchFailures(pr.ticket, ctx) < launchFailureLimit(ctx),
  then: (pr, ctx) => ctx.herdr.queueLaunch(pr.ticket, {
           ...launchAs(pr.ticket, ctx), uncapped: true,
           once: [pr.needsWork ? "relaunch" : "relaunch-thread", pr.head, ctx.herdr.worker(pr.ticket).goneSince,
                  ctx.treehouse.pool(pr.ticket.repo).returns],
         }),
});

// A paused ticket's worker is closed, once for each worker dispatch sees. Its tree
// stays leased for when you resume it.
whenever({
  name: "stop-paused-worker",
  each: "herdr.worker",
  if:   (w) => w.state !== "none" && w.ticket.paused,
  then: (w, ctx) => ctx.herdr.queueClose(w, { once: ["close", w.tree, w.seenAt] }),
});

// A launch killed after Claude started and before its kickoff ran leaves a Claude that is
// open with nothing to do. It looks like a live worker, so launch-worker never retries it
// and nothing nudges it. The launcher marks a launch unconfirmed until it returns, so a
// worker still marked after the grace period lost its launch. A Claude stopped at a
// startup dialog shows as blocked, so that counts too. A launch dispatch runs gives up
// after 25 seconds, but one run by hand can wait on herdr for about two minutes, and the
// grace period leaves it room. A paused ticket's worker is closed by stop-paused-worker, and
// that close must not count as a failed launch. Closing the workspace frees the ticket for
// launch-worker, and each close counts toward launch_failure_limit.
const KICKOFF_GRACE = minutes(5);
whenever({
  name: "close-unconfirmed-worker",
  each: "herdr.worker",
  if:   (w, ctx) => (w.state === "idle" || w.state === "blocked") && w.unconfirmedSince !== undefined
           && !w.ticket.paused && ctx.now - w.unconfirmedSince >= KICKOFF_GRACE,
  then: (w, ctx) => ctx.herdr.queueClose(w, { once: [UNCONFIRMED_CLOSE, w.tree, w.unconfirmedSince] }),
});
