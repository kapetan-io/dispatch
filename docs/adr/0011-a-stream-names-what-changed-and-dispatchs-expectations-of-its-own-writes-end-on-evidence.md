# 11. A stream names what changed, and dispatch's expectations of its own writes end on evidence

Date: 2026-11-05

## Status

Accepted

## Context

- Dispatch learns about the multiplexer by reading it. Rex can also report, as it happens, which pane changed (`rex events --all --json`). ENG-626 had assumed dispatch would take no multiplexer events at all ("No hc events"); this decision replaces that assumption.
- A Rex line for a child record says nothing about the worker's combined status, so a line cannot be applied as a fact. All three design sketches for ENG-672 reached "the event names a pane, dispatch reads that pane" on their own.
- Dispatch holds two memories of its own writes and trusts them over what the multiplexer says. The nudge mark (`World.Nudged`) shows a nudged worker as working. The rename memory (`renaming`, labels.go) treats a sent rename as done "whether or not it landed". Both ended only at the resync. With reads every 5 minutes that cost little. With a stream that brings evidence in seconds, a memory that waits for the resync throws that speed away: a nudged worker that worked and went idle stayed hidden until the resync.

## Decision

- A multiplexer's stream names the pane that may have changed and carries no fact. Dispatch reads that pane like any other read, in the same one-read-at-a-time order, so an event that arrives during a read waits for it. No rule can tell a fact from a stream read apart from one from a full read.
- A memory dispatch keeps of its own write (a nudge, a rename) is an expectation, not a fact. It ends when a read shows the write's effect, and the resync is the backstop. A timer alone never ends it. A read that does not yet show the effect keeps it.

## Consequences

- A stream needs no new history line kind, and replay is unchanged, because every fact still enters through a read.
- Each stream change costs a read of one pane instead of nothing.
- The nudge mark ends when any read sees the worker working or blocked, under every multiplexer.
- The rename memory still ends only at the resync. It is the one known holdout and moves to this rule the next time `labels.go` changes. Nothing shows a stale label today, so it has no ticket of its own.
- A new memory of a write that ends only on a timer needs to supersede this ADR first.
