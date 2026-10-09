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
