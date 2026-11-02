# 10. An in-process store is tested real, not faked

Date: 2026-11-01

## Status

Accepted

## Context

- ADR 0001 fakes external tools at their domain interfaces, and every external tool has a fake in `internal/tooltest`.
- The local ticket store is a bbolt file inside dispatch's own process. Its correctness is the invariants it checks inside each write transaction: unique ids, existing local links, no parent cycles, one status per job.
- A fake store would re-implement every one of those checks, with nothing to show it matches the real store.

## Decision

- ADR 0001 covers tools outside dispatch's process. The local ticket store has no port and no fake.
- Tests run the real store on a temp file, entered through `dispatch ticket`, the rules and the executor.

## Consequences

- Tests that touch local tickets write a small file per test, which bbolt opens in milliseconds.
- A future in-process store follows this ADR, not ADR 0001.
