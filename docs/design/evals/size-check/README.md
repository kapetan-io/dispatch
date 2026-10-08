# Size check eval

Status 2026-10-08: `arms/complete-pieces-5.md` is the adopted wording. Each piece becomes a build ticket under the same design. It passed attempt 6 on all 15 cases, 6 of them held out. See `log.md`.

Tests whether a size-check wording decides correctly when a reviewed design ships as one pull request, and how it splits a design that doesn't. The review worker runs the size check before the breakdown (breakdown step, settled 2). The improve stage reruns this suite on any change to that wording.

## Arms

- `arms/complete-pieces.md` is attempt 2's candidate, written after attempt 1 (see `log.md`). It states your goal of small deliverable pieces that are easy to review and ship a complete thing, groups a piece by what a user does together, and designs only the first piece of a split.
- `arms/complete-pieces-3.md` is attempt 4's candidate. It is complete-pieces-2 after a pass with Anthropic's skill-creator guidance: a description that says when to use it, a reason behind each rule, and the decision written as numbered steps. The rules are unchanged.
- `arms/complete-pieces-2.md` is attempt 3's candidate. It adds one rule from the same goal, that review effort sets the size in both directions: a design a reviewer can read in about an hour fits, and a piece that takes longer splits again.
- `arms/own-evidence.md` is the candidate. It adopts pstack's rule that "one PR is one change with its own evidence" and leans toward small designs.
- `arms/current.md` is the wording from the 2026-10-08 size check test in `decision-log.md`.
- `arms/by-layer.md` is a known-bad control. It splits by architectural layer, which yields designs with nothing a user can observe. If the graders pass it, the graders are broken.

Every arm has the same skill name and description, so only the body differs.

## Cases

| Case | Input | What happened | Expected |
|---|---|---|---|
| shape | git-server `technical-shape.md` without D19, D20, D27 | Split into slices. Each slice got its own blueprint and PR. | TOO_BIG |
| tiger | Tiger specification, 1,885 lines | 12 tickets shipped, 11 canceled in the reset | TOO_BIG |
| masked-blob | ENG-125 ticket | One PR, #43, +588/-146 | FITS |
| did-you-mean | ENG-137 ticket, rewritten from past tense to a plan | One PR, #47, +180/-423 | FITS |
| fetch | ENG-117 blueprint | One PR, #37, +2,773 | open |
| graph | ENG-132 blueprint | One PR, #45, +9,117 | TOO_BIG, 3 to 5 pieces (from attempt 2) |
| shallow | ENG-119 blueprint | One PR, #42, +3,548 | open |
| partial | ENG-120 blueprint | One PR, #41, +2,232 | open |
| auth | ENG-126 blueprint | One PR, #44, +3,188 | open |

"Open" means no verdict is expected. You want smaller designs from now on, so history's single PR is not the right answer for these. A split is fine when it passes the split checks below and the runs agree.

Known bias. fetch, graph, shallow, partial and auth call themselves slices or follow-ups of earlier work, which leans toward FITS.

## Graders, per run

- `wrote-file`. `size.yaml` exists.
- `one-verdict`. Exactly one `verdict: FITS` or `verdict: TOO_BIG` line.
- `expected-verdict`. Only on cases with an expected verdict.
- `surface` (LLM judge). Every split design delivers something a user or caller can observe at the surface. A design that is only a seam, backend, data model, package, refactor, or tests and docs fails.
- `no-deferred` (LLM judge, per-case answer key in `cases.json`). No split design exists mainly to build work the input defers or lists as a limitation, open question or future work.
- `skill-fired`. The size-check skill was loaded.

Both judge graders pass a FITS verdict. Each judge grader takes 2 of 3 votes.

## Pass bar (fixed 2026-10-08, before any run)

own-evidence is adopted only if all four hold.

