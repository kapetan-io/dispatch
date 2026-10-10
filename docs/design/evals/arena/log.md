# Arena eval log

## 2026-10-10, sensitivity check

`arms/careless.md`, 2 runs. It passed 2 of 8 non-`skill-fired` graders in each run (`fan-out`, `rubric`), 25% pooled, under the 40% limit. The graders tell the control from a skill that follows the phases.

## 2026-10-10, attempt 1: paraphrased skill

The first draft of the skill reworded pstack's arena instead of copying it. 5 runs, claude-sonnet-5-5 as the worker. Per grader passes of 5: `skill-fired` 5, `fan-out` 5, `rubric` 5, `candidates` 5, `prompt-premise` 5, `prompt-clean` 5, `note` 5, `premise-resolved` 5, `asked` 2. Bar not met, since `asked` is under 4 of 5. In the three failing runs the closing message gave the answer and never asked before anything is filed or changed.

The ticket then changed to require pstack's text verbatim, so this wording is dropped and not tuned. Attempt 2 starts from pstack's file.
