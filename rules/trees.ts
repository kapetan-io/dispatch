import { whenever } from "dispatch";
import type { Ctx, Tree } from "dispatch";

// True when returning the tree would lose nothing.
const finishedHolder = (tree: Tree) => tree.holder !== undefined && tree.holder.finished;

const safeToReturn = (tree: Tree) => tree.pushed && !tree.trackedChanges;

// A finished ticket's tree goes back to the pool once its work is pushed.
// If git state couldn't be read, this holds instead of guessing.
whenever({
  name: "teardown-clean-tree",
  each: "treehouse.tree",
  if:   (tree) => tree.leased && finishedHolder(tree) && safeToReturn(tree),
  then: (tree, ctx) => ctx.treehouse.queueReturn(tree, { once: ["return", tree.path, tree.leasedSince] }),
  // Closes the tree's workspace, inspects the tree again, deletes untracked files, then
  // returns it. Work found on the second look keeps the tree, and the receipt says why.
});

// A tree with unpushed work is kept. Its worker is closed so nothing more is written.
whenever({
  name: "stop-worker-on-kept-tree",
  each: "treehouse.tree",
  if:   (tree, ctx) => tree.leased && finishedHolder(tree) && !safeToReturn(tree)
           && ctx.herdr.workerIn(tree).state !== "none",
  then: (tree, ctx) => ctx.herdr.queueClose(ctx.herdr.workerIn(tree), { once: ["close", tree.path, ctx.herdr.workerIn(tree).seenAt] }),
});

// A return the executor refused, because the second look found work or an untracked
// nested repo that git clean left, has no fact on the tree to point at, so the receipt
// says why. Its detail is "kept: <reason>", which treehouse.return in internal/executor writes.
const refusedReturn = (tree: Tree, ctx: Ctx) =>
  ctx.ledger.receipts(tree.holder!, { kind: "return" })
    .find(r => r.outcome === "refused" && r.once[1] === tree.path && r.once[2] === tree.leasedSince);

// One comment per lease says why the tree was kept, and another when a reopened ticket
// finishes again: the key carries the holder's statusSince, which changes when it does. When you push, treehouse reports the tree clean, and teardown-clean-tree
// returns it.
whenever({
  name: "comment-kept-tree",
  each: "treehouse.tree",
  if:   (tree, ctx) => tree.leased && finishedHolder(tree) && (!safeToReturn(tree) || refusedReturn(tree, ctx) !== undefined),
  then: (tree, ctx) => ctx.linear.queueComment(tree.holder!,
           safeToReturn(tree)
             ? `The dispatcher kept this ticket's tree leased, because ${refusedReturn(tree, ctx)!.detail.replace(/^kept: /, "")}. ` +
               `Returning it would discard that work. Push or discard it, and the tree is returned.`
             : `The dispatcher kept this ticket's tree leased, because \`${tree.path}\` has ${tree.unfinished}. ` +
               `Returning it would discard that work. Push or discard it, and the tree is returned.`,
           { once: ["kept-tree", tree.path, tree.leasedSince, tree.holder!.statusSince] }),
});
