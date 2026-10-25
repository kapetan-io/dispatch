---
name: design-review
description: "Review a feature ticket's blueprint with the user, audit it against the repo's ADRs, review its fit with the existing system, then break the design down into build tickets. Use when started as `/dispatch:design-review <review ticket>`, or when asked to review a design held in a Linear blueprint document."
---

# Review a Blueprint

You run in the review worker for one `design-review` ticket, a child of the feature ticket that holds the blueprint as a Linear document. The dispatcher started you in a tree it leased, and you own the review ticket until it is Done or in Needs Human. Load `dispatch:converse` with the Skill tool first, and open a todolist for the steps below. You run unattended until a question needs the user, so ask in chat, as `dispatch:converse` says. Never run `/loop`, cron, `treehouse return`, or `dispatch tick` after your own move to Done, which would tear down the workspace you run in. The tick in `dispatch:breakdown`'s Start step runs while you are still In Progress, so it is safe. You never commit. Your tree is discarded.

## Goal

Make the blueprint **buildable**: an engineer can build the right feature from it without getting stuck on ambiguous contracts, contradictions, missing decisions or unverifiable acceptance criteria, and without the technical half drifting from the product intent. A goal-validation agent confirms this, not a count of findings. The review does not pursue completeness or polish.

## Steps

1. **Orient and resume.** Read the review ticket, its comments, its parent, and the parent's documents and children. No parent or no blueprint document: comment under `## In plain English` and move the ticket to Needs Human. You may be relaunched with no memory, so take the first row that is true.
   - The feature ticket lacks `design` and has children with a `Piece:` line: approval already happened, so move the review ticket to Done and stop.
   - The blueprint has a `Pieces` section: go to step 8.
   - Otherwise: step 2.
2. **Pre-work**, below.
3. **The loop**, Phases 1 to 6, at most 5 rounds.
4. **Five rounds without PASS.** Comment the remaining blockers on the review ticket, opening with `## In plain English`, move it to Needs Human, file nothing and stop. The user reads the ticket, not your scrollback.
5. **ADR audit.** When the repo has `docs/adr/`, run `dispatch:design-adr-audit` on the blueprint. A fix it causes always gets one more Phase 6 run, even when round 5 already passed, since an unvalidated fix could break the blueprint. Run it after step 6, which shares it. That run counts as a round, and if it fails with all 5 used, follow step 4 without a further round. Ask its closing question in chat.
6. **Fit review.** Run `dispatch:design-fit-review` on the blueprint. Put each Act on and Consider item in its verdict to the user as Phase 5 does, and apply and log the answer. If step 5 or this step changed the blueprint, run Phase 6 once more after both. That run counts as a round, as in step 5.
7. **End steps**, below.
8. **Break down.** Invoke `dispatch:breakdown` on the feature ticket.
9. **Finish.** After the user answers start or hold, comment a summary on the review ticket that says how to start held work, then move it to Done. You never move the review to Done before `design` is off on "start now". A crash between the two leaves the review In Progress, and step 1 finishes it. Open the comment with the final summary:

```
## Blueprint Review Complete
**Iterations**: N
**Auto-applied fixes**: N, one line each
**User-resolved findings**: N, one line each
**User dismissals**: N
Blueprint: <document link>
```

## Reading and writing the blueprint

Read the blueprint with `get_document` on the feature ticket and write it to `blueprint.md` in your scratchpad. Sub-agents read that file, never Linear, so a missing Linear connection can't leave one reviewing nothing. Rewrite the file from a fresh `get_document` after every patch, so each round sees the blueprint as the last patch left it. Point them at `docs/CONTEXT.md` (the domain glossary) if it exists, and give only the list of files in `docs/adr/`, so they read specific ADRs on demand.

Linear rewrites markdown on save: bullets come back as `*`, a leading H1 is dropped and lines can gain blanks. Apply each fix as a `save_document` patch whose old text you copied from a fresh `get_document`, and read again before the next patch. Never push the whole document back.

Log each user answer or dismissal in the blueprint's Decision Log as "you decided (review)" as you get it, so a rerun doesn't ask again.

## Pre-work

Before the loop, do three things and pass the results to the sub-agents.

