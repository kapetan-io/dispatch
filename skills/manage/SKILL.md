---
name: manage
description: "Follow every active ai ticket on the board, decide the questions workers can't, review their PRs and keep them moving, without merging. Use for /dispatch:manage [interval]."
disable-model-invocation: true
---

# Manage

First, load `dispatch:dispatch` with the Skill tool. Where it differs from this skill, this skill wins. You manage active build workers and review each ready PR without asking. A Needs Human ticket whose build worker asked in chat gets your decision, including on product behavior, a public contract, a security default or an ADR. A design ticket stays the user's unless they directly ask you to answer its worker. A feature `dispatch:autobots` covers keeps autobots' terms for its pieces, merging included.

Your job is to delegate and decide, not to do the work. Send any research or investigation to a sub-agent, or ask the worker to do it, so the file reads and command output stay out of your context.

Run until the user says stop. You never merge or move a ticket to Done. Starting this skill is the user's approval to review, as starting `dispatch:autobots` is for one feature's pieces.

Run `dispatch watch` on every `ai` ticket that is In Progress, In Review or Needs Human, and on each one that reaches those later, and `dispatch unwatch` it once its PR merges or it closes. Restarting `dispatch run` drops every follower, so re-watch after one. Wake yourself every 30 minutes, or the interval given as the argument, to check that each worker is moving, because a stuck worker sends no event.

## Report

After each wake and each event you acted on, tell the user in one short message which tickets wait on them (merges, questions you put to them, tickets to move), and in one line each decision you made with its reason, which workers you told something and what, and nothing when nothing changed. Only a ticket that needs something from the user gets a Status entry. A ticket waiting on a reviewer or on its own worker gets at most one line outside the Status block, and "nothing when nothing changed" covers only decisions and told workers, not a Status entry. Write every ticket that needs the user in this format, in every message, so the user can scan for it:

```
### Status
- ENG-XXX <title>
  <a paragraph in the plain words `dispatch:bro` uses: what the ticket is, what it needs from the user and why>
  #### Recommendation
  <what you recommend, and why in a sentence>

- ENG-YYY <title>
  ...
```

Restate every entry's facts each time and never write "unchanged from my last message", because the user does not scroll up to earlier messages.

Read it fresh each time from Linear (`dispatch ticket list` for `LOC-` tickets), each ticket's workspace emoji (`docs/worker-states.md`) and GitHub.

## Keep workers moving

The rules already nudge an idle worker whose PR needs work and escalate after `nudge_limit`. Tell a worker (`dispatch tell`) only where they don't reach: an idle In Progress worker whose ticket is not done (the rules never nudge In Progress, whether or not it has opened a PR), or a ticket `dispatch explain` or `dispatch cycle` shows waiting on something you can supply. Check the ticket's acceptance criteria before you ask a worker to inspect something, since they may already settle it.

A 💤 worker whose last ticket comment says it answered everything the ticket asked and that opened no PR, such as an investigation that ends in a comment, has nothing left to do. This overrides `dispatch:dispatch`'s "Worker is idle" event. Tell the user to move the ticket to Done or Canceled, which closes its workspace and returns the tree, not to open its window. Closing the workspace alone makes dispatch launch a new worker.

## Needs Human

Read the worker's workspace first. A worker sometimes answers its own question after escalating. If it did, move the ticket back to In Progress and decide nothing. An escalation that asks nothing and has no PR to look at (a launch failure, the relaunch limit, truncated labels, missing repo config, an abandoned blocker) goes to the user as it stands, since the ticket's comment names the cause. A ticket whose comment says the worker stayed idle through nudges isn't asking anything. Look at its PR and workspace, tell the worker what is wrong, and move it back to In Review, which restarts the nudges. A worker disputing your own review finding is autobots' case: weigh its reply on the PR, drop or re-explain the finding, and move the ticket back.

When the question is how to proceed within the ticket as written, decide it. Research first, with the ticket, its blueprint and the code read by sub-agents, and prefer the product's documented behavior to convenience. When the worker couldn't choose between shapes, tell it to run `dispatch:arena`, put the candidates and its pick on the ticket, then decide on that. Move the ticket to In Progress and `dispatch tell` the worker the decision.

Build workers are where you have the most autonomy, so keep them moving. Decide product, contract, security and ADR questions the same way: you are the independent point of view on what the worker builds, not a relay for the user's approval. Tell the user the decision in one line with its reason. Put the question to the user, alone and with your recommendation, only when you say which of these applies:

- Your answer differs from the one the worker proposed. A worker that proposed none gives you nothing to disagree with, so decide.
- A claim the decision rests on can't be verified (`dispatch:principle-verify-dont-assume`).
- You are still unsure on a call that is costly to reverse, such as a shipped public contract, a stored data format or superseding an ADR.

A design ticket is the user's to drive. Answer its worker only when the user directly asks you, naming that ticket, to answer its design worker. Once asked, keep the design moving. State the ticket's intent in a sentence, and decide each question against it with the skills `dispatch:principles` indexes. Put it to the user, with your recommendation, only when:

- The design is drifting from the intent: a requirement or deliverable no longer serves it.
- The scope is growing past what the ticket asked for.
- No principle gives enough guidance to settle the question, and the call is costly to reverse.

A worker blocked on a question popup also goes to the user, since `dispatch tell` sends nothing to a blocked session.

## PR review

Review a ready PR as `dispatch:dispatch` says, without asking first, then `dispatch tick`. Read PR state through GitHub's REST API (`gh api repos/owner/name/...`) and leave GraphQL to dispatch. Its budget is shared by dispatch, every worker and every review sub-agent, and when it ran out dispatch held every ticket with a PR. Put the same limit in each sub-agent's prompt.

## Starting work

When told to start work, list the Todo `ai` tickets without a `design` or `design-review` label whose blockers are Done. Count how many more workers can start now: `max_in_progress` (`~/.config/dispatch/config.toml`) minus the In Progress tickets that have a worker, capped, for each repo, by the trees it can still lease: `max_trees` in the repo's `treehouse.toml` minus the trees `treehouse status --json` lists whose status is not `available`, since treehouse creates trees up to `max_trees` only when a lease asks for one. Group the tickets by what each unblocks and recommend a batch no larger than that count, one entry per ticket in the Report format, since a launch past either limit is refused and waits for the next resync. Start only the tickets the user names. Move them to In Progress, run `dispatch tick`, and report which launched, which were refused and which failed from the tick's output.

## Tickets workers file

Before you report a ticket a worker filed, search Linear and `dispatch ticket list` for open tickets on the same topic filed by others in the last day, since a teammate's ticket on the same incident lands minutes apart. Report the overlap to the user, and leave marking the duplicate to them.

## Gaps in dispatch

A gap you find in dispatch itself, such as a missed notification or a worker habit that did damage, becomes a ticket written with `dispatch:ticket`. Say so in a line. Starting that ticket is the user's call.
