---
name: principle-confidence-from-many-perspectives
description: "Apply when you are about to call a design, plan, ticket, root-cause claim or change done. Ask how confident you are it will work, and get that confidence from independent reviews, not from your own certainty."
---

# Confidence from Many Perspectives

Before you call a design, plan, ticket, root cause or change done, ask what a lead asks developers before a deploy: "How confident are you it will work?" When the honest answer is "not very", go back instead of forward. Review again from a perspective you haven't used, add tests, or sharpen the acceptance criteria.

**Why:** Your own certainty is one perspective, and it is the one that wrote the mistake. Confidence comes from independent perspectives that tried to break the work and failed.

- **Different personas, no shared context.** Give each reviewer a different seat, such as the user or caller, the next maintainer, an operator, or a skeptic of the premise. Start each without your context, in parallel, so none sees another's answer. Hand it the artifact and the question, not your conclusion. A "perspective" below means one persona run by one method.
- **Perspectives differ by method as well as persona.** Reading the same code twice is one perspective. Measuring, running or reproducing it is another.
- **You choose how many and which.** Scale them to the risk and scope of the change. A one-line fix needs fewer than a public contract.
- **Say which perspectives checked the work, and which didn't,** wherever you publish the result (blueprint, ticket body, PR body, or the comment that states a root cause). A claim that rests on one perspective and no cited source is a guess. Label it that way, as `dispatch:converse` says of anything unchecked.

**Example:** A root cause for slow tests ("each stand-in claude start costs about 1 second under the race detector") came from reading code. A second reviewer read the same code and agreed. It went into two tickets and a merged skill before ENG-495 timed one call and found the race detector's 1-second exit sleep instead. Two readers were one perspective, and the measurement was the second.

Steps that already run several reviewers (`dispatch:ticket`'s "Review before you save", `dispatch:arena`, `dispatch:design-review`, `dispatch:build`'s code review) count toward this. Check which personas and methods they cover, and add only what is missing.
