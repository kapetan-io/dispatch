---
name: principle-confidence-from-many-perspectives
description: "Apply when you are about to call a design, plan, ticket, root-cause claim or change done. Ask how confident you are it will work, and get that confidence from independent reviews, not from your own certainty."
---

# Confidence from Many Perspectives

Before you call a design, plan, ticket, root cause or change done, ask what a lead asks developers before a deploy: "How confident are you it will work?" When the honest answer is "not very", go back instead of forward. Review again from a perspective you haven't used, add tests, or sharpen the acceptance criteria.

**Why:** Your own certainty is one perspective, and it is the one that wrote the mistake. Confidence comes from independent perspectives that tried to break the work and failed.

- **Different personas, none holding your conclusion.** Give each reviewer a different seat, such as the user or caller, the next maintainer, an operator, or a skeptic of the premise. Hand each the artifact and the question, not your conclusion or your session's history, and keep their answers apart so none sees another's.
- **The same ground rules for every reviewer.** Point each at the nearest `docs/CONTEXT.md` so they share one vocabulary, and tell them to coin no labels of their own. Name the principles (`dispatch:principles`) that bear on this work, so each judges against the same standards.
- **Perspectives differ by method as well as persona.** Reading the same code twice is one perspective. Measuring, running or reproducing it is another.
- **You choose how many and which,** scaled to the risk and scope of the change.
- **Say which perspectives checked the work, and which didn't,** wherever you publish the result. A claim that rests on one perspective and no cited source is a guess. Label it that way, as `dispatch:converse` says of anything unchecked.

**Example:** A root cause read from the code was confirmed by a second reviewer who read the same code, and both were wrong. Timing one call found the real cause. Two readers were one perspective, and the measurement was the second.

A step that already runs several reviewers counts toward this. Check which personas and methods it covers, and add only what is missing.
