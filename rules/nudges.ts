import { whenever, minutes } from "dispatch";
import { nudgeable, resumeOwed, heard, prNudges, escalate, stoppedSinceLastNudge, nudgeLimit } from "./shared";
import { RESUME_NUDGE, ACTIVITY_NUDGE, PR_NUDGE } from "./texts";

// Each nudge's once key names what it told the worker, in the source's own time, such
// as the PR activity time or the head commit. A nudge is owed while there's something
// newer, so activity that arrives while the worker is stuck in a question dialog is still
// owed when the dialog closes. No nudge of any kind goes out until the worker has stopped
// since the last one (stoppedSinceLastNudge).

// Once per move back from Needs Human. It also tells the worker to read the comments.
whenever({
  name: "nudge-resume",
  each: "ticket",
  if:   (t, ctx) => nudgeable(t) && ctx.worker.of(t).state === "idle" && resumeOwed(t, ctx)
           && stoppedSinceLastNudge(t, ctx),
  then: (t, ctx) => ctx.worker.queueNudge(t, RESUME_NUDGE(t), { once: ["resume", t.statusSince] }),
});

// Once per activity time, a GitHub time compared with GitHub times.
whenever({
  name: "nudge-pr-activity",
  each: "pr",
  if:   (pr, ctx) => !pr.closed && pr.ticket.status === "in_review" && nudgeable(pr.ticket) && !pr.needsWork
           && ctx.worker.of(pr.ticket).state === "idle"
           && !heard(pr.ticket, "activity", pr.lastActivity, ctx) && stoppedSinceLastNudge(pr.ticket, ctx),
  then: (pr, ctx) => ctx.worker.queueNudge(pr.ticket, ACTIVITY_NUDGE(pr), { once: ["activity", pr.lastActivity!.at] }),
});

// Every 5 minutes while the worker sits idle on a PR that needs work, then escalates.
// The key carries the head, the idle stretch and the period, so a push or the worker
// going back to work starts the nudges over. A nudged worker shows as working until the
// next resync, so each nudge starts a new idle stretch. The count and the escalation are per stretch
// in In Review, so moving the ticket back gives fresh nudges.
whenever({
  name: "nudge-pr-needs-work",
  each: "pr",
  if:   (pr, ctx) => !pr.closed && pr.needsWork && pr.ticket.status === "in_review" && nudgeable(pr.ticket)
           && ctx.worker.of(pr.ticket).state === "idle"
           && stoppedSinceLastNudge(pr.ticket, ctx),
  then: (pr, ctx) => {
    const w = ctx.worker.of(pr.ticket);
    const sent = prNudges(pr.ticket, pr.head, ctx);
    if (sent < nudgeLimit(ctx)) {
      return ctx.worker.queueNudge(pr.ticket, PR_NUDGE(pr),
        { once: ["pr", pr.head, w.idleSince!, ctx.every(minutes(5), w.idleSince!)] });
    } else {
      return escalate(pr.ticket, ctx, `its worker stayed idle through ${sent} nudges about ${pr.url} without pushing. Look at its herdr workspace.`,
        ["pr-ignored", pr.head, pr.ticket.statusSince]);
    }
  },
});
