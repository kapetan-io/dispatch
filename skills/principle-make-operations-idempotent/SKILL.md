---
name: principle-make-operations-idempotent
description: "Apply when designing commands, lifecycle steps or loops that run amid crashes, restarts and retries. Converge to the same end state however many times they run."
---

# Make Operations Idempotent

Retries and restarts are normal, so a state change must not depend on what a previous run left behind. For each one ask:

1. What happens if this runs twice in a row?
2. What happens if the previous run crashed at every possible point?
3. Does re-running converge to the same end state?

If any answer is "it depends on what state was left behind", add a reconciliation step. Scan for existing state, then clean up or adopt it, instead of assuming a fresh start.

Adapted from pstack by Lauren Tan (MIT). Notice in `principles/LICENSE-pstack`.
