# Grill eval

Tests that `dispatch:grill` grounds a real design before sketching it, and that the arena's candidates use the grounding. It follows `skills/hillclimb/SKILL.md`. The worker is told the Objective is already approved and to run only Phase A and Phase B, because the sandbox has no user, Linear or tree. The rest of the skill (discussion, blueprint, size check, child ticket, hand-off) needs a live user and is not tested here.

## Case

`grill-ci-failure-forwarding` clones this repo at commit `21a94dc` (the base of PR #36) and asks for a design that forwards a failing CI check to the worker that owns the pull request. It touches the nudge code in `internal/reconcile` and rests on the decision log's "dispatch is the event broker" rule, so grounding needs both `dispatch:how` (the code) and `dispatch:why` (the rationale). The worker joins the grounding and each candidate's rationale into `evidence.md` so a grader can compare them.

## Arms

- `skills/grill/SKILL.md` is the skill under test.
- `arms/careless.md` is a known-bad control. It skips grounding and gives the arena only the Objective. If it passes the graders, they are broken.

## Graders, per run

- `skill-fired`, `how-fired`, `why-fired`, `arena-fired`. A `tool_used` check on the Skill tool with the skill name in the input.
- `candidates-get-grounding`, `candidates-get-runner-prompt`, `candidates-get-objective`. An LLM judge reads the trace for at least 3 design candidate `Agent` calls given `grounding.md`, the runner prompt, and the Objective, either in the prompt or in a file the prompt points at. Attempts 1 to 3 used an exact check on the `Agent` call text only, which missed prompts that point at a file (see `log.md`).
- `grounding-traced`. An LLM judge reads `scratch/grounding.md` for three code locations with what each does, and one recorded reason.
- `candidates-cite-grounding`. An LLM judge reads `evidence.md` and checks that every candidate relies on a code location or decision that is in the grounding and not in the Objective. This is the check the ticket asks for.
- `last-message-asks-sketch`. The final message shows the contract sketch and asks the user to approve it, not to pick among candidates.

Each LLM grader takes 2 of 3 judge votes.

## Pass bar (fixed 2026-10-10, before any run)

1. 5 runs. Every grader passes in at least 4 of 5 runs, and `skill-fired`, `how-fired`, `arena-fired` and `candidates-cite-grounding` pass in 5 of 5.
2. All graders pooled pass in at least 90% of runs.
3. The control, 2 runs on the final `case.yaml`, passes fewer than 40% of the non-`skill-fired` graders, pooled. If not, the graders are broken and no result is read.

Budget: 5 runs, at most 3 attempts, and `--max-cost-usd 25` on each invocation. Five runs can't show a failure rate under about 20%. If the bar isn't met by attempt 3, the ticket goes to Needs Human with the scores.

Limits. One case, so the eval shows the grounding reaches the candidates and does not show the arena's design beats a single attempt. Linear's MCP isn't available in eval runs, so `why` cites git history and the decision log only.

## How to run

From this directory. Run the `cp arms/careless.md` line only for the control, and use `--runs 2` there.

```sh
WORK=$(mktemp -d)
mkdir -p "$WORK/plugin"
cp -R ../../.claude-plugin "$WORK/plugin/.claude-plugin"
cp -R ../../skills "$WORK/plugin/skills"
cp -R cases "$WORK/plugin/cases"
# control only
cp arms/careless.md "$WORK/plugin/skills/grill/SKILL.md"
sed -i.bak "s|@REPO@|$(git rev-parse --show-toplevel)|" "$WORK"/plugin/cases/*/scaffold.sh
cd "$WORK/plugin"
PATH=$HOME/.local/bin:$PATH claude plugin eval . --eval-dir cases --runs 5 --ablation none \
  --allow-tools Write Edit Bash --scaffold --judge-model claude-opus-5-5 --keep-temp --no-publish \
  --trust-plugin --threshold 0 -j 4 --max-cost-usd 25 --json "$WORK/result.json"
```

Put the real `claude` ahead of dispatch's `scripts/shims/claude` on PATH, because the shim rejects the flags the eval passes. Copy the result JSON into `results/`.
