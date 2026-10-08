# Size check eval log

One row per attempt. Pass bar is in `README.md`.

## 2026-10-08, attempt 1

All on `claude-opus-5-5`, judge on the same model.

| Arm | Runs | 1. grand designs TOO_BIG | 2. small designs FITS | 3. surface / no-deferred | 4. verdicts agree | Cost |
|---|---|---|---|---|---|---|
| own-evidence | 5 | pass (5/5, 5/5) | **fail**: did-you-mean TOO_BIG 5/5 | 100% / **84%** | **fail**: auth 3 FITS, 2 TOO_BIG | $22.08 |
| current | 3 | pass (3/3, 3/3) | **fail**: did-you-mean TOO_BIG 3/3 | **73% / 73%** | pass | $9.08 |
| by-layer (control) | 2 | pass | pass | **7%** / 86% | pass | $6.95 |

The control failed `surface` in 13 of 14 split runs, so the judge can fail a bad split.

own-evidence:
- Splits far finer than any history. Graph API into 8 to 11 designs (roughly one per endpoint group), shallow into 6 or 7, shape into 22 to 24, tiger into 88 to 110 (one per analyzer).
- All 5 `no-deferred` fails are tiger. The judge flagged the effect-system and refactoring-prover entries in the 100-item lists.
- Shape never led with the storage seam alone, which fixes current's failure. The first design was fetch, not push.

current:
- Shape led with the storage seam on its own in all 3 runs, which fails `surface`.
- Tiger split into 19 to 23 designs that include deferred work in all 3 runs.
- Graph API split into the same 4 designs in all 3 runs. Fetch, auth and partial fit in all 3.

Neither arm meets the bar. Next change waits on the granularity you want for a design like the graph API.

## 2026-10-08, attempt 2

complete-pieces, 5 runs per case, 45 runs, $18.62. graph's expected verdict became TOO_BIG with 3 to 5 pieces, after you agreed ENG-132 should have shipped as about four.

| Criterion | Result |
|---|---|
| 1. shape, tiger TOO_BIG | pass, 5/5 each |
| 2. masked-blob, did-you-mean FITS | **fail**. did-you-mean split into 2 in 5/5 (wrong-endpoint 404s, unknown-field errors) |
| 3. graph TOO_BIG with 3 to 5 pieces | **fail**. TOO_BIG 5/5, but 2 runs gave 2 pieces (browse, compare and diff) |
| 4. surface / no-deferred | 100% / 80%, all 5 fails on tiger |
| 5. verdicts agree | pass, every case 5/5. auth now fits 5/5 |

Pieces got coarser. Graph went from 8 to 11 pieces down to 2 or 3, shape from about 23 down to 7 to 11, and tiger from about 100 down to 9 to 17. Shape's first piece included push in all 5 runs.

**Grader bug.** tiger's `no-deferred` failed in every run of every arm, the control included. The answer key listed the effect system and refactoring prover as deferred, but Part V's Adoption section commits to them as Chains 3 and 6. "The honest limits" states their cost and does not defer them. With tiger set aside, `no-deferred` passed 100% for both own-evidence and complete-pieces, and 92% for current. The key is corrected for later attempts.

## 2026-10-08, attempt 3

complete-pieces-2, 5 runs per case, 45 runs, $18.98. Same bar as attempt 2, with tiger's answer key corrected.

| Criterion | Result |
|---|---|
| 1. shape, tiger TOO_BIG | pass, 5/5 each |
| 2. masked-blob, did-you-mean FITS | pass, 5/5 each |
| 3. graph TOO_BIG with 3 to 5 pieces | pass, 5/5, with 3 or 4 pieces |
| 4. surface / no-deferred | pass, 100% / 100% |
| 5. verdicts agree | pass, every case 5/5 |

Passes the bar. Graph split as browse, history, compare and diff (3 runs), with repos and refs split from file browsing in 1 run and compare split from diff in another. Shape's first piece included push in all 5 runs, and one run put push before fetch as history did. Shallow split into 2 or 3 pieces in every run (shallow clone and deepen, push from a shallow clone, shallow server repos). Fetch, partial and auth fit in every run.

