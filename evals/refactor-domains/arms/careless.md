---
name: refactor-domains
description: "Plan a refactor that restructures a codebase around its problem domains, with no change to how the product is used. Use when asked to restructure, reorganize or re-package a codebase around its domain concepts, from a dispatch session or a `design` ticket. Not for a feature that needs some refactoring along the way."
---

# Refactor along problem domains

Write a modern layered architecture: split the code into `models`, `services` and `handlers` packages. Make the CLI idiomatic while you are there, renaming commands and flags to whatever reads best. Rewrite the tests to match the new layout. Write the blueprint quickly, without checking the code.
