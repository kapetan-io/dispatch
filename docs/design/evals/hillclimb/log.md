# Hillclimb eval log

## 2026-10-10, no runs

The `claude plugin eval` command was denied in the session that wrote this eval, so the pass bar in `README.md` was committed and nothing ran. Neither the `arms/careless.md` sensitivity check nor the 15 skill runs exist.

Instead the skill got a review against skill-creator's guidance, with no scores involved. It changed the description to trigger on looser phrasing, split the control-and-scorer rule into two parts, replaced numbers that came from one climb, split the proposer and judge steps, and added a fallback for when there is no ticket. The review changed the wording only, so a later run tests the reviewed text.

Limits. The worker in each case can't run `claude`, so the eval covers the rules before and between runs, not a full climb. Ordering of the bar before attempt 1 is judged only through `queue.md`, because the fixtures aren't git repos.

## 2026-10-10, two grader fixes after review

Neither change looked at a score, since nothing has run. The `control` grader read only `queue.md`, so it could not tell whether the control arm existed, and a `control-written` grader now reads the README for the arm's path and bar. The after-failed fixture logged attempt 1 as kept although rename fell to 1/5 against a floor of 4 of 5, which the skill says to revert, so the fixture now logs it as reverted and the `new-arm` grader expects attempt 2 to start from the current wording.

## 2026-10-10, results-committed case added (ENG-279)

`SKILL.md` now says each score the log cites needs its result JSON in `results/`, committed with the `log.md` entry, and a new case `results-committed` checks it. The bar's run count went from 15 to 20 for the fourth case. No threshold moved.

**Control.** `arms/careless.md`, 2 runs per case, $1.34 (`results/control-careless-2-runs.json`). It passed 10 of 32 non-`skill-fired` checks (31%, under the 40% limit), and failed all three `results-committed` graders in both runs.

**Run 1, all four cases, 5 runs each, $5.56** (`results/skill-all-cases-run1.json`). Bar not met.

| Case | Grader | Pass |
|---|---|---|
| after-failed | new-arm | 1/5 |
| after-failed | no-leak, queued, bar-kept | 5/5 each |
| already-passing | stopped | 3/5 |
| already-passing | told | 0/5 |
| already-passing | unchanged | 5/5 |
| fresh-suite | bar, control-written, heldout | 5/5 each |
| fresh-suite | budget | 4/5 |
| fresh-suite | control, one-change | 0/5 each |
| results-committed | cites-file | 5/5 |
| results-committed | result-committed, same-commit | 0/5 each |

**Why `results-committed` read 0/5, from the transcripts of that run.** In run 1 workers committed the log entry but never saved the stand-in's JSON, so the wording failed. Step 4 of "Each attempt" now says to write the result JSON to `results/` before logging. Two test-data faults also hid results, both fixed for reasons that don't depend on scores. A commit hook meant to record the commits doesn't fire in the eval sandbox, so the prompt now has the worker write `git log --stat` to `commits.txt`. And `git` on PATH is a macOS shim that fails in the sandbox, so three workers could not commit at all until the prompt named `/opt/homebrew/bin/git`. Intermediate reruns (2/5 with the shim fault) have no saved file and are not scores.

**Run 2, `results-committed` only, 5 runs, $1.20** (`results/skill-results-committed-run2.json`). `result-committed` 4/5, `same-commit` 4/5, `cites-file` 5/5, `skill-fired` 5/5. This meets the per-grader bar of 4 of 5 for the new case. Control rerun on the final case (`results/control-results-committed.json`) failed all three graders, 0/2.

**Not met.** The full bar needs every grader at 4 of 5 on one attempt across all four cases, and run 1 shows `control`, `one-change`, `told`, `new-arm` and `stopped` below it. Those failures are in cases this ticket didn't touch, and the wording changes for ENG-279 don't reach them. They were not investigated here. Limits: tuned on the visible case only, no held-out case for `results-committed`, model claude-sonnet-5-5, judge claude-opus-5-5, 5 runs per case cannot show a failure rate under about 20%.
