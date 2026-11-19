import { whenever, minutes } from "dispatch";
import type { PR } from "dispatch";
import { nudgeable, heard, finalMergeRefusal } from "./shared";

// Notifications go to every session following the ticket, design tickets included.
// A notification that fails is sent again at the next resync while its rule still
// wants it.

// Once per stretch in Needs Human. A worker still in a turn holds the notice until it
// pauses, so the notice doesn't beat the question it is about; the hold ends after 5 minutes
// so a lost Stop report never silences it. With no worker there is nothing to wait for.
const HOLD = minutes(5);
whenever({
  name: "notify-needs-human",
  each: "ticket",
  if:   (t, ctx) => t.status === "needs_human"
           && (ctx.worker.of(t).state === "none" || ctx.worker.of(t).waiting || ctx.now - t.statusSince >= HOLD),
  then: (t, ctx) => ctx.notify.queueSend(t, `${t.id} is in Needs Human: ${t.title}`, { once: ["needs-human", t.statusSince], reason: "entered Needs Human" }),
});

// Once per activity time while the ticket is in Needs Human. Dispatch reads the PR only while the
// worker is idle, blocked on a popup or gone, and the worker hears nothing, because a nudge would
// make a worker with an open question ask again instead of reading the comments. Items that arrive
// between two reads share one notice, which names the newest. Activity dispatch read while the
// ticket was In Review, such as the review a pr-ignored escalation followed, is not news. When
// dispatch did not read the PR just before the ticket moved to Needs Human (its worker was working
// or blocked, the ticket came from In Progress, or it is a design ticket), it has no record of the
// activity, so the first read notifies for the newest item, even an old one.
whenever({
  name: "notify-needs-human-pr-activity",
  each: "pr",
  if:   (pr) => !pr.closed && pr.ticket.status === "needs_human" && pr.lastActivity !== undefined
           && pr.lastActivity.seenAt >= pr.ticket.statusSince,
  then: (pr, ctx) => ctx.notify.queueSend(pr.ticket,
           `${pr.ticket.id} is in Needs Human and its PR has new activity, ${pr.activitySummary}: ${pr.url}`,
           { once: ["needs-human-activity", pr.lastActivity!.at], reason: "PR activity while in Needs Human" }),
});

// Every blocked_renotify_minutes while the worker is blocked. The key carries the period,
// so each period is one notice.
whenever({
  name: "notify-blocked",
  each: "ticket",
  if:   (t, ctx) => (t.status === "in_progress" || t.status === "in_review") && !t.hasChildren
           && ctx.worker.of(t).state === "blocked",
  then: (t, ctx) => {
    const w = ctx.worker.of(t);
    // A reminder period of 0 means a notice in every cycle, so the key changes with each one.
    const every = ctx.config.rules.blocked_renotify_minutes;
    const period = every > 0 ? ctx.every(minutes(every), w.blockedSince!) : ctx.now;
    ctx.notify.queueSend(t, `${t.id} is waiting on your answer in its workspace`,
      { once: ["blocked", w.blockedSince!, period], reason: "worker is blocked on a question" });
  },
});

// Once per stretch of idling, when the worker's workspace turns 💤. waiting follows the label's
// own Stop-record and session-log test, so a message typed after the Stop silences the notice as it clears the label there. It tells followers and never types into the worker.
whenever({
  name: "notify-idle",
  each: "ticket",
  if:   (t, ctx) => t.status === "in_progress" && !t.hasChildren && ctx.worker.of(t).waiting,
  then: (t, ctx) => ctx.notify.queueSend(t, `${t.id}'s worker is idle with nothing running: ${t.title}`,
           { once: ["idle", ctx.worker.of(t).idleSince!], reason: "worker is idle" }),
});

// A ready PR notifies the user only while it needs them. In a repo whose workers open PRs
// as drafts, that is the ready draft, and then the approved PR when dispatch won't merge it.
// Between the two, an opened PR waits on its approvers and says nothing.
const readyNotice = (pr: PR) =>
  pr.waitingOn === "merge" ? { topic: "approved-ready", reason: "approved, ready to merge", text: `${pr.ticket.id} is approved and ready to merge: ${pr.url}` }
  : pr.openAs === "draft"  ? { topic: "draft-ready", reason: "draft ready for review", text: `${pr.ticket.id}'s draft is ready for your review: ${pr.url}` }
  :                          { topic: "ready", reason: "PR ready for review", text: `${pr.ticket.id} is ready for review: ${pr.url}` };

// Once per head and notice, and not for a PR auto-merge is about to merge.
whenever({
  name: "notify-ready-for-review",
  each: "pr",
  if:   (pr, ctx) => !pr.closed && (pr.waitingOn === "you" || pr.waitingOn === "merge")
           && pr.ticket.status === "in_review" && nudgeable(pr.ticket)
           && ctx.worker.of(pr.ticket).state === "idle" && heard(pr.ticket, "activity", pr.lastActivity, ctx)
           && !(pr.approved && pr.autoMerge && !pr.draft),
  then: (pr, ctx) => ctx.notify.queueSend(pr.ticket, readyNotice(pr).text, { once: [readyNotice(pr).topic, pr.head], reason: readyNotice(pr).reason }),
});

// Once per head: the merge's receipt says it was refused, and a push moves the head.
// A base-moved refusal is reported only after its retries run out.
whenever({
  name: "notify-merge-failed",
  each: "pr",
  if:   (pr, ctx) => !pr.closed && finalMergeRefusal(pr, ctx) !== undefined,
  then: (pr, ctx) => ctx.notify.queueSend(pr.ticket, `${pr.ticket.id}: merging ${pr.url} failed: ${finalMergeRefusal(pr, ctx)!.detail}`,
           { once: ["merge-failed", pr.head], reason: "PR approved" }),
});
