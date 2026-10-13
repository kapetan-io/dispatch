# Context

Terms dispatch's tests and docs use for the tools it drives. herdr, treehouse and kickoff keep their definitions from `design/decision-log.md`.

**Tool fake**:
An in-process Go state model of herdr, treehouse, gh or osascript that refuses what the real tool refuses.
_Avoid_: shell fake, mock, stub

**Real adapter**:
The production implementation of a tool's port, which invokes the installed tool.
_Avoid_: CLI wrapper, client

**Contract scenario**:
One situation run against both a tool fake and the real adapter, which must give the same outcome.
_Avoid_: shared test, conformance test

**Drift check**:
The contract scenarios run under `-tags drift` against the installed herdr and treehouse, in a throwaway `dispatch-drift-*` session and pool.
_Avoid_: integration test, live test

**Stand-in claude**:
A Go program the drift check starts in place of Claude, which shows the startup prompts and reports its state to herdr.
_Avoid_: fake claude (that name belongs to the `scripts/shims/claude` test)

**Repo settings**:
A `[repos.<label>]` table in `config.toml`, holding a repo's `path` and every per-repo setting. A table with only `path` behaves as dispatch did before per-repo settings.
_Avoid_: repo config, repo entry

**Listed author**:
A GitHub login in a repo's `ignore_comment_authors`. Dispatch and the worker act as if nothing it posts exists.
_Avoid_: bot, ignored bot (some bots, such as CodeRabbit, are not listed and still count)

**Optional check**:
A check GitHub does not mark required for merging the PR. It never counts in a repo with `ignore_optional_checks = true`.
_Avoid_: non-blocking check, flaky check

**PR phase**:
Draft or open, read from GitHub's `isDraft` on every tick. Each phase has its own ready notification.
_Avoid_: PR state (GitHub's `state` is OPEN for a draft too)

**Review question**:
A review or comment that asks rather than claims. The worker answers a question about what the code does, and sends a question needing a product decision to the user.
_Avoid_: review claim (a claim asserts something to check)

## Relationships

- Each port has one **real adapter** and one **tool fake**, and every **contract scenario** runs against both.
- The **drift check** runs the scenarios against the **real adapter** only.
