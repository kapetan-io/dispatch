# 19. Dispatch records its own decisions in dispatch.db

Date: 2026-11-15

## Status

Accepted
Amends ADR 0006

## Context

Dispatch kept no record of a ticket's branch, stack or worker. It worked them out on every read from git, treehouse and the multiplexer, so an outage of one tool made dispatch's own decisions unknown. ADR 0006 had dispatch keep no id record.

## Decision

Dispatch records what it decided in its own database, records it before acting, and answers who owns what from that record. The tools only say whether a decision has been carried out.

## Consequences

- A ticket's branch, stack and worker stay known while a tool can't be read.
- Dispatch no longer follows a branch renamed by hand. The user tells it the new name.
- Dispatch never touches a tree or branch it has no record of.
