# Supervise validator eval

Tests that a supervisor session, told to validate a review finding on a worker's PR, runs only the proof and not the full test suite (ENG-278). Follows `skills/hillclimb/SKILL.md`. The wording under test is principle 4 of `skills/supervise/SKILL.md`.

Each case is a tiny Go module (`shop`, three packages with one passing test each) committed on a `feature` branch in `./repo`. The prompt says CI already passed on the head, quotes one critical-review finding, and asks the session to validate it per the skill. Every existing test calls `mark.Ran`, which appends its name to `ran.log` next to the repo. A validator that runs only a new proof test, or `-run` with names it wrote, leaves `ran.log` empty. Any `go test` that executes an existing test, in any worktree and any sub-agent, leaves a line.

## Arms

- `skills/supervise/SKILL.md` is the wording under test.
- `arms/current.md` is principle 4 as it was on main before ENG-278. It is the baseline.
- `arms/careless.md` is the known-bad control. The same skill plus an instruction to run `go test ./...` before and after the proof.

## Cases

| Case | Finding | Held out |
|---|---|---|
| refund-rounding | `refund.Percent` truncates instead of rounding half up | no |
| ledger-last | `ledger.Total` drops the last entry | no |
| report-nil | `report.Name(nil)` panics | yes |

Held out is every third case. Read its score, never its transcript. A fresh case is written from the design, not from the winning wording, before the confirmation rerun.

## Graders, per run

- `skill-fired`. The Skill tool loaded `supervise`.
- `proof-only`. `ran.log` has no test name, so no existing test ran.
- `scoped-run`. At least one Bash call of the form `go test ... -run`, so the finding was validated by running something.

## Pass bar (fixed 2026-10-10, before any run)

1. Attempt run, 5 runs per case, 15 runs. `proof-only` and `scoped-run` each pass in at least 4 of 5 runs on every case, and `skill-fired` in 5 of 5.
2. `proof-only` and `scoped-run` pooled pass in at least 90% of runs.
3. Control, 2 runs per case, 6 runs, passes under 40% of `proof-only` and `scoped-run` checks pooled. Otherwise the graders are broken and no result is read.
4. Scorer check before any scored run. A stored run with an empty `ran.log` passes `proof-only` and one with a test name in it fails.
5. Confirmation rerun of the same wording, 5 runs per case on all cases including the fresh one, meets items 1 and 2 again.

The bar does not change after this commit. Changing it goes to the user through Needs Human.

## Budget

5 runs per case, at most 3 attempts, and a $40 ceiling with `--max-cost-usd 15` per invocation. Five runs per case can't show a failure rate under about 20%. The baseline (`arms/current.md`) is logged first. If it already meets the bar twice, the wording changes nothing and the ticket stops.

## How to run

From this directory. The `disable-model-invocation` line is stripped from the installed skill because the eval session invokes it through the Skill tool. Copy `arms/<arm>.md` over the skill instead for the baseline or the control.

```sh
WORK=$(mktemp -d)
mkdir -p $WORK/plugin
cp -R ../../../../.claude-plugin $WORK/plugin/.claude-plugin
cp -R ../../../../skills $WORK/plugin/skills
cp -R cases $WORK/plugin/cases
sed -i.bak '/^disable-model-invocation/d' $WORK/plugin/skills/supervise/SKILL.md
cd $WORK/plugin
PATH=$HOME/.local/bin:$PATH claude plugin eval . --eval-dir cases --runs 5 --ablation none \
  --allow-tools Write Edit Bash --scaffold --judge-model claude-opus-5-5 --keep-temp --no-publish \
  --trust-plugin --threshold 0 -j 4 --max-cost-usd 15 --json $WORK/result.json
```

Put the real `claude` ahead of dispatch's `scripts/shims/claude` on PATH. Copy the result JSON into `results/` under the run's name.
