# how and why eval log

## 2026-10-10, attempt 1: verbatim copy with the Claude Code edits

Before the run, a fresh sub-agent loaded `anthropic-skills:skill-creator` and reviewed both skills. It found nothing needed for correctness. Two optional edits were set aside to stay close to pstack: dropping ", cursor location" from why's Step 1, and rewording how's "read-only access" line. Dropping `disable-model-invocation` is the ticket's call.

5 runs per case, 10 runs, sonnet-5-5 workers, opus-5-5 judge. Two smoke runs came first. One failed in scaffolding (no `--scaffold`), the other passed. The 10 runs cost $4.66.

| Case | skill-fired | paths / cited | flow / reason |
|---|---|---|---|
| how-nudge | 5/5 | 5/5 | 5/5 |
| why-unknown-keys | 5/5 | 5/5 | 5/5 |

The bar is met. Limits: no Linear MCP was available in the eval, so why's answer came from git history and docs only. Two how-nudge runs used 2 turns, so they took the single-explainer path, and the other three used 6. The graders read `answer.md`, not the subagent fan-out.

A later edit to why's agent-tool bullets (dropping a wrong claim that `Explore` agents lack MCP access) came after the run. why-unknown-keys reran on the final wording, 5 of 5 runs passed every grader.

## 2026-10-10, review of the informal run and the rebuild under hillclimb

A reviewer found that the informal run above did not follow `dispatch:hillclimb`. It had no known-bad control, no held-out cases, no cost ceiling and no confirmation rerun of `how`. Its `skill-fired` check passed on any skill, `paths` could not tell an invented path from a real one, `cited` accepted code and docs alone, and nothing showed that how's explorers ran. Its result JSON (`results/informal/`) is kept for the record and does not count toward the bar.

The suite was rebuilt before any scored run, and the bar in `README.md` was committed first. Changes: `skill-fired` checks the skill name, `paths` carries the repo's file list at the pinned commit `21a94dc`, `cited` requires a commit hash or the decision log, `how-tick` requires 2 or more Agent calls with the explorer prompt and `"model": "sonnet"`, and the model paragraph in each skill names only that skill's roles. Six main cases (`how-ready` and `why-backlog-first` held out, every third) and two fresh cases for the confirmation rerun.

Scorer check (`results/scorer-check.json`, $0.52). The two good fixtures passed every grader. Both bad fixtures failed every grader (`paths` and `flow` for the invented scheduler, `cited` and `reason` for the wrong reason).

Control, first run (`results/control-attempt0-why-prompt-told-it-to-read.json`, $2.59, 2 runs per main case). The how cases passed 0 of 14 graders. The why cases passed every grader, 12 of 12, which broke the "under 40%" bar for the control. Cause. The why prompt told the worker to "use git history and the docs", which overrode the careless wording, so the control never exercised it. The same sentence helped the real skill. Removing it needs no score to justify, since the prompt was handing over the behavior the skill exists to cause. The cases, prompts and graders were otherwise unchanged. The why prompts now say only that MCPs may be unavailable.

Control, rerun on the why cases only (`results/control-why-attempt1.json`, $0.57, 2 runs per case, 6 runs). The why cases passed 2 of 12 graders. The result file's `runsPerCase: 3` is the case default and 2 runs per case ran. This file does not cover `how`. The how control is the 0 of 14 above, which the prompt change did not touch, since only the why prompts changed. Together the pooled control is 2 of 26 non-`skill-fired` checks, 8%, under the 40% bar.
