---
name: principle-question-the-requirement
description: "Apply when a requirement is about to cost structure: you are adding a mechanism, a special case, a criterion or a threshold to satisfy it, widening its scope, or repairing a feature to keep it."
---

# Question the Requirement

Requirements traceability, applied before design. Every requirement, criterion and number traces to a problem someone had. One that traces to nothing is a guess, however settled it reads.

**Why:** An untraced requirement drives structure just as hard as a real one. Finding out it was a guess is cheaper before the design is built around it than after.

- **Name the problem or put the requirement to the arena.** For each requirement, criterion and number, find the failure it prevents or the need it meets: a failing case, an incident, a user need in the design. Found one? Keep it. Found nothing? Run `dispatch:arena` on the question, with the design sketched with and without the requirement, and go with its answer. Record the outcome as "agent assumed" so the designer can override it. Bring the designer only a case where you disagree with the arena's answer. The same test applies before repairing a feature: name who still uses it.
- **A number without a need is not a requirement.** A time bound or size limit the author invented becomes the design driver. Never restructure a system to meet one.
- **A design that needs a story is serving a requirement that may not exist.** When explaining the design takes a chain of special cases, state the underlying problem plainly first. Often it is not there.
