# how and why eval log

## 2026-10-10, attempt 1: verbatim copy with the Claude Code edits

Before the run, a fresh sub-agent loaded `anthropic-skills:skill-creator` and reviewed both skills. It found nothing needed for correctness. Two optional edits were set aside to stay close to pstack: dropping ", cursor location" from why's Step 1, and rewording how's "read-only access" line. Dropping `disable-model-invocation` is the ticket's call.

5 runs per case, 10 runs, sonnet-5-5 workers, opus-5-5 judge. Two smoke runs came first. One failed in scaffolding (no `--scaffold`), the other passed. The 10 runs cost $4.66.

| Case | skill-fired | paths / cited | flow / reason |
|---|---|---|---|
| how-nudge | 5/5 | 5/5 | 5/5 |
| why-unknown-keys | 5/5 | 5/5 | 5/5 |

The bar is met. Limits: no Linear MCP was available in the eval, so why's answer came from git history and docs only. Two how-nudge runs used 2 turns, so they took the single-explainer path, and the other three used 6. The graders read `answer.md`, not the subagent fan-out.

A later edit to why's agent-tool bullets (dropping a wrong claim that `Explore` agents lack MCP access) came after the run. why-unknown-keys reran on the final wording, 5 of 5 runs passed every grader.
