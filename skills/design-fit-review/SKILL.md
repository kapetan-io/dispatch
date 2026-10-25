---
name: design-fit-review
description: "Review a blueprint for fit with the existing system: independent reviewers check each new CLI command, flag, rule scenario API member, setting, history or ledger field, or skill against the closest thing the repo already has, and the lead judges which findings change the design. Use when `design` or `design-review` calls for it, or when asked whether a design duplicates something. Read-only: it reports a verdict and edits nothing."
---

# Review a blueprint for fit

Nothing else in `design` or `design-review` asks whether a new thing should exist. A design can add a second CLI command or a second copy of a fact the repo already holds, and nobody notices until the code is written. This follows pstack's `interrogate` (reviewers from independent angles, then a lead's judgment), run on a blueprint instead of a diff. Do not apply changes. The caller acts on the verdict.

## Step 1, Determine scope

The blueprint or contract sketch under review, written to a scratchpad file (`get_document` for a Linear blueprint) so reviewers read the file and never Linear. Add the paths of the `docs/CONTEXT.md` files and the code the design touches, and the user's fixed decisions as constraints, so reviewers don't re-argue them.

## Step 2, State the intent

One paragraph, from the feature ticket and the blueprint's Objective: the problem and the outcome wanted. Reviewers challenge how the design meets it, not the intent.

## Step 3, Spawn reviewers

Launch two foreground `general-purpose` sub-agents in one message, one on `opus` and one on `sonnet`, and tell them to edit nothing. They get the same prompt and see the blueprint and the repo but not this conversation, so their read is not anchored on the design's own justification. The adversarial signal comes from model diversity, not assigned angles. Tell them to load `dispatch:principle-subtract-before-you-add`.

Read [`references/reviewer-prompt.md`](references/reviewer-prompt.md), fill in the intent and the blueprint path, and send it with Step 1's constraints and context paths.

## Step 4, Synthesize

Parse both reports, merge findings that describe the same overlap, and mark which model raised each. A finding both raised independently is the strongest signal. Note where one reviewer found a closest existing thing and the other said "none found".

## Step 5, Lead judgment

Read [`references/lead-judgment.md`](references/lead-judgment.md) before you rank anything. Check each finding against the cited line, since a reviewer sees only a slice of the repo.

Put every finding in one bucket, with the models that raised it and a one-line reason:

- **Act on.** The existing thing covers the need, so the design drops the new one or widens the existing one.
- **Consider.** A real overlap where the new thing may still earn its place.
- **Noted.** True and not worth changing now.
- **Dismissed.** Wrong, or missing context. Say why.

More than 5 under Act on means you are not filtering hard enough.

## Output

```
### Intent
> <the paragraph>

### Reviewers
- opus: N findings
- sonnet: N findings

### New things
<each new thing, its closest existing thing with file:line, and keep / use existing / extend>

### Act on
### Consider
### Noted
### Dismissed

### Agreement map
<where the models agreed or diverged, and what that says>
```

A finding changes the design, so the user decides each Act on and Consider item. The caller logs a new thing the user keeps over a finding as "you decided".
