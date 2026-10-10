---
name: hillclimb
description: Improve a skill's wording against a frozen `claude plugin eval` suite, one measured change at a time. Use whenever a ticket or the user says to hillclimb, tune, iterate on or improve a skill's or prompt's wording against an eval suite, asks whether a rewording helps, or wants to change a skill's text and needs evidence. Use it even when the ticket only says "improve this wording" and names no loop.
---

# Hillclimb a skill's wording

Change one piece of wording, measure it with a suite that doesn't move, and keep or revert the change.

The suite lives in `evals/<skill>/` at the repo root. A skill with several suites puts each in `evals/<skill>/<suite>/`. Name each case `<skill>-<scenario>` so names stay unique across suites. A suite holds `cases.json` (or `cases/`), `arms/` (each arm is the whole skill with only the wording under test replaced), `README.md`, `log.md` and `results/`. If there is no suite, build one under the same rules.

Every score the log cites needs its `claude plugin eval` result JSON in `results/`, committed with the `log.md` entry that cites it. That covers the baseline, control, attempts and reruns, partial ones too. A score with no committed file doesn't count, because a later reader can't recheck it.

## Before attempt 1

1. **Commit the pass bar** in the suite's `README.md` (git commit) before any run. Give numbers per case and pooled, plus one confirmation rerun of the final wording. Set the numbers from what you need, not from what the baseline scores. Changing the bar later is a product call, so ask the user, and never relax it to pass.
2. **Fail a known-bad control.** Write an arm that is wrong on purpose and set its own bar, such as "passes under 40% of checks". Make it plausible, so it fails on the behavior and not on format. If it passes, the checks are broken, so read no result until they are fixed. The control only exercises the wording, so also run the scorer on a stored or hand-written run known to be good and one known to be bad. A scorer that misses edits made through a path variable passes workers that edited first, and the control never shows it (ENG-261).
3. **Hold cases out.** Before any run, set aside about a third by a rule that ignores difficulty, such as every third case, and mark them `heldout` in `cases.json`, or in each case's `case.yaml`. They count toward the bar, but read only their scores, never their transcripts, and never describe their failures in a proposer's brief. Write a fresh case of the same kind before the confirmation rerun, from the design and not from the winning wording.
4. **Fix the budget** in the `README.md`: runs per case, number of attempts and a cost ceiling. Five runs per case can't show a failure rate under about 20%, so say so in the log. Run the confirmation rerun once per wording.
5. **Log a baseline** for today's wording. If the baseline and a confirmation rerun already meet the bar, change nothing, say so on the ticket and stop.

## Each attempt

1. Name the one idea and the failure it targets.
2. Start three fresh sub-agents with different angles, such as the failing visible transcripts, an outside source like pstack's playbooks, and skill-creator guidance. Give them the arms, scores and answer keys for visible cases only, and reject any wording that quotes a case's content.
3. Start a fourth fresh sub-agent to pick one proposal or graft them under a word cap you set before proposing.
4. Run the suite on the new arm at the bar's run count, and write the result JSON to `results/` before you log anything.
5. Append to `log.md`, in the commit with the result file: the idea, the scores, the cost, kept or reverted. Keep the change only if the bar's numbers move the right way and no case falls below the bar's per-case floor. Otherwise revert the wording in full, keep the result file, and log it anyway.

## Changing test data

Change a case, answer key or grader only for a reason you can state without the scores, and write it in the log. Then rerun the control and the baseline. If the only case for the change is what it does to the numbers, don't make it. A change to what the user wants (granularity, routing) is a product call, so ask the user.

## Stopping

Stop when an attempt meets the bar and a rerun of the same wording, on every case including the fresh one, meets it again. A failed rerun is a result, so log it and run another attempt. Stop at the attempt budget or cost ceiling too.

If the bar isn't met, leave the skill's wording unchanged and move the ticket to Needs Human (the dispatch status that waits for the user), or tell the user if there is no ticket. Say the best arm, its scores per case, what the visible failures show and what you'd try next.

If it is met, put the adopted wording in the skill, name the winning arm at the top of the suite's `README.md`, and log the limits: which cases tuned the wording, which were held out, runs per case and models.
