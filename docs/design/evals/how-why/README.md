# how and why eval

Tests that `dispatch:how` and `dispatch:why`, copied from pstack, work in Claude Code against this repo. Each case is the repo cloned into a scratch directory with one question and an `answer.md` the worker writes. Graders read `answer.md`, never the chat.

## Cases

| Case | Question | Expected |
|---|---|---|
| how-nudge | How does a dispatch tick decide whether to nudge an idle worker? | Traces the flow through `internal/reconcile/reconcile.go` (`Plan`, `watch`) with real paths, and names the nudge limit and the escalation. |
| why-unknown-keys | Why does dispatch reject unknown config keys? | Cites commit 334430a or `internal/config/config.go`, and gives the reason (a typo must not fall back to a default unnoticed). |

## Graders, per run

- `skill-fired`. The `how` (or `why`) skill was loaded.
- how-nudge: `paths` (every file path named exists in the repo and the answer names `internal/reconcile/reconcile.go`) and `flow` (idle In Progress worker gets a stall nudge, the nudge count is compared with a limit, and the ticket is escalated at the limit).
- why-unknown-keys: `cited` (cites a commit, file or decision-log entry that exists) and `reason` (says a mistyped setting would otherwise take its default silently).

Each LLM grader takes the judge's single verdict.

## Pass bar (fixed 2026-10-10, before any run)

Adopted only if all hold on one attempt of 5 runs per case, 10 runs.

1. Every grader passes in 4 of 5 runs on its case.
2. `skill-fired` passes in 5 of 5 runs.

Budget: 5 runs per case, 3 attempts at most. Five runs can't show a failure rate under about 20%. If the bar isn't met after 3 attempts the ticket goes to Needs Human with the scores. The bar does not change after this commit.

## How to run

```sh
WORK=$(mktemp -d)
mkdir -p $WORK/plugin
cp -R ../../../../.claude-plugin $WORK/plugin/.claude-plugin
cp -R ../../../../skills $WORK/plugin/skills
cp -R cases $WORK/plugin/cases
cd $WORK/plugin
DISPATCH_REPO=<a dispatch checkout> PATH=$HOME/.local/bin:$PATH claude plugin eval . --eval-dir cases --runs 5 --ablation none \
  --allow-tools Write Bash --judge-model claude-opus-5-5 --keep-temp --no-publish \
  --trust-plugin --threshold 0 -j 2 --json $WORK/result.json
```

Put the real `claude` ahead of dispatch's `scripts/shims/claude` on PATH, because the shim rejects the flags the eval passes.
