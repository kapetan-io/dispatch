---
name: how
description: "Use for \"how does X work\", code walkthroughs before changing something, and placement / ownership / layering questions (\"where should this live\", \"which package owns this\", \"is this the right layer\"). Explains subsystem architecture, runtime flow, onboarding mental models. Use why for motivation."
---

# How

Answer "how does X work?" at the level of a senior engineer onboarding onto a subsystem: a working mental model, not annotated source.

Each spawn below sets the Agent tool's `model` parameter. Valid values are `sonnet`, `opus`, `haiku` and `fable`. Explorers default to `sonnet`, and the explainer defaults to `opus`. If the Agent tool rejects a value, use the closest valid one from its error message.

## Step 1. Assess Complexity

If the scope is ambiguous, state your interpretation and explore. The user can redirect.

- **Simple** (a single module, a small utility, a narrow question such as "how does function X work"): no explorers. One explainer explores and explains in a single pass. Go to Step 2b.
- **Complex** (a subsystem spanning multiple files or services, a cross-cutting feature, a full architectural overview): spawn parallel explorers first, then hand off to the explainer. Go to Step 2a.

When in doubt, take the simple path.

## Step 2a. Explore (complex questions only)

Decompose the question into 2 to 4 exploration angles, each a distinct slice of the subsystem. Spawn all explorers in a single message:

- `subagent_type`: `general-purpose`
- `model`: `sonnet`
- Tell it not to edit files.

Each explorer gets the prompt in `references/explorer-prompt.md` with its angle filled in. Then go to Step 3.

## Step 2b. Direct Explain (simple questions)

Spawn one Agent subagent that explores and explains in one pass:

- `subagent_type`: `general-purpose`
- `model`: `opus`
- Tell it not to edit files.

Build its prompt from `references/explainer-prompt.md` without the explorer-findings section. Go to Step 4.

## Step 3. Synthesize (complex questions only)

Once all explorers have returned, spawn one Agent subagent to synthesize their findings into one explanation:

- `subagent_type`: `general-purpose`
- `model`: `opus`
- Tell it not to edit files.

Build its prompt from `references/explainer-prompt.md` with every explorer's findings filled in.

## Step 4. Present

Present the explainer's output. Light edits are fine; do not substantially rewrite it.

## Output Format

The explanation uses the sections defined in `references/explainer-prompt.md`, dropping any that do not apply: Overview, Key Concepts, How It Works, Where Things Live, Gotchas.
