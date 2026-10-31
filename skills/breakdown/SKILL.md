---
name: breakdown
description: "Break a design or a ticket into build tickets, one per piece in dependency order, then start the work or hold it. Use when asked to break down, split, size or file the pieces of a design or ticket, when a design ends without a review, and when asked to start a feature's held work."
---

# Break down

Turn a design into build tickets and ask whether to start the work or hold it. The input is a ticket holding a design: a feature ticket with a blueprint document, or any ticket whose body cites its design. That ticket is the parent. Load `dispatch:converse` and `dispatch:ticket` with the Skill tool first.

## Size it

Size the design before filing anything, unless the parent already has a `Pieces` section, which a rerun reuses. Before you tell the user the verdict, load `dispatch:principle-confidence-from-many-perspectives` with the Skill tool and apply it to the sizing. Tell the user the verdict and the pieces you counted.

> A **piece** is complete when, after it merges, a user or caller can do something they could not do before. Its acceptance criteria are observable at the surface and pass without any other unmerged piece. Group a piece by what a user does together, not by endpoint, flag or rule.
>
> A design is too big for one pull request when it holds more than one piece. Line count alone never makes a design too big. A design a reviewer can read in one sitting, about an hour, fits even when it touches more than one piece. When the two tests disagree, the one-sitting test wins, because easy review sets the size in both directions. Count only what the design commits to build. Work it defers, or lists as a limitation, open question or future work, is not part of any piece.

A design that fits one PR gets exactly one build ticket.

## File the tickets

- **Put `design` on the parent before filing.** The dispatcher never promotes the children of a `design` parent, so nothing filed can start early.
- **Write a `Pieces` section before filing**, one row per piece with a slug, what a user can do after it merges, the blueprint sections it cites, the acceptance criteria it owns (by number and opening words, so a renumbered list still resolves), the pieces it builds on, and any `Glossary changes` or `ADR:` sections it commits. Each glossary change names its target `CONTEXT.md` (a monorepo service may have its own). The first piece commits each change to that file and each ADR under the next free `NNNN-` number, because the design worker's tree is discarded and nobody else will. Every acceptance criterion belongs to exactly one piece, because the build worker tests the criteria its ticket names. An invariant's criterion for an operation belongs to the piece that adds that operation, so no piece ships an operation that writes an invariant's data untested. A rerun then doesn't judge the split again. For a ticket with no blueprint document, put the section in a comment on the parent.
- **List the parent's existing children before creating any, and end each body with a `Piece: <slug>` line.** A rerun after a crash creates only the slugs with no child, so a piece never gets two tickets.
- **File each build ticket in Todo with `dispatch:ticket`**, as a child of the parent, labeled `ai` and the repo. `dispatch:ticket` puts design tickets in Backlog, but a build ticket has to be Todo for the dispatcher to promote it. Each body names the acceptance criteria its piece owns and cites the design's sections instead of copying them, so the design stays the one source of truth.
- **The first piece blocks every other piece, and each piece is also blocked by the pieces it builds on.** The dispatcher reads 10 blocker relations per ticket and never promotes a ticket whose list was cut off, so a piece with more than 9 distinct blockers is a finding. Try merging pieces first, then ask.
- **Check every ticket's labels, status and blockers before you ask.** Read them back from Linear and add any missing blocker relation, since a ticket missing a label never starts, and one missing a blocker starts early.

## Start or hold

Ask the user whether to start the work now or hold it, with your recommendation first. Nothing starts before they answer.

- **Hold.** Leave `design` on the parent and the children in Todo. Tell the user to ask any session to "start <parent>'s work".
- **Start.** Move the parent to In Progress if it isn't, then remove `design`, then run `dispatch tick`. A parent with children never gets a worker, so this launches nothing itself. The parent is now an ordinary parent. The dispatcher promotes the first piece in that tick's cycle, the later pieces as their blockers reach Done, and closes the parent when every child is finished. Removing `design` is the start, so never do it before the user chooses.

When asked to start held work, do the Start move without re-asking.

## A worker breaking down its own ticket

A worker keeps the first piece on its own ticket and branch and files the other pieces as new tickets blocked by it, in Todo with `ai` and the repo label, using the same rules. These are not children, because a parent never gets a worker. The rerun rule still holds: list the tickets this one blocks that carry a `Piece:` line before filing, and keep the `Pieces` section in a comment on your own ticket. The worker moves the new tickets itself, since only promotion of a parent's child otherwise does. Don't break down another session's In Progress build ticket this way, since adding `design` there is meant for parents. Tell its worker instead. Holding leaves them in Todo. Starting moves them to In Progress, and the dispatcher holds each one until its blockers are Done.
