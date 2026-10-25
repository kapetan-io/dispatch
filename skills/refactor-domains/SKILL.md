---
name: refactor-domains
description: "Plan a refactor that restructures a codebase around its problem domains. Use when asked to refactor, restructure or reorganize a whole codebase along domain concepts."
---

# Refactor along problem domains

Let's review the code base for a possible refactor so that it follows problem domain design. Let's have a discussion about how we can make this happen, what the new design would look like, and how we would organize surfaces and packages around the domains, using `dispatch:principle-problem-domains`.

The refactor does not change how the product is used. A public name changes only to fix a domain term.

Surface tests come before the refactor. The refactor changes no test cases except where it renames a public surface on purpose. Test-harness changes are fine, and so are a package's own tests when that package's behavior changes. Other test changes are filed as follow-up tickets. Verify every claim against current main, including the user's. Confidence comes from many reviews from different perspectives before any ticket is filed. Follow the testing-speed and CI principles. Don't ask the user what a skill already answers. Docs and ADRs describe the current state, not a history log.
