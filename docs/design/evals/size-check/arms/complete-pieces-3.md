---
name: size-check
description: Decide whether a design is one small, complete, reviewable piece or must be split, and name the first piece to design. Use whenever a blueprint, spec or design document is about to become build tickets or a pull request, and whenever someone asks whether a feature is too big, how to slice it, or what to build first, even if they never say "size check".
---

# Size check

Aim for small deliverable pieces. Each piece is easy to review and ships a complete thing. A reviewer can only vouch for what they can read carefully, a user can only judge what they can try, and a convention that ships early gets corrected before later pieces copy it.

One design becomes one pull request, and one pull request is one piece.

## What a piece is

A piece is complete when, after it merges, a user or caller can do something they could not do before. Its acceptance criteria are observable at the surface, such as an HTTP response, a CLI command's output, a protocol exchange or an exported function's result, and they pass without any other unmerged piece.

Group a piece by what a user does together, not by endpoint, flag or rule. "Recover a forgotten password" is one piece, even though it needs a reset request, an emailed link and a new-password form. "Edit your profile" is another piece. Split the reset flow into three and no one can try any part of it, so no one can tell whether it works.

Work with nothing to observe on its own, such as a seam, interface, backend, package, build pipeline or refactor, rides inside the first piece that a user can observe through it. Reviewed alone, it can only be judged by guessing how it will be used.

Count only what the design commits to build. Work it defers, or lists as a limitation, open question or future work, was already decided against for now, so it belongs to no piece.

## How to decide

1. List the pieces the design commits to build.
2. If a careful reviewer can read the whole design in one sitting, about an hour, it fits, even when it touches more than one piece. Splitting it buys the reviewer nothing and adds a review cycle.
3. Otherwise, a design with more than one piece is too big.
4. A single piece that needs more than one sitting is too big when it splits along what users do into parts that are each complete. When it can't split that way, it fits, because it is one piece however long it runs.

Line count alone decides nothing. A long change to one piece can be easy to review, and a short one spread across several can be hard.

## When the design is too big

List the pieces in build order. Put first the smallest complete piece that the others build on, often the shared plumbing plus the simplest thing a user can do with it, and name the dependency that sets the order.

Only the first piece is designed now. Each later piece gets its own design after the piece before it ships, because building the first one teaches things that change the later designs, and a design written before that lesson locks in guesses.
