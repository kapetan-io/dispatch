---
name: ticket
description: Use when creating or rewriting a Linear ticket in a repo dispatch manages, whether asked or filing one yourself. Sets labels, assignee, status and body so dispatch picks the ticket up, and starts the design skill instead of a build for a `design` ticket.
---

# Write a dispatch ticket

The dispatcher manages only tickets assigned to the owner of its Linear API key that carry the AI label, and it starts a worker only after the user moves the ticket to In Progress. A ticket written any other way is ignored or started without approval. The body is read by a worker with no memory of this conversation, and by the user, who may not have seen the code in weeks.

## Read the config first

Read `~/.config/dispatch/config.toml` for `linear.teams`, `linear.ai_label`, `linear.repo_label_group`, `linear.statuses.paused`, `linear.statuses.in_progress` and the `[repos]` table. Hardcode none of them. Omitted keys take dispatch's defaults (`teams = ["ENG"]`, `ai_label = "ai"`, `repo_label_group = "repo"`, `paused = ["Backlog", "Todo"]`, `api_key_keychain = "dispatch-linear-api-key"`).

If the file is missing or this repo is not in `[repos]`, dispatch does not manage it. Write the body, title and a type label, but add no AI or repo label, skip the assignee and status rules, and skip the checklist.

## Fields

- **Team.** With one configured team, use it. With several, use the team of recent tickets carrying this repo's label, and ask the user if there are none.
- **Labels.** An idea or feature whose design isn't settled gets `design`, the AI label and this repo's label, and waits in Backlog. Moving it to In Progress starts the design skill. Otherwise apply the AI label, this repo's label from the `repo_label_group` group, and a type label (Bug, Feature) if the team has one. The `[repos]` key is the label's name and its value is the clone. Expand a leading `~` and compare it with `dirname "$(git rev-parse --path-format=absolute --git-common-dir)"`, since a worktree's own path never matches. Never create a label, because the workspace may belong to someone else. If a needed label is missing, don't create the ticket and tell the user which one.
- **Assignee.** The owner of dispatch's Linear API key, the only person the dispatcher manages tickets for. Read the key from `LINEAR_API_KEY` or the keychain service in `linear.api_key_keychain` (`security find-generic-password -s <service> -w` on macOS). Post `{"query":"{ viewer { id name } }"}` to `https://api.linear.app/graphql` with `Content-Type: application/json` (without it Linear rejects the call as CSRF) and `Authorization: <key>` (no `Bearer`), and assign that user by id. Never print the key. Don't use the Linear connector's `me` or a hardcoded name.
- **Status.** The `Todo` entry of `linear.statuses.paused`, or its `Backlog` entry for a `design` ticket. If the list is empty or lacks the entry you need, don't guess. Don't create the ticket, and ask which paused status new tickets should wait in. The ticket waits until the user moves it to `in_progress`, so never create it there or move it there unless told to. After you do move one, run `dispatch tick` and report whether its worker started, since otherwise it waits for the dispatcher's timer.
- **Title.** One plain phrase saying what changes, at most 50 characters and without the repo or tool name, which the repo label already carries. Shorten by dropping detail the body carries, not by abbreviating words.

## Body

Describe the problem and what is true afterwards. Leave out how to build it (flags to keep, wording to preserve, files to touch) and which tests to write, because the worker may treat every line as a requirement and your guesses become constraints nobody chose. State a decision only if the user made it, and say so ("the user wants X"). Otherwise drop it, or offer it in Notes for the worker as a labelled suggestion.

Use these sections in this order.

1. **In plain English.** Two to four sentences. Someone who has never seen the code should be able to repeat the problem back, so name no file, function or filter. Say what is wrong or missing for whoever uses the thing, and what happens because of it.
2. **What it delivers.** A bullet list. Each item must be observable at the surface (a command's output, an API response, a file that appears). For a bug, put `## What happened` and `## Cause` sections, with your evidence, between In plain English and this section.
3. **Governing design.** The documents the worker must read, by path or link, and the part of each that applies. Say what the change touches and whether it ships additively or behind a flag, or "none" if there is no design.
4. **Not decided here (ask, don't assume).** The product behavior, public contract, security default or ADR questions the worker must put to the user, or "Nothing open". Don't write a guess as if it were settled.
5. **Notes for the worker.** Anything that saves time, such as an example ticket in the same format, a trap you hit, a binary it must not touch, or a suggested approach labelled as one.

When the ticket's purpose is to create, change or evaluate a skill, Notes for the worker also carries these three instructions, stated as the user's requirements. Left alone, workers invent eval loops of 40 to 270 runs, which is why the no-evals line must be written out. They also skip reviewing the skill itself, and write long, prescriptive prose.

- Run no evals. The user has paused evals until they decide whether they're useful.
- Before opening the PR, a sub-agent loads `anthropic-skills:skill-creator` with the Skill tool, reads the changed skill and reviews it without running any test prompts. The worker addresses the review.
- Keep the skill brief. Cut detail and rules the reader would follow without being told.

## Check before you save (managed repos)

- Write each ticket you mention by its identifier (ENG-123) so Linear links it.
- Count the title's characters. Over 50, shorten it.
- Re-read In plain English as the user would. If it needs the code open to make sense, rewrite it.
- On a ticket that touches a skill, confirm Notes for the worker carries each skill instruction listed above.
- Confirm the ticket has the team, AI label, repo label, assignee and paused status. A missing one means the dispatcher never sees it or sees it too soon. A `design` ticket waits in Backlog on purpose, and the user moves it to In Progress to start the design skill.

## After saving

Run `dispatch watch <ticket>` so this session hears the ticket's events. If it fails, say so and carry on.
