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

We will fake each external tool behind a Go interface named for its domain role (`herdr.Herdr`, `treehouse.Pool`, `pullrequest.Repository`, `notify.Alerts`). Each interface has a real adapter that runs the installed tool and an in-memory fake that refuses what the real tool refuses. One set of contract scenarios runs against the fake in every test run, and against the real adapter under the `drift` build tag on a maintainer's machine.

## Consequences

- Root tests run in-process, with no shell or jq.
- A fake earns trust only by passing the same scenarios as the real adapter against the installed tool.
- Root tests no longer exercise command lines or JSON parsing. Only adapter tests and the drift check cover them, and CI never runs the drift check.
- Each tool's wire format exists once, in its real adapter.

## Note, 2026-10-22: programs dispatch runs outside a port

Some programs dispatch runs do not follow the Decision in full.

- git has no port and no fake. Test cases run real git on the trees under test, and a fake would force edits to those tests. A port with one adapter is an interface nothing swaps.
- `dispatch claude-shim` replaces itself with the real claude found on PATH. It has no port and no fake.
- The Linear API key lookup runs `security find-generic-password` for the macOS keychain and `sh -c <api_key_command>`. Neither has a port or a fake. Test cases set the key through Options.LinearKey or LINEAR_API_KEY, and the config tests run a real api_key_command.
- `dispatch rules check` runs `tsc`, with no port or fake.
- gh has a port (`pullrequest.Repository`) and a fake (`pullrequesttest`), but no contract scenarios run against both. Its drift tests check the flags dispatch passes against the installed gh's --help.
- Alerts have a port (`notify.Alerts`), two real adapters, osascript on macOS and `herdr notification show` elsewhere, and a fake (`notifytest.Alerts`). No contract scenarios run against them, and there is no drift test.

`sh -c <token_command>` is not listed: it runs inside the httpservice port's real adapter.

Test tooling is outside the Decision. It runs git and other programs directly.

## Note, 2026-10-30: the drift check touches installed tools only

The drift check runs against the herdr, treehouse, gh and Claude installed on the maintainer's machine and never against a GitHub or Linear account, so a failure means a fake or an adapter drifted from an installed tool, not that an account's settings changed. Linear has a port (`linear.Workspace`), a fake (`lineartest`) and contract scenarios, but the scenarios run against the fake only; `internal/linear/client_test.go` checks the real client's GraphQL against an HTTP test server. The herdr drift test names the dispatch its workers run through `herdr.CLI.WithDispatchBin`, because the test binary that calls the adapter in-process is not a dispatch.
