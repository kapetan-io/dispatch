import { whenever } from "dispatch";
import { isDesign, startableChild, openBlockers, abandonedBlocker } from "./shared";

// A parent closes once every child is finished. If the children couldn't be read,
// this holds. A parent with no children never closes either, as today, because
// every() is true of an empty list.
whenever({
  name: "close-parent",
  each: "linear.ticket",
  if:   (t, ctx) => t.status === "in_progress" && t.hasChildren && !isDesign(t)
           && ctx.linear.children(t).length > 0 && ctx.linear.children(t).every(c => c.finished),
  then: (t, ctx) => ctx.linear.queueMove(t, "close_parent", "every child is finished",
           { once: ["close-parent", t.statusSince] }),
});

// Promoting waits for a slot, like a launch, and holds it until a Linear read shows the
// child In Progress. The re-read after the move makes that immediate. A child that is
// itself a parent takes no slot.
whenever({
  name: "promote-child",
  each: "linear.ticket",
  if:   (c, ctx) => startableChild(c, ctx) && openBlockers(c, ctx).length === 0 && !abandonedBlocker(c, ctx),
  then: (c, ctx) => ctx.linear.queueMove(c, "in_progress", `child of ${ctx.linear.parent(c)!.id}`,
           { uncapped: c.hasChildren, once: ["promote", c.statusSince] }),
});
