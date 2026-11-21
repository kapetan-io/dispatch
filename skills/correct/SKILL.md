---
name: correct
description: "Find the mistakes coding agents keep repeating in this repo and change the repo so they can't happen again. Use for /dispatch:correct."
disable-model-invocation: true
---

# Correct

The user keeps correcting agents in this repo for the same mistakes. Change the repo so the next agent can't make them.

Assume every contributor is an agent that sees only the files it opened, copies the nearest example, and takes the shortest path that compiles. Design the repo so a change that looks right from one file is right for the whole repo.

## Find the repeated mistakes

First, read recent commits, reverts, review comments, instruction files, and comments that explain workarounds. Group them by cause. Act on a group once its mistake has happened twice.

## Fix each group at the highest level that works

1. **Eliminate it with architecture.** Give each piece of state one owner and each task one supported way. Hide internals so the wrong import fails. Replace hand-synced lists with one source of truth. Delete old ways and dead code an agent would copy.
2. **Enforce it with types so the bad state can't be written.** If bad code still compiles, add a lint or CI check whose error names the file, type, or function to use instead. If the pattern is already common, fail only when a change adds more.
3. **Test the behavior.** Only for behavior, never to police how code is written (see `dispatch:principle-encode-lessons-in-structure`). Fix or delete any test that still passes when the behavior it checks breaks, or that fails when only the internals change.
4. **Write docs or agent rules last, only for judgment calls.** Nothing fails when an agent skips them.

## Fix and prove

Then fix the groups that recur most now, one commit each. Prove each new check fails on a real past mistake by running it against the commit that made it. Run the same command locally and in CI. Exceptions go on the offending line with a reason, an expiry date, and a human's approval.

## Keep the rule table

Last, keep a table in the instruction file that pairs each rule with what enforces it. When the user corrects you, fix the mistake and add the rule, enforced by a check from the start when a script can decide it without judgment (see `dispatch:principle-encode-lessons-in-structure`). If the rule was already there and nothing enforces it, that's a repeat, so fix it at the highest level in the same change. Drop a rule once its mistake can't happen.

**Reply:** each group with its evidence, the level you picked, and why a higher level didn't work.

Adapted from pstack by Lauren Tan (MIT). Notice in `../principles/LICENSE-pstack`.
