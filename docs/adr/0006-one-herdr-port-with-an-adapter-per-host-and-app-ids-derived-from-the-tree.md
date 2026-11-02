# 6. One herdr port with an adapter per host, and app ids derived from the tree

Date: 2026-11-01

## Status

Accepted

## Context

- Dispatch drives workers through one port, `herdr.Herdr`, with one real adapter, `herdr.CLI`, for the herdr TUI. The user now also works in the Herdr app, whose terminals herdr-core runs and `hc` drives.
- Everything above the port passes the workspace, tab and pane ids it returns straight back to the port, or compares them for equality.
- A flatter worker-window port was weighed. It rewrote the fake and 17 root test files for no visible gain.
- herdr-core keeps no record that ties a space to a tree, and hc refuses a second live terminal with the same name.
- ADR 0001 says each interface has "a real adapter".

## Decision

We will keep `herdr.Herdr` and add `herdr.Core` as a second real adapter behind it, chosen by `[plugins.herdr] host`. Core derives the space id, the tab id and the terminal name from the resolved tree by hashing, and writes `dispatch.tree` and `dispatch.session` labels on each worker terminal. This amends ADR 0001: an interface has one or more real adapters, and each passes the same contract scenarios.

## Consequences

- The loop, executor and rules don't change, and can't tell which host runs a worker.
- The derived terminal name makes one session per tree atomic in herdr-core, with no id record in dispatch.
- The ids look random in `hc get` output. They are the lock.
- The label format and the derived ids live in the user's herdr-core, so changing either needs a migration of running and finished worker terminals.
- Contract scenarios split into host-neutral, TUI-only and Core-only sets.
