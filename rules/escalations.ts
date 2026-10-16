import { whenever, known } from "dispatch";
import type { Blocker, Ctx, Ticket } from "dispatch";
import { escalate, openBlockers, abandonedBlocker, startableChild, launchFailures, launchFailureList, relaunches,
         launchFailureLimit, nudgeLimit } from "./shared";

// Each of these moves a ticket to Needs Human and says why, once per stretch in its
// status. Moving the ticket back starts a new stretch, so the escalation can happen again.

const lastLaunchFailure = (t: Ticket, ctx: Ctx) => {
  const last = launchFailureList(t, ctx)[0];   // the rule runs at the limit, so there is one
  return last.kind === "close" ? "The last time, Claude opened but its launch died before the kickoff ran."
                               : `The launcher said: ${last.detail}`;
};

// Launches and In Review relaunches count alike, as today.
whenever({
  name: "escalate-launch-failures",
  each: "linear.ticket",
  if:   (t, ctx) => (t.status === "in_progress" || t.status === "in_review") && launchFailures(t, ctx) >= launchFailureLimit(ctx),
  then: (t, ctx) => escalate(t, ctx,
           `its worker failed to launch ${launchFailures(t, ctx)} times in a row. ${lastLaunchFailure(t, ctx)}`,
           ["launch-failures", t.statusSince]),
});

whenever({
  name: "escalate-relaunch-limit",
  each: "github.pr",
  if:   (pr, ctx) => !pr.closed && pr.needsWork && pr.ticket.status === "in_review"
           && ctx.herdr.worker(pr.ticket).state === "none"
           && relaunches(pr.ticket, pr.head, ctx) >= nudgeLimit(ctx),
  then: (pr, ctx) => escalate(pr.ticket, ctx,
           `its worker was relaunched ${nudgeLimit(ctx)} times for ${pr.url} without pushing. Look at its herdr workspace.`,
           ["relaunch-limit", pr.head, pr.ticket.statusSince]),
});

// These rules act because something is missing, so they ask known() instead of
// reading the missing value, which would hold the rule.
whenever({
  name: "escalate-truncated-labels",
  each: "linear.ticket",
  if:   (t, ctx) => t.status === "in_progress" && !t.hasChildren && !known(() => t.labels) && ctx.herdr.worker(t).state === "none",
  then: (t, ctx) => escalate(t, ctx,
           "it has more labels than the dispatcher reads, so it can't tell its repo or whether it is a design ticket. Remove some labels and move it back to In Progress.",
           ["truncated-labels", t.statusSince]),
});

whenever({
  name: "escalate-missing-repo",
  each: "linear.ticket",
  if:   (t, ctx) => t.status === "in_progress" && t.optedIn && !t.hasChildren
           && !ctx.config.hasRepo(t.repo) && ctx.herdr.worker(t).state === "none",
  then: (t, ctx) => escalate(t, ctx, t.repo === undefined
           ? "it has no repo label, so the dispatcher can't tell which repo to build in. Add one and move it back to In Progress."
           : `its repo "${t.repo}" is not in the dispatcher's config. Add a [repos.${t.repo}] table for it and move the ticket back to In Progress.`,
           ["missing-repo", t.statusSince]),
});

// An In Progress ticket with no worker, and a Todo child promote-child would start
// but for the blocker. A blocker that is merely Done isn't abandoned.
const abandonedText = (b: Blocker) =>
  `its blocker ${b.id} is ${b.status} and will never be Done, so it will not start. Remove the blocker relation, then move this ticket back.`;

whenever({
  name: "escalate-abandoned-blocker",
  each: "linear.ticket",
  if:   (t, ctx) => t.status === "in_progress" && !t.hasChildren
           && ctx.herdr.worker(t).state === "none" && abandonedBlocker(t, ctx) !== undefined,
  then: (t, ctx) => escalate(t, ctx, abandonedText(abandonedBlocker(t, ctx)!),
           ["abandoned", abandonedBlocker(t, ctx)!.id, t.statusSince]),
});

whenever({
  name: "escalate-abandoned-blocker-child",
  each: "linear.ticket",
  if:   (c, ctx) => startableChild(c, ctx) && abandonedBlocker(c, ctx) !== undefined,
  then: (c, ctx) => escalate(c, ctx, abandonedText(abandonedBlocker(c, ctx)!),
           ["abandoned", abandonedBlocker(c, ctx)!.id, c.statusSince]),
});

// One comment per distinct set of open blockers while the ticket waits. A change in the
// open blockers changes the key, and so does a new wait. A ticket that stops waiting
// and is blocked again gets the note again.
const holdingBlockers = (t: Ticket, ctx: Ctx) => {
  const all = known(() => ctx.linear.blockers(t));
  const ids = (all ? openBlockers(t, ctx) : ctx.linear.blockersRead(t).filter(b => !b.done)).map(b => b.id);
  if (!all) ids.push("relations the dispatcher did not read");
  return ids.sort().join(", ");
};
whenever({
  name: "note-blocker-hold",
  each: "linear.ticket",
  if:   (t, ctx) => t.status === "in_progress" && !t.hasChildren && ctx.herdr.worker(t).state === "none"
           && !abandonedBlocker(t, ctx)
           && (!known(() => ctx.linear.blockers(t)) || openBlockers(t, ctx).length > 0),
  then: (t, ctx) => ctx.linear.queueComment(t,
           `No worker is running because this ticket is blocked by ${holdingBlockers(t, ctx)}. A worker starts once every blocker is Done.`,
           { once: ["hold", holdingBlockers(t, ctx), ctx.linear.waitingSince(t)] }),
});

// The move to Needs Human landed but its comment didn't. This runs over escalation
// receipts, not tracked tickets, so it still finishes the job after the ticket has
// moved on or left the tracked set.
whenever({
  name: "retry-escalation-comment",
  each: "ledger.escalation",
  if:   (e) => e.moved === "done" && e.commented === "failed",
  then: (e, ctx) => ctx.linear.queueComment(e.ticket,
           `The dispatcher moved this ticket to Needs Human because ${e.reason}`, { once: ["escalation", e.started] }),
});
