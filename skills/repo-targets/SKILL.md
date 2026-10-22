---
name: repo-targets
description: "Apply when setting up a new repo, adding a way to build, install or run tests, or editing a CI workflow. Commands live as Makefile targets (or the repo's task runner, such as Taskfile) that humans and CI both call."
---

# Repo Targets

A command that lives only in a CI workflow or README drifts from the one developers run, and CI then passes or fails on something nobody can reproduce. Put every build, install and test command in a Makefile target (a Taskfile target when the repo already uses `task`), and make every CI workflow run the same targets a developer runs. A workflow step with its own test command (such as `go test`) is a second definition that will drift.

`help` is the default goal, and the README's development section names the targets.

Test targets, with Go as the example:

| Target | Runs |
|---|---|
| `test` | The fast suite: no race detector, no `tests_slow` tests. |
| `test-slow` | The suite plus the tests tagged `tests_slow` (`go test -tags tests_slow ./...`). |
| `test-race` | The fast suite under the race detector, run by hand now and then. |
| `ci` | Every check CI makes: vet, the suite with `tests_slow`, and any linters. |

The checks live in the target so a developer can run what CI runs. CI-only setup (cache restore, setting file modification times) stays in the workflow before the `ci` call, because inside the target it would touch a developer's checkout. Targets that need tools CI lacks, such as a drift check against installed tools, stay out of `ci`. CI skips the race detector (budget in `dispatch:principle-fast-tests`). Add these targets before tagging a test `tests_slow`, or nothing runs it. A speed report target (`test-report` in dispatch) includes the `tests_slow` tests so tagging doesn't hide a test from the report.
