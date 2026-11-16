# 18. One dispatch runs on a machine and serves every project

Date: 2026-11-15

## Status

Accepted

## Context

Dispatch is a hub for events and for managing agents. Nothing said whether a second project gets its own dispatch.

## Decision

One `dispatch run` runs per machine and user and serves every project. A new project is added to that dispatch, never run as a second one.

## Consequences

- Dispatch's records never have to say which dispatch wrote them.
- A project cannot be isolated by running its own dispatch.
