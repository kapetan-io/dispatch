# 13. Dispatch has one delivery path for nudges, with no fallback to typing

Date: 2026-11-07

## Status

Accepted

## Context

- Typed nudges answered questions that opened between the status read and Enter, carried or cleared a user's draft, and lost the start of long text under Rex.
- The user's reasoning: dispatch has no introspection into the harness and cannot know whether it delivers by socket or by typing, so delivery is the harness's job.

## Decision

We will send every nudge through the worker's harness (`Deliver`) and never type one into the terminal. A nudge the harness can't deliver fails with nothing sent. A rule's nudge is retried at the next cycle, and a failed `dispatch tell` is reported to its caller. The kickoff and startup dialog answers stay typed.

## Consequences

- A host or harness problem shows as a failed nudge receipt, not a typed nudge that may land wrong.
- `Herdr.Prompt` and its refusal machinery leave the herdr port.
- A harness with no message channel can't be nudged until its adapter implements `Deliver`.
