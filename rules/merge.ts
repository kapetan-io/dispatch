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
  // the PR. It reaches followers even if the macOS alert fails, and is done once they
  // are told.
  then: (pr, ctx) => [
    ctx.pr.queueMerge(pr, { once: mergeOnce(pr, ctx) }),   // only if the head hasn't moved, once per retry
    ctx.notify.queueSend(pr.ticket, `${pr.ticket.id} was approved and merged: ${pr.url}`,
      { once: ["merged", pr.head], followersAlways: true, reason: "PR approved" }),
  ],
});