- **Coverage map.** Read the blueprint end to end and note which sections cover which topics across both halves (problem, users, scope, correctness constraints, acceptance criteria, stories, interfaces, architecture, data, security, PII, scale, testing, failure modes). Note whether it has Correctness Constraints and User Stories sections, since some finding categories depend on them. Read the title of every ADR, and the full text of any that caps product scope or names a topic the blueprint touches. A topic an ADR settles is not a gap because the blueprint refers out to it.
- **Code drift scan.** Where code implements the technical half and disagrees with the blueprint, note the passage, the code location, what differs, and whether the code is authoritative (shipped and tested) or unclear. Skip this when no code exists.
- **Soft-flag triage.** For each `[NEEDS CLARIFICATION: ...]` marker, check whether the document answers it elsewhere. Classify it stale or unresolved, with evidence.

## The loop

Run Phases 1 to 5, then Phase 6, each round on the blueprint as the last round left it. Exit on a Phase 6 PASS, or after 5 rounds. Phase 6 always runs, even after a round with no findings. From round 2, Phase 1 receives only the blockers Phase 6 named, and a deferred list of what the user dismissed, which it must not re-file unless the surrounding text changed.

All sub-agents are foreground and use `model: "sonnet"`. Every sub-agent prompt, the reviewer, validator and cold reader included, says to use the glossary's terms and the words the code already uses, to coin no labels such as step codes, and to define any term in neither.

## Phase 1: Review agent

Prompt: you are reviewing a Blueprint, one document holding a feature's product definition and technical design. Find gaps, inconsistencies and ambiguities that would force product or implementation decisions during the build, and places where the technical half does not satisfy the product intent. Give it the blueprint path, the glossary path, the ADR file list, the coverage map, the pre-work findings, the round number, and in round 2+ the prior fixes, the deferred list and the blockers.

