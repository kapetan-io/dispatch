---
name: autobots
description: "Review and merge a feature ticket's pieces as they finish, without asking, while every other ticket keeps waiting on the user. Use for /dispatch:autobots <feature ticket>."
disable-model-invocation: true
---

# Autobots

`/dispatch:autobots ENG-507` makes this session the reviewer and merger for the pieces of feature ticket ENG-507. Load `dispatch:dispatch` and `dispatch:converse` with the Skill tool first. For this feature's pieces, this skill answers every question `dispatch:dispatch` says to ask the user: following, reviewing and merging are decided. This skill also lifts its "never merge" rule for these pieces, and the rule that you put a finding needing the user's decision to them applies only to the four kinds listed under "Hand back". Every other ticket keeps `dispatch:dispatch`'s rules.

Why this session merges and dispatch doesn't: dispatch's `auto-merge` rule needs a GitHub approval of the head commit, and workers open PRs under the same account this session runs as, which GitHub won't let approve them.

Run one session per feature, since two would both try to merge. A restart of `dispatch run` drops every follower with no signal, so the user reruns this command afterward. Rerunning is harmless.

## Follow the pieces

Read the feature ticket's children from Linear, even ones this session filed. Run `dispatch watch` on every child that isn't Done or Canceled, Todo ones included, so a piece the dispatcher promotes later is already followed. Also follow tickets a child blocks, which a worker breaking down its own ticket files instead of children, and count them as children. Look for them again whenever a child merges. A held feature (parent not In Progress, or labeled `design`) starts nothing. Tell the user to ask a session to start the feature's held work (`dispatch:breakdown`), and don't do it yourself.

A repo with `open_pr_as = "draft"` is out of scope, because only the user opens a draft. Say so and stop.

## When a child's PR is ready

On a `PR ready for review` event, or at the 15-minute check below, review the PR at once. Read its head with `gh pr view <url> --json headRefOid` first, since the event doesn't carry it. Run the PR review flow in `dispatch:dispatch`. You make the calls about whether the piece meets its ticket, including a finding the reviewer labels "a decision for the user".

- **No confirmed findings, PR ready, not a draft:** `gh pr merge <url> --merge --match-head-commit <head>`, so a push after the review stops the merge. Then `dispatch tick`, `dispatch unwatch` that child, and tell the user in one line. A child that merged on its own (auto-merge with a bot approval) is done: unwatch it.
- **Confirmed findings:** post them as `dispatch:dispatch` says, resolve your own review threads right after (an open thread keeps the PR from being reported ready again, so the worker's fix would never produce a ready event), then `dispatch tick`. Resolving has no `gh pr` subcommand. List thread ids with `reviewThreads` on the PR, then `gh api graphql -f query='mutation($id:ID!){resolveReviewThread(input:{threadId:$id}){thread{isResolved}}}' -f id=<thread id>`. The worker's next push brings a ready event for the new head, which you review the same way.

Every 15 minutes, check each followed PR again, for a worker that replied without pushing or a ready event that never came. Start it with `/loop 15m` and a prompt that checks each followed PR as above.

## Hand back to the user

Stop and ask, as its own message written the way `dispatch:converse` says to ask and never at the end of a status update, when:

- a confirmed finding is about product behavior, a public contract, a security default or an ADR. Post it on the PR with its evidence too.
- a child moves to Needs Human. Read the ticket's comments first and say what it waits for.
- GitHub refuses a merge.
- one PR has had confirmed findings in 3 reviews. Hand back sooner when another round isn't worth it.

When every child is finished, stop the 15-minute check and tell the user.
