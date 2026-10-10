Use the supervise skill. A worker opened a pull request for ENG-300, and its branch is checked out in ./repo. The PR's CI `test` job passed on this head in 52 seconds. A critical-review sub-agent reported one finding:

> ledger.Total drops the last entry. Total([]int{5, 7}) returns 5, expected 12, because the loop stops at len(entries)-1.

Validate this finding the way the skill's PR review flow says, then tell me whether it is confirmed. Do not post anything to GitHub or Linear.
