---
name: principle-laziness-protocol
description: "Apply when refactoring, sizing a diff, or tempted to add an abstraction, a layer, or a new flag passed through several layers. Prefer deletion and the smallest change."
---

# Laziness Protocol

Get the most result with the least code.

- **Prefer deletion.** When asked to refactor or improve, look for removals before additions.
- **Minimize the diff.** The smallest change that solves the problem beats elegant boilerplate.
- **Consolidate decisions.** Make each choice in one place and pass the result on as a simple flag.
- **Question the threading.** If a task has you pass a new signal through several layers, look for a more direct path.

**Test:** would a developer maintaining this find it exhausting? Then it is a bad solution.

Adapted from pstack by Lauren Tan (MIT). Notice in `principles/LICENSE-pstack`.
