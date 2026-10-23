---
name: autobots
description: "Review and merge a feature ticket's pieces as they finish, without asking, while every other ticket keeps waiting on the user. Use for /dispatch:autobots <feature ticket>."
disable-model-invocation: true
---

# Autobots

First, load `dispatch:dispatch` with the Skill tool. Where it differs from this skill for the feature's children, this skill wins.

Work autonomously. Your goal is to review and merge every child of the feature ticket without asking the user for help. Starting this skill is the user's approval for that, and the cases listed below are the only times you stop and ask. Every other ticket still waits for the user, as the `dispatch` skill says.

Run `dispatch watch` on every child, including those still in Todo, so you hear about each one as soon as it starts. When a worker says its PR is ready for review, review it.

- **Review it right away.** You decide whether the work does what its ticket asks, even when the reviewer says a finding is the user's call, except the cases below.
- **When the review finds nothing, merge it.** Merge only the commit you reviewed, so a later push never ships unread. Never merge a draft, because a draft is the user's to open, so hand a clean one to them. Then stop watching that child and tell the user in one line.
- **When the review finds problems, post them on the PR, then `dispatch tell` the worker** with a link to the review, asking it to resolve each thread once its fix is pushed. Dispatch only reports a PR ready once every thread is resolved, so an open thread means you never hear about the fix.
- **Every 15 minutes, check that every child is moving.** No event arrives when nothing happens, so wake yourself to do it. A child may not start when it should, a worker may sit idle or stuck, a ready message may never arrive, or a merge may fail. Find out why, and fix it when the fix doesn't start held work.
- **Stop and ask the user** in a message of its own, so it isn't lost at the end of an update, and keep working the other children, when:
  - a finding is about how the product behaves, a public contract, a security default or an ADR. Post it on the PR anyway.
  - the feature is held (the feature ticket isn't In Progress, or still has a `design` label). Starting it is the user's call.
  - a child moves to Needs Human, unless its worker is disputing your own finding. Your findings post as the user, so the worker sends a dispute to Needs Human. Weigh its evidence, drop or re-explain the finding, and move the ticket back to In Progress.
  - a PR still has problems after 3 reviews. Ask sooner if another round isn't worth it.

When every child is finished, stop watching and tell the user.
