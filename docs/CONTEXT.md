# Context

Terms dispatch's tests and docs use for the tools it drives. herdr, treehouse and kickoff keep their definitions from `design/decision-log.md`.

**Tool fake**:
An in-process Go state model of herdr, treehouse, gh, Linear or osascript that refuses what the real tool refuses.
_Avoid_: shell fake, mock, stub

**Port**:
An interface dispatch owns and names for a tool's role (`treehouse.Pool`, `herdr.Herdr`), implemented by a real adapter and a tool fake.
_Avoid_: domain interface (ADR 0001 and older designs use it for the same thing), client interface

**Real adapter**:
The production implementation of a tool's port, which invokes the installed tool.
_Avoid_: CLI wrapper, client

**Contract scenario**:
One situation run against both a tool fake and the real adapter, which must give the same outcome.
_Avoid_: shared test, conformance test

**Drift check**:
The contract scenarios run under `-tags drift` against the installed herdr and treehouse, in a throwaway `dispatch-drift-*` session and pool. Linear is the exception. It has no throwaway workspace, so its drift check runs only the unseeded scenarios, reading the workspace `LINEAR_API_KEY` belongs to and aiming moves and comments at a ticket that does not exist.
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

**Approval check**:
A check that passes only after a human approves the PR, such as "Mergify Merge Protections". A repo names them in `approval_checks`, and they count for nothing, so a draft waiting for review is still ready.
_Avoid_: gate, blocking check

**PR phase**:
Draft or open, read from GitHub's `isDraft` on every resync. Each phase has its own ready notification.
_Avoid_: PR state (GitHub's `state` is OPEN for a draft too)

**Review question**:
A review or comment that asks rather than claims. The worker answers a question about what the code does, and sends a question needing a product decision to the user.
_Avoid_: review claim (a claim asserts something to check)

## Relationships

- Each port has one **real adapter** and one **tool fake**, and every **contract scenario** runs against both.
- The **drift check** runs the scenarios against the **real adapter** only.

**World**:
What the sources say right now, held in memory and updated by each read. Every value in it is a fact.
_Avoid_: state, store, snapshot (a snapshot is the world written at the head of a history segment)

**Fact**:
One value in the world, with the time it was first seen at that value (its `since`), or unknown.
_Avoid_: field, property

**Unknown**:
A fact whose read failed or came back cut short. A `whenever` rule that reads one holds for that record, and an `on` rule drops its event, so a failed read never counts as "no".
_Avoid_: missing, empty, null

**Cycle**:
One run of every rule over the world and the ledger, after an event. It gives what dispatch wants now, what waits for a slot, and what is held and why. `dispatch rules check` runs one against the world as it is now.
_Avoid_: plan, pass

**Once key**:
The value a rule gives each action, naming what must change before the action runs again, such as a comment time or a PR head. The ledger runs an action once per key.
_Avoid_: dedupe key, idempotency key

**Event**:
Anything telling dispatch something changed: a source read that differs from what it knew, an action's result, the resync, `dispatch tick`. Each event runs a cycle.
_Avoid_: notification, trigger

**Resync**:
The 5-minute event that re-reads every source (Linear, GitHub, herdr, treehouse and Stop records) and runs a cycle. It is the only clock rules wait on, so a wait ends up to 5 minutes after its deadline.
_Avoid_: tick (`dispatch tick` forces one early), poll interval

**Ledger**:
What dispatch did, one receipt per action, keyed by the action's once key. A rule asks it whether an action already happened.
_Avoid_: log, history (history is the segment files the ledger is written to)

**Receipt**:
The ledger's record of one action: key, rule, outcome (done, refused, failed; absent while attempted), detail, times.
_Avoid_: result, audit entry

**Executor**:
Starts each action a cycle wants whose once key the ledger doesn't cover, off the loop, and writes its receipt.
_Avoid_: runner, worker (a worker is a Claude session)

**Command**:
A message to a worker that starts with `/name`, as Claude took it, recorded with its arguments (cut at 1KB), the time and whether a person or dispatch sent it.
_Avoid_: prompt, message (both include text that isn't a command), skill (a skill the model loads by itself is not a command)

**Turn**:
One reply a worker's Claude session finished, ending in a Stop, with whether a background task was still running.
_Avoid_: stop (a Stop record keeps only the latest turn)

**Session log**:
The file of one worker session's commands and turns, appended by the plugin's hooks and read by the world.
_Avoid_: transcript (Claude's own file), history (dispatch's segments)

**Send record**:
A hash of a slash command dispatch typed into a tree, written before typing and claimed by the command it matches.
_Avoid_: ledger entry, receipt (both are in history)

**Workspace emoji**:
The first word of a worker's herdr workspace label. It says who the ticket is waiting on: the worker (🟢), CI (🟡), you (👀, 💤 or 🔴), or nobody (⏸️). `worker-states.md` lists every state, its emoji, and what dispatch does in it.
_Avoid_: status icon, status
