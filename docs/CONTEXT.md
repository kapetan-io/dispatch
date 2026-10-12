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

## Relationships

- Each port has one **real adapter** and one **tool fake**, and every **contract scenario** runs against both.
- The **drift check** runs the scenarios against the **real adapter** only.
