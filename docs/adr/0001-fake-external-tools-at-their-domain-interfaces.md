# 1. Fake external tools at their domain interfaces

Date: 2026-10-12

## Status

Accepted

## Context

- dispatch drives herdr, treehouse, gh and osascript as external programs.
- Tests replaced each with a bash script on PATH that parsed its arguments and printed JSON, often through jq. Each call started a shell, which made up most of the root package's 85-second run.
- Each script was a guess at the real tool. A fake `treehouse return` ignored the lease holder and a fake `gh pr merge` ignored the head commit, so tests passed where the real tools refuse.
- Faking at the process level, with one Go command runner and fakes that parse argv, keeps tests exercising argument building and JSON parsing. It also puts a second copy of every tool's wire format in the fakes.

## Decision

We will fake each external tool behind a Go interface named for its domain role (`herdr.Herdr`, `treehouse.Pool`, `pullrequest.Repository`, `delivery.Alerts`). Each interface has a real adapter that runs the installed tool and an in-memory fake that refuses what the real tool refuses. One set of contract scenarios runs against the fake in every test run, and against the real adapter under the `drift` build tag on a maintainer's machine.

## Consequences

- Root tests run in-process, with no shell or jq.
- A fake earns trust only by passing the same scenarios as the real adapter against the installed tool.
- Root tests no longer exercise command lines or JSON parsing. Only adapter tests and the drift check cover them, and CI never runs the drift check.
- Each tool's wire format exists once, in its real adapter.
