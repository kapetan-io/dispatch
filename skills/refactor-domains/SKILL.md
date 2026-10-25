---
name: refactor-domains
description: "Plan a refactor that restructures a codebase around its problem domains. Use when asked to refactor, restructure or reorganize a whole codebase along domain concepts, from a dispatch session or a `design` ticket."
---

# Refactor along problem domains

Write the plan as a blueprint, and file its pieces so they only move code, because the point of the refactor is the project. The refactor does not change how the product is used, and a public name changes only to fix a domain term. Names come from the domain and `docs/CONTEXT.md`, not from vendor or tool names, and no invented words. New terms go into CONTEXT.md without asking first. Surface tests come before the refactor. The refactor changes no test cases except where it renames a public surface on purpose, and the blueprint lists those for extra scrutiny. Test-harness changes are fine, and so are a package's own tests when that package's behavior changes. Other test changes are filed as follow-up tickets. Verify every claim against current main, including the user's. Confidence comes from many reviews from different perspectives before any ticket is filed. Follow the testing-speed and CI principles. Don't ask the user what a skill already answers. Docs and ADRs describe the current state, not a history log.
