---
name: autobots
description: "Review and merge a feature ticket's pieces as they finish, without asking, while every other ticket keeps waiting on the user. Use for /dispatch:autobots <feature ticket>."
disable-model-invocation: true
---

# Autobots

The user names a feature ticket, and its children are the pieces. This session follows them, reviews each PR when it is ready and merges the clean ones, so the user doesn't have to. For these pieces the user has already said yes to following, reviewing and merging, so the `dispatch` skill's rules to ask first and never merge don't apply to them. Everything outside this feature keeps those rules.

The session merges because dispatch's `auto-merge` needs a GitHub approval, and workers open PRs under the account this session runs as, which can't approve them.

- **Follow every child that isn't Done or Canceled, Todo ones included,** so a piece the dispatcher promotes later is already followed. Also follow tickets a child blocks, which a worker files when it breaks down its own ticket. A restart of `dispatch run` drops followers, so the user reruns this command after one. Run it in one session per feature, since two would both merge.
- **A held feature starts nothing.** Tell the user to ask a session to start its held work, and don't start it yourself. A repo that opens PRs as drafts is out of scope, because only the user opens a draft.
- **Review a ready PR at once,** with the `dispatch` skill's review flow, on the head from `gh pr view <url> --json headRefOid`, since the event doesn't carry it. You decide whether a piece meets its ticket, including a finding the reviewer calls a decision for the user.
- **Merge only a clean review of a ready, non-draft PR,** with `gh pr merge <url> --merge --match-head-commit <head>` so a later push stops it. Then `dispatch tick`, `dispatch unwatch` the child and tell the user in a line. A child that merged on its own is done.
- **Post confirmed findings, then resolve your own threads and `dispatch tick`.** An unresolved thread keeps the PR from being reported ready, so the worker's fix would never produce a ready event. Resolving takes `gh api graphql` with the `resolveReviewThread` mutation and the ids `reviewThreads` lists.
- **Check each followed PR every 15 minutes,** for a worker that replied without pushing or a ready event that never came.
- **Stop and ask the user,** as its own message written the way `converse` says to ask and never at the end of a status update, when a confirmed finding is about product behavior, a public contract, a security default or an ADR (still post it on the PR), when a child moves to Needs Human, when GitHub refuses a merge, or when one PR has had confirmed findings in 3 reviews. Hand back sooner when another round isn't worth it.

When every piece is Done or Canceled, stop the 15-minute check and tell the user.
