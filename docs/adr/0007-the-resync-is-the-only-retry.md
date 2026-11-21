# 7. The resync is the only retry

Date: 2026-11-02

## Status

Accepted

## Context

- `dispatch run` rereads every source every `resync` (5 minutes by default) and acts on what it finds. Anything that failed and still needs doing is wanted again at the next resync.
- The owner, turning down a retry loop: "we should not introduce retries, there is no reason too when we have the 5 minute resync cycle which acts ass a naturla place to retry" (spelling as written).
- A failed workspace rename is tried again at the next resync, not inside the loop.
- A merge GitHub refused because the base branch moved is retried by a rule that reads the ledger, at most 5 more times with a pause of a minute between tries (`MERGE_RETRIES` and `MERGE_RETRY_PAUSE` in the shipped rules). It takes the next cycle that sees the pause over, so nothing sleeps and tries again in place.
- Trying a treehouse lease and a `git switch` 3 times in place, for failures whose cause was never found, hid the failure. Those launch failures also counted toward `launch_failure_limit`, so a passing flake could send a ticket to Needs Human.

## Decision

- Dispatch adds no retry loop of its own for a failed action. Something that failed is tried again at the next resync, and no code sleeps and tries again in place.
- A launch tries a treehouse lease and a `git switch` once.
- A failed lease or switch doesn't count toward `launch_failure_limit`, since the next resync launches again. `dispatch run` prints it as an error line at every resync it fails, so someone watching sees the problem without the ticket stopping.
- A rule that wants an action again after a pause, read from the ledger on a later cycle, is not a loop of dispatch's own (the merge retry above).

## Consequences

- A flaky lease or checkout costs one resync (5 minutes by default) instead of seconds.
- A ticket whose lease or switch fails for good is relaunched at every resync and never reaches Needs Human on its own. The error line in `dispatch run`'s output is the signal.
- A launch still counts a failed pool read, a lease returned without a path, an interrupted wait for another launch while dispatch is still running, a branch or worker row it cannot write, and a recorded branch that is gone (`branch_missing`). A full pool (`pool_exhausted`) still never counts.
- A launch that fails after `dispatch run` was asked to stop (SIGINT or SIGTERM) never counts, whatever its error. Ctrl-C kills the launch's treehouse child with dispatch, so the failure is the stop's doing. The receipt keeps the `failed` outcome, so the next run launches again, and its detail starts with `interrupted:`, which `dispatch explain` shows as `interrupted`.
- A proposal for a retry loop, a backoff or an in-place second try needs to supersede this ADR first.
