---
name: ticket
description: Use when creating or rewriting a Linear ticket in a repo dispatch manages, whether asked or filing one yourself. Sets labels, assignee, status and body so dispatch picks the ticket up, and starts the design skill instead of a build for a `design` ticket.
---

# Write a dispatch ticket

The dispatcher manages only tickets assigned to the owner of its Linear API key that carry the AI label, and it starts a worker only after the user moves the ticket to In Progress. A ticket written any other way is ignored or started without approval. The body is read by a worker with no memory of this conversation, and by the user, who may not have seen the code in weeks.

## Read the config first

Read `~/.config/dispatch/config.toml` for `linear.teams`, `linear.ai_label`, `linear.repo_label_group`, `linear.statuses.paused`, `linear.statuses.in_progress` and the `[repos.<label>]` tables. Hardcode none of them. Omitted keys take dispatch's defaults (`teams = ["ENG"]`, `ai_label = "ai"`, `repo_label_group = "repo"`, `paused = ["Backlog", "Todo"]`).

If the file is missing or this repo has no `[repos.<label>]` table, dispatch does not manage it. Write the body, title and a type label, but add no AI or repo label, skip the assignee and status rules, and skip the checklist.

## Fields

- **Team.** With one configured team, use it. With several, use the team of recent tickets carrying this repo's label, and ask the user if there are none.
- **Labels.** An idea or feature whose design isn't settled gets `design`, the AI label and this repo's label, and waits in Backlog. Moving it to In Progress starts the design skill. A design review is a child of the feature ticket labeled `design-review`, the AI label and this repo's label, never `design`, in Todo. Moving it to In Progress starts a review worker on the next tick. The design skill makes that move itself when the user chooses review. Otherwise apply the AI label, this repo's label from the `repo_label_group` group, and a type label (Bug, Feature) if the team has one. The `[repos.<label>]` table name is the label's name and its `path` is the clone. Expand a leading `~` and compare it with `dirname "$(git rev-parse --path-format=absolute --git-common-dir)"`, since a worktree's own path never matches. Never create a label, because the workspace may belong to someone else. If a needed label is missing, don't create the ticket and tell the user which one. Label names match ignoring case, because Linear allows no two that differ only in case, so an existing `Design` is the `design` label, not a missing one.
- **Assignee.** The owner of dispatch's Linear API key, the only person the dispatcher manages tickets for. Run `dispatch linear-viewer`, which prints that user's `id` and `name` as JSON, and assign that user by id. If it fails, don't create the ticket. Report the error. Don't use the Linear connector's `me` or a hardcoded name.
- **Status.** The `Todo` entry of `linear.statuses.paused`, or its `Backlog` entry for a `design` ticket. If the list is empty or lacks the entry you need, don't guess. Don't create the ticket, and ask which paused status new tickets should wait in. The ticket waits until the user moves it to `in_progress`, so never create it there or move it there unless told to. To convert a ticket that is already `in_progress`, first move it to the paused status chosen above, then add the AI label in a later save, because a save that does both can start a worker nobody approved. After you do move one, run `dispatch tick` and report whether its worker started, since otherwise it waits for the dispatcher's timer.
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

When the ticket's purpose is to create, change or evaluate a skill, Notes for the worker also carries these three instructions, stated as the user's requirements. Left alone, workers invent eval loops of 40 to 270 runs, which is why the no-evals line must be written out. They also skip reviewing the skill itself, and write long, prescriptive prose.

- Run no evals. The user has paused evals until they decide whether they're useful.
- Before opening the PR, a sub-agent loads `anthropic-skills:skill-creator` with the Skill tool, reads the changed skill and reviews it without running any test prompts, and is told to use the nearest `docs/CONTEXT.md` terms, coin no labels such as step codes, and define any term in neither. The worker addresses the review.
- Keep the skill brief. Cut detail and rules the reader would follow without being told.

## Review before you save (managed repos)

You wrote the draft from a conversation the worker never saw, so you can't see what it leaves out, and your own certainty is one perspective. A guess you stated as a question, or a code fact you didn't check, costs the user a round trip. One fresh reviewer is the default, and `dispatch:principle-confidence-from-many-perspectives` decides when the ticket's risk needs more. Skip this for a `design` or `design-review` ticket, which is worked with the user present. For any other ticket, write the draft to a file in your scratchpad, with a copy of any cited design that isn't a repo file, and start a foreground sub-agent (`model: "sonnet"`). Give it those paths, the repo and the name of the branch the worker will build on, and nothing from the conversation, so it sees what the worker sees. Tell it to use the nearest `docs/CONTEXT.md` terms and the words the code already uses, to coin no labels such as step codes, and to define any term in neither. Tell it to run `git fetch` and read the code from that branch (`git show origin/<base>:<path>`), not the current checkout, which may be stale.

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
- Confirm the ticket has the team, AI label, repo label, assignee and paused status. A missing one means the dispatcher never sees it or sees it too soon. A `design` ticket waits in Backlog on purpose, and the user moves it to In Progress to start the design skill.
