import type { Ticket, Ctx, Receipt, Seen, Once } from "dispatch";

// A worker gets launch_failure_limit launch attempts in a row, and nudge_limit nudges or
// relaunches per PR head, before the ticket goes to Needs Human. Both are [rules] keys in
// config.toml, so they change without a PR.
export const launchFailureLimit = (ctx: Ctx): number => ctx.config.rules.launch_failure_limit;
export const nudgeLimit = (ctx: Ctx): number => ctx.config.rules.nudge_limit;

// One notice for every this many stall nudges with no status change.
export const STALL_NOTIFY_EVERY = 6;

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
  const parent = ctx.linear.parent(c);
  return parent?.status === "in_progress" && !isDesign(parent)
      && c.status === "todo" && c.optedIn && !isDesign(c) && !ctx.treehouse.leased(c);
};

export const openBlockers = (t: Ticket, ctx: Ctx) => ctx.linear.blockers(t).filter(b => !b.done);

// One abandoned blocker is enough, so this looks only at the blockers that were read.
export const abandonedBlocker = (t: Ticket, ctx: Ctx) => ctx.linear.blockersRead(t).find(b => b.abandoned);

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

// Whether a ticket's worker has heard about a comment or PR activity. A worker dispatch
// first saw after the item started out knowing it, and a launch's kickoff tells it
// everything dispatch had seen. A resume nudge tells it to read every comment. Otherwise
// a nudge tells what its once key names, such as ["comment", at] or ["activity", at],
// in the source's own time.
export const heard = (t: Ticket, topic: "comment" | "activity", item: Seen | undefined, ctx: Ctx) => {
  if (item === undefined) return true;
  const w = ctx.herdr.worker(t);
  if (w.state !== "none" && w.seenAt >= item.seenAt) return true;
  return ctx.ledger.receipts(t, { kind: ["launch", "nudge"] }).some(r => told(r) && (
    r.kind === "launch"                              ? r.started > item.seenAt
    : r.once[0] === "resume" && topic === "comment"  ? r.started > item.seenAt
    :                                                  r.once[0] === topic && String(r.once[1]) >= item.at));
};

export const owedComment = (t: Ticket, ctx: Ctx) => !heard(t, "comment", t.lastHumanComment, ctx);

// nudge-comment wants to tell the worker about a comment. A worker gets one nudge at a
// time, comment first, as today, so the stall and PR nudges stand back while this holds.
// The resume nudge covers the comments, so this stands back for it in turn. It leaves
// out stoppedSinceLastNudge, so the merge and ready notice wait for an owed comment even
// while the nudge itself waits for the worker to stop.
export const commentNudgeOwed = (t: Ticket, ctx: Ctx) => {
  const w = ctx.herdr.worker(t);
  return (t.status === "in_progress" || t.status === "in_review") && nudgeable(t)
      && (w.state === "idle" || w.state === "working")
      && !(w.state === "idle" && resumeOwed(t, ctx))
      && owedComment(t, ctx);
};

// A worker gets no second nudge of any kind until it stops after the last one, so a
// nudge never lands on top of the one before it. A worker with no Stop record has a
// lastStop from when dispatch saw this stretch of idling begin. A nudged worker shows as
// working until the next resync, so that stretch starts after the nudge and the worker
// isn't stranded. Only nudges sent to this worker count, and a Done receipt from
// flagged reset was never sent, so neither holds anything back.
export const stoppedSinceLastNudge = (t: Ticket, ctx: Ctx) => {
  const w = ctx.herdr.worker(t);
  const last = ctx.ledger.receipts(t, { kind: "nudge" }).find(r => sent(r) && r.started > w.seenAt);  // newest first
  return last === undefined || (w.lastStop !== undefined && w.lastStop.at > last.started);
};

// You answered in Needs Human and moved the ticket back. The resume nudge says so,
// once per move.
export const resumeOwed = (t: Ticket, ctx: Ctx) =>
  t.status === "in_progress" && t.previousStatus === "needs_human"
  && !ctx.ledger.receipts(t, { kind: "nudge", topic: "resume" })
       .some(r => r.once[1] === t.statusSince && r.outcome !== "failed");

// Failures since the last launch that worked or found the pool full, or since the
// escalation they caused, so fixing the problem and moving the ticket back starts over.
// Launches and In Review relaunches count alike, as today.
export const launchFailures = (t: Ticket, ctx: Ctx) => {
  const r = ctx.ledger.receipts(t, { kind: ["launch", "escalate"] });          // newest first
  const reset = r.findIndex(x => x.kind === "escalate" || x.outcome === "done" || x.detail === "pool_exhausted");
  return r.slice(0, reset < 0 ? r.length : reset).filter(x => x.outcome === "failed").length;
};

// Nudges about a PR that needs work, at this head, since the ticket's last status change.
export const prNudges = (t: Ticket, head: string, ctx: Ctx) =>
  ctx.ledger.receipts(t, { kind: "nudge", topic: "pr" })
    .filter(r => sent(r) && r.once[1] === head && r.started > t.statusSince).length;

// Relaunches for a PR that needed work, at this head, since the ticket's last status change.
export const relaunches = (t: Ticket, head: string, ctx: Ctx) =>
  ctx.ledger.receipts(t, { kind: "launch", topic: "relaunch" })
    .filter(r => r.outcome === "done" && r.once[1] === head && r.started > t.statusSince).length;

// Moves the ticket to Needs Human and comments why. The receipt keeps the two steps
// apart, so retry-escalation-comment can finish a comment that failed after the move.
export const escalate = (t: Ticket, ctx: Ctx, why: string, once: Once) =>
  ctx.linear.queueEscalate(t, why, { once });
