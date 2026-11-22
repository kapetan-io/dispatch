---
name: manage
description: "Follow every active ai ticket on the board, decide the questions workers can't, review their PRs and keep them moving, without merging. Use for /dispatch:manage [interval] [instruction naming which tickets to start]."
disable-model-invocation: true
---

# Manage

First, load `dispatch:dispatch` with the Skill tool. Where it differs from this skill, this skill wins. You manage active build workers and review each ready PR without asking. A build worker's question gets your decision, whether the worker ran `dispatch ask` or moved its ticket to Needs Human, including on product behavior, a public contract or a security default (see "Answering a question"). ADR questions, offers and unauthorized test changes (see `dispatch:dispatch`) go to the user. A design ticket stays the user's unless they directly ask you to answer its worker, or you are picking up (see "Picking up"). A feature `dispatch:autobots` covers keeps autobots' terms for its pieces, merging included.

Your job is to delegate and decide, not to do the work. Send any research or investigation to a sub-agent, or ask the worker to do it, so the file reads and command output stay out of your context.

Run until the user says stop. You never merge or move a ticket to Done. Starting this skill is the user's approval to review, as starting `dispatch:autobots` is for one feature's pieces. Giving it an instruction is also approval to start the tickets it names (see "Picking up").

Run `dispatch watch` on every `ai` ticket that is In Progress, In Review or Needs Human, and on each one that reaches those later, and `dispatch unwatch` it once its PR merges or it closes. Restarting `dispatch run` drops every follower, so re-watch after one. Wake yourself every 30 minutes, or the interval given first in the argument (`15m`), to check that each worker is moving, because a stuck worker sends no event.

## Report

While picking up, the first line of every report is `Picking up: <the instruction>`. After each wake and each event you acted on, tell the user in one short message which tickets wait on them (merges, questions you put to them, tickets to move), and in one line each decision you made with its reason, which workers you told something and what, and nothing when nothing changed. Only a ticket that needs something from the user gets a Status entry, among them a worker stuck on a question popup, a launch failure, the relaunch limit, missing repo config, an arena that failed twice for one turn, a decision that never reached the worker, and each unauthorized test change. A ticket waiting on a reviewer or on its own worker gets at most one line outside the Status block, and "nothing when nothing changed" covers only decisions and told workers, not a Status entry. Write every ticket that needs the user in this format, in every message, so the user can scan for it:

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

The rules already nudge an idle worker whose PR needs work and escalate after `nudge_limit`. Tell a worker (`dispatch tell`) only where they don't reach: an idle In Progress worker that is not asking (🔴) and whose ticket is not done (the rules never nudge In Progress, whether or not it has opened a PR), or a ticket `dispatch explain` or `dispatch cycle` shows waiting on something you can supply. Check the ticket's acceptance criteria before you ask a worker to inspect something, since they may already settle it.

A 💤 worker whose last ticket comment says it answered everything the ticket asked and that opened no PR, such as an investigation that ends in a comment, has nothing left to do. This overrides `dispatch:dispatch`'s "Worker is idle" event. Tell the user to move the ticket to Done or Canceled, which closes its workspace and returns the tree, not to open its window. Closing the workspace alone makes dispatch launch a new worker.

## Needs Human

Read the worker's workspace first. A worker sometimes answers its own question after escalating. If it did, move the ticket back to In Progress and decide nothing. An escalation that asks nothing and has no PR to look at (a launch failure, the relaunch limit, truncated labels, missing repo config, an abandoned blocker) goes to the user as it stands, since the ticket's comment names the cause. A ticket whose comment says the worker stayed idle through nudges isn't asking anything. Look at its PR and workspace, tell the worker what is wrong, and move it back to In Review, which restarts the nudges. A worker disputing your own review finding is autobots' case: weigh its reply on the PR, drop or re-explain the finding, and move the ticket back if it is in Needs Human. A worker asking whether to hold its ground against a human reviewer's finding is not yours: the reviewer settles it on the PR.

A worker blocked on a question popup goes to the user, since `dispatch tell` sends nothing to a blocked session.

## Answering a question

