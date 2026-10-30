import { whenever, exempt } from "dispatch";
import type { Ticket, PR, Ctx, Blocker } from "dispatch";
import { openBlockers, abandonedBlocker, startableChild, isDesign, nudgeable, launchAs, heard, escalate,
         launchFailures, launchFailureLimit, stoppedSinceLastNudge } from "./shared";
import { ACTIVITY_NUDGE } from "./texts";

// Stacked tickets. Import this from dispatch.ts to build a chain of dependent tickets at once
// instead of one merge at a time. A ticket whose only open blocker is In Review with a ready PR
// launches on that PR's head, and its PR opens against the blocker's branch. When the blocker
// merges, the worker moves its own commits onto the default branch (a restack) and the ticket
// becomes an ordinary one. Edit the bar and the text here.
// The exemptions at the bottom keep the shipped rules off a stacked ticket until its restack.

// The PR a ticket can stack on. This is the bar; edit it here.
export const stackablePR = (t: Ticket, ctx: Ctx): PR | undefined => {
  const open = openBlockers(t, ctx);
  if (open.length !== 1 || abandonedBlocker(t, ctx)) return undefined;
  const parent = open[0].ticket;
  if (parent === undefined || parent.repo !== t.repo || parent.status !== "in_review") return undefined;
  const pr = ctx.pr.of(parent);
  return pr !== undefined && !pr.closed && pr.ready ? pr : undefined;
};

// The stack parent stopped blocking: it is Done, or the relation was removed (the "?? true"). Linear
// sets Done when the parent's PR merges, which is how a merge shows here, since dispatch reads only
// open PRs. restackOwed reads every relation, so it is unknown when they were cut short. parentBlocker
// reads only the ones that were read, so the escalation still works on a cut-short list.
const parentBlocker = (t: Ticket, ctx: Ctx): Blocker | undefined =>
  ctx.ticket.blockersRead(t).find(b => b.id === t.stack!.parent);
const restackOwed = (t: Ticket, ctx: Ctx) =>
  t.stack !== undefined && (ctx.ticket.blockers(t).find(b => b.id === t.stack!.parent)?.done ?? true);

const RESTACK = (t: Ticket) => {
  const { parent, forkPoint } = t.stack!;
  return [
    `${parent}, the ticket this one is stacked on, has merged or stopped blocking it. Move this ticket's own commits onto the default branch.`,
    `1. Check that ${parent}'s PR merged. If it did not, move this ticket to Needs Human with a comment saying so, and stop.`,
    `2. Run gh repo view "$(git remote get-url origin)" --json nameWithOwner,defaultBranchRef. Below, <repo> is nameWithOwner and <default> is the default branch's bare name, such as main.`,
    `3. git fetch origin, then git rebase --onto origin/<default> ${forkPoint}. The fork point is where this branch started, so only your own commits move.`,
    `4. On a conflict, run git rebase --abort, push nothing, and move this ticket to Needs Human with a comment naming the conflicting files.`,
    `5. git push --force-with-lease origin ${t.branch}. If the push is refused, stop and move this ticket to Needs Human with a comment saying why.`,
    `6. Point the PR at the default branch with gh pr edit --repo <repo> --base <default>. If this branch has no PR yet, open one with gh pr create --repo <repo> --base <default>.`,
    `7. Remove the stack record: git config --unset branch.${t.branch}.dispatchStackParent and git config --unset branch.${t.branch}.dispatchStackForkPoint.`,
    `8. Then follow whatever this repo's own instructions say to do after a restack (a project skill, CLAUDE.md, or wherever it keeps them). Skip this step when they say nothing.`,
  ].join("\n");
};

const abandonedText = (b: Blocker) =>
  `its stack parent ${b.id} is ${b.status} and will never be Done, so this stacked ticket cannot merge. Decide whether to restack it onto the default branch by hand or drop it, then move it back.`;

