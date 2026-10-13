---
name: principle-minimize-reader-load
description: "Apply when code is hard to trace. Count the layers between question and answer and the hidden state a reader must hold, then collapse one-caller wrappers and shrink mutable scope."
---

# Minimize Reader Load

Reader load has two independent axes. A flat file with 50 globals is as hard to follow as a six-layer adapter stack.

1. **Layers to trace.** Inline wrappers with one caller, adapters with no second implementation, and layers that repeat the next layer's methods.
2. **State to hold.** Prefer returns over mutations, locals over fields, fields over module state. Derive instead of sync, and name an invariant once at the boundary instead of in every consumer.

Prefer a simple interface that hides real decisions. A broad interface that hides little makes readers learn both the surface and the implementation. If answering the question needs more than three files, flatten it.

**Test:** can a new reader answer "where does X come from?" and "what can change X?" in 30 seconds? If not, cut layers or state.

Adapted from pstack by Lauren Tan (MIT). Notice in `principles/LICENSE-pstack`.