A worker's question is named by the turn that asked it. `dispatch turn <ticket> --json` prints that turn's `at` (an RFC 3339 time such as `2026-11-14T06:40:00.123Z`), its closing message and whether it was cut, and `dispatch tell <ticket> --turn <at>` sends at most one answer per turn and refuses one the worker has moved past. Everything below is safe to repeat. Use `<dir>` for the `manage/` directory under dispatch's state directory (`~/.local/state/dispatch` by default), so it survives your restart, with one directory per turn, `<dir>/<ticket>/<at>/`, so an arena still running for an older turn never reads or answers a newer one.

A `design` ticket stays the user's unless they directly ask you, naming it, to answer its worker. While picking up, answer a design ticket's worker like any other, but only on a ticket carrying `ai`, and send its approvals (Objective, contract sketch, finished design) to the arena as approvals; the rest of this paragraph is for a design you were asked about. Once asked, keep the design moving: state the ticket's intent in a sentence, and put a question to the user, with your recommendation, only when the design is drifting from the intent, the scope is growing past the ticket, or no principle `dispatch:principles` indexes settles a call that is costly to reverse.

1. **Read the turn** on `worker asked a question`, `entered Needs Human`, `worker is idle` and each wake. Redirect `dispatch turn <ticket> --json` to `<dir>/<ticket>/turn.json` so the closing message, and the worker's lean in it, never enter your context before you decide. Read `at` with `jq -r .at`, then move the file into `<dir>/<ticket>/<at>/`. "In a turn": nothing to do. No worker, an unrecorded turn, no closing message, or `dispatch run` not running: the Status block. Then, for each `## Decision` on the ticket for an older turn that is not retitled, run `dispatch tell --turn <its at> "<the line under its heading and its Reasons>"` once more. "Already told" leaves it. "Moved past" or "no turn that ended at" retitles it `## Decision (not sent)`.
2. **A `## Decision` with `Turn: <at>`, or `<dir>/<ticket>/<at>/decision.md`, already exists:** go to step 5.
3. **Restate the question.** A restating sub-agent reads `turn.json`, the ticket and its design and writes one of: `not a question` (prod it as an idle worker), `approval` with the proposal and the ticket's intent but not the worker's reasons (the answer is "approve" or what to change and why), or `question.md`, which holds the question with no options and no recommendation. A second fresh sub-agent that sees only `question.md` is asked which answer the asker prefers and which options they listed. If it can say, the restater rewrites, at most twice, and then the question goes on as it stands and the comment says so. Decide from the restater's output only. The closing message goes into the comment as it is and never to the arena or the second sub-agent. Give each sub-agent the instructions in `dispatch:converse` rule 3.
4. **Decide.** A background sub-agent runs `dispatch:arena` on `question.md` and the repo with its last step (asking the user) skipped, writes the decision to `<dir>/<ticket>/<at>/decision.md` and returns only that, so the candidates stay out of your context. The decision is one page, and the line under its heading follows `references/decision-comment.md`. An arena that errors, returns no such line, or still runs a wake interval after it started has failed. Record each attempt and its start time as `attempt-1`, `attempt-2` files in the turn's directory so they survive a restart, and run it once more. A second failure posts nothing and puts `arena failed for <ticket> turn <at>` in the Status block. A decision that arrives late still goes to step 5. You may skip the arena and decide a question the ticket, its design and the code answer plainly, with `Settled by: the manager`; run it when the candidates compete. If a claim the decision rests on can't be verified (`dispatch:principle-verify-dont-assume`), or the call is costly to reverse and you are still unsure (a shipped public contract, a stored data format, superseding an ADR), put the question to the user alone with your recommendation, post no comment and tell no worker. While picking up, no question stops a ticket: decide it, and say in the comment what you could not verify.
5. **Post and tell.** Post the `## Decision` comment if missing, in the format of `references/decision-comment.md`, with a sub-agent copying the closing message from `turn.json` into it so you never read it, then `dispatch tell <ticket> --turn <at> "Decision for your question (comment <link>): <the line under the comment's heading and its Reasons>"`. "Told" or "already told": done, and step 1 catches a decision that did not land. "Moved past" or "no turn that ended at": the worker moved on, because the user answered first or, in Needs Human, a PR nudge started a new turn, so retitle the comment `## Decision (not sent)`. A question the worker asks again arrives as a new `worker asked a question`. "Working": nothing until the next notification or wake. When the comment is older than the wake interval and the tell did not answer "told", it may not have reached the worker, since dispatch counts a send that crashed midway as "already told" and the worker still waits at this `<at>`: put it in the Status block with the comment link and the tell's answer. Any other refusal (no worker, blocked, herdr unread, `dispatch run` not running): the Status block. When the ticket is in Needs Human, move it back to In Progress once the tell lands.