1. shape and tiger come back TOO_BIG in 5 of 5 runs. A grand design judged FITS is the failure that matters most.
2. masked-blob and did-you-mean come back FITS in 5 of 5 runs.
3. `surface` and `no-deferred` each pass in at least 90% of own-evidence's TOO_BIG runs, all cases pooled.
4. On every case, at least 4 of 5 runs give the same verdict.

The control must fail `surface` in more than half of its TOO_BIG runs. If it doesn't, the judge is broken, and no result is read until the grader is fixed.

current is scored on the same four measures for comparison. If both pass, prefer the arm with more stable verdicts on the open cases.

Reported but not gating:
- On shape, whether the first design includes push, and whether push comes before fetch. The real reason push came first (the conformance oracle seeds every fetch with a push) is not in the input.
- The spread in the number of split designs per case.
- Whether the split boundaries agree across runs, judged by a fresh agent that doesn't know which arm produced them.

## Pass bar for attempt 2 (fixed 2026-10-08, before any run)

You agreed on 2026-10-08 that ENG-132 should have shipped as about four pieces (contract pipeline with auth and repo and ref reads, then tree, file and commit reads, then history, then diff). So graph is no longer open.

complete-pieces is adopted only if all five hold.

1. shape and tiger come back TOO_BIG in 5 of 5 runs.
2. masked-blob and did-you-mean come back FITS in 5 of 5 runs.
3. graph comes back TOO_BIG in 5 of 5 runs, with 3 to 5 pieces each time.
4. `surface` and `no-deferred` each pass in at least 90% of TOO_BIG runs, all cases pooled.
5. On every case, at least 4 of 5 runs give the same verdict.

The graders are unchanged from attempt 1, so the control is not rerun. Reported but not gating: whether shape's first piece includes push, and piece counts on shape and tiger.

Runs are halved from attempt 1. Only complete-pieces runs, 5 per case, 45 in all.

Attempt 3 uses the attempt 2 bar unchanged, with tiger's answer key corrected (see `log.md`), 45 runs.

## Held-out cases (added for attempt 4)

Six designs from five other repos that no wording was tuned on.

| Case | Input | What happened | Expected |
|---|---|---|---|
| slip-stream | slip-stream ENG-69 v1 blueprint | One PR, #13, +22,788, 82 files | TOO_BIG |
| change-bot | Mechanical-Change Bot blueprint, the whole product in phases | Not built | TOO_BIG |
| tiger-wave1 | tiger ENG-147 wave 1 blueprint | One PR, #1, +12,689, 184 files (CLI, 20 analyzers, golangci plugin) | TOO_BIG |
| heartbeat | duh.go ENG-75 stream heartbeat | One PR, #14, +718 | FITS |
| dlq | querator ENG-55 dead-letter single writer | One PR, #68, +1,197 | FITS |
| docs-cmd | duh-cli docs subcommand | One PR, #7, +3,451 | open |

## Pass bar for attempt 4 (fixed 2026-10-08, before any run)

complete-pieces-3 runs on all 15 cases, 3 runs each, 45 runs. It is adopted only if all four hold.

1. Every case with an expected verdict gets it in 3 of 3 runs.
2. graph splits into 3 to 5 pieces in every run.
3. `surface` and `no-deferred` each pass in at least 90% of TOO_BIG runs, all cases pooled.
4. On every open case, all 3 runs give the same verdict.

The held-out cases are also reported on their own, since they are the test of whether the wording generalizes. If complete-pieces-3 misses a held-out case, complete-pieces-2 runs on that case too, to tell whether the skill-creator rewrite or the rule caused it.

## Runs

own-evidence 5 per case, current 3, by-layer 2, on `claude-opus-5-5`, with the judge on the same model.

## How to run

`run.sh <arm> <runs>` builds a plugin folder in the scratch directory under a neutral name, with no arm name or "eval" in any path the agent sees, then runs `claude plugin eval` with `--keep-temp`. `report.py` reads the result JSON and the kept traces, pulls each run's `size.yaml` into `results/`, and prints the measures above.
