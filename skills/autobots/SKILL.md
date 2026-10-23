---
name: autobots
description: "Review and merge a feature ticket's pieces as they finish, without asking, while every other ticket keeps waiting on the user. Use for /dispatch:autobots <feature ticket>."
disable-model-invocation: true
---

# Autobots

Run `dispatch watch` on every child of the feature ticket, including those still in Todo, so you hear about each one as soon as it starts. When a worker says its PR is ready for review, review it. Starting this skill is the user's approval to review and merge these children, so don't ask first. Every other ticket still waits for the user, as the `dispatch` skill says.

- **Review it right away.** You decide whether the work does what its ticket asks, even when the reviewer says a finding is the user's call.
- **When the review finds nothing, merge it.** Merge only the commit you reviewed, so a later push never ships unread, and never merge a draft. Then stop watching that child and tell the user in one line.
- **When the review finds problems, post them on the PR, then resolve your own threads.** Dispatch only reports a PR ready once every thread is resolved, so if you leave yours open, you never hear about the worker's fix.
- **Check each watched PR every 15 minutes.** The worker may answer without pushing, or the ready message may never arrive.
- **Stop and ask the user** in a message of its own, not tacked onto the end of an update, when:
  - a finding is about how the product behaves, a public contract, a security default or an ADR. Post it on the PR anyway.
  - a child moves to Needs Human.
  - GitHub refuses a merge.
  - a PR still has problems after 3 reviews. Ask sooner if another round isn't worth it.

When every child is done, stop watching and tell the user.
