# 5. A failed read of a source nothing references keeps its last good read

Date: 2026-10-31

## Status

Accepted

## Context

- When Linear can't be read, every ticket record goes unknown and rules over tickets are held (`FromWorld` in `internal/rules/state.go`). When a PR can't be read, its record goes unknown. Worker, tree, PR and escalation records point at tickets and PRs, so a stale value there would mislead rules about other records.
- Nothing points at an inbox item. The owner chose that a failed inbox read should change nothing: "If there was a failure, there's nothing to change. There's no reason to pause rules."
- A design that held every inbox rule on a failed read was weighed and dropped for that reason.

## Decision

- When a read fails for a source that no other record references, the world keeps the last good read as known facts, and rules run as normal.
- The world records when the last good read happened (`inboxReadAt` on the `dispatch` record, reachable as `ctx.dispatch`), so a rule can judge staleness itself.
- A failed read at startup, with no earlier read, gives no records and an undefined read time.

## Consequences

- During an outage rules act on stale data unless they check the read time. Once keys keep stale data from repeating an action.
- A rule that wants Linear's behavior writes the check itself (`ctx.now - ctx.dispatch.inboxReadAt`).
- A future source that other records reference does not follow this ADR. It follows the unknown pattern, and the next source added must say which it is.
