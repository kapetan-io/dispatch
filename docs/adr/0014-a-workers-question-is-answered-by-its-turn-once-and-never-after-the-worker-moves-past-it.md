# 14. A worker's question is answered by its turn, once, and never after the worker moves past it

Date: 2026-11-13

## Status

Accepted

## Context

- An unattended manager answers worker questions through an arena that runs for minutes. Meanwhile the user may answer in the pane, the worker may ask again, and `dispatch run` or the manager may restart.
- A plain `dispatch tell` cannot tell those cases apart, so an answer could land on top of the user's, answer the wrong question, or go out twice.

## Decision

We will name each worker question by the time the turn that asked it ended, from its Stop record. `dispatch tell --turn` sends at most one answer per turn, keyed in the ledger, and refuses an answer to a turn the worker has moved past, judged from dispatch's session log and the harness's own transcript. A plain `dispatch tell` is unchanged.

## Consequences

- The manager skill, the decision comment's `Turn:` line and the ledger's Once key all depend on the turn id.
- A lost hook report delays an answer and never sends one over the user's.
- A harness whose adapter cannot say whether its session took a message refuses every `tell --turn`.
