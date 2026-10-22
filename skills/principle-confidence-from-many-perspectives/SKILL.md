---
name: principle-confidence-from-many-perspectives
description: "Apply when you are about to call a design, plan, ticket or root-cause claim done, or a change that would be costly if wrong. Ask how confident you are it will work, and get that confidence from independent reviews, not from your own certainty."
---

# Confidence from Many Perspectives

Before you call a design, plan, ticket, root cause, or a change that would be costly if wrong, done, ask what a lead asks developers before a deploy: "How confident are you it will work?" When the honest answer is "not very", go back instead of forward. Review again from a perspective you haven't used, add tests, or sharpen the acceptance criteria.

**Why:** Your own certainty is one perspective, and it is the one that wrote the mistake. Confidence comes from independent perspectives that tried to break the work and failed.

- **Different personas.** A perspective is a persona run by a method. Give each reviewer a persona, such as the user or caller, the next maintainer, an operator, or a skeptic of the premise. Hand each the artifact and the question, not your conclusion or session history, and keep answers apart so none just echoes another.
- **Same ground rules for every reviewer.** Point each at the nearest `docs/CONTEXT.md` for one vocabulary, tell it to coin no labels, and name the principles (`dispatch:principles`) that bear on the work, so each judges against the same standards.
- **Different methods.** Reading the same code twice is one perspective. Measuring, running or reproducing it is another.
- **You choose how many and which,** scaled to risk and scope.
- **Report coverage.** Where you publish the result, say which perspectives checked the work and which didn't. A claim resting on one perspective and no cited source is a guess. Label it that way, as `dispatch:converse` says of anything unchecked.

**Example:** A root cause read from the code was confirmed by a second reviewer reading the same code, and both were wrong. Timing one call found the real cause. Two readers were one perspective, and the measurement was the second.

A step that already runs several reviewers counts toward this. Check which personas and methods it covers, and add only what is missing.
