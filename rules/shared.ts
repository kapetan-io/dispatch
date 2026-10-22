import { known, minutes } from "dispatch";
import type { Ticket, PR, Ctx, Receipt, Seen, Once } from "dispatch";

// A worker gets launch_failure_limit launch attempts in a row, and nudge_limit nudges or
// relaunches per PR head, before the ticket goes to Needs Human. Both are [rules] keys in
// config.toml, so they change without a PR.
export const launchFailureLimit = (ctx: Ctx): number => ctx.config.rules.launch_failure_limit;
export const nudgeLimit = (ctx: Ctx): number => ctx.config.rules.nudge_limit;

export const isDesign = (t: Ticket) => t.hasLabel("design") || t.hasLabel("design-review");

// The skill a ticket's worker runs. A ticket carrying both design labels is a review.
export const skill = (t: Ticket) =>
  t.hasLabel("design-review") ? "design-review" : t.hasLabel("design") ? "design" : "build";

// The model and first command a ticket's worker starts with. A build worker gets the
// launcher's default kickoff, as today.
export const launchAs = (t: Ticket, ctx: Ctx): { model: string; kickoff?: string } =>
  skill(t) === "build"
    ? { model: ctx.config.rules.worker_model }
    : { model: ctx.config.rules.design_model, kickoff: `/dispatch:${skill(t)} ${t.id}` };

// Tickets whose worker dispatch talks to. Parents have no worker, and design
// workers wait on you, so neither is nudged.
export const nudgeable = (t: Ticket) => !t.hasChildren && !isDesign(t);

// A Todo child of an In Progress parent that promote-child would start once its
// blockers are Done. A child that still holds a tree was paused on purpose.
export const startableChild = (c: Ticket, ctx: Ctx) => {
  const parent = ctx.ticket.parent(c);
  return parent?.status === "in_progress" && !isDesign(parent)
      && c.status === "todo" && c.optedIn && !isDesign(c) && !ctx.tree.leased(c);
};

export const openBlockers = (t: Ticket, ctx: Ctx) => ctx.ticket.blockers(t).filter(b => !b.done);

// One abandoned blocker is enough, so this looks only at the blockers that were read.
export const abandonedBlocker = (t: Ticket, ctx: Ctx) => ctx.ticket.blockersRead(t).find(b => b.abandoned);

// A message counts as told once it landed, or once dispatch tried to send it. After a
// crash, an attempt with no outcome may have landed, so it is never sent again.
export const told = (r: Receipt) => r.outcome === undefined || r.outcome === "done";

// A message dispatch actually sent. A Done receipt flagged reset stands for a message dispatch never
// sent, flagged as reset, so counts skip them.
export const sent = (r: Receipt) => told(r) && !r.reset;

// Whether a message with this topic was told to the subject within the window. A rule
// writes "not again within an hour" as !recentlySent(t, "topic", hours(1), ctx).
export const recentlySent = (t: Ticket, topic: string, window: number, ctx: Ctx) =>
  ctx.ledger.receipts(t, { kind: ["nudge", "notify", "comment"], topic })
    .some(r => sent(r) && r.started > ctx.now - window);

// Whether a ticket's worker has heard about PR activity. A worker dispatch first saw
// after the item started out knowing it, and a launch's kickoff tells it everything
// dispatch had seen. Otherwise an activity nudge tells what its once key names, in the
// source's own time. A launch or nudge started in the very cycle that first saw the item
// counts, because that cycle shares the item's millisecond (so >=, not >).
export const heard = (t: Ticket, topic: "activity", item: Seen | undefined, ctx: Ctx) => {
  if (item === undefined) return true;
  const w = ctx.worker.of(t);
  if (w.state !== "none" && w.seenAt >= item.seenAt) return true;
  return ctx.ledger.receipts(t, { kind: ["launch", "nudge"] }).some(r => told(r) && (
    r.kind === "launch" ? r.started >= item.seenAt
    :                     r.once[0] === topic && String(r.once[1]) >= item.at));
};

// A worker gets no second nudge of any kind until it stops after the last one, so a
// nudge never lands on top of the one before it. A worker with no Stop record has a
// lastStop from when dispatch saw this stretch of idling begin. A nudged worker shows as
// working until the next resync, so that stretch starts after the nudge and the worker
// isn't stranded. Only nudges sent to this worker count, and a Done receipt from
// flagged reset was never sent, so neither holds anything back. While the kickoff is
// unconfirmed the Claude is idle only because it was never told anything, and a nudge
// would race the kickoff, so close-unconfirmed-worker handles it.
export const stoppedSinceLastNudge = (t: Ticket, ctx: Ctx) => {
  const w = ctx.worker.of(t);
  if (w.unconfirmedSince !== undefined) return false;
  const last = ctx.ledger.receipts(t, { kind: "nudge" }).find(r => sent(r) && r.started >= w.seenAt);  // newest first
  return last === undefined || (w.lastStop !== undefined && w.lastStop.at > last.started);
};

