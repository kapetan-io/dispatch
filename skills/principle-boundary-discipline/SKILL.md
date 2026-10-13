---
name: principle-boundary-discipline
description: "Apply when wiring validation, error handling, or adapters. Guard at system boundaries, trust internal types, keep business logic in pure functions."
---

# Boundary Discipline

Validate, narrow types and handle errors at system boundaries (CLI args, config, network, external APIs). Inside, trust the types and propagate errors. Business logic lives in pure functions the thin shell calls.

- Parse raw data into domain types at the boundary.
- Expose domain concepts, not the boundary's transport, storage or wire types. Keep general mechanism inside and special-purpose policy at the edge.
- Production callers hold domain types, and dependencies are injected as domain types.

**Tests:** Is this data crossing a boundary right now? If not, the check is redundant. Can this be a pure function the shell just calls? If yes, extract it.

Adapted from pstack by Lauren Tan (MIT). Notice in `principles/LICENSE-pstack`.
