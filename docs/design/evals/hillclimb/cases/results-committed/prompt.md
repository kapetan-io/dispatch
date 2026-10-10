Run one hillclimb attempt on skills/commit-message/SKILL.md against the suite in evals/commit-message, using the log's next attempt number.

The `claude` command isn't available in this session. `./run-suite.sh <arm file>` stands in for the suite run and prints the result JSON to stdout. Commit your work in the git repo here. Use `/opt/homebrew/bin/git`, because `git` on PATH is a macOS shim that fails in this sandbox. When the attempt is logged and committed, run `git log --stat > commits.txt` and stop.