// A ticket whose only open blocker has a ready PR gets a worker on that PR's head. stackOn is what
// makes the launcher start the branch there and write the stack record. The key is launch-worker's
// with another topic, so a full pool is retried only after a tree comes back.
whenever({
  name: "launch-stacked-worker",
  each: "ticket",
  if:   (t, ctx) => t.status === "in_progress" && t.optedIn && !t.hasChildren && !isDesign(t)
           && ctx.config.hasRepo(t.repo)
           && !restackOwed(t, ctx) && stackablePR(t, ctx) !== undefined
           && launchFailures(t, ctx) < launchFailureLimit(ctx)
           && ctx.worker.of(t).state === "none",
  then: (t, ctx) => {
    const pr = stackablePR(t, ctx)!;
    return ctx.worker.queueLaunch(t, {
      ...launchAs(t, ctx), kickoff: `/dispatch:build ${t.id} --base ${pr.ticket.branch}`, stackOn: pr,
      once: ["stacked-launch", ctx.worker.of(t).goneSince, ctx.tree.pool(t.repo).returns],
    });
  },
});

// promote-child's twin for a feature piece whose only open blocker is ready to stack on.
whenever({
  name: "promote-stacked-child",
  each: "ticket",
  if:   (c, ctx) => startableChild(c, ctx) && !c.hasChildren && stackablePR(c, ctx) !== undefined,
  then: (c, ctx) => ctx.ticket.queueMove(c, "in_progress", `child of ${ctx.ticket.parent(c)!.id}, stacked on ${stackablePR(c, ctx)!.ticket.id}`,
           { once: ["promote", c.statusSince] }),
});

// A worker still In Progress whose stack parent merged before it opened its PR gets the same text,
// and step 6 opens the PR on the default branch. Once per stretch in the status. Needs Human is left
// out, because the restack's own exits move the ticket there and would start the nudge over.
whenever({
  name: "nudge-restack",
  each: "ticket",
  if:   (t, ctx) => t.stack !== undefined && (t.status === "in_progress" || t.status === "in_review")
           && nudgeable(t) && ctx.worker.of(t).state === "idle"
           && restackOwed(t, ctx) && stoppedSinceLastNudge(t, ctx),
  then: (t, ctx) => ctx.worker.queueNudge(t, RESTACK(t), { once: ["restack", t.stack!.parent, t.statusSince] }),
});

// A stacked ticket with no Claude gets one back to restack. It is the only rule that launches the
// ticket while a restack is owed, and it is uncapped, so it never waits for a slot.
whenever({
  name: "relaunch-for-restack",
  each: "ticket",
  if:   (t, ctx) => t.stack !== undefined && (t.status === "in_progress" || t.status === "in_review")
           && t.optedIn && nudgeable(t) && ctx.config.hasRepo(t.repo) && ctx.worker.of(t).state === "none"
           && restackOwed(t, ctx) && launchFailures(t, ctx) < launchFailureLimit(ctx),
  then: (t, ctx) => ctx.worker.queueLaunch(t, {
           model: launchAs(t, ctx).model, kickoff: RESTACK(t), uncapped: true,
           once: ["restack", t.stack!.parent, t.statusSince, ctx.worker.of(t).goneSince, ctx.tree.pool(t.repo).returns],
         }),
});

// The escalator for a stacked ticket whose stack parent is abandoned. It acts whatever the worker is
// doing, because the PR can no longer merge where it points. escalate-abandoned-blocker is
// exempted for that case, and the two share a once key, so the ticket is escalated once even when
// another of its blockers is abandoned too. With only another blocker abandoned, the shipped rule acts.
whenever({
  name: "escalate-abandoned-stack-parent",
  each: "ticket",
  if:   (t, ctx) => (t.status === "in_progress" || t.status === "in_review") && t.stack !== undefined
           && ctx.ticket.blockersRead(t).some(b => b.id === t.stack!.parent && b.abandoned),
  then: (t, ctx) => escalate(t, ctx, abandonedText(parentBlocker(t, ctx)!), ["abandoned", t.stack!.parent, t.statusSince]),
});

// notices.ts stays quiet about an approved PR in an auto_merge repo, because auto-merge is about to
// merge it. auto-merge is exempted for a stacked PR, so this tells you it waits. The condition is
// notify-ready-for-review's with its last clause flipped, and the once key is the same, which the
// scenarios pin.
const stackedReadyNotice = (pr: PR) =>
  pr.waitingOn === "merge" ? { topic: "approved-ready", text: `${pr.ticket.id} is approved and ready to merge: ${pr.url}` }
  : pr.openAs === "draft"  ? { topic: "draft-ready", text: `${pr.ticket.id}'s draft is ready for your review: ${pr.url}` }
  :                          { topic: "ready", text: `${pr.ticket.id} is ready for review: ${pr.url}` };
