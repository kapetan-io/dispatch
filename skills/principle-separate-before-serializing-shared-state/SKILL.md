---
name: principle-separate-before-serializing-shared-state
description: "Apply when concurrent actors might write the same file, branch, key or object. Remove the sharing first, and serialize only when one shared writer is a real invariant."
---

# Separate Before Serializing Shared State

Ask first whether the actors need the same mutable object. Usually they are publishing independent facts, so give each its own file, key or branch and merge only at the read boundary. Two workers each writing their own field into one `state.json` is still shared mutation, while `indexer-state.json` plus `metrics-state.json` is not.

When one shared writer is a real invariant, enforce it structurally with a lockfile, sequential phases, a single-writer actor or compare-and-swap. A convention is not concurrency control. Treat "we need a lock" as a design smell to check, not the default answer.

Adapted from pstack by Lauren Tan (MIT). Notice in `principles/LICENSE-pstack`.
