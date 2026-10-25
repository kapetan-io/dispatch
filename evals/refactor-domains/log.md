# Refactor-domains eval log

Each entry records a run's scores and cost. The $10 cap covers all of them.

## 2026-10-24, attempt 1 (the only attempt)

The runs scored the draft of `skills/refactor-domains/SKILL.md` whose SHA-256 starts `f7b21846b4c6730b`. Review rounds after the runs changed the text (one by a sub-agent that loaded `anthropic-skills:skill-creator`), so the numbers describe the draft and not the shipped skill. Judge model `claude-sonnet-5-5`. Cost, from the result files: the worker runs cost $2.40 (skill, runs $0.86, $0.86, $0.68) and $0.98 (control, runs $0.48 and $0.50), which is what `--max-cost-usd` and the files' `costUsd` count. The judge model cost another $1.87 (skill, $0.67, $0.67, $0.52) and $0.67 (control, $0.33 and $0.34). Everything together is $5.92 of the $10 cap.

Control (`results/attempt1-control.json`, 2 runs): 8 of 24 non-`skill-fired` checks pass (33%), under the 40% limit. It passes `product-use-kept`, `saved-data-kept`, `test-cases-unchanged` and `left-out-listed` in both runs. The fixture is small enough that a careless plan still reads `docs/CONTEXT.md` and keeps the CLI.

Skill (`results/attempt1-skill.json`, 3 runs), passes of 3: `skill-fired` 3, `problem-domains-fired` 0, `surface-testing-fired` 0, `purpose-line` 3, `problems-cited` 1, `product-use-kept` 3, `saved-data-kept` 3, `names-from-domain` 2, `new-package-earns-place` 1, `test-cases-unchanged` 2, `pieces-have-kinds` 2, `pieces-have-proof` 0, `left-out-listed` 3. Pooled 23 of 39 (59%). Bar not met. Five graders pass in fewer than 2 of 3 runs, and the pooled rate is under 85%.

Reading, from the blueprints the judges saw. Nothing was reworded or rerun, because ENG-574 says to record the scores and stop.

- `problem-domains-fired` and `surface-testing-fired` fail 0 of 3. The worker followed the skill's rules without loading the two skills it points at, so the skill's "Load ..." pointers do not make a worker load them. This is a known gap, and the control cannot pass these two graders by construction, which counts 2 of its 12 non-`skill-fired` checks against it.
- `pieces-have-proof` fails 0 of 3. Runs 0 and 2 write the single `git diff` command once above the piece list, and the grader asks for it in each piece. Run 1 describes the check in prose and never writes the command. The grader may be too strict or the skill too loose, and the scores don't say which.
- The judges warned they are noisy on files over about 10,000 characters, and runs 0 and 1 wrote more than that.
- The skill beats the control on `purpose-line` (3 of 3 against 0 of 2), `pieces-have-kinds` (2 of 3 against 0 of 2), `names-from-domain` (2 of 3 against 0 of 2) and `new-package-earns-place` (1 of 3 against 0 of 2). It does not beat it on the four graders the control also passes.

After the runs, the skill gained the line "Write each piece's check inside the piece, not once above the list" because of what the runs showed for `pieces-have-proof`. That is one reading of the scores applied once, in the review that followed, and the skill was not rerun against it. Its effect is unmeasured.
