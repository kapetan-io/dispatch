---
name: size-check
description: Decide whether a reviewed design ships as one pull request, and how to split it when it does not. Use after a design review, before creating build tickets.
---

# Size check

A build worker turns this design into exactly one pull request, working alone, in one repo. One reviewer reviews that pull request.

Decide whether this design can ship as one pull request.

Give your reason in at most 5 sentences, citing the design.

If it is too big, list the separate designs you would split it into, each one shippable as one pull request, in build order, with a reason for the order.
