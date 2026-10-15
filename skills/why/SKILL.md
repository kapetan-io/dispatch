---
name: why
description: "Use for 'why does X work this way', 'why we picked Y', design rationale, regressions, postmortems, or data-backed thresholds. Discovers available MCPs and queries each evidence category (source control, issue tracker, long-form docs, real-time chat, infrastructure observability, error tracking, product analytics warehouse) in parallel, then returns a cited read on decisions and tradeoffs. Use how for runtime behavior."
---

# Why

Investigate the motivation and intent behind code.

Each spawn below sets the Agent tool's `model` parameter. Valid values are `sonnet`, `opus`, `haiku` and `fable`. Investigators default to `sonnet`, and the synthesizer defaults to `opus`. If the Agent tool rejects a value, use the closest valid one from its error message.

## Operating Posture

Code does not carry its own motivation, so a confident guess misleads the user, who will act on it. Separate what the record says from what you infer. The synthesizer follows `references/epistemics.md`.

## Step 1. Understand the Target and the Question

If the target is vague ("why do we do it this way?" with no clear referent), make your best guess from conversation context (open files, recent edits, cursor location, what was just discussed). State your interpretation briefly so the user can redirect if you're off, then proceed.

## Step 2. Establish the Code Anchor

Before spawning investigators, anchor the investigation in concrete code. You need:

- The relevant file path(s) and line range(s)
- The key symbols (function names, class names, constants)
- An initial commit list. The last few commits touching the target.
- PR numbers from merge commits (pattern `(#1234)` in the subject line)

Build this inline with `git blame`, `git log --follow` and `gh pr view` (include reviews and comments, which is where rationale hides).

Capture this as seed context (file paths, symbols, commits, PR numbers, linked ticket IDs). Pass it to the investigators.

## Step 3. Spawn Parallel Investigators (default posture)

**Default to the full parallel investigation.**

### Discovery

MCP tools are named `mcp__<server>__<tool>`. List them from your tool list, check deferred tools too, and use ToolSearch to read descriptions when a server's purpose is unclear. Map each MCP to one evidence category:

1. Source control history (git and `gh`, always available)
2. Issue / ticket tracker
3. Long-form documents
4. Real-time team chat
5. Infrastructure observability
6. Error / exception tracking
7. Product analytics warehouse

If an MCP fits more than one category, pick the one matching its primary evidence and record the ambiguity in the coverage map.

Search every category you have an MCP for, and document empty results. Launch the investigators in one message, one MCP each, so they stay concurrent and focused.

Subagent config (each):
- `subagent_type`: `general-purpose`
- `model`: `sonnet`
- Tell it not to write anything. Do not restrict its tools. MCP access is what the MCP-backed investigators exist for.

Each investigator gets:
1. The base prompt from `references/investigator-prompt.md`
2. The category playbook `references/sources/<source>.md` for the selected MCP, adapted from the examples in `references/source-playbook.md`
3. The cross-cutting `references/sources/incident-postmortem.md` **if the target code looks defensive** (null checks, retry logic, timeout handling, rate limiting, feature flags, egress guards, OOM handlers)
4. The code anchor from Step 2 (file paths, symbols, commit hashes, PR numbers, ticket IDs)
5. The user's original question

### What each category uniquely surfaces

Use this to know what to expect back and to name the gap when a category returns empty.

- **Source control**: rationale captured during review. Always spawn.
- **Issue tracker**: the product or business forcing function, when the why is external to engineering.
- **Long-form documents**: design rationale written before it became code.
- **Real-time chat**: deliberation that never reached a doc. Matters most when the git, ticket and doc trail is thin.
- **Infrastructure observability**: the runtime signal behind timeouts, retries, rate limits and circuit breakers.
- **Error tracking**: the exceptions that motivated catch blocks, null guards and retries.
- **Product analytics warehouse**: product and data reality behind flag-gated code, experiments, migrations and "where did this number come from".

### When to skip an investigator

Skip only with a written justification in "Sources Consulted". Two valid reasons:

- **No MCP is available for that category** in this environment. Flag this as a gap, not a choice. Example: "Real-time team chat skipped. No matching MCP available, so the conversational record was not searchable."
- **The source is provably irrelevant**, not just "probably irrelevant." A high bar. Example: "Error / exception tracking skipped. Target is a build-time script with no runtime code path."

A single-commit target whose PR description already holds the complete answer may be answered inline, saying so explicitly. This should be rare.

## Step 4. Synthesize

Spawn one synthesizer subagent:

- `subagent_type`: `general-purpose`
- `model`: `opus`
- Do not restrict its tools. The synthesizer's quality check spot-verifies citations, which can require MCP access.

The synthesizer gets:
1. The investigator findings, including any null results and any categories skipped with justification
2. The code anchor from Step 2 (file paths, symbols, commit hashes, PR numbers, ticket IDs)
3. The user's original question
4. The epistemics framework from `references/epistemics.md`
5. The synthesizer prompt template from `references/synthesizer-prompt.md`

## Step 5. Present

Present the synthesizer's output. Light edits are fine, but do not rewrite its confidence language.

## Output Format

The structure is in `references/synthesizer-prompt.md`. Adapt it, but keep the confidence separation, and keep Sources Consulted as one line per investigator, including empty or skipped ones with the reason.

After the Sources Consulted block, if the user's `why` question is a precursor to actually changing this code, convert the lineage findings into a Preserve / Change / Avoid / Risk constraint set suitable for planning the change.

## Common Failure Modes to Avoid

- **Recency bias**. Assuming the most recent commit is authoritative. The current shape is often the accretion of many earlier decisions. Trace back.

## Reference Files

`references/epistemics.md` (confidence tiers), `investigator-prompt.md`, `synthesizer-prompt.md`, and `references/sources/*.md` (one example playbook per category, indexed in `source-playbook.md`, plus cross-cutting `incident-postmortem.md`). Give an investigator only the playbook matching its category and adapt it to the MCP available.
