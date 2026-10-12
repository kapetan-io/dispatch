---
name: design-adr-audit
description: "Audit a blueprint against the repository's Accepted ADRs to find where it violates or contradicts a binding decision, using parallel sub-agents that each own a slice of the ADR corpus, then a synthesized verdict. Use when `design-review` calls for it, or when asked to check a blueprint or plan against the ADRs. Read-only: it reports findings and edits nothing."
---

# Audit a blueprint against the repository's ADRs

Decide whether a plan violates or contradicts any **Accepted** Architecture Decision Record in force. The plan is usually a blueprint, and the audit judges design intent, so there may be no code yet. `design-review` checks the design against itself; this checks it against the binding decisions around it, so a contradiction surfaces as a design fix and not a mid-build surprise.

This is **read-only**. You never edit the plan, the ADRs or code. Acting on findings is a follow-up the user approves.

## 1. Resolve the plan

Use what the user or `design-review` supplied. For a feature ticket's blueprint, a Linear document that sub-agents can't read, write its text and any spec document to files in your scratchpad and list those paths. Assemble every file an agent must read to know what the plan decides, including any ADR the plan itself records. If you can't identify the plan, ask.

## 2. Scope the ADR corpus

Find the ADR directories with `find . -type d -name adr -not -path '*/node_modules/*'`, then decide which sets bind the plan.

- Root ADRs (`docs/adr/`) bind every service. Include them.
- The owning service's ADRs (`services/<svc>/docs/adr/`) bind that service. Include them.
- Another service's ADRs bind the plan only through a shared contract. Include them only then, and say so.

State the scope before dispatching. Only Accepted ADRs are enforceable, and each agent checks the status.

## 3. Partition and dispatch

Every agent reads the whole plan. Split only the corpus, by filename sort order and never by suspected relevance, which would bring in your bias. Give each agent an explicit file list of about 5 to 8 ADRs, keeping corpus boundaries so each gets the right scope note. A corpus of about 10 ADRs or fewer needs one agent. An ADR the plan records is plan input, so list it in the plan files and keep it out of every slice.

Launch all agents at once in one message (`general-purpose`), with the same template and no hints about what to look for, because hints manufacture false positives.

```
You are an ADR-compliance auditor. Decide whether the plan VIOLATES or CONTRADICTS any
Accepted ADR in your batch. Read-only: edit nothing, run no builds or tests.

PLAN (read all in full first; mark any ADR the plan records as part of the plan):
{PLAN_FILES}

YOUR ADR BATCH (audit only these):
{ADR_BATCH}

SCOPE: {SCOPE_NOTE}
Only Accepted ADRs bind. Read each status line, and honor Supersedes links. If the plan
depends on a non-Accepted ADR in your batch, flag it.

METHOD: extract the plan's load-bearing decisions (contract, storage, auth, eventing,
package boundaries, tests, deferred work). Derive each ADR's rule from the ADR text, never
from the plan's account of it. Test each decision against each ADR.

CLASSES, most severe first:
- VIOLATION: does what an ADR forbids or omits what it mandates. Blocking.
- CONTRADICTION: asserts a decision that conflicts with an ADR without superseding it. Blocking.
- SUPERSEDES(confirm): overrides an ADR explicitly. Surface it for a human to confirm.
- TENSION: gray area. Say what would resolve it. Non-blocking.
- COMPLIANT: respects a relevant ADR. One line each, no padding.

EVIDENCE: cite the ADR (number and title), quote the binding clause, and give the plan
location. Silence is a violation only if an ADR mandates the thing. When unsure, use
TENSION.

OUTPUT:
BATCH: <ids>
VERDICT: PASS or FAIL (FAIL on any VIOLATION or CONTRADICTION)
FINDINGS: [CLASS] ADR-<n> <title> | RULE: <clause> | PLAN: <location> | RESOLUTION: <line>
COMPLIANT & RELEVANT: <ids and decisions>
NOT APPLICABLE: <ids>
NON-ACCEPTED DEPENDENCY: <any, or none>
```

## 4. Synthesize

Merge the results. The overall verdict is FAIL if any agent reports a VIOLATION or CONTRADICTION. List findings most severe first with class, ADR, the tension in one line and when it resolves (now, at implementation, or in a named follow-up). Prefix ADR numbers with their corpus, since root ADR-0007 is not service ADR-0007. When several ADRs flag the same underspecified element, say so, because that element needs its own decision. Report a disagreement between a doc and an ADR as a finding, and fix neither side. End by separating act-on-now items from carry-forward items, and ask the user what to do through AskUserQuestion.
