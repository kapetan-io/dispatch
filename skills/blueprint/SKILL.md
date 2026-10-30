---
name: blueprint
description: "Write or update a blueprint, the Linear document on a feature ticket that holds the feature's product definition and technical design. Carries the outline, the saving rules and how to patch a saved blueprint without damaging it. Use when writing, reading or updating a blueprint, including patching it after review feedback, adding a `Glossary changes` or `ADR:` section, checking its contract spec document, and when `dispatch:design` or `dispatch:design-review` tells you to."
---

# Blueprint

A blueprint is one document holding a feature's product definition and technical design.

## Writing

Save the blueprint as a Linear document on the feature ticket, not under `docs/features/`. Write one document, product half first and then the technical half, with only the sections the discussion covered:

Objective, Mental Model, User Stories, Correctness Constraints (State Invariants, Behavioral Constraints), Acceptance Criteria, Success Metrics, Scope (In Scope, Out of Scope), Dependencies and Constraints, then Functional, Architecture, Data Design (Invariant Preservation, Illegal State Analysis), Security, PII, Scale, Testing, Limitations and Future Work, Open Questions, Decision Log (every entry marked "you decided" or "agent assumed").

Keep PII as its own section. Mark anything unresolved with `TODO:` and a line on what is missing. Keep it short, since a section that drives no decision is noise. Write for a reader who was not in the discussion, with no "the option we discussed". "We considered X but chose Y because Z" is useful.

When a question can't be settled now, insert `[NEEDS CLARIFICATION: <question>]` and continue with the best assumption.

When a run settled a question, record the question, the observation and the script in the blueprint.

State the principle that a domain must not contain logic that belongs to a domain it calls when an interface has several implementations.

If the feature has an OpenAPI spec, the contract sketch (the surface a consumer or test binds to) becomes a second document on the ticket, as a fenced YAML block. If the blueprint defines or changes a consumer-facing contract, that spec document must list every endpoint, schema and error code the blueprint names, and nothing it doesn't.

If the discussion resolved domain terms, add a `Glossary changes` section to the blueprint in the format of [CONTEXT-FORMAT.md](../design/CONTEXT-FORMAT.md), naming the `CONTEXT.md` each term belongs in. Your working tree is discarded when you finish, so the first build piece commits the terms, as `dispatch:breakdown` arranges. Keep the section current with the terms a review corrected or added.

An ADR the user approved goes in as an `ADR: <title>` section in the format of the ADRs already in `docs/adr/`, unnumbered. The first piece that implements it commits it under the next free `NNNN-` number.

## Updating a saved blueprint

Drafts of a blueprint you are writing can be saved whole. A blueprint saved before your session is changed only by patch.

Read the blueprint with `get_document` on the feature ticket. Linear rewrites markdown on save: bullets come back as `*`, a leading H1 is dropped and lines can gain blanks. Apply each fix as a `save_document` patch whose old text you copied from a fresh `get_document`, and read again before the next patch. Never push the whole document back.

Keep unchanged sections in their order, and replace each resolved `[NEEDS CLARIFICATION]` marker with the decision.

When reviewing a saved blueprint, log each user answer or dismissal in the blueprint's Decision Log as "you decided (review)" as you get it, so a rerun doesn't ask again.
