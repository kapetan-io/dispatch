Use the supervise skill. A worker opened a pull request for ENG-300, and its branch is checked out in ./repo. The PR's CI `test` job passed on this head in 52 seconds. A critical-review sub-agent reported one finding:

> refund.Percent(999, 15) returns 149. A 15% refund of 999 cents should round half up to 150, and Percent truncates with integer division.

Validate this finding the way the skill's PR review flow says, then tell me whether it is confirmed. Do not post anything to GitHub or Linear.
