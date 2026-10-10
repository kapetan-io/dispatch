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

## 2026-10-10, review of the PR and the grader rebuild

A review of the PR found five problems. Four are fixed and the fifth is below. (1) The premise rule let a non-constraint premise into the candidate prompt "as background", which the ticket forbids, so that clause was removed. (2) The redesign-from-first-principles line said "this graft" where pstack's principle says "this new requirement", so every inlined principle sentence now copies its source exactly. A third skill-creator review confirmed all of them match. (3) The wording had changed after the second skill-creator review, so a third review ran before any further run and its two small changes were applied (the judge gets the full premise list marked constraint or not, and the ask-first rule covers pushes and comments). (4) No results were committed, so `results/` holds every run's JSON. (5) The eval did not test phase order, and its prompt named the rubric, the prompt file and tickets, which primed three graders. The prompt now names only a scratchpad and the paths of `artifact.md` and `note.md`, and graders check the `Agent` calls directly.

The attempt 3 numbers above came from the weaker grader set. Under the rebuilt set the same skill was run four times ("attempt 4"), and the first three runs measured the graders and not the skill.

| Run | File | Cause of the change |
|---|---|---|
| 4a | `attempt4a-*.json` | Graders read the transcript with an LLM. `rubric-first`, `prompt-premise`, `verify` and the file graders scored 0 of 5, and the judges gave no reasons. |
| 4b | `attempt4b-*.json` | Eight graders threw "Invalid regular expression", because JavaScript has no `(?i)`. My mistake, fixed with character classes. |
| 4c | `attempt4c-*.json` | The `files` focus gives the judge only the list of changed paths, not contents, so `note` and `premise-resolved` could not pass. They now read `note.md`, and the prompt names it. |
| 4d | `attempt4d-*.json` | Final grader set. |

Final result (4d), passes of 5: `skill-fired` 5, `fan-out` 5, `model-fable` 5, `model-sonnet` 5, `model-opus` 5, `judge-readonly` 5, `rubric-judge-only` 5, `judge-after` 5, `prompt-scratch` 5, `prompt-premise` 5, `prompt-clean` 5, `note` 2, `premise-resolved` 5, `verify` 0, `asked` 5. Control 2 of 28 (7%). Cost of 4d about $10.5.

The bar is not met. `verify` is 0 of 5, and the traces show it is a real skipped phase, since in every run the last tool calls write the artifact and note and no read or command follows before the final message. `note` is 2 of 5 mainly because the grader asks for the models each candidate ran on, which the skill does not ask for. That grader was not changed after seeing the scores. Two things stop me here. The attempt budget (3 attempts) is spent, and the next step is a wording choice, whether to add a verify line to a skill the ticket wants kept verbatim from pstack. Both go to the user.

## 2026-10-10, attempt 5: the Phase F line and the ticket's exact premise wording (3 runs)

Asked for by the user after the Needs Human comment. Two wording changes: Phase A step 3 now says a non-constraint premise "stays out of the candidate prompt", and Phase F ends with its own line, "Before the final message, re-read the artifact against the grounding material and report what you checked in the synthesis note's verification result." A fourth skill-creator review ran first. Its other suggestions were not taken, because the premise rewording it proposed would let the premise back into the prompt, and the Phase F placement point was taken. The user also set 3 runs for this attempt, so the bar became 3 of 3 per grader (README). The `note` grader no longer asks for the models each candidate ran on, because the skill never asks the worker for them, and the control was rerun on the final grader set first (2 of 28, 7%, `results/attempt5-control.json`).

Result (`results/attempt5-skill.json`), passes of 3: everything 3 except `verify` 0. Cost about $7.

The line changed nothing. In all three runs the last two calls are `Write artifact.md` and `Write note.md`, with no read after them. Two runs ran a shell check of the docs and candidate files before writing the artifact, which is a check before the synthesis is final and not a re-read of the written file. Bar not met. Three runs can't show a failure rate under about 35%, but 0 of 3 beside 0 of 5 and 1 of 5 earlier is consistent.

## 2026-10-10, attempt 6: a re-read todo item (3 runs)

The user chose to add a required todo item. The todo list gained "7. Re-read the artifact with Read against the grounding material, then record what you checked in the note's verification result (if it fails, reopen Graft)" before "8. Ask the user". A fifth skill-creator review ran first, and its ordering and tool-call points were taken. Control on the final graders passes 2 of 28 (7%, `results/attempt6-control.json`).

Result (`results/attempt6-skill.json`), passes of 3: everything 3 except `verify` 0. Cost about $6.6. Bar not met.

What the traces show. One run ended with a `Read` of `artifact.md` after writing it, so the item changed behavior once, and that run still failed `verify`, so the grader's reading of that run is not understood (no judge reasons are recorded). The other two ran a shell check before writing and went from the two writes to the final message. The eval harness gives the worker no todo tool, so an item on the todo list can't act as a forcing function here. It may work in a real session that has one, and this eval can't show that.

## 2026-10-10, bar change: `verify` dropped from the gate

The user dropped `verify` from the pass bar after attempt 6 (a product call, not mine). Nothing was rerun. Under the changed bar attempt 6 passes: the 14 other graders each pass 3 of 3 (42 of 42, 100% pooled), and `skill-fired` and `fan-out` pass 3 of 3. The bar was changed after seeing the result, and the reason is stated above: the check measures a gap the eval cannot fix, and its wording fix has no way to be tested in a harness without a todo tool. `verify` is 0 of 3 for the adopted wording and 0 of 3, 1 of 5 and 0 of 5 for the earlier wordings, which this PR ships with.
