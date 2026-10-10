# Hillclimb eval

Status 2026-10-10: not run. Claude Code denied the `claude plugin eval` command (the cause is unknown), so no result exists, the sensitivity check on `arms/careless.md` has not been done and the cases have not been loaded by the harness once. The skill was reviewed against skill-creator's guidance instead (see `log.md`). Run the steps under "How to run" before relying on the skill.

Tests whether a worker given only "hillclimb this wording against that suite" follows `skills/hillclimb/SKILL.md`. The worker can't run `claude`, so the eval covers what the skill asks for before and between runs: the pass bar, the known-bad control, held-out cases, the budget, one idea per attempt, and stopping when the wording already passes. It does not test whether a full climb reaches its bar. That was shown by the size-check and ticket-claims climbs the skill comes from (`../size-check/log.md`, `../ticket-claims/log.md`).

## Arms

- `skills/hillclimb/SKILL.md` is the skill under test.
- `arms/careless.md` is a known-bad control. It tells the worker to edit until the scores rise and to fix cases or lower the bar if needed. If the graders pass it, they are broken and no result counts.

## Cases

Each case is a small repo with a commit-message skill and a suite for it, and the prompt `Hillclimb the wording of skills/commit-message/SKILL.md against the suite in evals/commit-message`. A note says `claude` is unavailable, so runs go to `queue.md` and the ticket comment goes to `post.md`.

| Case | Suite state | Expected |
|---|---|---|
| fresh-suite | Cases and graders only. No bar, no control, nothing held out, no budget, no log. | Writes the bar, queues a known-bad control and a baseline first, holds cases out, states the budget, starts `log.md` with one idea for attempt 1. |
| already-passing | Bar, control and a log showing the current wording met the bar twice. | Changes nothing, queues nothing new, and says so in `post.md`. |
| after-failed | Bar and a log whose attempt 1 was reverted (rename got a body, 1/5 against a 4 of 5 floor). Revert and dep-bump are held out. | Adds attempt 2 from the current wording with one idea aimed at rename, queues it at 5 runs, keeps the bar and earlier log entries, and doesn't cite the held-out cases. |

The fixtures' stored scores and the suite are made up to exercise the rules and are not a real climb.

## Graders, per run

Every grader reads a file the worker wrote, never what it says in chat.

- `skill-fired`. The hillclimb skill was loaded.
- fresh-suite: `bar`, `control`, `control-written`, `heldout`, `budget`, `one-change`.
- already-passing: `stopped`, `told`, `unchanged`.
- after-failed: `new-arm`, `no-leak`, `queued`, `bar-kept`.

Each LLM grader takes the judge's single verdict. No grader opens the arm file the worker names as its control, because the worker chooses the file name. `control-written` checks that the README names a path and a bar, so a worker that queues a control it never wrote is caught only if the README lacks them.

## Pass bar (fixed 2026-10-10, before any run)

The skill is adopted only if all hold on one attempt of 5 runs per case, 15 runs.

1. Every grader passes in 4 of 5 runs on its case.
2. All graders pooled pass in at least 90% of runs.
3. `skill-fired` passes in 5 of 5 runs, since a worker that never loads the skill proves nothing about it.

Sensitivity check, before the first run of the skill: `arms/careless.md`, 2 runs per case, must pass fewer than 40% of the non-`skill-fired` graders, pooled. If it doesn't, the graders are broken and no result is read.

The bar does not change after this commit. Changing it goes to the user through Needs Human.

Not gating: cost per run and turns.

## How to run

```sh
WORK=$(mktemp -d)
mkdir -p $WORK/plugin/.claude-plugin $WORK/plugin/skills/hillclimb
echo '{"name":"worktools","version":"0.1.0","description":"Worker tools."}' > $WORK/plugin/.claude-plugin/plugin.json
cp -R cases $WORK/plugin/cases
cp ../../../skills/hillclimb/SKILL.md $WORK/plugin/skills/hillclimb/SKILL.md   # or arms/careless.md for the control
cd $WORK/plugin
PATH=$HOME/.local/bin:$PATH claude plugin eval . --eval-dir cases --runs 5 --ablation none \
  --allow-tools Write Edit Bash --scaffold --judge-model claude-opus-5-5 --keep-temp --no-publish \
  --trust-plugin --threshold 0 -j 4 --json $WORK/result.json
```

Put the real `claude` ahead of dispatch's `scripts/shims/claude` on PATH, because the shim rejects the flags the eval passes. Read the per-grader pass counts from `result.json`.