// You answered in Needs Human and moved the ticket back. The resume nudge says so,
// once per move.
export const resumeOwed = (t: Ticket, ctx: Ctx) =>
  t.status === "in_progress" && t.previousStatus === "needs_human"
  && !ctx.ledger.receipts(t, { kind: "nudge", topic: "resume" })
       .some(r => r.once[1] === t.statusSince && r.outcome !== "failed");

// The once topic of closing a worker whose kickoff never ran. The executor matches the
// same string to word the log line.
export const UNCONFIRMED_CLOSE = "close-unconfirmed";

// The launch failures since the last launch that worked or found the pool full, or since
// the escalation they caused, newest first, so fixing the problem and moving the ticket
// back starts over. Launches and In Review relaunches count alike. Closing a Claude whose
// launch died before its kickoff counts too, because the launch that started it never
// reported.
export const launchFailureList = (t: Ticket, ctx: Ctx) => {
  const r = ctx.ledger.receipts(t, { kind: ["launch", "escalate", "close"] })   // newest first
    .filter(x => x.kind !== "close" || x.once[0] === UNCONFIRMED_CLOSE);
  const reset = r.findIndex(x => x.kind === "escalate" || x.outcome === "done" && x.kind === "launch"
                                 || ["already_running", "pool_exhausted"].includes(x.detail));
  return r.slice(0, reset < 0 ? r.length : reset)
    .filter(x => x.kind === "close" ? x.outcome === "done" : x.outcome === "failed" && x.detail !== "crashed");
};
export const launchFailures = (t: Ticket, ctx: Ctx) => launchFailureList(t, ctx).length;

// Nudges about a PR that needs work, at this head, since the ticket's last status change.
export const prNudges = (t: Ticket, head: string, ctx: Ctx) =>
  ctx.ledger.receipts(t, { kind: "nudge", topic: "pr" })
    .filter(r => sent(r) && r.once[1] === head && r.started >= t.statusSince).length;

// Relaunches for a PR that needed work, at this head, since the ticket's last status change.
export const relaunches = (t: Ticket, head: string, ctx: Ctx) =>
  ctx.ledger.receipts(t, { kind: "launch", topic: "relaunch" })
    .filter(r => r.outcome === "done" && r.once[1] === head && r.started >= t.statusSince).length;

// Moves the ticket to Needs Human and comments why. The receipt keeps the two steps
// apart, so retry-escalation-comment can finish a comment that failed after the move.
// notify-needs-human leaves a design ticket alone (a review ticket it covers), so the notice for one dispatch moved itself
// follows the move. The list is ordered, so a move that fails announces nothing.
export const escalate = (t: Ticket, ctx: Ctx, why: string, once: Once) => {
  const move = ctx.ticket.queueEscalate(t, why, { once });
  return !known(() => t.labels) || skill(t) !== "design" ? move
    : [move, ctx.notify.queueSend(t, `${t.id} is in Needs Human: ${t.title}`, { once: ["needs-human-escalated", ...once], reason: "entered Needs Human" })];
};

// Retries of a base-moved refusal, and the pause before each.
const MERGE_RETRIES = 5;
const MERGE_RETRY_PAUSE = minutes(1);

// GitHub refuses a merge with this text when another PR merged into the base branch a
// moment before. The same merge goes through once GitHub catches up. Every other
// refusal waits for a push.
const baseMoved = (r: Receipt) => r.detail.includes("Base branch was modified");
const attemptOf = (r: Receipt) => Number(r.once[2] ?? 0);
const retryable = (r: Receipt) => baseMoved(r) && attemptOf(r) < MERGE_RETRIES;

// The newest refused merge of the PR's head (receipts come newest first).
const lastMergeRefusal = (pr: PR, ctx: Ctx) => ctx.ledger.receipts(pr.ticket, { kind: "merge" })
  .find(r => r.once[1] === pr.head && r.outcome === "refused");

// The once key of the next merge. The first try has no attempt number and each retry
// appends one, so the ledger counts a retry as a new merge.
export const mergeOnce = (pr: PR, ctx: Ctx): Once => {
  const last = lastMergeRefusal(pr, ctx);
  if (last === undefined) return ["merge", pr.head];
  const attempt = attemptOf(last) + (retryable(last) ? 1 : 0);
  return attempt === 0 ? ["merge", pr.head] : ["merge", pr.head, attempt];
};

// Whether the last refusal will be retried but its pause hasn't passed.
export const mergeRetryPaused = (pr: PR, ctx: Ctx) => {
  const last = lastMergeRefusal(pr, ctx);
  return last !== undefined && retryable(last) && ctx.now < (last.ended ?? last.started) + MERGE_RETRY_PAUSE;
};

// The refusal the merge-failed notice reports: one that won't be retried. A base-moved
// refusal still inside its retries isn't reported, because the retry usually lands.
export const finalMergeRefusal = (pr: PR, ctx: Ctx) => {
  const last = lastMergeRefusal(pr, ctx);
  return last !== undefined && !retryable(last) ? last : undefined;
};
