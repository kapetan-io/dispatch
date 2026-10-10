# Arena eval

Tests whether a session told "arena this" on a sample question follows `skills/arena/SKILL.md`: a rubric before fan-out, three candidates on different models, a candidate prompt that makes them question the question's premises, a synthesis note, and a closing message that asks before anything changes.

## Arms

- `skills/arena/SKILL.md` is the skill under test.
- `arms/careless.md` is a known-bad control. It answers alone and asks one sub-agent to agree. If it passes the graders, they are broken and no result counts.

## Case

`sample-question` is a small repo with two docs. The inventory service polls a supplier API every 10 minutes, and the supplier docs show a `stock.changed` webhook that makes polling unnecessary. The prompt is `Arena this: how often should the inventory service poll the supplier API for stock levels?` The polling premise is the one a candidate should question.

The worker saves the candidate prompt as `arena/prompt.md` and the rubric as `arena/rubric.md`, and writes `artifact.md`, `note.md` and `final.md`.

## Graders, per run

Every grader reads a file the worker wrote, never what it says in chat.

`skill-fired` (the Skill tool was used), `fan-out` (the Agent tool was used), `rubric`, `candidates`, `prompt-premise`, `prompt-clean`, `note`, `premise-resolved`, `asked`. Each LLM grader takes the judge's single verdict.

## Pass bar (fixed 2026-10-10, before any run)

The skill is adopted only if all hold on one attempt of 5 runs, with the skill's wording unchanged since the review that preceded the run.

1. Every grader passes in 4 of 5 runs.
2. All graders pooled pass in at least 90% of runs.
3. `skill-fired` and `fan-out` pass in 5 of 5 runs.

Sensitivity check, before the first run of the skill: `arms/careless.md`, 2 runs, must pass fewer than 40% of the non-`skill-fired` graders, pooled. If it doesn't, the graders are broken and no result is read.

Budget: 5 runs per case, at most 3 attempts (an attempt is a change to the skill's wording followed by 5 runs), no cost ceiling beyond that. Five runs can't show a failure rate under about 20%. If the bar isn't met by attempt 3, the ticket goes to Needs Human with the results so far.

Limit: one case, so the eval shows the phases followed on one question. It does not show the arena beats a single attempt.

The bar does not change after this commit. Changing it goes to the user through Needs Human.

## How to run

```sh
WORK=$(mktemp -d)
mkdir -p $WORK/plugin/.claude-plugin $WORK/plugin/skills/arena
echo '{"name":"worktools","version":"0.1.0","description":"Worker tools."}' > $WORK/plugin/.claude-plugin/plugin.json
cp -R cases $WORK/plugin/cases
cp ../../../../skills/arena/SKILL.md $WORK/plugin/skills/arena/SKILL.md   # or arms/careless.md for the control
cd $WORK/plugin
PATH=$HOME/.local/bin:$PATH claude plugin eval . --eval-dir cases --runs 5 --ablation none \
  --allow-tools Write Edit Bash --scaffold --judge-model claude-opus-5-5 --keep-temp --no-publish \
  --trust-plugin --threshold 0 -j 4 --json $WORK/result.json
```

Put the real `claude` ahead of dispatch's `scripts/shims/claude` on PATH, because the shim rejects the flags the eval passes.
