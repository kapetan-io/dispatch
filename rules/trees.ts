import { whenever, known } from "dispatch";
import type { Ctx, Ticket, Tree } from "dispatch";
import { pausedChild } from "./shared";

// In Progress and In Review tickets keep their trees, because dispatch relaunches a worker into them.
const relaunchable = (holder: Ticket) => holder.status === "in_progress" || holder.status === "in_review";

// What a return throws away. Commits are not in it, because the ticket's branch keeps them.
const discards = (tree: Tree) => tree.trackedChanges || tree.untracked !== "";

// A tree full of untracked files must not make a comment the ticket system refuses.
const MAX_LISTED = 20;
const listUntracked = (tree: Tree) => {
  const names = tree.untracked.split("\n");
  return names.slice(0, MAX_LISTED).join(", ") + (names.length > MAX_LISTED ? `, and ${names.length - MAX_LISTED} more` : "");
};

// A tree goes back to the pool once no worker runs in it, unless its ticket is In Progress or In
// Review, where dispatch relaunches a worker into the same tree. A Todo child of an In Progress
// parent keeps its tree, because a held tree is how dispatch marks a child paused on purpose.
// A finished ticket's tree goes back even with a worker in it, as long as the worker has left
// nothing to discard; stop-worker-on-kept-tree closes a worker that has.
// internal/world inspectTrees reads git for the same trees, so keep the two in step: a tree it
// skips reads Unknown here, and the rule holds. If git state couldn't be read, this holds too.
const returnable = (tree: Tree, ctx: Ctx) => {
  const holder = tree.holder!;
  // A holder that isn't one of your tickets has no status, and its tree is never yours to return.
  return tree.leased && known(() => holder.status) && !relaunchable(holder)
    && !pausedChild(holder, ctx)
    && ((holder.finished && !discards(tree)) || ctx.worker.in(tree).state === "none");
};

const discardKey = (tree: Tree) => ["discarded-tree", tree.path, tree.leasedSince, tree.cleanSince];

// The comment naming the discarded work has landed for the tree as it reads now.
const noted = (tree: Tree, ctx: Ctx) => {
  const key = discardKey(tree);
  return ctx.ledger.receipts(tree.holder!, { kind: "comment" })
    .some(r => r.outcome === "done" && key.every((part, i) => r.once[i] === part));
};

whenever({
  name: "teardown-clean-tree",
  each: "tree",
  if:   (tree, ctx) => returnable(tree, ctx) && (!discards(tree) || noted(tree, ctx)),
  // The key carries when the tree last changed, so a return refused for late work is tried
  // again once that work is gone, and a tree that stays as it is is refused only once.
  then: (tree, ctx) => ctx.tree.queueReturn(tree, { discard: discards(tree), once: ["return", tree.path, tree.leasedSince, tree.cleanSince] }),
  // Closes the tree's workspace, inspects the tree again, deletes untracked files and
  // uncommitted changes, then returns it. The ticket's branch and commits stay.
});

// The loss goes on the ticket before the tree is returned, because the tree is gone after.
whenever({
  name: "comment-discarded-work",
  each: "tree",
  if:   (tree, ctx) => returnable(tree, ctx) && discards(tree),
  then: (tree, ctx) => ctx.ticket.queueComment(tree.holder!,
           `The dispatcher is returning \`${tree.path}\` to its pool, because no worker runs in it. ` +
           `It discards ${[tree.trackedChanges ? "uncommitted changes to tracked files" : "",
                          tree.untracked !== "" ? `untracked files (${listUntracked(tree)})` : ""]
                         .filter(part => part !== "").join(" and ")}. ` +
           `Branch \`${tree.holder!.branch}\` and its commits stay.`,
           { once: discardKey(tree) }),
});

// A finished ticket's worker that left work to discard is closed, so nothing more is written
// before the work is noted and the tree is returned.
whenever({
  name: "stop-worker-on-kept-tree",
  each: "tree",
  if:   (tree, ctx) => tree.leased && tree.holder!.finished && discards(tree)
           && ctx.worker.in(tree).state !== "none",
  then: (tree, ctx) => ctx.worker.queueClose(ctx.worker.in(tree), { once: ["close", tree.path, ctx.worker.in(tree).seenAt] }),
});

// A return the executor refused, because the second look found an untracked nested repo
// that git clean left, has no fact on the tree to point at, so the receipt says why. Its
// detail is the reason alone, as the executor's return action writes it.
const refusedReturn = (tree: Tree, ctx: Ctx) =>
  ctx.ledger.receipts(tree.holder!, { kind: "return" })
    .find(r => r.outcome === "refused" && r.once[1] === tree.path && r.once[2] === tree.leasedSince);

// One comment per lease says why the tree stayed. Remove the nested repository, and
// teardown-clean-tree tries the return again.
whenever({
  name: "comment-kept-tree",
  each: "tree",
  if:   (tree, ctx) => tree.leased && known(() => tree.holder!.status) && !relaunchable(tree.holder!)
           && refusedReturn(tree, ctx) !== undefined,
  then: (tree, ctx) => ctx.ticket.queueComment(tree.holder!,
           `The dispatcher kept this ticket's tree leased, because ${refusedReturn(tree, ctx)!.detail}. ` +
           `Returning it would discard that work. Remove it, and the tree is returned.`,
           { once: ["kept-tree", tree.path, tree.leasedSince, tree.holder!.statusSince] }),
});
