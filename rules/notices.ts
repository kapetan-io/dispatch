import { whenever, known, minutes, hours } from "dispatch";
import type { Ctx, PR, Ticket } from "dispatch";
import { nudgeable, skill, heard, sent, STALL_NOTIFY_EVERY } from "./shared";

// Notifications go to macOS and to every session following the ticket. A design
// worker waits on you by design, so its ticket doesn't notify. A review does.
// A ticket whose labels were cut short still notifies from Needs Human.
// A notification that fails is sent again at the next resync while its rule still
// wants it.

// Once per stretch in Needs Human.
whenever({
  name: "notify-needs-human",
  each: "linear.ticket",
  if:   (t) => t.status === "needs_human" && !(known(() => t.labels) && skill(t) === "design"),
  then: (t, ctx) => ctx.notify.queueSend(t, `${t.id} is in Needs Human: ${t.title}`, { once: ["needs-human", t.statusSince] }),
});

// Every blocked_renotify_minutes while the worker is blocked. The key carries the period,
// so each period is one notice.
whenever({
  name: "notify-blocked",
  each: "linear.ticket",
  if:   (t, ctx) => (t.status === "in_progress" || t.status === "in_review") && !t.hasChildren
           && skill(t) !== "design" && ctx.herdr.worker(t).state === "blocked",
  then: (t, ctx) => {
    const w = ctx.herdr.worker(t);
    // A reminder period of 0 means a notice in every cycle, so the key changes with each one.
    const every = ctx.config.rules.blocked_renotify_minutes;
    const period = every > 0 ? ctx.every(minutes(every), w.blockedSince!) : ctx.now;
    ctx.notify.queueSend(t, `${t.id} is waiting on your answer in its herdr workspace`,
      { once: ["blocked", w.blockedSince!, period] });
  },
});

// In a repo whose workers open PRs as drafts, a ready draft gets one notice, and
// opening it gets another once its reviews are handled. Opening a draft doesn't move
// the head, so the two notices carry different topics, and followers read the reviews-handled
// one as a different event from a fresh PR.
const readyNotice = (pr: PR) =>
  pr.openAs === "draft" && pr.draft ? { topic: "draft-ready", text: `${pr.ticket.id}'s draft is ready for your review: ${pr.url}` }
  : pr.openAs === "draft"           ? { topic: "reviews-handled", text: `${pr.ticket.id}'s reviews are handled: ${pr.url}` }
  :                                   { topic: "ready", text: `${pr.ticket.id} is ready for review: ${pr.url}` };

// Once per head and notice, and not for a PR auto-merge is about to merge.
whenever({
  name: "notify-ready-for-review",
  each: "github.pr",
  if:   (pr, ctx) => !pr.closed && pr.ready && pr.ticket.status === "in_review" && nudgeable(pr.ticket)
           && ctx.herdr.worker(pr.ticket).state === "idle" && heard(pr.ticket, "activity", pr.lastActivity, ctx)
           && !(pr.approved && pr.autoMerge && !pr.draft),
  then: (pr, ctx) => ctx.notify.queueSend(pr.ticket, readyNotice(pr).text, { once: [readyNotice(pr).topic, pr.head] }),
});

// Its own rule rather than a step after the stall nudge, so a failed notification
// is retried while the count still stands.
const stallNudges = (t: Ticket, ctx: Ctx) => ctx.ledger.receipts(t, { kind: "nudge", topic: "stall" })
  .filter(r => sent(r) && r.started >= t.statusSince).length;
whenever({
  name: "notify-stall-count",
  each: "linear.ticket",
  if:   (t, ctx) => t.status === "in_progress" && stallNudges(t, ctx) > 0 && stallNudges(t, ctx) % STALL_NOTIFY_EVERY === 0,
  then: (t, ctx) => ctx.notify.queueSend(t, `${t.id} has had ${stallNudges(t, ctx)} stall nudges with no status change`,
           { once: ["stall-count", t.statusSince, stallNudges(t, ctx)] }),
});

// A stall nudge has gone unanswered for an hour. It was the last nudge, and the worker
// hasn't stopped since. A worker with no lastStop yet holds nothing back and never throws.
const lastNudge = (t: Ticket, ctx: Ctx) => ctx.ledger.receipts(t, { kind: "nudge" }).find(sent);
whenever({
  name: "notify-unresponsive",
  each: "linear.ticket",
  if:   (t, ctx) => {
    const w = ctx.herdr.worker(t);
    const n = lastNudge(t, ctx);
    return t.status === "in_progress" && w.state === "idle" && !w.busy
        && n?.once[0] === "stall" && (w.lastStop === undefined || w.lastStop.at < n.started)
        && ctx.now >= n.started + hours(1);
  },
  then: (t, ctx) => ctx.notify.queueSend(t, `${t.id}: the worker hasn't responded to its nudge in an hour`,
           { once: ["unresponsive", lastNudge(t, ctx)!.once[1]] }),
});

// Once per head. The merge's receipt says it was refused, and a push moves the head.
// Followers hear it even when the macOS alert fails, as today.
const mergeRefused = (pr: PR, ctx: Ctx) => ctx.ledger.receipts(pr.ticket, { kind: "merge" })
  .find(r => r.once[1] === pr.head && r.outcome === "refused");
whenever({
  name: "notify-merge-failed",
  each: "github.pr",
  if:   (pr, ctx) => !pr.closed && mergeRefused(pr, ctx) !== undefined,
  then: (pr, ctx) => ctx.notify.queueSend(pr.ticket, `${pr.ticket.id}: merging ${pr.url} failed: ${mergeRefused(pr, ctx)!.detail}`,
           { once: ["merge-failed", pr.head], followersAlways: true }),
});
