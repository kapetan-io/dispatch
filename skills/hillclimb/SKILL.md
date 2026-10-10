---
name: hillclimb
description: Improve a skill's wording against a frozen `claude plugin eval` suite, one measured change at a time. Use when a ticket or the user says to hillclimb, tune or improve a skill's wording against a suite, or when a change to a skill needs evidence that it helps.
---

# Hillclimb a skill's wording

You change one piece of wording, measure it with a suite that does not move, and keep the change or undo it. The score is evidence only if the suite cannot be bent to fit the wording. Most of these rules guard that.

The suite lives in a directory next to the skill, such as `docs/design/evals/<name>/`, with `cases.json`, the arms (each arm is the whole skill with only the wording under test replaced), a `README.md` and a `log.md`. If there is no suite yet, build it first and follow the same rules.

## Before attempt 1

1. **Commit the pass bar.** Write it in the suite's `README.md`, as numbers per case and pooled (for example "4 of 5 runs on every case, 90% pooled"), plus a confirmation rerun of the final wording. Commit it before any run. Changing the bar later is a product call, so it goes to Needs Human with the reason, and you never relax it to pass.
2. **Fail a known-bad control.** Write an arm that is wrong on purpose, such as one that tells the worker the opposite of the behavior you want. Run it. Every check must fail it, and every check must also pass a run known to be good. If the control passes, the checks are broken, so read no result until they are fixed. Test the scorer on stored runs in both directions. In the ticket-claims climb, the scorer missed an edit made through a path variable (`p='bin/tool'` then `open(p,'w')`) and counted `>` inside `awk 'NR>5'` as a file write. It passed workers that edited first and failed honest ones, and the wording control never showed it, because that control only exercised the wording. See ENG-261.
3. **Hold cases out.** Set aside about a third of the cases, and never read their transcripts or put their results in a proposer's brief. A held-out case that leaks into tuning stops being held out. Add a fresh case of the same kind before the confirmation rerun. A wording that passed the nine tuned cases and failed the fresh one is the normal outcome, and the held-out case is how you find out.
4. **Set the run budget.** Fix the runs per case, the number of attempts (6 is plenty) and a cost ceiling, and write them in the `README.md`. Five runs per case cannot show a failure rate under about 20%, so say so in the log's limits.
5. **Log first.** Create `log.md` with a baseline entry for today's wording. It shows the headroom. If the baseline and a confirmation rerun already meet the bar, change nothing, say so in the ticket comment and stop, because a wording that passes has nothing to climb.

## Each attempt

1. Name the one idea this attempt tests and the failure it targets. Change one thing, since two changes in one attempt leave you not knowing which one moved the score. A rewrite that bundles several changes made one run in three over-split a small design in the size-check climb.
2. Start three fresh sub-agents, each with a different angle (the failing visible transcripts, pstack's wording, skill-creator guidance), and give them the arms, the failing scores and the answer keys for the visible cases only. Their briefs may not describe a held-out case's failure. A fourth fresh sub-agent judges the proposals and picks one or grafts them under a word cap.
3. Run the suite on the new arm, with the runs per case the bar names.
4. Append the entry to `log.md` before the next attempt: the idea, the table of scores, the cost, and keep or revert. Keep a change only if the bar's numbers move the right way and no case regresses. Otherwise revert it in full and log it anyway.

## Changing the test data

Change a case, an answer key or a grader only for a reason you can state without the scores, and write that reason in the log. If you can only justify the change by what it does to the numbers, don't make it. After any change, rerun the known-bad control and the baseline. A wrong answer key can hide in plain sight: in size-check, `no-deferred` failed the control and every arm on tiger because the key listed work the design commits to as deferred. Reading the design settled it, with no scores involved. A change to what the user wants (granularity, routing) is a product call, so ask.

## Stopping

Stop when an attempt meets the bar and the confirmation rerun of the same wording meets it again. The rerun can fail where the first pass succeeded, and when it does, that is a result: log it and run another attempt. Stop after the budgeted attempts or when the cost ceiling is hit.

When the bar is not met, move the ticket to Needs Human. The comment says the best arm, its scores per case, the cases still failing with what the visible transcripts show, and what you would try next. Leave `main`'s wording unchanged.

When the bar is met, put the adopted wording in the skill, name the winning arm at the top of the suite's `README.md`, and add the limits to the log: which cases tuned the wording, which were held out, how many runs per case, and the models used.
