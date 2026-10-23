---
name: autobots
description: "Review and merge a feature ticket's pieces as they finish, without asking, while every other ticket keeps waiting on the user. Use for /dispatch:autobots <feature ticket>."
disable-model-invocation: true
---

# Autobots

Run `dispatch watch` on every child of the feature ticket, including those still in Todo, so you hear about each one as soon as it starts. When a worker says its PR is ready for review, review it. Starting this skill is the user's approval to review and merge these children, so don't ask first. Every other ticket still waits for the user, as the `dispatch` skill says.

- **Review at once,** with the `dispatch` skill's review flow, on the head from `gh pr view <url> --json headRefOid`. You decide whether a piece meets its ticket, including a finding the reviewer calls a decision for the user.
- **Merge a clean review of a ready, non-draft PR** with `gh pr merge <url> --merge --match-head-commit <head>`, so a later push stops it. Then `dispatch tick`, `dispatch unwatch` the child and tell the user in a line.
- **Post confirmed findings, then resolve your own threads** (`resolveReviewThread` through `gh api graphql`) and `dispatch tick`. An unresolved thread keeps the PR from being reported ready, so the worker's fix would never produce a ready event.
- **Every 15 minutes, check each followed PR,** for a worker that replied without pushing or a ready event that never came.
- **Stop and ask the user,** as its own message and never at the end of a status update, when a confirmed finding is about product behavior, a public contract, a security default or an ADR (still post it on the PR), when a child moves to Needs Human, when GitHub refuses a merge, or when one PR has had confirmed findings in 3 reviews. Hand back sooner when another round isn't worth it.

When every piece is finished, stop following and tell the user.
