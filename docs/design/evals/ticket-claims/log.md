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

## 2026-10-09, attempt 2

Idea: replace attempt 1's "if it changes what gets built" test with one question the worker can answer, whether the ticket's acceptance criteria can still be met as written once the claim is corrected. A wrong file, helper or recipe leaves the ask intact, so the worker comments, creates or works around what is missing, and keeps going. Needs Human is for a ticket that now asks for something else. This targets the 3 of 5 over-escalations on unknown-keys. Nothing in unknown-keys' transcripts or ticket was read to write it. The proposers (pstack and skill-creator angles) got only the score and the key's expected routing, and a judge sub-agent grafted them. No visible case failed in attempt 1, so no third proposer read transcripts. The judge also cut two sentences ("don't re-prove a plain truth", "a fix written in the same step as its test proves nothing"). The first is covered by "Skip claims that don't carry weight" and "A claim that checks out needs no comment".

Results, 5 runs per case, 45 runs, $6.27. **Bar met.** Caught 25/25, planted clean 24/25, control clean 20/20, routed 45/45, repro 15/15. The one miss is a false alarm on unknown-keys in 1 of 5 runs (4/5 clean, within the bar's 4 of 5). The over-escalation is gone: unknown-keys routed 5/5.

| Case | caught | clean | routed | repro |
|---|---|---|---|---|
| config-default | 5/5 | 5/5 | 5/5 | |
| config-default-true | | 5/5 | 5/5 | |
| expand-home | | 5/5 | 5/5 | |
| empty-teams (held out) | | 5/5 | 5/5 | |
| hint-bug-true | | 5/5 | 5/5 | 5/5 |
| hint-cause | 5/5 | 5/5 | 5/5 | 5/5 |
| hint-fixed | 5/5 | 5/5 | 5/5 | 5/5 |
| max-range (leaked) | 5/5 | 5/5 | 5/5 | |
| unknown-keys (held out) | 5/5 | 4/5 | 5/5 | |

## 2026-10-09, replacement held-out case and confirmation

Attempt 1's leak left one planted held-out case, unknown-keys, and a tuned-on max-range. Before the confirmation rerun, `nudge-floor` (ENG-310) is added as a fresh held-out planted case of max-range's kind: the ticket attributes a requirement to the blueprint that the blueprint doesn't make. No wording has seen it. Only the case count and the pooled denominators change. Per-case thresholds are unchanged. The confirmation rerun is 5 runs per case on all ten cases, 50 runs.

## 2026-10-09, confirmation rerun of attempt 2

10 cases, 5 runs each, 50 runs, $7.00. **Bar not met.**

| Case | caught | clean | routed | repro |
|---|---|---|---|---|
| config-default | 5/5 | 5/5 | 5/5 | |
| config-default-true | | 5/5 | 5/5 | |
| expand-home | | 5/5 | 5/5 | |
| empty-teams (held out) | | 5/5 | 5/5 | |
| hint-bug-true | | 5/5 | 5/5 | 5/5 |
| hint-cause | 5/5 | 5/5 | 5/5 | 5/5 |
| hint-fixed | 5/5 | 4/5 | 5/5 | 5/5 |
| max-range (leaked) | 5/5 | 5/5 | 5/5 | |
| nudge-floor (held out, new) | **2/5** | 5/5 | **0/5** | |
| unknown-keys (held out) | 5/5 | **3/5** | 5/5 | |

The wording that met the bar on the original nine cases does not generalize to the fresh design-claim case. On nudge-floor, 3 of 5 workers did not flag that the blueprint makes no such requirement, so they built the floor as the ticket described. Unknown-keys' false alarms went from 1 of 5 to 2 of 5, so that was noise at 5 runs, not a fix. Attempt 2's pass on the first nine was a pass that included a case max-range whose failure had leaked into the tuning. The confirmation did its job. Attempt 3 follows. nudge-floor and unknown-keys transcripts stay unread, and max-range and hint-fixed are the readable cases.