Each finding has a category, half (Product, Technical, Stories, Cross-half), priority, section, evidence, issue and suggested fix, plus authoritativeness for drift. Priorities are P0 (blocks the build), P1 (forces a decision an engineer shouldn't make alone) and P2 (polish). File nothing below P2.

**Buildability gate.** File a finding only if an engineer reading just the blueprint and the ADRs could get stuck, build the wrong thing, or make an irreversible mistake. A blueprint fixes intent, contracts and decisions, not implementation detail.

**Categories**, each needing the evidence noted:
- Product: *Gap (product)* (a needed topic entirely omitted; one substantive sentence is not a gap; name the sections searched), *Inconsistency* (quote both passages), *ADR conflict* (quote the passage and name the ADR), *Scope ambiguity*, *Implicit assumption*, *Unresolved open question*, *Correctness gap* (a requirement too imprecise to test without a product decision; only where the domain has a deterministic answer the blueprint hides).
- Technical: *Gap (technical)* (a topic the product half requires and the technical half omits), *Drift from code* (quote both the passage and the code location), *Ambiguous implementation decision* (name two incompatible interpretations), *Missing testing surface*, *Missing failure mode*, *Implementation leakage* (function bodies, full SQL, pinned versions; a frozen signature for an internal surface; consumer-facing signatures stay).
- Correctness translation, only with a Correctness Constraints section: *Missing invariant preservation*, *Representable illegal state*, *Infeasible behavioral constraint*.
- *Domain boundary leak*: a domain branching on which implementation backs an interface, or relying on an implementation property that is not part of the interface contract.
- *Ambiguous unset setting*: a setting whose unset (Go zero) value could mean two things, or one named for what it holds rather than what the code does with it, per `dispatch:principle-zero-value-settings`. Quote the setting and both readings.
- Stories, only with a User Stories section: *Incorrect story*, *Missing story*, *Missing acceptance criterion*, *Story scope issue*, *Unverifiable criterion*, *Invariant violation (stories)*, *Missing correctness story*.
- *Product/technical inconsistency*: quote both passages that diverge.

**Anti-scope-expansion.** A review sharpens what the blueprint covers. It adds no features, user segments or integrations, flags no topic the coverage map shows as covered, no "what if" the product half doesn't require, and no missing success metrics unless the blueprint claims an outcome that needs measuring. A finding without evidence is not filed.

## Phase 2: Validation agent

Give it the findings, the blueprint path, the glossary path, the ADR list and the coverage map. For each finding it reads the cited section and answers **Confirmed**, **False positive** (say why: covered elsewhere, an ADR settles it, quoted out of context, same concept in different words) or **Needs clarification** (a real ambiguity only the user can settle). It verifies drift locations, both passages of an inconsistency and the ADR itself. For stories, silence is not a violation, and an unverifiable criterion must truly be unverifiable.

## Phase 3: Skeptic agent

Skip when Phase 2 confirmed nothing. Give the confirmed findings and the blueprint path to an agent that tries to disprove each, reading the blueprint itself and verifying every quote. Verdicts are **Upheld**, **Downgraded to false positive**, **Downgraded to clarification**, or **Dismissed as implementation detail**. Its tests are buildability (would an engineer get stuck?), requirement tracing (does the product half require this?), not-a-tutorial (does it ask for how rather than what?) and diminishing returns (does it lengthen the document without removing an ambiguity?). It downgrades inflated priorities and misquotes, and upholds what is genuinely correct.

## Phase 4: Fix proposal agent

Drop false positives and implementation details. With no survivors, skip to Phase 6. Give the survivors and the blueprint path to an agent that proposes 2 to 3 concrete patch options per finding with a recommendation, and a **Routing** of auto-apply or surface-to-user. Fixes stay short, expand no scope, and read without the review's context. Tightly coupled findings get one combined fix.

**auto-apply** (no product or design decision, no new scope): stale soft flags; implementation leakage; drift where the code is authoritative; a product/technical inconsistency the technical half resolved in the only reasonable way; clear-cut story corrections the blueprint already specifies. Never auto-apply anything that adds requirements, features, segments or integrations, or narrows product scope.

**surface-to-user**: gaps, inconsistencies, ADR conflicts, scope ambiguity, implicit assumptions, open questions, correctness gaps, ambiguous implementation decisions, missing testing surfaces or failure modes, invariant findings, domain boundary leaks, story findings needing judgment, drift where authority is unclear, an inconsistency where it is unclear which side is right, and every needs-clarification verdict.

## Phase 5: Apply and ask

Apply each auto-apply fix as a patch, and note it for the step 9 comment.

For each surface-to-user finding, ask one chat message per finding, headed "Finding N: <title>", with the category in plain words. Say what leaving it open would cost, then give your recommendation with the trade-off of each alternative, and wait for the answer before the next finding. A clarification finding frames the ambiguity as the question. Apply the answer at once, and log it. If the user says it's already in the blueprint, re-read that section before going on.

## Phase 6: Goal validation

A separate agent reads `blueprint.md` cold, with the glossary path and ADR list, and answers one question: could an engineer build the right thing from this tomorrow, or would they get stuck or build the wrong thing? It does not look for improvements. It checks: (1) product intent is clear and acceptance criteria are mechanically verifiable; (2) contracts are unambiguous enough for two engineers to build compatible sides; (3) nothing contradicts, including across the halves; (4) no decision is deferred that an engineer needs before writing code; (5) the technical half covers each product requirement that needs a technical decision; (6) with Correctness Constraints, a reader can see how each invariant is preserved and no invariant rests on an unstated implementation property; (7) with User Stories, each has verifiable criteria and the stories cover the workflows and constraints. Missing detail, brevity and "could be clearer" are not blockers.

It answers **VERDICT: PASS or FAIL**, one line per criterion, and on FAIL at most 5 blockers naming what is wrong and where. PASS ends the loop. FAIL starts the next round with those blockers, or follows step 4 after round 5.

## End steps

After the loop, read the document once more to confirm every resolved finding landed, and apply any that did not. Keep unchanged sections in their order, replace resolved soft flags with the decision, and add nothing for dismissed findings.

1. **Glossary.** Keep the blueprint's `Glossary changes` section current with the terms the review corrected or added (format in [CONTEXT-FORMAT.md](../design/CONTEXT-FORMAT.md)). Your tree is discarded, so `dispatch:breakdown` hands the section to the first piece to commit.
2. **ADRs.** Offer an ADR for a logged decision only if it is hard to reverse, surprising without context, and a real trade-off. Don't re-offer ones already offered. For each the user approves, add an `ADR: <title>` section in the format of the ADRs already in `docs/adr/`, unnumbered. The first piece that implements it commits it under the next free `NNNN-` number.
3. **Contract.** If the blueprint defines or changes a consumer-facing contract, its spec document on the feature ticket must list every endpoint, schema and error code the blueprint names, and nothing it doesn't. If `duh` is on PATH, run `duh lint` on the spec until it passes, patching the result back and reading it back before the next lint. A design with no such surface skips this.