Limits of this result. The wording was tuned over three attempts on these same nine cases, so a pass here can be overfitting. tiger's `no-deferred` now has nothing to fail on, so it tests nothing for tiger.

## 2026-10-08, attempt 4

complete-pieces-3, the skill-creator pass on complete-pieces-2, on 15 cases (9 known, 6 held out), 3 runs each, 45 runs, $17.79.

| Criterion | Result |
|---|---|
| 1. expected verdicts, 3 of 3 | **fail**. did-you-mean TOO_BIG in 2 of 3. Every other expected verdict held, including all five held-out ones |
| 2. graph 3 to 5 pieces | pass, 3, 3, 4 |
| 3. surface / no-deferred | pass, 100% / 100% |
| 4. open cases agree | pass. docs-cmd split into export and serve 3/3; fetch, partial, auth FITS 3/3; shallow TOO_BIG 3/3 |

Held out: slip-stream, change-bot and tiger-wave1 TOO_BIG 3/3, heartbeat and dlq FITS 3/3. Change-bot's first piece was its own Phase 1 evaluator CLI in all 3 runs. Slip-stream's first piece was produce and fetch in all 3.

complete-pieces-2 fit did-you-mean 5/5 in attempt 3, so the rewrite caused the regression. The run that split it judged the 400-to-455 change for unknown fields, a client-visible change on every endpoint, to need its own review. complete-pieces-2 now runs on the held-out cases to see whether the unrewritten rules generalize as well.

## 2026-10-08, held-out check of complete-pieces-2

complete-pieces-2 on the 6 held-out cases, 3 runs each, 18 runs, $7.48. slip-stream, change-bot and tiger-wave1 came back TOO_BIG 3/3, and heartbeat and dlq FITS 3/3. docs-cmd split into export and serve 3/3, the same as complete-pieces-3. `surface` and `no-deferred` passed 100%.

complete-pieces-2 now passes every criterion on all 15 cases: the 9 known ones at 5 runs in attempt 3, and the 6 held-out ones at 3 runs here. It generalizes as well as complete-pieces-3 without the did-you-mean regression, so it is the adopted wording.

The skill-creator pass gave a better description. Its rewrite of the body, which added a reason to each rule and turned the decision into numbered steps, made one run in three over-split a small design. A skill-creator benchmark and review page for both versions is in `viewer/` (`review.html`; `iteration-1/benchmark.md`).

## 2026-10-08, attempt 5

You confirmed that one design splits into build tickets, one per piece, and that no piece gets a design of its own. complete-pieces-4 is complete-pieces-2 with its last paragraph changed from "only the first piece is designed now" to "each piece becomes a build ticket, blocked by the pieces it builds on". 15 cases, 3 runs each, 45 runs, $17.15.

| Criterion | Result |
|---|---|
| 1. expected verdicts, 3 of 3 | **fail**. change-bot FITS in 2 of 3 |
| 2. graph 3 to 5 pieces | pass, 5, 5, 5 |
| 3. surface / no-deferred | pass, 95% / 100% (one shape run failed `surface`) |
| 4. open cases agree | pass |

The two FITS runs on change-bot read its "Phase 2, designed here, built later" as deferred work and judged Phase 1 alone as one piece. complete-pieces-2 and -3 had called it TOO_BIG in all 6 runs. More pieces per split too: shape 13 to 14, tiger 16 to 21, graph 5. Splitting into tickets reads as cheaper than splitting into designs.

complete-pieces-5 adds that work a design schedules for a later phase or milestone is still committed and splits into pieces. Only deferred, limitation, open-question and future-work items are left out.

## 2026-10-08, attempt 6

complete-pieces-5, 15 cases, 3 runs each, 45 runs, $19.41. Same bar as attempt 4.

Passes every criterion. Every expected verdict held in 3 of 3, change-bot included. graph split into 5 pieces in every run: repos and refs, files at a revision, history, diff, compare. `surface` and `no-deferred` passed 100%, and every open case agreed with itself.

complete-pieces-5 is the adopted wording. It replaces complete-pieces-2, because you confirmed that a design splits into tickets, not into more designs.
