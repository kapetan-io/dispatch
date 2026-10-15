import { whenever } from "dispatch";
import { nudgeable, heard, commentNudgeOwed } from "./shared";

// pr.autoMerge is true when the PR's repo table sets auto_merge in config.toml.
// A draft is never merged. Once per head, so a merge GitHub refuses isn't tried again
// until a push.
// The merge waits until the worker has been told about the latest review and the
// latest comment, however they arrive, so a "don't merge yet" comment always reaches
// the worker first, as today. Before it merges, the executor re-reads the ticket from
// Linear, so a comment posted since the last resync is seen and this rule decides again.
// A worker nudged since the last resync shows as working, so the merge waits for it.
whenever({
  name: "auto-merge",
  each: "github.pr",
  if:   (pr, ctx) => !pr.closed && !pr.draft && pr.ready && pr.approved && pr.autoMerge
           && pr.ticket.status === "in_review" && nudgeable(pr.ticket)
           && ctx.herdr.worker(pr.ticket).state === "idle" && heard(pr.ticket, "activity", pr.lastActivity, ctx)
           && !commentNudgeOwed(pr.ticket, ctx),
  // An ordered list. The notice runs once the merge is done, though the merge closed
  // the PR. It reaches followers even if the macOS alert fails, and is done once they
  // are told.
  then: (pr, ctx) => [
    ctx.github.queueMerge(pr, { once: ["merge", pr.head] }),   // only if the head hasn't moved
    ctx.notify.queueSend(pr.ticket, `${pr.ticket.id} was approved and merged: ${pr.url}`,
      { once: ["merged", pr.head], followersAlways: true }),
  ],
});
