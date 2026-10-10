# how and why eval

Tests that `dispatch:how` and `dispatch:why`, copied from pstack, work in Claude Code against this repo. It follows `skills/hillclimb/SKILL.md`. Each case clones the repo at commit `21a94dc` (the base of PR #36, so answers don't move as the repo grows), asks one question and has the worker write `answer.md`. Graders read `answer.md` and the tool calls, never the chat.

Status 2026-10-10: not yet run under this suite. The earlier informal run (10 runs, no control, no held-out cases) is kept in `results/informal/` and `log.md` and does not count toward the bar.

## Cases

| Case | Question | Held out | Expected |
|---|---|---|---|
| how-nudge | How does a dispatch tick decide whether to nudge an idle worker? | no | Stall nudge for an idle In Progress worker, count compared with `NudgeLimit`, escalation at the limit. |
| why-unknown-keys | Why does dispatch reject unknown config keys? | no | Cites commit `334430a`. A typo would otherwise fall back to a default unnoticed. |
| how-ready | How does dispatch decide that a pull request is ready for review? | yes | The six ready conditions, driving a notification and a label. |
| how-tick | How does one dispatch tick work from start to finish? | no | Reads Linear, herdr and GitHub, plans in `internal/reconcile`, acts, saves the cache. Also needs 2 or more sonnet explorers. |
| why-ready-notify | Why does dispatch notify me when a pull request is ready for review? | no | Cites the decision log. An In Review ticket stays In Review while the worker fixes findings. |
| why-backlog-first | Why does the breakdown step create each ticket in Backlog first? | yes | Cites the decision log. A tick could promote a Todo ticket before its blockers are linked. |
| how-retire | How does dispatch retire the worker and tree of a finished ticket? | fresh | Teardown, keep, and the tree pool. |
| why-working-copy | Why does the design review kickoff keep its working copy outside the worktree? | fresh | Cites the decision log. So it never makes the tree dirty. |

The held-out cases are every third of the six main cases. Read their scores, never their transcripts. The two fresh cases were written from the design (the decision log and `internal/reconcile`), before the confirmation rerun, and are not run until then. Linear's MCP isn't available in eval runs, so `why` can cite git history and the decision log but not tickets. That is the ticket's own list of sources.

## Graders, per run

- `skill-fired`. A `tool_used` check on the Skill tool with `input_match` on `"skill": "dispatch:how"` (or `dispatch:why`), so loading any other skill fails.
- `paths` (how). An LLM judge reads `answer.md` with the full list of the repo's files at `21a94dc` in its criteria. Any named path not in the list fails.
- `flow` (how) and `reason` (why). An LLM judge against a per-case answer key.
- `cited` (why). A regex on `answer.md` for a commit hash or the decision log, so citing code or docs alone fails.
- `explorers` (how-tick only). At least 2 Agent calls whose input holds the explorer prompt's opening line and `"model": "sonnet"`, so it shows the sonnet explorers ran.

Each LLM grader takes 2 of 3 judge votes.

## Known-bad control

`arms/careless-how.md` and `arms/careless-why.md` replace the two skills' bodies with "answer from what you know, read nothing, spawn nothing". Same names and descriptions, so the Skill still fires and the control fails on behavior. 2 runs per main case, 12 runs. It must pass fewer than 40% of the non-`skill-fired` graders, pooled. If it doesn't, the graders are broken and no result is read.

## Scorer check

Four fixture cases (tag `scorer`) skip the skill and copy a hand-written answer to `answer.md`. The `-good` ones must pass every grader and the `-bad` ones (invented paths, a wrong flow, no commit, a wrong reason) must fail at least one grader on each of the case's judge or regex graders. Run once each, before any scored run.

## Pass bar (fixed 2026-10-10, before any run under this suite)

The wording is adopted only if all hold.

1. Attempt run: 5 runs per main case, 30 runs. Every grader passes in at least 4 of 5 runs on its case, and `skill-fired` passes in 5 of 5.
2. All non-`skill-fired` graders pooled pass in at least 90% of runs.
3. The control and the scorer check come out as above.
4. Confirmation rerun of the same wording, 5 runs per case on the 6 main cases and the 2 fresh cases, 40 runs. Items 1 and 2 hold again.

The bar does not change after this commit. Changing it goes to the user through Needs Human.

## Budget

5 runs per case. At most 3 attempts. A cost ceiling of $60 across all runs for this ticket, with `--max-cost-usd 25` on each invocation. Five runs per case can't show a failure rate under about 20%. The baseline is the wording in the PR today, and it is attempt 1. If the bar isn't met by attempt 3 or the ceiling, the ticket goes to Needs Human with the scores.

## How to run

From this directory. Run the two `cp arms/...` lines only for the control.

```sh
WORK=$(mktemp -d)
mkdir -p $WORK/plugin
cp -R ../../../../.claude-plugin $WORK/plugin/.claude-plugin
cp -R ../../../../skills $WORK/plugin/skills
cp -R cases $WORK/plugin/cases
# control only, skip for the real wording
cp arms/careless-how.md $WORK/plugin/skills/how/SKILL.md
cp arms/careless-why.md $WORK/plugin/skills/why/SKILL.md
sed -i.bak "s|@REPO@|$(git rev-parse --show-toplevel)|" $WORK/plugin/cases/*/scaffold.sh
cd $WORK/plugin
PATH=$HOME/.local/bin:$PATH claude plugin eval . --eval-dir cases --runs 5 --ablation none \
  --case '<glob>' --allow-tools Write Bash --scaffold --judge-model claude-opus-5-5 --keep-temp --no-publish \
  --trust-plugin --threshold 0 -j 4 --max-cost-usd 25 --json $WORK/result.json
```

Select cases with `--case <glob>` (such as `how-*`) or `--tag scorer`, and swap `--case` for `--tag` when you use it. Held-out cases carry the tag `heldout` and fresh ones `fresh`. Copy the result JSON into `results/` under the attempt's name. Put the real `claude` ahead of dispatch's `scripts/shims/claude` on PATH, because the shim rejects the flags the eval passes. The path list in the `paths` criteria comes from `git ls-tree -r --name-only 21a94dc`.
