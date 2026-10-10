# Commit message suite log

## 2026-10-09, control

always-body, 5 runs per case, 30 runs, $4.10. It failed `body` in 14 of 15 runs on typo-fix, rename and dep-bump, so the graders can fail a bad wording.

## 2026-10-09, baseline

current, 5 runs per case, 30 runs, $4.30. **Bar not met.**

| Case | subject | body | cause |
|---|---|---|---|
| typo-fix | 5/5 | 5/5 | |
| rename | 5/5 | 3/5 | |
| bugfix-cause | 5/5 | 5/5 | 2/5 |
| revert (held out) | 5/5 | 5/5 | 4/5 |
| migration | 5/5 | 5/5 | 3/5 |
| dep-bump (held out) | 5/5 | 5/5 | |

Writers describe the diff in the body (what changed) and skip why. Rename gets a body in 2 of 5 runs.

## 2026-10-09, attempt 1

Idea: say the body gives the reason for the change and never restates the diff. `arms/attempt-1.md`, 5 runs per case, 30 runs, $4.20. **Bar not met.** `cause` rose on bugfix-cause (4/5) and migration (4/5), but rename got a body in 4 of 5 runs, because writers read "give the reason" as a need to invent one.

| Case | subject | body | cause |
|---|---|---|---|
| typo-fix | 5/5 | 5/5 | |
| rename | 5/5 | **1/5** | |
| bugfix-cause | 5/5 | 5/5 | 4/5 |
| revert (held out) | 5/5 | 5/5 | 5/5 |
| migration | 5/5 | 5/5 | 4/5 |
| dep-bump (held out) | 5/5 | 5/5 | |

Kept as the best arm so far.
