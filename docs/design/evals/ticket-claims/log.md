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

## 2026-10-09, attempt 3

Idea: a ticket's "the design says X" is a claim the worker settles by finding the sentence in the design. A limit, floor, range, default or allowed value the design never states is the ticket's own product call, so the ticket goes to Needs Human with its claim quoted beside what the worker found. This targets nudge-floor's 3 of 5 misses, where the claim sounded reasonable and workers had nothing concrete to grep for. Three proposers read max-range's five runs (all flagged the missing range by grepping the blueprint for the claimed words), pstack's epistemics passage, and skill-creator guidance. A judge merged them under a 300-word cap (290 words). It also adds "post what you ran and saw, not guesses" for Needs Human comments, which comes from one hint-fixed run whose comment speculated and was flagged. That is my reading, not a grader output. It cuts "Skip claims that don't carry weight" and the "final message is not the ticket" sentence (the rule to write the comment first stays). nudge-floor, unknown-keys and empty-teams transcripts stay unread.

Results, 5 runs per case, 50 runs, $6.95. **Bar not met**, on one case.

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
| nudge-floor (held out) | 5/5 | 5/5 | 4/5 | |
| unknown-keys (held out) | 5/5 | **1/5** | **2/5** | |

The nudge-floor miss is fixed (2/5 caught to 5/5, 0/5 routed to 4/5). Unknown-keys has failed in every run set (attempt 1 routed 2/5, attempt 2 clean 4/5 then 3/5, attempt 3 clean 1/5), whatever the wording.

## 2026-10-10, unknown-keys ticket narrowed (approved on ENG-248)

Reason, which holds without the scores. At fixture `2700f6c` the blueprint's example config has `todo` under `[linear.statuses]` and a `[linear.needs_human]` table with `status`. `Config` has neither. `git show 2700f6c:docs/design/blueprint.md` shows `todo = "Todo"` and `[linear.needs_human]` in the Config section (lines 276 and 282), and `git show 2700f6c:internal/config/config.go` has no `Todo` field and no `linear.needs_human` table (`NeedsHuman` is a field of `Statuses`, as `needs_human`). Decoding the example into `Config` leaves those keys undecoded. The ticket asked to reject unknown keys "at any depth" and said "Nothing in the design changes", so rejecting would break the documented config, and a worker that stopped was giving a defensible answer. The old key (note it, keep going) was wrong. The case had a second, unplanted true problem.

How I noticed, which is a held-out read. While checking the graders after the first trust-arm run (before attempt 1), I read the `post.md` of eight runs across four cases, unknown-keys among them. One unknown-keys comment listed the blueprint's `todo` and `[linear.needs_human]` as keys `Config` lacks. I read no unknown-keys transcript from any attempt. I asked before changing the case because I had already seen its scores, and the user approved on ENG-248.

Change: the ticket now asks only for unknown top-level keys, which the example doesn't trip, and keeps the planted recipe claim (testdata and `loadFixture` don't exist). The expected routing is still "note it, keep going". Attempt 3's wording is unchanged. Attempt 3's unknown-keys numbers above are discarded, and the full ten cases rerun with the same wording, plus the trust-2 sensitivity check.

## 2026-10-10, attempt 3 rerun on the narrowed suite

Same wording, 10 cases, 5 runs each, 50 runs, $6.92. **Bar met.** Caught 30/30, planted clean 30/30, control clean 20/20, routed 50/50, repro 15/15. Every case is 5/5 on every measure it has. The trust-2 recheck on the narrowed suite caught 0 of 12 planted claims (2 runs per planted case, 6 cases), so the graders still fail a bad wording. Cost $2.33.

The confirmation rerun of the final wording follows.