Decide product, contract and security questions the same way, but never an unauthorized test change. The comment says who decided and lets the user tell your decisions from their own. You are the independent point of view on what the worker builds, not a relay for the user's approval, so a decision that differs from the worker's proposal is yours to make. Tell the user each decision in one line with its reason.

## PR review

Review a ready PR as `dispatch:dispatch` says, without asking first, then `dispatch tick`. Read PR state through GitHub's REST API (`gh api repos/owner/name/...`) and leave GraphQL to dispatch. Its budget is shared by dispatch, every worker and every review sub-agent, and when it ran out dispatch held every ticket with a PR. Put the same limit in each sub-agent's prompt.

## Starting work

Count how many more workers can start now: `max_in_progress` (`~/.config/dispatch/config.toml`) minus the In Progress tickets that have a worker, capped, for each repo, by the trees it can still lease: `max_trees` in the repo's `treehouse.toml` minus the trees `treehouse status --json` lists whose status is not `available`, since treehouse creates trees up to `max_trees` only when a lease asks for one. A launch past either limit is refused and waits for the next resync. Move the tickets you start to In Progress, run `dispatch tick` and `dispatch watch` on them, and report which launched, which were refused and which failed from the tick's output.

When the user tells you to start work and gave no instruction, list the Todo `ai` tickets without a `design` or `design-review` label whose blockers are Done, group them by what each unblocks, and recommend a batch no larger than that count, one entry per ticket in the Report format. Start only the tickets the user names.

## Picking up

`/dispatch:manage 15m everything in the Billing project` starts every matching ticket without asking. The text after the optional interval is the instruction, which names which tickets you may work, such as "tickets labeled `dispatch`". With no instruction, ask once which tickets to pick up, using `dispatch:bro`, and start nothing until the user answers. An answer of "none" or "don't pick up" is the plain mode above. The instruction lives only in this session, so put it in your wake prompt, and expect the user to give it again after a restart. "Stop picking up" returns to the plain mode above, and "stop" ends the session.

At start, on each wake and when a ticket leaves In Progress, start every eligible ticket, up to the count under "Starting work" and in this order: Linear priority, then how many tickets it unblocks, then oldest. A ticket is eligible when it:

- carries `ai`, is in Todo or Backlog, and is assigned to `dispatch linear-viewer`'s user;
- has every blocker Done, and is not a child of a ticket carrying `design`, whose pieces dispatch promotes after breakdown;
- matches the instruction, or its body has a `Found by: <ticket>` line naming a ticket you are working (In Progress, In Review or Needs Human, and itself in scope).

A `design` ticket is eligible like any other, and dispatch promotes its pieces after breakdown. A ticket without `ai` is never started and its worker's questions are not answered, so removing `ai` is how the user parks a ticket, or drives a design themselves. Moving it back to Todo does not park it. When you find a ticket you started back in Todo or Backlog, put one line in the Status block saying that removing `ai` parks a ticket.

You never change a ticket's labels, assignee or body. A design or build worker's question about extra scope and a design's review and start-or-hold choices are decided like any other question: extra scope is answered "file it as a new ticket, without a `Found by:` line, so it starts only if the instruction matches it", review and start are answered yes. Eligibility is read from Linear each time, so a restart starts nothing already started. A worker stuck on a popup, a launch failure and the other Status entries still go to the user.

## Tickets workers file

Before you report a ticket a worker filed, search Linear and `dispatch ticket list` for open tickets on the same topic filed by others in the last day, since a teammate's ticket on the same incident lands minutes apart. Report the overlap to the user, and leave marking the duplicate to them. While picking up, the overlap goes in the Status block and the ticket still starts if it is eligible.

## Gaps in dispatch

A gap you find in dispatch itself, such as a missed notification or a worker habit that did damage, becomes a ticket written with `dispatch:ticket`. Say so in a line. Starting that ticket is the user's call, except while picking up, when it starts only if the instruction matches it.
