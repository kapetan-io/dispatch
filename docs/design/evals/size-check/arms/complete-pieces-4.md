---
name: size-check
description: Decide whether a reviewed design ships as one pull request, and how to split it when it does not. Use after a design review, before creating build tickets.
---

# Size check

The goal is small deliverable pieces. Each piece is easy to review and ships a complete thing.

Each piece becomes one build ticket and one pull request. A design can hold several pieces, and each becomes its own ticket under that design.

A **piece** is complete when, after it merges, a user or caller can do something they could not do before. Its acceptance criteria are observable at the surface, such as an HTTP response, a CLI command's output, a protocol exchange or an exported function's result, and they pass without any other unmerged piece.

Group a piece by what a user does together, not by endpoint, flag or rule. "Recover a forgotten password" is one piece, even though it needs a reset request, an emailed link and a new-password form. "Edit your profile" is another piece. Splitting the reset flow into three pieces gives a user nothing complete to try.

Work with no surface of its own, such as a new seam, interface, backend, package, build pipeline or refactor, is never a piece by itself. It ships inside the first piece that a user can observe through it.

A design is too big for one pull request when it holds more than one piece. Line count alone never makes a design too big.

Easy to review sets the size in both directions. A design that a reviewer can read in one sitting, about an hour, fits even when it touches more than one piece, because splitting it buys the reviewer nothing. A piece that would take a reviewer more than one sitting is split again along what users do, and each part must still be complete.

Count only what the design commits to build. Work that it defers, or lists as a limitation, open question or future work, is not part of any piece.

When a design is too big, list its pieces in build order, and put first the smallest complete piece that the others build on, often the shared plumbing plus the simplest thing a user can do with it. Name the dependency that sets the order. Each piece becomes a build ticket, blocked by the pieces it builds on.
