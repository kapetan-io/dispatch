# 3. Name rule records and actions for what they are, keep Go from choosing or parsing the rules' words, and hide the Linear key settings from rules

Date: 2026-10-22

## Status

Accepted

## Context

- Records, events and ctx helpers are named for the tool that reports them (`linear.ticket`, `ctx.herdr.queueNudge`, `github.check.completed`). That puts the tools ADR 0001 keeps behind ports into every rule.
- Each action has two names: a tool kind (`herdr.nudge`) that rules and rule scenarios use, and a receipt name (`nudge`) that history writes.
- Memory keys each fact by record kind (`linear.ticket/ENG-1/status`), and four facts hold a reference saved with the kind in its value.
- Go and the shipped rules share strings no type names: Go maps once-key topics the rules pick to the words followers act on, and Go writes a "kept: " prefix into a refused return's detail that a rule strips.
- `ctx.config` shows rules the Linear key settings (`api_key_command`, `api_key_keychain`), while it already hides `token_command`.

## Decision

- We will name records and their ctx helpers for what the record is (`ticket`, `pr`, `worker`, `tree`, `escalation`), and events the same way (`comment.posted`, `check.completed`). The `dispatch` record, `ctx.notify`, `ctx.http` and `ctx.ledger` keep their names.
- We will name actions by their receipt name everywhere, in rule scenarios too, and delete the tool kind.
- We will have a rule pass a notification's reason as a `reason` option, with the once key's topic as the reason when it passes none, and write a refused return's detail as the reason alone, so Go maps and parses none of the rules' words. Following sessions act on the reason words the shipped rules pass.
- We will hide the Linear key settings from `ctx.config`.
- We will not rewrite history saved before this decision, and history carries no version number.

## Consequences

- The binary and the rules directory (`[dispatch].rules`) change together. A rule spelling an old record kind or event fails to load and stops the whole load; one calling an old ctx helper from a `dispatch` rule loads and throws when it runs. `dispatch rules check`, run with the new binary on PATH, rewrites `types/` and names every old spelling.
- At the first restart onto a build with these names, memory saved by an earlier build no longer matches: every ticket, PR, worker, tree and escalation fact gets a new `since`, at the first cycle that reads Linear, herdr and every pool. Once keys built on a `since` change, so rules act once more. A rule that reads a present worker's `seenAt`, `idleSince` or `blockedSince` would also act early or drop a nudge, and auto-merge could merge a PR whose latest review never reached its worker; so the first restart is made only when no Claude session runs in any worker, and none of those apply. What still happens once:
  - an alert for each ticket in Needs Human, a kept-tree comment on each tree kept for unpushed work, and a nested-repo refusal run again, which is refused again;
  - In Review tickets with a PR that needs work or has a review thread are relaunched, and In Progress tickets that came back from Needs Human at any time get a resume nudge once their new Claude session is idle;
  - nudge and relaunch counts since the last status change restart at zero, so escalations for ignored PRs and relaunch limits come later;
  - a ticket moved out of Needs Human while dispatch was down is not seen as moved, so it gets no resume nudge.
- A notification whose rule passes no `reason` carries its once key's topic, as before. A later step of an ordered list saved by an earlier build carries no `reason`, so a notify resumed from it carries its topic instead of the words the earlier build mapped it to.
- `dispatch replay` over a segment written before the rename fails to load its recorded rules and exits 1, and stops there; `--from` a time after the restart starts past such segments. `dispatch replay --rules` over such a segment reports a difference at the first notify want, whose options now hold a `reason`, or at the first cycle that wants an action keyed on a `since`, whichever comes first; it compares truly only over segments written after the restart. `dispatch explain` finds a ticket in such a segment by its receipts and decisions, not its saved facts.
- Rolling back means rolling back the binary and the rules directory together, and the older build meets memory keyed by the new names, so the same one-time effects happen again. A build whose executor switches on the tool kind fails each saved later step that has no tool kind, with "no executor for".
