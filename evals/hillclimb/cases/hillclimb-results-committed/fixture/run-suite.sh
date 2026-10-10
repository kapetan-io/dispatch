#!/bin/bash
# Stand-in for `claude plugin eval`: prints a result JSON for the arm named in $1.
arm="${1:?usage: run-suite.sh <arm file>}"
cat <<J
{"arm":"$arm","runs_per_case":5,"cases":{"typo-fix":{"subject":5,"body":5,"cause":null},"rename":{"subject":5,"body":4,"cause":null},"bugfix-cause":{"subject":5,"body":5,"cause":4},"revert":{"subject":5,"body":5,"cause":5},"migration":{"subject":5,"body":5,"cause":4},"dep-bump":{"subject":5,"body":5,"cause":null}},"cost_usd":4.2}
J
