# Design eval log

## 2026-10-10, attempt 1 (5 runs)

Control (`results/attempt1-control.json`, 2 runs): 7 of 18 non-`skill-fired` checks pass (39%), under the 40% limit. It fails `how-fired`, `why-fired` and `grounding-traced`. `candidates-cite-grounding` does not separate it from the skill, because the case prompt makes even the control write a grounding file.

Skill (`results/attempt1-skill.json`), passes of 5: `skill-fired` 5, `how-fired` 1, `why-fired` 0, `arena-fired` 3, `candidates-get-grounding` 2, `candidates-get-runner-prompt` 2, `candidates-get-objective` 2, `grounding-traced` 3, `candidates-cite-grounding` 3, `last-message-asks-sketch` 3. Pooled 19 of 45. Cost about $25.6, at the invocation ceiling. Bar not met.

Diagnosis from the traces. Run 0 loaded none of `how`, `why` or `arena` and read the repo itself. Run 1 loaded `how` and `arena` but not `why`. Runs 3 and 4 show 0 turns and cost $3.7 each, so the $25 `--max-cost-usd` ceiling probably cut them off. They count as failures that say nothing about the skill.

## 2026-10-10, attempt 2: todo entries name the Skill calls (5 runs, ceiling $45)

The only wording change is in the Start todolist. Entry 3 says to load `dispatch:how` with the Skill tool and `dispatch:why` over any existing shape the design extends or replaces, and entry 4 says to load `dispatch:arena` with the Skill tool. The copied Phase A and Phase B text is unchanged.

Result (`results/attempt2-skill.json`), passes of 5: `skill-fired` 5, `how-fired` 2, `why-fired` 0, `arena-fired` 2, `candidates-get-grounding` 2, `candidates-get-runner-prompt` 2, `candidates-get-objective` 2, `grounding-traced` 5, `candidates-cite-grounding` 5, `last-message-asks-sketch` 5. Pooled 25 of 45. Cost about $30. Bar not met.

The content graders now pass, so the grounding is traced and the candidates cite it. The skill-loading graders fail because the worker rebuilds the how, why and arena steps itself (run 4 loaded none of them, run 3 only `how`) and then also skips the candidate prompt checks, which depend on the arena's own text. The todo entries did not move it, probably because the eval gives the worker no todo tool (the arena eval hit the same limit).

## 2026-10-10, attempt 3: reasons for loading each skill, in Phase A and Phase B (5 runs, ceiling $45)

Phase A gains "Choosing between extending an existing mechanism and adding a new one counts as layering" and a paragraph saying to load `how` and `why` with the Skill tool, with the reason each holds. Phase B gains a sentence to load the arena with the Skill tool rather than reproduce it. These are dispatch additions to the copied text.

Result (`results/attempt3-skill.json`), passes of 5: `skill-fired` 5, `how-fired` 5, `why-fired` 5, `arena-fired` 4, `candidates-get-grounding` 2, `candidates-get-runner-prompt` 2, `candidates-get-objective` 2, `grounding-traced` 5, `candidates-cite-grounding` 5, `last-message-asks-sketch` 4. Pooled 34 of 45 (76%). Cost about $38.7. Bar not met, and the attempt budget is spent.

The wording works for what the ticket asks. The worker now loads `how` and `why` every time, and the grounding is traced and cited by every candidate. The three `candidates-get-*` graders read only the text of the `Agent` call. In runs 1, 2 and 4 the candidate prompt is a 220 to 270 character pointer to a file the worker wrote (`Read scratch/arena-ci/task.md`), and the grounding path, the runner prompt and the Objective are in that file. That is what the arena's Phase B allows ("the path to the shared grounding"), so those three failures are a grader limit and not a skill defect. The judge, a fourth `Agent` call, reads the grounding in the same runs. `arena-fired` 4 of 5 and `last-message-asks-sketch` 4 of 5 each lost one run; run 2 never loaded the arena skill.

Decision for the user: the bar, fixed before the runs, is not met. Changing the three graders to read the file the prompt points at is a bar change, so it is not made here.

## 2026-10-10, grader fix and rescore (no model run)

The supervisor decided, for the user, that the three `candidates-get-*` graders read the wrong place, so they now also read the file the `Agent` prompt points at (`cases/ci-failure-forwarding/case.yaml`). They changed from exact checks to LLM trace graders. The stored traces hold the files the workers wrote, so the stored runs were rescored with a script instead of the judge model: for each `Agent` call made after `grounding.md` was written, the prompt plus any file the prompt names, written earlier by `Write` or a shell redirect, was searched with the old patterns. The script approximates the new LLM graders and is not the graders themselves. It is `rescore.py` in this directory, and `results/rescore.json` holds its per-run verdicts for the attempt 3 skill runs and the control runs.

Attempt 3 rescored: all three graders pass in 5 of 5 runs, so the skill's pooled score is 43 of 45 (96%). Under the bar's item 1 the skill still falls short on one count, `arena-fired` 4 of 5 where the bar asks 5 of 5 (run 2 never loaded the arena skill).

Control rescored: `candidates-get-grounding` and `candidates-get-objective` now pass in both control runs, because the case prompt makes the control write a grounding file too and its candidates read it through the same pointer. Pooled the control passes 9 of 18 non-`skill-fired` checks (50%), over the 40% limit in item 3. The sensitivity check fails with the new graders. The likely fix is a stricter control arm that withholds the grounding file from the candidates, or a case prompt that doesn't demand the file from the control, and either needs model runs and a decision that was not given.

Bar not met: `arena-fired` 4 of 5, and the control at 50% (9 of 18).

## 2026-10-10, PR review (CodeRabbit)

Five claims were checked against the code and all held. The shared case prompt ordered a grounding file from both arms, which is why the control scored 50%. The prompt now says to write the file "if your instructions have you write a grounding file", so the control stub no longer has to. Every result above predates that change, and the control has not been rerun, because the attempt budget is spent. The score tables in this log stand for the old prompt only. The next attempt reruns the control and the skill on the new prompt.

Correction from review: the control has 9 graders besides `skill-fired`, so two runs give a denominator of 18, not 20. The original control is 7 of 18 (39%) and the rescored control is 9 of 18 (50%). Both are still read against the 40% limit, and the original passes it by one check, so the conclusion is unchanged.
