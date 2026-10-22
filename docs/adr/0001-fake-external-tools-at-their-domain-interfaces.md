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

## Note, 2026-10-22: programs dispatch runs outside a port

Some programs dispatch runs do not follow the Decision in full. The file and line cites below are as of this note's date; later moves are appended below.

- git has no port and no fake. Test cases run real git on the trees under test, such as staging a file mid-close (teardown_test.go:795) and reading a branch after a launch (launch_test.go:1154). A fake would force edits to those tests, and a port with one adapter is an interface nothing swaps. dispatch runs git in these places:
  - the tree checks before a return: Unfinished, Inspect, Untracked and RemoveUntracked (internal/treehouse/treehouse.go:157-262);
  - the launcher: the repo root, the git common dir, the ticket's branch and the default branch (internal/launcher/run.go:171,275,290-295,393-398,502,540-588);
  - the hooks: `workerTree` (worker_tree.go:24,28,34), run by `dispatch record-stop` (record_stop.go:38) and `dispatch wake` (wake.go:25), runs git for the worktree root through `worktreeRoot` (record_stop.go:59-60) and for the clone root through `cloneRoot` and `repoAt` (worker_tree.go:28,34); `dispatch ticket-hint` runs git for the clone root (ticket_hint.go:51,69-70).
- `dispatch claude-shim` replaces itself with the real claude found on PATH (claude_shim.go:36,100-120). It has no port and no fake.
- The Linear API key lookup runs `security find-generic-password` for the macOS keychain (dispatch.go:308) and `sh -c <api_key_command>` (dispatch.go:326). Neither has a port or a fake; test cases set the key through Options.LinearKey or LINEAR_API_KEY, and config_test.go runs a real api_key_command (such as config_test.go:663).
- `dispatch rules check` runs `tsc` (rules.go:268), with no port or fake.
- gh has a port (pullrequest.Repository) and a fake (pullrequesttest), but no contract scenarios run against both. Its drift tests check the flags dispatch passes against the installed gh's --help (internal/pullrequest/drift_test.go:22) and send two read-only queries to GitHub through gh (drift_test.go:48,59).
- Alerts have a port (delivery.Alerts), two real adapters, osascript on macOS (NotificationCenter, internal/delivery/alert.go:20-50) and `herdr notification show` elsewhere (HerdrNotifications, alert.go:53-80), chosen by NewAlerts (alert.go:82-88), and a fake (deliverytest.Alerts). No contract scenarios run against them, and there is no drift test.

`sh -c <token_command>` (internal/httpservice/real.go:162) is not listed: it runs inside the httpservice port's real adapter.

Test tooling is outside the Decision. It runs git too: claimeval packs its fixtures with git archive (internal/claimeval/claimeval.go:237), herdrtest makes the drift check's checkout (internal/herdrtest/checkout.go:22), and committimes reads the tracked files and their last commit times for CI (internal/committimes/committimes.go:59,130). claimeval also runs tar and go (claimeval.go:241,247), commenteval runs claude (internal/commenteval/commenteval.go:133), herdrtest runs herdr and go (internal/herdrtest/session.go:46,181), and slowtests runs go test for the speed report (internal/slowtests/slowtests.go:124).

2026-10-22: `delivery.Alerts` is now `notify.Alerts`, in internal/notify/alert.go, and `deliverytest` is `notifytest`.
