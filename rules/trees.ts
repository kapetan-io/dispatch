import { whenever } from "dispatch";
import type { Ctx, Tree } from "dispatch";

// True when returning the tree would lose nothing.
const finishedHolder = (tree: Tree) => tree.holder !== undefined && tree.holder.finished;

const safeToReturn = (tree: Tree) => tree.pushed && !tree.trackedChanges;

// A finished ticket's tree goes back to the pool once its work is pushed.
// If git state couldn't be read, this holds instead of guessing.
whenever({
  name: "teardown-clean-tree",
  each: "tree",
  if:   (tree) => tree.leased && finishedHolder(tree) && safeToReturn(tree),
  // The key carries when the tree last became safe, so a return refused for late work is
  // tried again once that work is pushed, and a tree that stays safe is refused only once.
  then: (tree, ctx) => ctx.tree.queueReturn(tree, { once: ["return", tree.path, tree.leasedSince, tree.cleanSince] }),
  // Closes the tree's workspace, inspects the tree again, deletes untracked files, then
  // returns it. Work found on the second look keeps the tree, and the receipt says why.
});

// A tree with unpushed work is kept. Its worker is closed so nothing more is written.
whenever({
  name: "stop-worker-on-kept-tree",
  each: "tree",
  if:   (tree, ctx) => tree.leased && finishedHolder(tree) && !safeToReturn(tree)
           && ctx.worker.in(tree).state !== "none",
  then: (tree, ctx) => ctx.worker.queueClose(ctx.worker.in(tree), { once: ["close", tree.path, ctx.worker.in(tree).seenAt] }),
});

// A return the executor refused, because the second look found work or an untracked
// nested repo that git clean left, has no fact on the tree to point at, so the receipt
// says why. Its detail is the reason alone, as the executor's return action writes it.
const refusedReturn = (tree: Tree, ctx: Ctx) =>
  ctx.ledger.receipts(tree.holder!, { kind: "return" })
    .find(r => r.outcome === "refused" && r.once[1] === tree.path && r.once[2] === tree.leasedSince);

// One comment per lease says why the tree was kept, and another when a reopened ticket
// finishes again: the key carries the holder's statusSince, which changes when it does.
// When you push, treehouse reports the tree clean, and teardown-clean-tree returns it.
whenever({
  name: "comment-kept-tree",
  each: "tree",
  if:   (tree, ctx) => tree.leased && finishedHolder(tree) && (!safeToReturn(tree) || refusedReturn(tree, ctx) !== undefined),
  then: (tree, ctx) => ctx.ticket.queueComment(tree.holder!,
           safeToReturn(tree)
             ? `The dispatcher kept this ticket's tree leased, because ${refusedReturn(tree, ctx)!.detail}. ` +
               `Returning it would discard that work. Push or discard it, and the tree is returned.`
             : `The dispatcher kept this ticket's tree leased, because \`${tree.path}\` has ${tree.unfinished}. ` +
               `Returning it would discard that work. Push or discard it, and the tree is returned.`,
           { once: ["kept-tree", tree.path, tree.leasedSince, tree.holder!.statusSince] }),
});
