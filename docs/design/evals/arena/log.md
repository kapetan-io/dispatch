# Arena eval log

## 2026-10-10, sensitivity check

`arms/careless.md`, 2 runs. It passed 2 of 8 non-`skill-fired` graders in each run (`fan-out`, `rubric`), 25% pooled, under the 40% limit. The graders tell the control from a skill that follows the phases.

## 2026-10-10, attempt 1: paraphrased skill

The first draft of the skill reworded pstack's arena instead of copying it. 5 runs, claude-sonnet-5-5 as the worker. Per grader passes of 5: `skill-fired` 5, `fan-out` 5, `rubric` 5, `candidates` 5, `prompt-premise` 5, `prompt-clean` 5, `note` 5, `premise-resolved` 5, `asked` 2. Bar not met, since `asked` is under 4 of 5. In the three failing runs the closing message gave the answer and never asked before anything is filed or changed.

The ticket then changed to require pstack's text verbatim, so this wording is dropped and not tuned. Attempt 2 starts from pstack's file.

## 2026-10-10, attempt 2: pstack's text with the Claude Code changes, after the second skill-creator review

5 runs, cost about $7.80. Passes of 5: `skill-fired` 5, `fan-out` 5, `rubric` 5, `candidates` 5, `prompt-premise` 5, `prompt-clean` 3, `note` 4, `premise-resolved` 5, `asked` 5. Pooled 42 of 45 (93%). Bar not met, since `prompt-clean` is under 4 of 5. The judge gave no reasons. In the two failing prompts the task list in the candidate prompt ("numbers, rate-limit budget, the oversell problem, out-of-order and missed events") reads like rubric criteria, and the grader fails a prompt that includes them. pstack says candidates only see the task, but it doesn't forbid criteria in the task text.

Attempt 3 adds one sentence to Phase A step 2, "Do not put the criteria in the candidate prompt."

## 2026-10-10, attempt 3: adopted

5 runs, cost about $7.66. Passes of 5: `skill-fired` 5, `fan-out` 5, `rubric` 5, `candidates` 4, `prompt-premise` 5, `prompt-clean` 5, `note` 5, `premise-resolved` 5, `asked` 5. Pooled 44 of 45 (98%). The bar is met: every grader is at 4 of 5 or better, and `skill-fired` and `fan-out` are 5 of 5. The one `candidates` miss was not read.

Limits. One case, so this shows the phases followed on one question and does not show the arena beats a single attempt. Five runs can't show a failure rate under about 20%. The worker ran on claude-sonnet-5-5 and the judge on claude-opus-5-5. There is no held-out case. The attempt 3 change followed reading the two failing prompts of attempt 2, which is the same case that scored it.
