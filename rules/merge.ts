import { whenever } from "dispatch";
import { nudgeable, heard, mergeOnce, mergeRetryPaused } from "./shared";

// pr.autoMerge is true when the PR's repo table sets auto_merge in config.toml.
// A draft is never merged. Once per head, so a merge GitHub refuses isn't tried again
// until a push, except a base-moved refusal, which shared.ts retries.
// The merge waits until the worker has been told about the latest review, however it
// arrives. A worker nudged since the last resync shows as working, so the merge waits for it.
whenever({
  name: "auto-merge",
  each: "pr",
  if:   (pr, ctx) => !pr.closed && !pr.draft && pr.ready && pr.approved && pr.autoMerge
           && pr.ticket.status === "in_review" && nudgeable(pr.ticket)
           && ctx.worker.of(pr.ticket).state === "idle" && heard(pr.ticket, "activity", pr.lastActivity, ctx)
           && !mergeRetryPaused(pr, ctx),
  // An ordered list. The notice runs once the merge is done, though the merge closed
  // the PR.
  then: (pr, ctx) => [
    ctx.pr.queueMerge(pr, { once: mergeOnce(pr, ctx) }),   // only if the head hasn't moved, once per retry
    ctx.notify.queueSend(pr.ticket, `${pr.ticket.id} was approved and merged: ${pr.url}`,
      { once: ["merged", pr.head], reason: "PR approved" }),
  ],
});

// A merge closes a Linear ticket through Linear's GitHub integration. Nothing closes a local ticket,
// so this does, from any status outside a finished one. pr.merged is true only for a local ticket's
// PR that merged into the default branch. A merge older than the ticket's current status belongs to
// an earlier round of work, so a ticket moved back out of Done is not closed again. The ticket goes
// to the status that does blocker_done, which is Done in a new database. The system test is
// redundant with pr.merged, which is false for Linear, and keeps the rule's reach visible.
whenever({
  name: "finish-merged-local-ticket",
  each: "pr",
  if:   (pr) => pr.merged && pr.ticket.system === "local" && !pr.ticket.finished
           && pr.mergedAt > pr.ticket.statusSince,
  then: (pr, ctx) => ctx.ticket.queueMove(pr.ticket, "blocker_done", "its PR merged",
                                          { once: ["merged", pr.head] }),
});
