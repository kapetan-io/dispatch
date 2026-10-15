import { whenever } from "dispatch";
import type { Tree } from "dispatch";

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

// One comment per lease says why the tree was kept. When you push, treehouse reports
// the tree clean, and teardown-clean-tree returns it.
whenever({
  name: "comment-kept-tree",
  each: "treehouse.tree",
  if:   (tree) => tree.leased && finishedHolder(tree) && !safeToReturn(tree),
  then: (tree, ctx) => ctx.linear.queueComment(tree.holder!,
           `The dispatcher kept this ticket's tree leased, because \`${tree.path}\` has ${tree.unfinished}. ` +
           `Returning it would discard that work. Push or discard it, and the tree is returned.`,
           { once: ["kept-tree", tree.path, tree.leasedSince] }),
});
