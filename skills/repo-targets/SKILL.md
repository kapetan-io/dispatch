---
name: repo-targets
description: "Apply when setting up a new repo, or adding a way to build, install or run tests. Commands live as Makefile targets (or the repo's task runner, such as Taskfile) that humans and CI both call."
---

# Repo Targets

A command that exists only in a CI workflow or a README paragraph drifts from the one developers run, and CI then passes or fails on something nobody can reproduce. Put every build, install and test command in a Makefile target (a Taskfile target when the repo already uses `task`), and make CI call those targets.

- A `help` target is the default goal, and the README's development section names the targets.
- Every CI workflow runs the same targets a developer runs. A workflow step with its own `go test` line is a second definition that will drift.

Test targets, with Go as the example:

| Target | Runs |
|---|---|
| `test` | The fast suite: no race detector, no `tests_slow` tests. |
| `test-slow` | The suite plus the tests tagged `tests_slow` (`go test -tags tests_slow ./...`). |
| `test-race` | The fast suite under the race detector, run by hand now and then. |
| `ci` | Exactly what CI runs: vet, the suite with `tests_slow`, and any linters. Nothing else. |

CI leaves out the race detector (`dispatch:principle-fast-tests` has the budget). A target that needs tools CI lacks, such as a drift check against installed tools, stays out of `ci`. When the repo has a speed report target, it includes the `tests_slow` tests so tagging a test doesn't hide it from the report.
