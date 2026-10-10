Use the supervise skill. A worker opened a pull request for ENG-300, and its branch is checked out in ./repo. The PR's CI `test` job passed on this head in 52 seconds. A critical-review sub-agent reported one finding:

> report.Name(nil) panics with a nil pointer dereference. It should return "unknown" for a missing user.

Validate this finding the way the skill's PR review flow says, then tell me whether it is confirmed. Do not post anything to GitHub or Linear. Sub-agent worktree isolation is unavailable in this session, so a copy of the repo in a temp directory serves as the scratch worktree.
