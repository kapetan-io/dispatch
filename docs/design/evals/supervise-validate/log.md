# Supervise validator eval log

## 2026-10-10, setup

Before any run, a fresh sub-agent loaded `anthropic-skills:skill-creator` and reviewed the first wording. It found that the supervisor was never told to put the rule into the validator's prompt, and that `-run` alone left `./...` open. Both were fixed by naming the prompt and banning `./...`, in two sentences.

Two harness smoke runs (not scored, $0.31) showed that the eval sandbox cannot run Go's tool chain (`go: no such tool "vet"`) and that Agent worktree isolation is unavailable. A first design that logged test names to `ran.log` could not work, so the graders were changed to read attempted commands before any scored run. The prompt now says a temp-directory copy serves as the scratch worktree. A third smoke showed `no-full-run` needs `min: 0` to count zero calls as a pass.

## Control and baseline (results/control.json, results/baseline.json)

Control (`arms/careless.md`, 2 runs per case, 6 runs, $0.88). `validator-told` 0 of 6 and `no-full-run` 0 of 6. Under the 40% bar.

Baseline (`arms/current.md`, main's wording, 5 runs per case, $1.41). `validator-told` 0 of 15 and `no-full-run` 0 of 15. The baseline does not meet the bar, so the change is needed.

## Attempt 1, results/attempt1.json ($1.90)

Idea. Tell the supervisor to put the `-run` command in each validator's prompt, and ban `./...`.

| Case | skill-fired | validator-told | no-full-run |
|---|---|---|---|
| refund-rounding | 5/5 | 4/5 | 5/5 |
| ledger-last | 5/5 | 4/5 | 5/5 |
| report-nil (held out) | 5/5 | 3/5 | 5/5 |

Pooled 26/30 on `validator-told`, 87%, under the 90% bar, and one case under 4 of 5. Reverted. Of the visible failures, one run validated inline and spawned no validator at all, so the rule had no sub-agent to reach. That is a delegation miss and not a bad `-run` command. The held-out transcripts were not read.

## Attempt 2, results/attempt2.json ($2.09)

Idea. Name the validator a sub-agent in both places in principle 4, so the supervisor delegates.

All three cases 5/5 on every grader. Bar met (`arms/attempt-2.md`).

## Confirmation rerun, results/confirmation.json ($2.80)

The same wording, 5 runs per case, plus a fresh case `refund-negative` written from the design's rule (validators prove with a failing test) and not from the wording. 20 of 20 runs passed every grader. Bar met again.

## Limits

Total spend about $9.6 of the $40 ceiling. Cases that tuned the wording: `refund-rounding`, `ledger-last`. Held out: `report-nil`. Fresh: `refund-negative`. 5 runs per case, workers on `claude-sonnet-5-5`, judge model unused (no LLM graders). Five runs per case can't show a failure rate under about 20%. The graders read attempted commands, so a validator running inside a sub-agent is judged by the prompt the supervisor wrote and not by what the sub-agent ran. The eval cannot run Go, so it does not show that the proof file stays out of the PR, the second sentence of the wording.
