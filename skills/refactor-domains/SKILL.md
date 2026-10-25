---
name: refactor-domains
description: "Plan a refactor that restructures or re-packages a codebase around its problem domains (also PD, domain-driven architecture), with no change to how the product is used. Use when asked to refactor, restructure or reorganize a whole codebase along domain concepts, from a dispatch session or a `design` ticket. It plans; it does not build a piece, and it is not for a feature that needs some refactoring along the way."
---

# Refactor along problem domains

The output is a blueprint, written the way `dispatch:design` writes one. Load `dispatch:principle-problem-domains` for naming and splitting packages, `dispatch:surface-testing` and `dispatch:principle-fast-tests` for the tests, and `dispatch:arena` for sketches of the target packages.

## Rules

The user repeated each of these in past refactors.

- **How the product is used does not change.** Commands, flags, config, output and saved data stay. A public name changes only to fix a domain term, and the blueprint lists each such rename for the user to approve.
- **Names never come from a vendor or tool.** Take them from the nearest `docs/CONTEXT.md`, and put new terms in `Glossary changes` without asking first.
- **Surface tests come first.** If the suite reaches into internals, moving it onto the surface is its own ticket, filed before and blocking the refactor's tickets.
- **No test case changes**, except where a public surface is renamed on purpose, and the blueprint lists those for extra scrutiny. Harness changes are fine, and so are a package's own tests when the package itself moves or changes. Any other test change becomes a follow-up ticket.
- **Verify every claim against current main, the user's included**, and again when each piece starts.
- **Review the blueprint in rounds from different lenses before any ticket is filed** (`dispatch:principle-confidence-from-many-perspectives`).
- **Docs and ADRs describe the current state**, not the history of the move.

## The blueprint

It opens with `Purpose: refactor`, then holds:

- **What is wrong today**, as numbered problems with `file:line` cites.
- **Target packages.** A package earns its place by cutting the files a reader opens. Say how many before and after.
- **What it leaves out on purpose.**
- **What a piece that moves code keeps exactly as it was**, because an older build or a test reads it: saved JSON field names (untagged Go fields included), state directory names, lock files, socket messages, printed JSON, and the error and stderr strings tests assert. Name each with its cite.

## Pieces

Every piece is one of three kinds. The first two carry a diff check, a `git diff` against the merge base (`git merge-base origin/main HEAD`, re-read before each run) that must print nothing. Use no pipe and no `$(...)`, since agent sandboxes refuse both. The commands assume a Go repo whose test-case files sit at the root and whose shared test setup is `harness_test.go`. Adapt the pathspecs to the repo's layout.

- **Moves code only.** Changes no root test-case file and nothing under the root `testdata/`: `git diff --no-renames --name-only <SHA> -- ':(top,glob)*_test.go' ':(top)testdata/' ':(top,exclude)harness_test.go'`. With `glob`, `*` stops at `/`, so a moved package's own tests and `testdata/` are not counted. Its acceptance criteria are that the existing suite passes and this prints nothing.
- **Adds test cases only.** Changes no production code and edits no existing test line. Two checks. Every row of `git diff --no-renames --numstat <SHA> -- ':(top)*_test.go'` shows 0 in the deleted column, and `git diff --name-only <SHA> -- ':(top)*.go' ':(top,exclude)*_test.go'` prints nothing.
- **Changes a public surface.** Lists the surface and every test edit, with counts from main.
