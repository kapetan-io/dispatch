---
name: design-fit-review
description: "Review a blueprint for fit with the existing system: independent reviewers check each new CLI command, flag, rule scenario API member, setting, history or ledger field, or skill against the closest thing the repo already has, and the lead judges which findings change the design. Use when `design` or `design-review` calls for it, or when asked whether a design duplicates something. Read-only: it reports a verdict and edits nothing."
---
# Review a blueprint for fit

Nothing else in `design` or `design-review` asks whether a new thing should exist. This is pstack's `interrogate`, run on a blueprint instead of a diff.

Spawn one reviewer per model to adversarially review the design. Each model gets the same prompt and rubric. The adversarial signal comes from model diversity, not assigned personas.

The deliverable is a synthesized verdict. Do NOT auto-apply changes. The user decides each Act On and Consider item, and the caller logs a new thing the user keeps over a finding as "you decided".

## Step 1, Determine Scope

The blueprint or contract sketch under review, written to a scratchpad file (`get_document` for a Linear blueprint) so reviewers read the file and never Linear. Add the paths of the `docs/CONTEXT.md` files and the code the design touches, and the user's fixed decisions as constraints.

## Step 2, State the Intent

Before spawning reviewers, state the intent explicitly. Derive this from:

- The feature ticket
- The blueprint's Objective

Write one clear paragraph. If you're unsure about the intent, ask the user before proceeding.

## Step 3, Spawn Reviewers

Launch both reviewers in a single message using the Agent tool.

| Subagent | Model |
|----------|-------|
| Reviewer A | `opus` |
| Reviewer B | `sonnet` |

For each reviewer, use `subagent_type` `general-purpose`, run it in the foreground, and tell it to edit nothing. If the Agent tool rejects a model, run that reviewer on the default model and say so.

Read `references/reviewer-prompt.md` and fill in the template with:
1. The stated intent
2. The blueprint path and the fixed decisions
3. The review rubric from `references/rubric.md`

The same filled template goes to all reviewers, so every model applies the same rubric.

## Step 4, Synthesize

As results come back, build a unified picture:

1. **Parse all findings** from the reviewers
2. **Identify consensus**. Findings raised by 2+ models independently are highest signal.
3. **Identify lone-model findings**. Still worth reading, but weight accordingly.
4. **Deduplicate**. Different models may describe the same issue differently. Merge these and note which models raised it.
5. **Note disagreements**. If one model flags something and another explicitly says the opposite, that's useful context for the verdict.

## Step 5, Lead Judgment

You are the lead reviewer, a pragmatic senior engineer, not a neutral aggregator.

Read `references/lead-judgment.md` for the full framework.

Categorize every finding using these buckets:

- **Act on**. Real issues affecting fit or maintainability given the actual goals. These would block a real design.
- **Consider**. Legitimate points, but you're not sure they outweigh the cost of addressing them right now. Worth the user's attention.
- **Noted**. Technically valid but not actionable. Context-dependent, premature optimization, or low-impact given the current stage.
- **Dismissed**. Wrong, nitpicky, or missing context. Brief explanation why.

For each finding, include:
- Which model(s) raised it
- The category (act on / consider / noted / dismissed)
- A one-line rationale for the categorization

## Output Format

Present the verdict in this structure:

### Intent
> [The stated intent paragraph from Step 2]

### Reviewers
- Reviewer [label]: [model name], [N findings] (one bullet per reviewer)

### New Things
[Each new thing the design adds, its closest existing thing with file:line, and keep / use existing / extend.]

### Act On
[Findings that should be addressed. For each: description, which models raised it, why it matters.]

### Consider
[Findings worth thinking about. For each: description, which models raised it, tradeoff involved.]

### Noted
[Valid but low-priority. Brief list.]

### Dismissed
[Rejected findings with brief rationale.]

### Agreement Map
[Where did models agree, where did they diverge, and what does the pattern of agreement/disagreement tell us?]

Adapted from pstack by Lauren Tan (MIT). Notice in `../principles/LICENSE-pstack`.
