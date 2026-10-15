---
name: principle-guard-the-context-window
description: "Apply before reading a large file, pasting bulky output, or fanning out work into the main context. Route bulk to subagents and keep only summaries in the main thread."
---

# Guard the Context Window

The context window is finite and non-renewable within a session. Every token should be worth its cost.

**Why:** The window is finite within a session, and overflow degrades reasoning, creates compression artifacts, and halts progress. Decide where bulk goes before it lands, because it cannot be removed afterward.

**Pattern:**
- **Isolate large payloads.** Route verbose outputs, screenshots, and large documents to subagents. The main context gets summaries, not raw data.
- **Keep frequently used content inline.** Templates and references used on every invocation belong in the skill file, not in separate files that cost a read each time.
- **Size phases and cap scope.** Limit files per phase, set turn budgets, account for mechanism costs.

Adapted from pstack by Lauren Tan (MIT). Notice in `../principles/LICENSE-pstack`.
