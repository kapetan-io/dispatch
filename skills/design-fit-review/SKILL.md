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

Prompt: you are an adversarial reviewer of a design. The intent is `{INTENT}`; the design is at `{BLUEPRINT_PATH}`. List every new public or persisted thing it adds (CLI command, flag, rule scenario API member, setting, history or ledger field, skill). For each, search before you name anything (`git grep`, the command list, the schema) and report:

- **Closest existing thing**, with file and line, or "none found" and what you searched.
- **Finding**, only if one of these holds. Could an existing thing absorb it? Is it a second way to do one task, or a second copy of one fact, including an option that restates an existing one? Is it bolted on, so it would look different if the need had been known from the start? Could it go without losing an acceptance criterion? Say what the existing thing already covers and what the design drops. If the existing thing can't take the need, say why in terms of what it holds or does.
- **Remove first**: anything that already exists and should go before the design builds on it.
- **Severity**: `critical` (duplicates a fact or behavior, so the two will drift), `warning` (overlaps, and the design should widen the existing thing), `nit`.
- **Evidence**: the file and line you read for every claim about the code. Label a guess as a guess. Don't praise the design. Zero findings is a valid review.

Report only what fails, most important first, then one line listing what passed. Keep it under 700 words.

Ask: if the intent had been known from the start, would the code look like this? Use the `docs/CONTEXT.md` terms and the words the code already uses, coin no labels such as step codes, and define any term in neither.

## Step 4, Synthesize

Parse both reports, merge findings that describe the same overlap, and mark which model raised each. A finding both raised independently is the strongest signal. Note where one reviewer found a closest existing thing and the other said "none found".

## Step 5, Lead judgment

You are the lead, a pragmatic senior engineer, not an aggregator. Reviewers see a slice of the repo, so check each finding against the cited line before you rank it. A similar name is not a similar job, so read the code a finding cites before acting on it. Dismiss "I would have done it differently" and findings that miss a constraint the conversation or an ADR already settles. Don't dismiss a consensus finding because it is inconvenient. Check a "speculative, no acceptance criterion" finding against the ticket's notes and the Objective as well as the criteria, since a note may give the reason. Hold a finding that merges two things to the same bar as one that adds a thing, because the merge can hide a mode inside the existing one.

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
