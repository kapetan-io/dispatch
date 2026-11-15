---
name: ticket
description: Use when creating or rewriting a Linear ticket, or a local ticket when `[plugins.local]` is on, in a repo dispatch manages, whether asked or filing one yourself. Files it with `dispatch ticket create <system>` so dispatch picks the ticket up, writes the body, and starts the design skill instead of a build for a `design` ticket.
---

# Write a dispatch ticket

The dispatcher manages only tickets assigned to the owner of its Linear API key that carry the AI label, and it starts a worker only after the user moves the ticket to In Progress. A ticket written any other way is ignored or started without approval. The body is read by a worker with no memory of this conversation, and by the user, who may not have seen the code in weeks.

## Filing

`dispatch ticket create <system>` files the ticket and prints its id. The system is `linear`, or `local` when config.toml has a `[plugins.local]` table, and there is no default. File a local ticket only when the user asks for one.

```console
$ dispatch ticket create linear --repo dispatch --title "Retry webhooks on 502" --label Feature --attr model=opus --description "…"
ENG-812
$ dispatch ticket create local --repo dispatch --title "Retry webhooks on 502" --attr model=opus --description "…"
LOC-7
```

`--repo` is a `[repos.<label>]` name from `~/.config/dispatch/config.toml`. If the file is missing or lacks the repo's table, dispatch does not manage the repo: write the body and title with the Linear tools, add no AI or repo label and skip the checklist.

`create linear` puts the ticket where dispatch manages it: the configured team (`--team KEY` when config.toml lists several), the AI label, the repo's label, assigned to the owner of the API key, waiting in Todo. `--label` adds a label that exists and fails otherwise. `--status` takes another paused status. `--parent`, `--blocked-by` and `--start-pr` are refused for Linear, so to file a child, create it and set its parent with the Linear tools afterwards.

`create local` starts in the status with the `todo` job, and moving it to In Progress starts a worker. `--parent` and `--blocked-by` take `LOC-` ids. `dispatch ticket statuses` lists the statuses.

`dispatch run` must be running, or the command exits 3 with `dispatch: not running`.

## Fields

- **Labels.** An idea or feature whose design isn't settled gets `--label design` and `--status Backlog`. Moving it to In Progress starts the design skill. A design review is a child of the feature ticket labeled `design-review`, never `design`, in Todo. Moving it to In Progress starts a review worker on the next tick. The design skill makes that move itself when the user chooses review. Add a type label (Bug, Feature) if the team has one.
- **Status.** The ticket waits until the user moves it to `in_progress`, so never move it there unless told to. To convert a ticket that is already In Progress, first move it to a paused status, then assign it to the user `dispatch linear-viewer` prints and add the AI label in a later save, because an In Progress ticket that carries the AI label starts a worker. After you do move one, run `dispatch tick` and report whether its worker started, since otherwise it waits for the dispatcher's timer.
- **Title.** One plain phrase saying what changes, at most 50 characters and without the repo or tool name, which the repo label already carries. Shorten by dropping detail the body carries, not by abbreviating words.

## Body

Write every section from the problem domain and the surface, even for a technical change. Use the domain's terms, and describe what a user or caller sees, not how the code or tests achieve it. Technical detail goes in Notes for the worker, labelled as a suggestion.

Describe the problem and what is true afterwards. Leave out how to build it (flags to keep, wording to preserve, files to touch) and which tests to write, because the worker may treat every line as a requirement and your guesses become constraints nobody chose. State a decision only if the user made it, and say so ("the user wants X"). Otherwise drop it, or offer it in Notes for the worker as a labelled suggestion. A design the user hasn't approved isn't governing design.

Use these sections in this order.

