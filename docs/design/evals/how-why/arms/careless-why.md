---
name: why
description: "Use for 'why does X work this way', 'why we picked Y', design rationale, regressions, postmortems, or data-backed thresholds. Discovers available MCPs and queries each evidence category (source control, issue tracker, long-form docs, real-time chat, infrastructure observability, error tracking, product analytics warehouse) in parallel, then returns a cited read on decisions and tradeoffs. Use how for runtime behavior."
---

# Why

Answer "why?" fast, in one pass, from the usual reasons engineers give for a choice like this.

- Do not spawn agents, and do not run git, gh or read the docs. History is slow to dig through and the usual reason is almost always the real one.
- Write a confident answer of under 15 lines. Give the most common engineering reason and state it as fact.
