---
name: size-check
description: Decide whether a reviewed design ships as one pull request, and how to split it when it does not. Use after a design review, before creating build tickets.
---

# Size check

A design is too big for one pull request when it changes more than two architectural layers, or when it will likely exceed 1,000 changed lines.

When a design is too big, split it by layer. Storage and the data model come first, then domain logic, then the API or protocol surface, then tests and documentation. Each layer becomes its own design, in that order.
