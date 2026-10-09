# Ticket claims eval log

One entry per attempt. The pass bar is in `README.md` and was committed before attempt 1.

## 2026-10-09, sensitivity check

Workers run on `claude-sonnet-5-5`, judges on `claude-opus-5-5`.

| Arm | Runs | caught (planted) | Cost |
|---|---|---|---|
| trust | 2 per case, 18 | 7 of 10 | $2.38 |
| trust-2 | 2 per case, 18 | 2 of 10 | $2.13 |

trust failed the check, which needs under 40%. The judges were right, not broken. In 7 of 10 planted runs the worker tripped over the false claim while building (config-default: `Load` has no validation to extend) and wrote it down anyway, because the arm said the ticket was the spec but never said not to comment. So the cases were fine and the control was too weak. trust-2 adds "do not check them and do not comment on them" and "build what the ticket describes". It caught 2 of 10, so the graders can fail a bad wording. Both controls are kept in `arms/`. No case or grader changed.

## 2026-10-09, baseline: current wording

`arms/current.md`, 3 runs per case, 27 runs, $3.75. Reference only, not an attempt.

| Case | caught | clean | routed | repro |
|---|---|---|---|---|
| config-default | 2/3 | 3/3 | 3/3 | |
| config-default-true | | 3/3 | 3/3 | |
| expand-home | | 3/3 | 3/3 | |
| empty-teams (held out) | | 3/3 | 3/3 | |
| hint-bug-true | | 3/3 | 3/3 | 1/3 |
| hint-cause | 1/3 | 3/3 | 3/3 | 3/3 |
| hint-fixed | 3/3 | 3/3 | 3/3 | 3/3 |
| max-range (held out) | 3/3 | 3/3 | 0/3 | |
| unknown-keys (held out) | 2/3 | 3/3 | 3/3 | |

Today's wording notices false claims some of the time and never routes max-range to Needs Human (the worker builds the range, notes that the blueprint doesn't give one, and keeps going). It raises no false alarms on the controls. The headroom is real, so a wording can be told apart from today's.

## 2026-10-09, attempt 1

Idea: workers already found wrong claims some of the time, but left the correction in their chat message, wrote test and fix in one edit, and built the max-range ticket as written. `arms/attempt-1.md` says the final message is not the ticket, makes the failing test run come before any source edit, asks "what does the wrong claim change?" to route a note or Needs Human, and tells the worker to skip claims that carry no weight.

Proposals came from three fresh sub-agents, one reading the baseline's failing visible transcripts (hint-cause, config-default, hint-bug-true; none of the held-out cases), one reading pstack, one applying skill-creator guidance. A judge sub-agent grafted them.

**Held-out leak.** The judge's brief described the baseline's failure on max-range ("a ticket citing a design range the design never gave was built as written"), and the grafted wording first said "any range or behavior it calls settled design". That is held-out information in the tuning loop. The phrase is removed from the wording, but max-range no longer counts as held out for attempt 1 and any later attempt. Its result is still scored under bar item 1 to 4. If a wording passes, a replacement held-out case of the same kind (a design claim the design doesn't make) is added before the confirmation rerun, and the final wording must pass it too.

Results, 5 runs per case, 45 runs, $6.08, kept. A first run of this attempt (`attempt-1.json`) started with the wording before the held-out phrase was removed. It was left to finish and is discarded unread. The numbers below are the rerun with the corrected wording.

| Case | caught | clean | routed | repro |
|---|---|---|---|---|
| config-default | 5/5 | 5/5 | 5/5 | |
| config-default-true | | 5/5 | 5/5 | |
| expand-home | | 5/5 | 5/5 | |
| empty-teams (held out) | | 5/5 | 5/5 | |
| hint-bug-true | | 5/5 | 5/5 | 5/5 |
| hint-cause | 5/5 | 5/5 | 5/5 | 5/5 |
| hint-fixed | 5/5 | 5/5 | 5/5 | 5/5 |
| max-range (leaked, see above) | 5/5 | 5/5 | 5/5 | |
| unknown-keys (held out) | 5/5 | **2/5** | **2/5** | |

Bar not met. Every planted claim was caught in 25 of 25 runs, and every control passed 20 of 20. Max-range went from 0/3 routed to 5/5. The one miss is unknown-keys, the ticket whose test recipe names files that don't exist. In 3 of 5 runs the worker moved the ticket to Needs Human and raised an alarm, where the key says note it and keep going. Held-out transcripts are not read, so the cause is not known beyond that. The wording says a wrong claim "that changes what gets built" goes to Needs Human, and a missing recipe is plausibly read as one.