1. **In plain English.** Two to four sentences. Someone who has never seen the code should be able to repeat the problem back, so name no file, function or filter. Say what is wrong or missing for whoever uses the thing, and what happens because of it.
2. **What it delivers.** A bullet list. Each item must be observable at the surface (a command's output, an API response, a file that appears). For a bug, put `## What happened` and `## Cause` sections, with your evidence, between In plain English and this section.
3. **Governing design.** The documents the worker must read, by path or link, and the part of each that applies. Say what the change touches and whether it ships additively or behind a flag, or "none" if there is no design.
4. **Not decided here (ask, don't assume).** Product behavior, public contract, security default and ADR questions are the user's. Sort each one, including any the review below turns up. If this conversation has the facts the user needs to answer it, ask the user before you save and write the answer into the ticket as theirs ("the user wants X"). Otherwise list it here, which means one of three things: answering needs research you can't do now, no user is present (a worker breaking down its own ticket), or the user chose not to decide it. With none listed, write "Nothing open". Don't write a guess as if it were settled. A `design` or `design-review` ticket keeps its questions for the design session.
5. **Notes for the worker.** Anything that saves time, such as an example ticket in the same format, a trap you hit, a binary it must not touch, or a suggested approach labelled as one.

Set an attribute with `--attr key=value` on `create`, and change one later with `dispatch ticket edit <id> --attr key=value` or remove it with `--unset-attr key`, on either system. `edit` of a Linear ticket rewrites only its `## Attributes` section. To set the model this ticket's worker runs on, set the attribute `model` to a model name, such as `--attr model=opus`.

A worker files a bug it found outside its own ticket's change with a last line `Found by: <the worker's ticket>`, which tells a managing session that picks tickets up to start it. No one else writes that line.

When the ticket's purpose is to create, change or evaluate a skill, Notes for the worker also carries these three instructions, stated as the user's requirements. Left alone, workers invent eval loops of 40 to 270 runs, which is why the no-evals line must be written out. They also skip reviewing the skill itself, and write long, prescriptive prose.

- Run no evals. The user has paused evals until they decide whether they're useful.
- Before opening the PR, a sub-agent loads `anthropic-skills:skill-creator` with the Skill tool, reads the changed skill and reviews it without running any test prompts, and is told to use the nearest `docs/CONTEXT.md` terms, coin no labels such as step codes, define any term in neither, and follow `dispatch:principle-verify-dont-assume` (check the claims it relies on, quote file:line, label what it didn't read as a guess). The worker addresses the review.
- Keep the skill brief. Cut detail and rules the reader would follow without being told.

## Review before you save (managed repos)

You wrote the draft from a conversation the worker never saw, so you can't see what it leaves out, and your own certainty is one perspective. A guess you stated as a question, or a code fact you didn't check, costs the user a round trip. One fresh reviewer is the default, and `dispatch:principle-confidence-from-many-perspectives` decides when the ticket's risk needs more. Skip this for a `design` or `design-review` ticket, which is worked with the user present. For any other ticket, write the draft to a file in your scratchpad, with a copy of any cited design that isn't a repo file, and start a foreground sub-agent (`model: "sonnet"`). Give it those paths, the repo and the name of the branch the worker will build on, and nothing from the conversation, so it sees what the worker sees. Tell it to use the nearest `docs/CONTEXT.md` terms and the words the code already uses, to coin no labels such as step codes, to define any term in neither, and to follow `dispatch:principle-verify-dont-assume` (check the claims it relies on, quote file:line, label what it didn't read as a guess). Tell it to run `git fetch` and read the code from that branch (`git show origin/<base>:<path>`), not the current checkout, which may be stale.

Ask it to:

1. State the ticket's intent in a sentence.
2. Check every claim about the code against the repo: cause, repro, what exists today, what the change touches. Quote the claim and give file:line for what it found. Look for the other places the same thing lives (copies, callers, stored data, other surfaces) that the ticket doesn't mention.
3. For each "Not decided" question, check whether the code or design already answers it or makes it moot.
4. Name any deliverable that isn't observable at the surface, or that doesn't serve the intent.

It returns the intent sentence, then only findings, each as the claim or question quoted, what the repo shows, and file:line where the finding is about code. No fixes, no praise. A claim about code it can't back with a file is not a finding, and "nothing found" is an answer. Check its intent sentence against what you meant.

You judge each finding, because only you know what the conversation settled. Check the evidence yourself first.

- **Real gap or wrong claim.** Fix the ticket.
- **Settled in the conversation.** Write the decision into the ticket, as the user's ("the user wants X"). "We discussed it" is not a reason to drop a finding unless what was discussed is now in the ticket.
- **Deliberately out of play.** Say so in the ticket (non-goal, or "Nothing open" with the reason), so the worker doesn't rediscover it.
- **Moot question.** Delete it.
- **A call the user hasn't made.** Ask or list it as "Not decided here" says.
- **Wrong.** Set it aside only with the evidence that disproves it.

Review again only if you changed what the ticket asks for, not for wording. Tell the user in a line what the review changed.

## Check before you save (managed repos)

- Write each ticket you mention by its identifier (ENG-123) so Linear links it.
- Write a GitHub issue or pull request as its full URL, never a bare `#N`. Linear links a bare number to whichever repo's item of that number it finds first.
- Count the title's characters. Over 50, shorten it.
- Unless it's a `design` or `design-review` ticket, confirm the review ran and each finding landed in the ticket or was set aside with evidence.
- Confirm each "Not decided" entry needs research, has no one to ask, or was declined, and that you asked every other call.
- Re-read In plain English as the user would. If it needs the code open to make sense, rewrite it.
- On a ticket that touches a skill, confirm Notes for the worker carries each skill instruction listed above.
