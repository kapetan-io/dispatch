---
name: ticket
description: Use when creating or rewriting a Linear ticket in a repo dispatch manages, whether asked or filing one yourself. Sets labels, assignee, status and body so dispatch picks the ticket up, or ignores it while its design is unsettled.
---

# Write a dispatch ticket

The dispatcher manages only tickets assigned to the owner of its Linear API key that carry the AI label, and it starts a worker only after the user moves the ticket to In Progress. A ticket written any other way is either ignored or started without approval. The body is read by a worker with no memory of this conversation, and by the user, who may not have seen the code in weeks.

## Read the config first

Read `~/.config/dispatch/config.toml`. You need `linear.teams`, `linear.ai_label`, `linear.repo_label_group`, `linear.statuses.paused`, `linear.statuses.in_progress` and the `[repos]` table. Hardcode none of them, since the workspace and the repo change from run to run. A key the file omits takes dispatch's default (`teams = ["ENG"]`, `ai_label = "ai"`, `repo_label_group = "repo"`, `paused = ["Backlog", "Todo"]`, `api_key_keychain = "dispatch-linear-api-key"`). If the file does not exist, or this repo is not in `[repos]`, dispatch does not manage it. Then write the body below, the title and a type label, but add no AI label and no repo label, and skip the assignee and status rules, since no dispatcher would pick the ticket up. The checklist at the end does not apply.

## Fields

- **Team.** With one configured team, use it. With several, use the team of recent tickets that carry this repo's label, and ask the user if there are none.
- **Labels.** An idea or feature whose design isn't settled gets the `design` label and no AI label, and waits in Backlog instead of Todo. The dispatcher ignores it, and it is visibly not ready. Otherwise apply the AI label, this repo's label from the `repo_label_group` group (the `[repos]` key is the label's name, and its value is the clone; expand a leading `~`, and compare it with `dirname "$(git rev-parse --path-format=absolute --git-common-dir)"`, since a worktree's own path never matches), and a type label (Bug, Feature) if the team has one. Never create a label, because the workspace may belong to someone else. If a needed label is missing, do not create the ticket. Tell the user which label is missing.
- **Assignee.** The owner of dispatch's Linear API key, since that is the only person the dispatcher manages tickets for. Look the owner up with the key (`LINEAR_API_KEY`, or the keychain service in `linear.api_key_keychain`) with curl: read the key (`security find-generic-password -s <service> -w` on macOS), post the JSON body `{"query":"{ viewer { id name } }"}` to `https://api.linear.app/graphql` with the headers `Content-Type: application/json` (without it Linear rejects the call as CSRF) and `Authorization: <key>` (no `Bearer`), and assign that user by id. Never print the key. Do not use the Linear connector's `me`, which is whoever the connector logged in as, and do not hardcode a name.
- **Status.** The `Todo` entry of `linear.statuses.paused` (the default list is Backlog, Todo, and Backlog is for work nobody has approved to be queued). If the configured list has no `Todo` (or is empty), do not guess an order. Do not create the ticket, and ask the user which paused status new tickets should wait in. The ticket waits there until the user approves it by moving it to the `in_progress` status, so never create it In Progress and never move it there yourself unless the user told you to. When you do move a ticket, run `dispatch tick` right away and report whether its worker started, because otherwise the ticket waits for the dispatcher's timer.
- **Title.** One plain phrase saying what changes. Do not prefix it with the repo or tool name, since the repo label does that job. Keep it to 50 characters or fewer, and shorten it by dropping detail the body already carries, not by abbreviating words.

## Body

Use these sections in this order.

1. **In plain English.** Two to four sentences. Someone who has never seen the code should be able to repeat the problem back, so name no file, function or filter. Say what is wrong or missing for whoever uses the thing, and what happens because of it.
2. **What it delivers.** A bullet list. Each item must be observable at the surface (a command's output, an API response, a file that appears), because the worker writes one passing surface test per item and cannot test anything else. For a bug, put `## What happened` and `## Cause` sections, with the evidence you gathered, between In plain English and this section.
3. **Governing design.** The documents the worker must read, by path or link, and the part of each that applies. Also say what the change touches and whether it ships additively or behind a flag. Say "none" if there is no design.
4. **Not decided here (ask, don't assume).** The product behavior, public contract, security default or ADR questions the worker must put to the user. Write "Nothing open" when there are none. Do not write down a guess as if it were settled.
5. **Notes for the worker.** Anything that saves a worker time, such as an example ticket in the same format, a trap you hit, or a binary it must not touch.

When the ticket's purpose is to create, change or evaluate a skill, this section must also carry two instructions, because workers left alone invent eval loops of 40 to 270 runs and never review the skill itself. First, before any eval run, a sub-agent loads `anthropic-skills:skill-creator` with the Skill tool, reads the changed skill and reviews it against that guidance, and the worker addresses the review before running anything. Second, the eval budget is 5 runs per case and at most 3 attempts. If the pass bar isn't met by then, the worker stops and posts a Needs Human comment with the results so far instead of trying again.

## Check before you save (managed repos)

- Write each ticket you mention by its identifier (ENG-123) so Linear links it.
- Count the title's characters. Over 50, shorten it.
- Re-read the In plain English section as the user would. If it needs the code open to make sense, rewrite it.
- On a ticket that creates, changes or evaluates a skill, confirm Notes for the worker carries both instructions. The skill-creator review comes before any eval and the worker addresses it before running anything. The budget is 5 runs per case and at most 3 attempts, after which the worker stops and posts a Needs Human comment with the results so far.
- Confirm the ticket has the team, the AI label, the repo label, the assignee and the paused status. A missing one means the dispatcher never sees it or sees it too soon. A `design` ticket is the exception, since it carries `design` in Backlog and no AI label on purpose.
