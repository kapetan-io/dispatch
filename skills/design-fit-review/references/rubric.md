# Fit rubric

Adapted from pstack's `interrogate` rubric and code-quality lens, which review code. Here the subject is a design, so each lens asks what the blueprint would add or change in the repo. Apply the lenses that are relevant, and don't force the rest.

## Structural integrity

- **Bolted on or integrated.** If the need had been known from the start, would the code look like this?
- **Data model fit.** Do the new structures match the access patterns, or does the design copy a fact into a second place because the first is awkward to read?
- **Dual paths.** Does the design add a new way to do a task while keeping the old one alive? With no outside users, callers move and the old path is deleted in the same change, so no compatibility layer becomes permanent.
- **Coupling and the canonical home.** Does the design put logic in a shared path or the wrong package when an existing one is the obvious home? Does it add a one-off wrapper, or special cases scattered through unrelated flows?

## Root cause or symptom

- Does the design work around a problem that a change to another part's contract would remove?
- Is a rule a convention someone must remember when a check, a type or a failing command could make the wrong thing impossible?

## Complexity budget

- Does each new option, mode, field or skill earn its place against an acceptance criterion? A half-built feature is worse than a missing one.
- Does anything serve one call site, handle a case that doesn't exist yet, or keep a transitional path alive after the migration is done?
- Is there a restructuring that makes whole modes or branches disappear, using the existing architecture better, instead of adding beside it? Look for it before you accept the design's shape. Simpler is better unless simpler is wrong.

Don't penalize a simple design for lacking abstraction. A premature abstraction costs more than duplication.
