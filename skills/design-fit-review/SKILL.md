---
name: design-fit-review
description: "Review a blueprint for fit with the existing system: for each new CLI command, flag, rule scenario API member, setting, history or ledger field, or skill it adds, name the closest thing the repo already has and say whether that thing should do the job instead. Use when `design` or `design-review` calls for it, or when asked whether a design duplicates something. Read-only: it reports findings and edits nothing."
---

# Review a blueprint for fit

Nothing else in `design` or `design-review` asks whether a new thing should exist. A design can add a second CLI command or a second copy of a fact the repo already holds, and nobody notices until the code is written.

Read-only. You report, and the caller acts on it.

## Run

Write the blueprint to a scratchpad file (`get_document` for a Linear blueprint) and give one foreground `general-purpose` sub-agent that path. It sees the blueprint and the repo but not this conversation, so its read is not anchored on the design's own justification. Tell it to use the nearest `docs/CONTEXT.md` terms and the words the code already uses, to coin no labels such as step codes, and to define any term in neither.

It lists every new public or persisted thing the blueprint adds (CLI command, flag, rule scenario API member, setting, history or ledger field, skill). For each it must search before it names anything (`git grep`, the command list, the schema), then report the closest existing thing with file and line, or "none found" and what it searched, and one verdict:

- **Keep.** The existing thing can't take the need, and why, in terms of what it holds or does.
- **Use existing.** The existing thing already covers the need, and the blueprint drops the new one.
- **Extend.** The existing thing covers most of it, and the blueprint should widen it instead of adding beside it.

A copy of data the repo already holds, and a new option that restates an existing one, are **Use existing**.

## Check and report

Check each **Use existing** and **Extend** finding against the cited line before you relay it, since a sub-agent can mistake a similar name for a similar job. Drop the ones that fail. Report the survivors and any **Keep** that is a close call. A finding changes the design, so the user decides each one.
