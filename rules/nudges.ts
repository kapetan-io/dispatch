import { whenever, minutes } from "dispatch";
import { nudgeable, resumeOwed, commentNudgeOwed, heard, prNudges, escalate, stoppedSinceLastNudge, nudgeLimit } from "./shared";
import { STALL_NUDGE, RESUME_NUDGE, COMMENT_NUDGE, ACTIVITY_NUDGE, PR_NUDGE } from "./texts";

// Each nudge's once key names what it told the worker, in the source's own time, such
// as the comment time, the PR activity time or the Stop it answered. A nudge is owed while
// there's something newer, so a comment that arrives while the worker is stuck in a
// question dialog is still owed when the dialog closes. A worker gets one nudge at a
// time, and the comment nudge goes first, as today. No nudge of any kind goes out until
// the worker has stopped since the last one (stoppedSinceLastNudge).

whenever({
  name: "nudge-comment",
  each: "linear.ticket",
  if:   (t, ctx) => commentNudgeOwed(t, ctx) && stoppedSinceLastNudge(t, ctx),  // stands back for the resume nudge
  then: (t, ctx) => ctx.herdr.queueNudge(t, COMMENT_NUDGE(t), { once: ["comment", t.lastHumanComment!.at] }),
});

// Once per move back from Needs Human. It also tells the worker to read the comments.
whenever({
  name: "nudge-resume",
  each: "linear.ticket",
  if:   (t, ctx) => nudgeable(t) && ctx.herdr.worker(t).state === "idle" && resumeOwed(t, ctx)
           && stoppedSinceLastNudge(t, ctx),
  then: (t, ctx) => ctx.herdr.queueNudge(t, RESUME_NUDGE(t), { once: ["resume", t.statusSince] }),
});

// Once per Stop. A Stop with background tasks still running counts as busy for an
// hour after it, and the next cycle after that hour sees it end. A worker with no Stop
// record has a lastStop from when dispatch first saw it idle.
whenever({
  name: "nudge-stall",
  each: "linear.ticket",
  if:   (t, ctx) => {
    const w = ctx.herdr.worker(t);
    return t.status === "in_progress" && nudgeable(t) && w.state === "idle" && !w.busy
        && w.lastStop !== undefined && !resumeOwed(t, ctx) && !commentNudgeOwed(t, ctx)
        && stoppedSinceLastNudge(t, ctx);
  },
  then: (t, ctx) => ctx.herdr.queueNudge(t, STALL_NUDGE(t), { once: ["stall", ctx.herdr.worker(t).lastStop!.at] }),
});

// Once per activity time, a GitHub time compared with GitHub times.
whenever({
  name: "nudge-pr-activity",
  each: "github.pr",
  if:   (pr, ctx) => !pr.closed && pr.ticket.status === "in_review" && nudgeable(pr.ticket) && !pr.needsWork
           && ctx.herdr.worker(pr.ticket).state === "idle" && !commentNudgeOwed(pr.ticket, ctx)
           && !heard(pr.ticket, "activity", pr.lastActivity, ctx) && stoppedSinceLastNudge(pr.ticket, ctx),
  then: (pr, ctx) => ctx.herdr.queueNudge(pr.ticket, ACTIVITY_NUDGE(pr), { once: ["activity", pr.lastActivity!.at] }),
});

// Every 5 minutes while the worker sits idle on a PR that needs work, then escalates.
// The key carries the head, the idle stretch and the period, so a push or the worker
// going back to work starts the nudges over. A nudged worker shows as working until the
// next resync, so each nudge starts a new idle stretch. The count and the escalation are per stretch
// in In Review, so moving the ticket back gives fresh nudges.
whenever({
  name: "nudge-pr-needs-work",
  each: "github.pr",
  if:   (pr, ctx) => !pr.closed && pr.needsWork && pr.ticket.status === "in_review" && nudgeable(pr.ticket)
           && ctx.herdr.worker(pr.ticket).state === "idle" && !commentNudgeOwed(pr.ticket, ctx)
           && stoppedSinceLastNudge(pr.ticket, ctx),
  then: (pr, ctx) => {
    const w = ctx.herdr.worker(pr.ticket);
    const sent = prNudges(pr.ticket, pr.head, ctx);
    if (sent < nudgeLimit(ctx)) {
      ctx.herdr.queueNudge(pr.ticket, PR_NUDGE(pr),
        { once: ["pr", pr.head, w.idleSince!, ctx.every(minutes(5), w.idleSince!)] });
    } else {
      escalate(pr.ticket, ctx, `its worker stayed idle through ${sent} nudges about ${pr.url} without pushing. Look at its herdr workspace.`,
        ["pr-ignored", pr.head, pr.ticket.statusSince]);
    }
  },
});
