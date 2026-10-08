---
name: size-check
description: Decide whether a reviewed design ships as one pull request, and how to split it when it does not. Use after a design review, before creating build tickets.
---

# Size check

One design becomes one pull request. A pull request is one change with its own evidence.

A **change** is one capability that a user or caller could start using without the others. It can be stated in one sentence without "and". "Users can reset a forgotten password" is one change. "Users can reset a password and change their email" is two, because a user could use either one without the other.

**Its own evidence** means the change's acceptance criteria are observable at the surface, such as an HTTP response, a CLI command's output, a protocol exchange or an exported function's result, and they pass or fail without any other unmerged change.

Work with no surface of its own is never a change by itself. A new seam, interface, backend, package or refactor ships inside the first change that a user can observe through it.

A design is too big when it holds more than one change. Size alone never makes a design too big. A design that delivers one capability is one change, however many lines it takes.

Count only what the design commits to build. Work that it defers, or lists as a limitation, open question or future work, is not part of the design.

When a design is too big, split it into separate designs, one change each. Put first the smallest change that the others build on, and name the dependency that sets the order. Prefer small designs.
