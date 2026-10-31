---
name: blueprint
description: "Write or update a blueprint, the Linear document on a feature ticket that holds the feature's product definition and technical design. Carries the outline, the saving rules and how to patch a saved blueprint without damaging it. Use when writing, reading or updating a blueprint, including patching it after review feedback, adding a `Glossary changes` or `ADR:` section, checking its contract spec document, and when `dispatch:design` or `dispatch:design-review` tells you to."
---

# Blueprint

A blueprint is one document holding a feature's product definition and technical design.

## Writing

Save the blueprint as a Linear document on the feature ticket, not under `docs/features/`. Write one document, product half first and then the technical half, with only the sections the discussion covered:

Objective, Mental Model, User Stories, Correctness Constraints (State Invariants, Behavioral Constraints), Acceptance Criteria, Success Metrics, Scope (In Scope, Out of Scope), Dependencies and Constraints, then Functional, Architecture, Data Design (Invariant Preservation, Illegal State Analysis), Security, PII, Scale, Testing, Limitations and Future Work, Open Questions, Decision Log (every entry marked "you decided" or "agent assumed").

Keep PII as its own section. Keep it short, since a section that drives no decision is noise. Write for a reader who was not in the discussion, with no "the option we discussed". "We considered X but chose Y because Z" is useful.

When a question can't be settled now, insert `[NEEDS CLARIFICATION: <question>]` and continue with the best assumption. A question the build waits on is a marker, not an Open Question, because breakdown leaves open questions out of every piece.

Level of detail. Leave a detail out if a reader of the blueprint could write it without losing the contract. Keep it if losing it would change what a user, caller or operator depends on. That includes text and formats something else parses (CLI output a hook reads, comment headings another skill matches, log lines someone greps, history file fields), and for a changed format, its migration order. Pin a signature only where a consumer or test binds to it. Describe an internal surface's responsibility, inputs and outputs, invariants and error categories, and leave its signature to the implementor, who sees the real types. A schema is pinned in full, and the interface that wraps it is not.

Link only what a build worker can open, never a scratchpad path or a session transcript. Refer to stories by title, not number, so an edit doesn't renumber them.

When a run settled a question, record the question, the observation and the script in the blueprint.

State the principle that a domain must not contain logic that belongs to a domain it calls when an interface has several implementations.

If the feature has an OpenAPI spec, the contract sketch (the surface a consumer or test binds to) becomes a second document on the ticket, as a fenced YAML block. If the blueprint defines or changes a consumer-facing contract, that spec document must list every endpoint, schema and error code the blueprint names, and nothing it doesn't.

If the discussion resolved domain terms, add a `Glossary changes` section to the blueprint in the format of [CONTEXT-FORMAT.md](../design/CONTEXT-FORMAT.md), naming the `CONTEXT.md` each term belongs in. Your working tree is discarded when you finish, so the first build piece commits the terms, as `dispatch:breakdown` arranges. Keep the section current with the terms a review corrected or added.

The ADR test: offer an ADR only for a decision that is hard to reverse, surprising without context, and a real trade-off. An ADR the user approved goes in as an `ADR: <title>` section in the format of the ADRs already in `docs/adr/`, unnumbered. The first piece that implements it commits it under the next free `NNNN-` number.

## Updating a saved blueprint

Drafts of a blueprint you are writing can be saved whole. A blueprint saved before your session is changed only by patch.

Read the blueprint with `get_document` on the feature ticket. Linear rewrites markdown on save: bullets come back as `*`, a leading H1 is dropped and lines can gain blanks. Apply each fix as a `save_document` patch whose old text you copied from a fresh `get_document`, and read again before the next patch. Never push the whole document back.

Keep unchanged sections in their order, and replace each resolved `[NEEDS CLARIFICATION]` marker with the decision.

When designing or reviewing a blueprint, log each user answer or dismissal, an ADR offer and its answer included, in the Decision Log as you get it ("you decided (review)" in a review), so a rerun doesn't ask again.
