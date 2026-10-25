# Refactor-domains eval

Status 2026-10-24: the pass bar below is not met on the one attempt the budget allowed. `log.md` has the scores. The control stayed under its 40% limit (33%), so the graders do tell a careless plan from a followed skill.

Tests that a design worker told to plan a problem-domain restructuring of a small repo loads `dispatch:refactor-domains` and writes a blueprint that follows it. It borrows the layout of `skills/hillclimb/SKILL.md` (pass bar fixed first, known-bad control, budget), but never climbs: the budget below is spent once and the scores are recorded whatever they are. The sandbox has no user, Linear or tree, so only the blueprint is scored. The pieces' tickets, the review rounds and the built refactor are not tested.

## Case

`refactor-billing-cli` is a small Go CLI with the commands `issue`, `pay` and `statement` (4 production files, 2 test files). Two things tempt a careless plan. The `util` and `store` packages mix several concepts (`Manager` stores, numbers invoices and renders statements), which invites a layered split. `store.Invoice` has no json tags, so its Go field names are saved on disk, which a plan that moves or renames fields breaks without noticing. `docs/CONTEXT.md` names the domain terms. The test cases in `billing_test.go` drive the CLI through `Run`.

## Arms

- `skills/refactor-domains/SKILL.md` is the skill under test.
- `arms/careless.md` is a known-bad control. It tells the worker to layer into `models`, `services` and `handlers`, rename commands freely and rewrite the tests. If it passes the graders, they are broken.

## Graders, per run

- `skill-fired`, `problem-domains-fired`, `surface-testing-fired`. A `tool_used` check on the Skill tool with the skill name in the input.
- LLM judges read `blueprint.md`. `purpose-line` (opens `Purpose: refactor`), `problems-cited` (numbered problems with real `file:line` cites), `product-use-kept`, `saved-data-kept` (the untagged `Invoice` fields and the on-disk names), `names-from-domain`, `new-package-earns-place`, `test-cases-unchanged`, `pieces-have-kinds`, `pieces-have-proof` (one `git diff` that must print nothing) and `left-out-listed`.

Each LLM grader takes 2 of 3 judge votes, on the judge model named below.

## Pass bar (fixed 2026-10-24, before any run)

1. 3 skill runs. Every grader passes in at least 2 of 3 runs, and `skill-fired`, `purpose-line`, `product-use-kept` and `saved-data-kept` pass in 3 of 3.
2. All 13 graders pooled pass in at least 85% of runs.
3. The control, 2 runs, passes fewer than 40% of the 12 non-`skill-fired` graders, pooled (fewer than 10 of 24). If not, the graders are broken and no result is read.

Budget: one attempt, $10 total across every invocation (the cap ENG-574 set for this suite). The skill invocation passes `--max-cost-usd 6` and the control `--max-cost-usd 3`, so the caps sum to $9. A run cut off by its ceiling counts as a failure that says nothing about the skill. A miss is recorded and not reworded and rerun.

Limits. One case, three runs, so a failure rate under about 30% can't show. A blueprint is scored by a judge reading text, so it can't tell whether the plan would build.

## How to run

From this directory. Run the `cp arms/careless.md` line only for the control, and use `--runs 2` and `--max-cost-usd 3` there.

```sh
WORK=$(mktemp -d)
mkdir -p "$WORK/plugin"
cp -R ../../.claude-plugin "$WORK/plugin/.claude-plugin"
cp -R ../../skills "$WORK/plugin/skills"
cp -R cases "$WORK/plugin/cases"
# control only
cp arms/careless.md "$WORK/plugin/skills/refactor-domains/SKILL.md"
cd "$WORK/plugin"
PATH=$HOME/.local/bin:$PATH claude plugin eval . --eval-dir cases --runs 3 --ablation none \
  --allow-tools Write Edit Bash --scaffold --judge-model claude-sonnet-5-5 --keep-temp --no-publish \
  --trust-plugin --threshold 0 -j 3 --max-cost-usd 6 --json "$WORK/result.json"
```

Put the real `claude` ahead of dispatch's `scripts/shims/claude` on PATH, because the shim rejects the flags the eval passes. Copy the result JSON into `results/`.
