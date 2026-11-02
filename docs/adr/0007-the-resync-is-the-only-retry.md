# 7. The resync is the only retry

Date: 2026-11-02

## Status

Accepted

## Context

- `dispatch run` rereads every source every `resync` (5 minutes by default) and acts on what it finds. Anything that failed and still needs doing is wanted again at the next resync.
- Retry loops proposed since have been turned down for that reason. The owner on the ENG-630 review: "we should not introduce retries, there is no reason too when we have the 5 minute resync cycle which acts ass a naturla place to retry" (spelling as written).
- Earlier proposals and what came of them:
  - ENG-630 (#223) first retried a failed workspace rename inside the loop. It now retries only at the next resync (`e01aa31`).
  - ENG-491 (#154) retries a merge GitHub refused because the base branch moved. A rule decides it from the ledger, at most 5 more times with a pause of a minute between tries (`MERGE_RETRIES` and `MERGE_RETRY_PAUSE` in `rules/shared.ts`). It takes the next cycle that sees the pause over, so nothing sleeps and tries again in place.
  - ENG-492 (#155) made a launch try a treehouse lease and a `git switch` 3 times in place, for failures whose cause was never found. This ADR removes those loops, and #155's clearer lease and switch messages stay.
- A retry inside the action hid the failure. Those launch failures also counted toward `launch_failure_limit`, so a passing flake could send a ticket to Needs Human.

## Decision

- Dispatch adds no retry loop of its own for a failed action. Something that failed is tried again at the next resync, and no code sleeps and tries again in place.
- A launch tries a treehouse lease and a `git switch` once.
- A failed lease or switch doesn't count toward `launch_failure_limit`, since the next resync launches again. `dispatch run` prints it as an error line at every resync it fails, so someone watching sees the problem without the ticket stopping.
- A rule that wants an action again after a pause, read from the ledger on a later cycle, is not a loop of dispatch's own (the merge retry above).

## Consequences

- A flaky lease or checkout costs one resync (5 minutes by default) instead of seconds.
- A ticket whose lease or switch fails for good is relaunched at every resync and never reaches Needs Human on its own. The error line in `dispatch run`'s output is the signal.
- A launch still counts a failed pool read, a lease returned without a path, an interrupted wait for another launch and a failed stack-record write. A full pool (`pool_exhausted`) still never counts.
- A proposal for a retry loop, a backoff or an in-place second try needs to supersede this ADR first.
