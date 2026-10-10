# Arena eval

Status 2026-10-10: the pass bar is not met. Attempt 5 (3 runs, with a Phase F line telling the session to re-read the artifact before its final message) passes every grader 3 of 3 except `verify`, which is 0 of 3. See `log.md`; raw results are in `results/` (each grader's evidence trimmed to 600 characters).

Tests whether a session told "arena this" on a sample question follows `skills/arena/SKILL.md`: a rubric before fan-out, three candidates on different models, a candidate prompt that makes them question the question's premises, a synthesis note, and a closing message that asks before anything changes.

## Arms

- `skills/arena/SKILL.md` is the skill under test.
- `arms/careless.md` is a known-bad control. It answers alone and asks one sub-agent to agree. If it passes the graders, they are broken and no result counts.

## Case

`sample-question` is a small repo with two docs. The inventory service polls a supplier API every 10 minutes, and the supplier docs show a `stock.changed` webhook that makes polling unnecessary. The prompt is `Arena this: how often should the inventory service poll the supplier API for stock levels?` The polling premise is the one a candidate should question.

The prompt also names the scratchpad (`scratch/`) and where the final artifact (`artifact.md`) and the note (`note.md`) go.

## Graders, per run

Each grader reads either the arguments of the worker's `Agent` calls (exact checks), a file the worker was told to write, or the transcript. The prompt names only where the work goes (`scratch/`, `artifact.md`, `note.md`) and says nothing about rubrics, tickets or asking.

- `skill-fired`, `fan-out` (at least 4 `Agent` calls), `model-fable`, `model-sonnet`, `model-opus` (at least 2, one is the judge). Exact.
- `judge-readonly`: an `Agent` call with `subagent_type: "Plan"`. Exact.
- `rubric-judge-only`: exactly one `Agent` call mentions a rubric or criteria, which means the candidates were not given them. Exact. It does not show the rubric was written before fan-out, because that happens in the worker's reasoning, which the trace doesn't hold. Rubric-before-fan-out is not tested.
- `prompt-scratch`, `prompt-premise`: at least 3 `Agent` calls contain "from scratch with every signal now available" and "premise". Exact.
- `prompt-clean`: no `Agent` prompt says the service must, should or needs to poll. Exact and narrow. It passes vacuously for a worker that starts no sub-agents.
- `judge-after`: an LLM reads the transcript to check the judge started after every candidate finished.
- `note`, `premise-resolved`: an LLM reads `note.md`.
- `verify`: an LLM reads the transcript for a check of the artifact against the real material before the final message.
- `asked`: an LLM reads the worker's last message.

## Pass bar for attempt 5 (fixed 2026-10-10, before the run)

The user asked for 3 runs per case instead of 5 for this attempt. Four of five is 80%, and two of three is only 67%, so each grader must pass in 3 of 3 runs. All graders pooled must pass in at least 90% of runs, and `skill-fired` and `fan-out` must pass in 3 of 3. The wording under test is the one on the PR after the third skill-creator review plus the two edits reviewed in a fourth. Sensitivity check as below, 2 runs of the control on the final `case.yaml`. Three runs can't show a failure rate under about 35%.

## Original pass bar (fixed 2026-10-10, before any run)

The skill is adopted only if all hold on one attempt of 5 runs, with the skill's wording unchanged since the review that preceded the run.

1. Every grader passes in 4 of 5 runs.
2. All graders pooled pass in at least 90% of runs.
3. `skill-fired` and `fan-out` pass in 5 of 5 runs.

Sensitivity check: `arms/careless.md`, 2 runs, must pass fewer than 40% of the non-`skill-fired` graders, pooled. With the final grader set it passed 2 of 28 (7%, `results/attempt4d-control.json`). The control passes only the vacuous `prompt-clean`, and it fails `fan-out` because it starts one sub-agent.

Budget: 5 runs per case, at most 3 attempts (an attempt is a change to the skill's wording followed by 5 runs), no cost ceiling beyond that. Five runs can't show a failure rate under about 20%. If the bar isn't met by attempt 3, the ticket goes to Needs Human with the results so far.

Limits. One case, so the eval shows the phases followed on one question and does not show the arena beats a single attempt. `rubric-judge-only` does not show the rubric came before fan-out. `note` also requires the models each candidate ran on, which the skill's Outputs section does not ask for, so it over-asks.

The bar's thresholds did not change. The grader set did, after review (see `log.md`). Changing it goes to the user through Needs Human.

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