whenever({
  name: "notify-stacked-ready",
  each: "pr",
  if:   (pr, ctx) => !pr.closed && (pr.waitingOn === "you" || pr.waitingOn === "merge")
           && pr.ticket.status === "in_review" && nudgeable(pr.ticket)
           && ctx.worker.of(pr.ticket).state === "idle" && heard(pr.ticket, "activity", pr.lastActivity, ctx)
           && pr.approved && pr.autoMerge && !pr.draft && pr.ticket.stack !== undefined,
  then: (pr, ctx) => ctx.notify.queueSend(pr.ticket, `${stackedReadyNotice(pr).text} It waits on ${pr.ticket.stack!.parent} to merge, then its restack.`,
           { once: [stackedReadyNotice(pr).topic, pr.head], reason: "PR ready, stacked" }),
});

// nudge-pr-activity skips a PR that needs work, and a conflict with the base counts as work. A
// stacked PR conflicts until its restack, so this tells the idle worker about review activity
// then. It copies nudge-pr-activity's condition and once key, and a scenario pins them.
whenever({
  name: "nudge-stacked-activity",
  each: "pr",
  if:   (pr, ctx) => !pr.closed && pr.ticket.status === "in_review" && nudgeable(pr.ticket)
           && pr.ticket.stack !== undefined && pr.conflict && !restackOwed(pr.ticket, ctx)
           && ctx.worker.of(pr.ticket).state === "idle"
           && !heard(pr.ticket, "activity", pr.lastActivity, ctx) && stoppedSinceLastNudge(pr.ticket, ctx),
  then: (pr, ctx) => ctx.worker.queueNudge(pr.ticket, ACTIVITY_NUDGE(pr), { once: ["activity", pr.lastActivity!.at] }),
});

// Merged now, a stacked PR would land in its stack parent's branch.
exempt({
  name:  "stacked-pr-not-merged",
  each:  "pr",
  rules: ["auto-merge"],
  if:    (pr) => pr.ticket.stack !== undefined,
});

// A worker told to fix the conflict may rebase onto the stack parent's newer commits and break the
// fork point. The restack is the fix.
exempt({
  name:  "stacked-conflict-is-not-work",
  each:  "pr",
  rules: ["nudge-pr-needs-work", "relaunch-for-pr", "escalate-relaunch-limit"],
  if:    (pr) => pr.ticket.stack !== undefined && pr.conflict,
});

// The restack nudge goes alone, so nudge-pr-needs-work can't send a second message in the same cycle.
// Only launches are limited to one per ticket per cycle.
exempt({
  name:  "restack-before-pr-work",
  each:  "pr",
  rules: ["relaunch-for-pr", "nudge-pr-needs-work", "nudge-pr-activity"],
  if:    (pr, ctx) => restackOwed(pr.ticket, ctx),
});

// Once the stack parent is Done the ticket may have no open blockers, and a plain /dispatch:build
// would open a PR against the default branch carrying the stack parent's commits. With another
// blocker still open, the hold comment would say no worker starts while relaunch-for-restack starts one.
exempt({
  name:  "restack-owns-the-launch",
  each:  "ticket",
  rules: ["launch-worker", "note-blocker-hold"],
  if:    (t, ctx) => restackOwed(t, ctx),
});

// The ticket is about to launch, not wait for every blocker.
exempt({
  name:  "stacked-launch-not-held",
  each:  "ticket",
  rules: ["note-blocker-hold"],
  if:    (t, ctx) => t.optedIn && !isDesign(t) && ctx.config.hasRepo(t.repo) && stackablePR(t, ctx) !== undefined,
});

exempt({
  name:  "stack-parent-escalates",
  each:  "ticket",
  rules: ["escalate-abandoned-blocker"],
  if:    (t, ctx) => t.stack !== undefined && ctx.ticket.blockersRead(t).some(b => b.id === t.stack!.parent && b.abandoned),
});
