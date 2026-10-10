---
name: hillclimb
description: Improve a skill's wording against a frozen `claude plugin eval` suite, one measured change at a time. Use whenever a ticket or the user says to hillclimb, tune, iterate on or improve a skill's or prompt's wording against an eval suite, asks whether a rewording actually helps, or wants to change a skill's text and needs evidence. Use it even when the ticket only says "improve this wording" and names no loop.
---

# Hillclimb a skill's wording

You change one piece of wording, measure it with a suite that does not move, and keep the change or undo it. The score is evidence only if the suite cannot be bent to fit the wording. Most of these rules guard that.

The suite lives in a directory next to the skill, such as `docs/design/evals/<name>/`, with `cases.json`, the arms (each arm is the whole skill with only the wording under test replaced), a `README.md` and a `log.md`. If there is no suite yet, build it first and follow the same rules.

## Before attempt 1

1. **Commit the pass bar.** Write it in the suite's `README.md`, as numbers per case and pooled (for example "4 of 5 runs on every case, 90% pooled"), plus a confirmation rerun of the final wording. Commit it before any run. Changing the bar later is a product call, so it goes to Needs Human with the reason, and you never relax it to pass.
2. **Fail a known-bad control.** Write an arm that is wrong on purpose, such as one that tells the worker the opposite of the behavior you want, and run it. Set the bar for it too, such as "passes under 40% of the checks". If it passes, the checks are broken, so read no result until they are fixed.

   The control only exercises the wording, so also test the scorer on stored runs in both directions. Feed it a run known to be bad, which must fail, and one known to be good, which must pass. In the ticket-claims climb the scorer missed an edit made through a path variable (`p='bin/tool'` then `open(p,'w')`), so workers that edited first passed. It also counted `>` inside `awk 'NR>5'` as a file write, so honest workers failed. The wording control showed neither. See ENG-261.
3. **Hold cases out.** Set aside about a third of the cases, and never read their transcripts or put their results in a proposer's brief. A held-out case that leaks into tuning stops being held out. Add a fresh case of the same kind before the confirmation rerun. A wording that passed every tuned case and failed the fresh one is a normal outcome (ticket-claims attempt 2 did exactly that), and the held-out case is how you find out.
4. **Set the run budget.** Fix the runs per case, the number of attempts (the two climbs took 3 and 6) and a cost ceiling, and write them in the `README.md`. Five runs per case cannot show a failure rate under about 20%, so say so in the log's limits.
5. **Log first.** Create `log.md` with a baseline entry for today's wording. It shows the headroom. If the baseline and a confirmation rerun already meet the bar, change nothing, say so in the ticket comment and stop, because a wording that passes has nothing to climb.

## Each attempt

1. Name the one idea this attempt tests and the failure it targets. Change one thing, since two changes in one attempt leave you not knowing which one moved the score. A rewrite that bundles several changes made one run in three over-split a small design in the size-check climb.
2. Start three fresh sub-agents so no proposal inherits your blind spots. Give each a different angle, such as the failing visible transcripts, an outside source of wording (pstack's playbooks), and skill-creator guidance. Give them the arms, the scores and the answer keys for the visible cases only, and describe no held-out case's failure in any brief.
3. Start a fourth fresh sub-agent to judge the proposals. It picks one or grafts them under a word cap, which keeps the wording from growing each attempt.
4. Run the suite on the new arm, with the runs per case the bar names.
5. Append the entry to `log.md` before the next attempt: the idea, the table of scores, the cost, and keep or revert. Keep a change only if the bar's numbers move the right way and no case regresses. Otherwise revert it in full and log it anyway.

## Changing the test data

Change a case, an answer key or a grader only for a reason you can state without the scores, and write that reason in the log. If you can only justify the change by what it does to the numbers, don't make it. After any change, rerun the known-bad control and the baseline. A wrong answer key can hide in plain sight: in size-check, `no-deferred` failed the control and every arm on tiger because the key listed work the design commits to as deferred. Reading the design settled it, with no scores involved. A change to what the user wants (granularity, routing) is a product call, so ask.

## Stopping

Stop when an attempt meets the bar and the confirmation rerun of the same wording meets it again. The rerun can fail where the first pass succeeded, and when it does, that is a result: log it and run another attempt. The rerun covers every case, including the fresh held-out one. Stop after the budgeted attempts or when the cost ceiling is hit.

When the bar is not met, move the ticket to Needs Human (or tell the user, when there is no ticket). The comment says the best arm, its scores per case, the cases still failing with what the visible transcripts show, and what you would try next. Leave `main`'s wording unchanged.

When the bar is met, put the adopted wording in the skill, name the winning arm at the top of the suite's `README.md`, and add the limits to the log: which cases tuned the wording, which were held out, how many runs per case, and the models used.
