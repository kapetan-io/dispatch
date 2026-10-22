import { whenever, known, minutes } from "dispatch";
import type { PR } from "dispatch";
import { nudgeable, skill, heard, finalMergeRefusal } from "./shared";

// Notifications go to macOS and to every session following the ticket. A design
// worker waits on you by design, so its ticket doesn't notify. A review does.
// A ticket whose labels were cut short still notifies from Needs Human.
// A notification that fails is sent again at the next resync while its rule still
// wants it.

// Once per stretch in Needs Human.
whenever({
  name: "notify-needs-human",
  each: "ticket",
  if:   (t) => t.status === "needs_human" && !(known(() => t.labels) && skill(t) === "design"),
  then: (t, ctx) => ctx.notify.queueSend(t, `${t.id} is in Needs Human: ${t.title}`, { once: ["needs-human", t.statusSince], reason: "entered Needs Human" }),
});

// Every blocked_renotify_minutes while the worker is blocked. The key carries the period,
// so each period is one notice.
whenever({
  name: "notify-blocked",
  each: "ticket",
  if:   (t, ctx) => (t.status === "in_progress" || t.status === "in_review") && !t.hasChildren
           && skill(t) !== "design" && ctx.worker.of(t).state === "blocked",
  then: (t, ctx) => {
    const w = ctx.worker.of(t);
    // A reminder period of 0 means a notice in every cycle, so the key changes with each one.
    const every = ctx.config.rules.blocked_renotify_minutes;
    const period = every > 0 ? ctx.every(minutes(every), w.blockedSince!) : ctx.now;
    ctx.notify.queueSend(t, `${t.id} is waiting on your answer in its herdr workspace`,
      { once: ["blocked", w.blockedSince!, period], reason: "worker is blocked on a question" });
  },
});

// In a repo whose workers open PRs as drafts, a ready draft gets one notice, and
// opening it gets another once its reviews are handled. Opening a draft doesn't move
// the head, so the two notices carry different topics, and followers read the reviews-handled
// one as a different event from a fresh PR.
const readyNotice = (pr: PR) =>
  pr.openAs === "draft" && pr.draft ? { topic: "draft-ready", reason: "draft ready for review", text: `${pr.ticket.id}'s draft is ready for your review: ${pr.url}` }
  : pr.openAs === "draft"           ? { topic: "reviews-handled", reason: "reviews handled", text: `${pr.ticket.id}'s reviews are handled: ${pr.url}` }
  :                                   { topic: "ready", reason: "PR ready for review", text: `${pr.ticket.id} is ready for review: ${pr.url}` };

// Once per head and notice, and not for a PR auto-merge is about to merge.
whenever({
  name: "notify-ready-for-review",
  each: "pr",
  if:   (pr, ctx) => !pr.closed && pr.ready && pr.ticket.status === "in_review" && nudgeable(pr.ticket)
           && ctx.worker.of(pr.ticket).state === "idle" && heard(pr.ticket, "activity", pr.lastActivity, ctx)
           && !(pr.approved && pr.autoMerge && !pr.draft),
  then: (pr, ctx) => ctx.notify.queueSend(pr.ticket, readyNotice(pr).text, { once: [readyNotice(pr).topic, pr.head], reason: readyNotice(pr).reason }),
});

// Once per head: the merge's receipt says it was refused, and a push moves the head.
// A base-moved refusal is reported only after its retries run out. Followers hear it
// even when the macOS alert fails.
whenever({
  name: "notify-merge-failed",
  each: "pr",
  if:   (pr, ctx) => !pr.closed && finalMergeRefusal(pr, ctx) !== undefined,
  then: (pr, ctx) => ctx.notify.queueSend(pr.ticket, `${pr.ticket.id}: merging ${pr.url} failed: ${finalMergeRefusal(pr, ctx)!.detail}`,
           { once: ["merge-failed", pr.head], followersAlways: true, reason: "PR approved" }),
});
