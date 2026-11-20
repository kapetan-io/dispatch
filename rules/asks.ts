import { whenever, exempt } from "dispatch";
import { HELD_ACTIONS } from "./shared";

// A worker that needs the user runs `dispatch ask`, and the facts asking and askedAt say so. Every
// rule about an ask lives here. A ticket's status never changes for one.

// Once per ask. askedAt stays the same across a restart, so a restarted dispatch says nothing new,
// and a second ask after an answer has a new time and is heard again. In Needs Human,
// notify-needs-human covers an ask from before the move, and an ask after it is a new question,
// such as one a worker repeats after handling a PR nudge.
whenever({
  name: "notify-asked",
  each: "ticket",
  if:   (t, ctx) => !t.hasChildren && ctx.worker.of(t).asking && (t.status === "in_progress" || t.status === "in_review"
           || t.status === "needs_human" && ctx.worker.of(t).askedAt! > t.statusSince),
  then: (t, ctx) => ctx.notify.queueSend(t, `${t.id}'s worker asked a question in its workspace: ${t.title}`,
           { once: ["asked", ctx.worker.of(t).askedAt!], reason: "worker asked a question" }),
});

// An asking worker waits on the user, so no rule nudges it, merges its PR or escalates it for
// ignoring PR nudges, whichever rule queued it, including one written later. dispatch tell still
// delivers. asking is false while the session log can't be read, so the actions go through then.
// In Needs Human only merges and escalations are held, so the worker hears of reviews and comments
// on its PR, since nobody else acts on them.
exempt({
  name:    "an-asking-worker-is-left-alone",
  each:    "ticket",
  actions: HELD_ACTIONS,
  if:      (t, ctx) => ctx.worker.of(t).asking && t.status !== "needs_human",
});

exempt({
  name:    "no-merge-or-escalation-while-asking-in-needs-human",
  each:    "ticket",
  actions: HELD_ACTIONS.filter(a => a !== "nudge"),
  if:      (t, ctx) => ctx.worker.of(t).asking && t.status === "needs_human",
});

// The asked notification stands in for the idle one, and a ready PR waits for the answer. Naming
// the topics also holds notify-stacked-ready when rules/stacking.ts is loaded, since it shares them.
exempt({
  name:    "an-asking-worker-hears-one-notice",
  each:    "ticket",
  actions: [{ kind: "notify", topic: "idle" }, { kind: "notify", topic: "ready" },
            { kind: "notify", topic: "draft-ready" }, { kind: "notify", topic: "approved-ready" }],
  if:      (t, ctx) => ctx.worker.of(t).asking,
});
