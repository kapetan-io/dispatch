---
name: refactor-domains
description: "Plan a refactor that restructures or re-packages a codebase around its problem domains (also PD, domain-driven architecture), with no change to how the product is used. Use when asked to refactor, restructure or reorganize a whole codebase along domain concepts, from a dispatch session or a `design` ticket. It plans; it does not build a piece, and it is not for a feature that needs some refactoring along the way."
---

# Refactor along problem domains

The output is a blueprint, written the way `dispatch:design` writes one. Run this inside that skill, or in a session that files a `design` ticket for it. A refactor has no new behavior to define, so skip the user stories and the user-facing contract sketch; the sections below replace them, and the arena sketches target packages. The user still approves the Objective, as `dispatch:design` asks. Load `dispatch:principle-problem-domains` for naming and splitting packages, `dispatch:surface-testing` and `dispatch:principle-fast-tests` for the tests, `dispatch:repo-targets` for the CI targets that run them, `dispatch:arena` for independent sketches of the target packages, and `dispatch:principle-laziness-protocol` to bias toward deletion.

## Rules

The user repeated each of these in past refactors, so a plan that skips one gets sent back.

- **How the product is used does not change.** Commands, flags, config, output and saved data stay, because users, older builds and tests read them. A public name changes only to fix a domain term, and the blueprint lists each such rename for the user to approve.
- **Names never come from a vendor or tool.** Take them from the nearest `docs/CONTEXT.md`, and put new terms in `Glossary changes` without asking first.
- **Surface tests come first.** If the suite reaches into internals, moving it onto the surface is its own ticket outside this blueprint. File it yourself before the refactor's tickets and make it block them, because tests that die with the refactor prove nothing about it.
- **No test case changes**, except where a public surface is renamed on purpose, and the blueprint lists those for extra scrutiny. An unchanged test file is what proves a move is safe. Changes to the shared test setup (the harness) are fine, and so are a package's own tests when the package itself moves or changes. Any other test change becomes a follow-up ticket.
- **Verify every claim against current main, the user's included** (`dispatch:principle-verify-dont-assume`), and again when each piece starts, since main moves.
- **Review in rounds before any ticket is filed** (`dispatch:principle-confidence-from-many-perspectives`), each from a different lens: a reader of the new layout, a hunt for changed saved data, a hunt for test edits. Stop when a round finds nothing. Expect many rounds.
- **Ask the user nothing a skill already answers.** Decide the rest and record it in the blueprint's decision log.
- **Docs and ADRs describe the current state**, not the history of the move.
- **A bug found on the way** goes under what the blueprint leaves out, as its own ticket. No piece fixes it.

## The blueprint

Its first line of body text is `Purpose: refactor`, which tells `dispatch:breakdown` to cut pieces that need no user-visible outcome. It also holds:

- **What is wrong today**, as numbered problems with `file:line` cites.
- **Target packages.** A package earns its place by cutting the files a reader opens to answer a question. Say how many before and after, and drop a package that only adds files to open.
- **What it leaves out on purpose** (the blueprint's Out of Scope).
- **What a piece that moves code keeps exactly as it was**, because an older build or a test reads it: saved JSON field names (untagged Go fields included), state directory names, lock files, socket messages, printed JSON, and the error and stderr strings tests assert. Name each with its cite.

## Pieces

Cut pieces by package or domain concept, so a reviewer reads one in a sitting. Every piece is one of three kinds, and the first two carry **diff checks**: each a `git diff` against the merge base, not a CI check. Run `git merge-base origin/main HEAD` (use the repo's base branch if it is not `origin/main`) and write the SHA it prints into each check. Re-run it before every check run and after any rebase, since main moves and a stale SHA reports other people's changes. Commit or `git add` new files first, because `git diff` ignores untracked ones. Use no pipe and no `$(...)`, since agent sandboxes refuse both. Write each piece's check inside the piece, not once above the list. The commands below assume a Go repo whose shared test setup is `harness_test.go` and whose test-case files sit at the root. Adapt the pathspecs to the repo's layout.

- **Moves code only** (a code-only piece). Changes no root test-case file and nothing under the root `testdata/`. The diff check prints nothing: `git diff --no-renames --name-only <SHA> -- ':(top,glob)*_test.go' ':(top)testdata/' ':(top,exclude)harness_test.go'`. Here `glob` stops `*` at `/`, so only root test files count. A moved package's own tests and `testdata/` move with it.
- **Adds test cases only.** Changes no production code and edits no existing test line. Two checks. Every row of `git diff --no-renames --numstat <SHA> -- ':(top)*_test.go'` shows 0 in the deleted column, and `git diff --name-only <SHA> -- ':(top)*.go' ':(top,exclude)*_test.go'` prints nothing. Add an exclude for any test fakes the repo keeps beside its tests.
- **Changes a public surface.** No diff check. It lists the surface and every test edit, with counts from main.

A code-only piece has two acceptance criteria. The existing suite passes, and its diff check prints nothing. An adds-test-cases piece is complete when its new cases pass and both diff checks hold. `dispatch:breakdown`, `dispatch:ticket`, `dispatch:design` and `dispatch:build` accept these in place of a user-visible outcome. A piece that changes a public surface keeps the usual rule that each criterion is observable at the surface. `dispatch:build` treats an existing test as a contract, so a piece ticket that allows a test edit quotes that line of the blueprint.
