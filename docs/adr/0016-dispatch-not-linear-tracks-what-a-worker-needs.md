# 16. Dispatch, not Linear, tracks what a worker needs

Date: 2026-11-15

## Status

Accepted

## Context

- The user works with workers in their workspaces and follows dispatch's labels and notifications.
- A status change in Linear for a worker's momentary state is a write nobody reads and a second copy of a fact dispatch already holds.

## Decision

A ticket's status in Linear says where the ticket is in its life and decides whether a worker runs on it. What a running worker is doing, including that it has stopped to wait for the user's answer, is tracked by dispatch alone and is never written to Linear as a status. A worker does not move its own ticket to say it is waiting on the user.

## Consequences

- Linear alone no longer shows that a worker is waiting on the user.
- Needs Human remains a ticket status, set only by dispatch when it escalates a ticket.
