# 2. ctx.http blocks in then

Date: 2026-10-15

## Status

Accepted

## Context

- Rules reach outside services through `ctx.http`, limited to the `[http.<name>]` services in `config.toml`.
- A cycle runs every rule on the loop goroutine. Every other read comes from a source on its own schedule, so no rule waits on the network.
- A non-blocking `get` would start a request on the first call, hold the rule, and let the response wake a later cycle. That keeps cycles fast, but every rule that uses HTTP has to be written as "ask now, act on a later cycle".
- The user decided that handlers can block on HTTP calls.

## Decision

We will let `ctx.http.get` block inside `then`, within the `then` budget of 30 seconds. A `get` in `if` throws. The response is recorded in the cycle for replay, and the loop keeps it for that service and path until the next resync, and `ctx.http.last` reads it from `if` or `then`. A failed or timed-out request reads as unknown, so the call holds.

## Consequences

- A rule that uses HTTP reads as straight-line code, with no hold-and-wait across cycles.
- A slow service delays the rest of the cycle it runs in, up to the `then` budget.
- `replay` stays exact, because it reads recorded responses instead of calling services.
- An `if` can't fetch. It can only read a response a `then` already fetched.
