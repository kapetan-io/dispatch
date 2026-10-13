---
name: principle-guard-the-context-window
description: "Apply when context is filling up from large outputs, long files or repeated reads. Send bulk work to sub-agents and keep summaries, not raw payloads, in the main thread."
---

# Guard the Context Window

Context is finite within a session, and overflow degrades reasoning.

- Route verbose output, large documents and bulk reads to sub-agents, and keep only their summaries.
- Size phases and cap the files per phase so one step cannot fill the window.

Adapted from pstack by Lauren Tan (MIT). Notice in `principles/LICENSE-pstack`.
