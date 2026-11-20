import { whenever, exempt, minutes } from "dispatch";
import { nudgeable, heard, prNudges, escalate, stoppedSinceLastNudge, nudgeLimit, HELD_ACTIONS, onLastMessage } from "./shared";
import { ACTIVITY_NUDGE, PR_NUDGE } from "./texts";

// Each nudge's once key names what it told the worker, in the source's own time, such
// as the PR activity time or the head commit. A nudge is owed while there's something
// newer, so activity that arrives while the worker is stuck in a question dialog is still
// owed when the dialog closes. No nudge of any kind goes out until the worker has stopped
// since the last one (stoppedSinceLastNudge).

// A worker that took a message, typed or sent by dispatch, works on it before anything else lands
// on top: no nudge, no merge and no pr-ignored escalation until its turn ends, or MESSAGE_HOLD after
// the message when that turn end is lost. It covers an answered ask and any other message alike.
exempt({
  name:    "a-worker-finishes-its-message-first",
  each:    "ticket",
  actions: HELD_ACTIONS,
  if:      (t, ctx) => onLastMessage(t, ctx),
});

// Held by a-worker-finishes-its-message-first above, and in In Review by an-asking-worker-is-left-alone
// (asks.ts). Once per activity time, a GitHub time compared with GitHub times. A Needs Human worker
// hears of each review or comment even while it asks, since nobody else acts on them, and even on a
// PR that needs work, since no PR nudge goes out in Needs Human.
whenever({
  name: "nudge-pr-activity",
  each: "pr",
  if:   (pr, ctx) => !pr.closed && nudgeable(pr.ticket)
           && (pr.ticket.status === "in_review" && !pr.needsWork || pr.ticket.status === "needs_human")
           && ctx.worker.of(pr.ticket).state === "idle"
           && !heard(pr.ticket, "activity", pr.lastActivity, ctx) && stoppedSinceLastNudge(pr.ticket, ctx),
  then: (pr, ctx) => ctx.worker.queueNudge(pr.ticket, ACTIVITY_NUDGE(pr), { once: ["activity", pr.lastActivity!.at] }),
});

// Held by an-asking-worker-is-left-alone (asks.ts) and a-worker-finishes-its-message-first above,
// and its pr-ignored escalation is held too.
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
      return escalate(pr.ticket, ctx, `its worker stayed idle through ${sent} nudges about ${pr.url} without pushing. Look at its workspace.`,
        ["pr-ignored", pr.head, pr.ticket.statusSince]);
    }
  },
});
