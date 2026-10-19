---
name: surface-testing
description: Write surface tests that exercise the system through its surface, the outermost layer consumers use (HTTP endpoints, CLI entry points, exported functions). Never test internal functions directly. Expose observability APIs when async behavior isn't visible at the surface.
---

# Surface Testing

Worked Go and Kotlin examples are in [references/examples.md](references/examples.md). They are one way to apply these rules. Read them only if they help, and use your own approach where it is better.

A test should change only when the system's requirements change. Refactors, new features and bug fixes must leave existing tests green, and only a behavior change should break one. Tests that enter through the surface verify behavior and survive a rewrite. Tests that reach into internals verify implementation and die with it.

> **The MIME parser.** A parser gathered 3,000 tests against its public `Parse()` over years of real-world edge cases. A rewrite from recursive descent to a stateful non-recursive design was validated by those same tests, which proved correctness and a speedup. Tests of the recursive internals would have been thrown away with the recursion.

**Fidelity matters more than speed.** A test is worth the share of the production path it runs: real TCP, real HTTP client, real serialization, real migrations. Every substitution below trades some of that away, so make the trade only where you must.

## Core rule

Every test interacts with the system the way its end users would. Never call internal or private functions from a test. If you can't observe a behavior through the surface, fix the design, not the test strategy. Models reach for internals by default, because the function is right there.

## Enter at the highest surface

Every test pins the surface it enters through, so a package test pins the package's API. If a refactor merges or deletes the package, its tests go with it while the product tests stay green. Reach each behavior from the outermost surface first (the CLI or service a user runs). Write a package test only when one of these holds, and a one-line comment on the test names which:

- The scenario is unreachable from above, such as concurrent callers, a crash between two steps, or a fault the product can't inject.
- The package is combinatorial, like a parser or codec, and driving every input through the product is slow or hides the case.
- The package ships to other consumers.
- The package wraps an external tool, or is a fake of one, so its adapter and contract tests (see Ports and adapters) run against the real thing.

A package test that meets none of these gets the deletion check. Imagine deleting the package and rewriting its callers. If the test would die while the product tests stay green, it was pinning structure, so delete it. You may delete tests you wrote yourself on this ticket. Leave any other existing test in place unless the ticket's intent is to delete it, and name the ones that repeat a product test in the PR instead.

## Finding the surface

- **HTTP/gRPC service**: boot the application and send real requests. Don't instantiate handlers or services.
- **CLI**: call `Run(ctx, args, opts)` with arguments and capture output. `main()` stays a thin wrapper around it, so `main` and tests enter the same way.
- **Library**: call the exported functions as a consumer would.
- **Internal library boundary**: a package that could be extracted and published without changing its interface. In design, implementation and review, recognize or introduce these boundaries at domain edges. It is a surface of last resort, tested only under the conditions in "Enter at the highest surface" above. Test it through its exported API only, from `package xxx_test`. Consumers use the real package, so there is nothing to substitute unless it wraps an external tool.

```go
func main() {
    os.Exit(cmd.Run(context.Background(), os.Args[1:], cmd.Options{Stdout: os.Stdout, Stderr: os.Stderr}))
}
```

If a test won't compile in `package xxx_test`, it is reaching for internals. Trace back to the public entry point that exercises the code. If none does, the code is dead (delete it) or the design hides behavior that matters (fix the design, not the test).

When behavior spans services, enter through the one a real user would call and assert the downstream effect.

## When the "then" is empty

The request succeeded but the work is asynchronous. That is a design gap, so work down this list.

1. **Assert a downstream effect** through the public interface, polling with `require.Eventually`. An accepted order shows up in `/billing/pending`.
2. **Expose observability** when the effect is internal and matters for correctness (periodic flush, compaction, cache eviction). A `Stats()` method returning counters is not test-only code, because an operator wants the same numbers. The test polls `Stats()` until the counter moves.
3. **Drop the test** when the behavior is internal and doesn't matter.

## Substituting external dependencies

Fake only what sits outside the deployment unit: object stores, brokers, payment gateways, other services in the repo, command-line tools. Use the real dependency whenever you can run it. Consider a fake when running it makes the suite slow or flaky, or it isn't always reachable. Anything inside the surface runs for real, including internal services and domain logic. An assertion like `verify { repository.save(any()) }` tests implementation, so don't write one. A fake is a working lightweight implementation. A mock checks how code is called, which leaks implementation into the test and makes it brittle, so prefer the real dependency, then a fake, then a stub, and a mock last ([Software Engineering at Google, ch. 13](https://abseil.io/resources/swe-book/html/ch13.html)).

A fake encodes someone's understanding of the real tool, and it drifts. A fake `treehouse return` that ignored the lease holder passed tests the real tool failed. For a service with a wire protocol, prefer substitutes in this order. Each of the first three runs the real protocol, and the earlier ones are faster.

1. **The real service, embedded.** When the dependency is in your language and ships an in-process constructor, boot it from the test and substitute only its own externals. A service other services consume should ship such a constructor.
2. **A published in-process fake** that speaks the real protocol, such as `gofakes3` for S3. Your client, codec and connection handling all run.
3. **A published fake in a container** (testcontainers). Same protocol over a real network hop, slower to start.
4. **A fake you write**, behind a port (next section). For an external command-line tool there is usually no published substitute, so this is the default. It has to refuse what the real tool refuses. A spy that accepts every call and records it proves your handler calls your fake, nothing more. Give it the real tool's state and rules, and make it return the errors the real tool returns.

> **The DNS server.** A team ran a real Go DNS server in tests and the service made actual lookups against it. A new Go version changed resolver behavior and the suite caught it before deploy. A mocked resolver would have shipped the bug.

### Ports and adapters

When tests use a dependency only as passive state or somewhere to send work, put it behind a port, an interface your code owns and names for the dependency's role (`OrderRepository` for a collection of domain objects, `PaymentGateway` for any other external system), not for the tool behind it (`PostgresClient`). The port has two adapters. The real adapter talks to the dependency, and a fake stands in for it where running the dependency makes tests slow or flaky. The port is the edge of the deployment unit, so substituting the fake breaks no rule above.

A fake needs contract tests, one set of scenarios run against both the fake and the real dependency. Never delete or skip them to get a build green. When the real dependency's behavior changes, update the scenarios and the fake together, and never edit a scenario only so the fake passes. Gating the real side behind a build tag or flag is fine. Removing it, or letting it pass without running, is not. The real-dependency run is what catches drift, so run it as often as you can and state in the PR where it runs. Faking at this level has a cost. Root tests no longer exercise command lines or JSON parsing, so adapter tests must cover them.

- **Contract tests as their own suite** when the fake only speeds up tests. Feature tests use the fake and trust the contract tests to catch drift.
- **The whole suite against both** when the fake is also a deployment target (embedded, end-to-end or local-development mode), because then it is production code.

### Databases

A database fake almost always diverges where it matters: transaction and rollback semantics, null ordering, unique-constraint timing, row-level locks. **Default to the real database** (testcontainers or a local instance) and run your real migrations, never inline schema creation, so the migrations are part of what the test validates. A repository fake for passive-state tests is a deliberate opt-out that needs contract tests.

## Controlling time

Inject a clock so retries, expiries and scheduled flushes are testable without sleeping. For Go, `kapetan-io/tackle/clock` gives a per-test provider you freeze, inject through the options struct and `Advance`. A clock is a global dependency, so injecting one doesn't break the rule against substituting internals. On the JVM, inject a `java.time.Clock` through DI, or use `TestCoroutineScheduler` for coroutine timing.

**Inject consistently, don't capture.** One `time.Now()` call, or a provider captured at construction, escapes the freeze. The test then flakes or quietly relies on wall-clock time.

## Fault injection

Retries, circuit breakers and crash recovery only show up under failure. Inject the fault at the dependency boundary so the system sees a real error, timeout or disconnect. Kill or pause the container, put toxiproxy in front of it, or give a fake a "next call fails with X" hook. Then assert recovery through the surface. If recovery isn't observable, expose it as above. Never mock an internal function to return an error, since that tests whether your error handling compiles.

## Tests derived from the design's correctness claims

Surface testing says how to test. A design's correctness section says what to test.

- **Invariant**: attempt to violate it through the surface and assert the operation is rejected and the state unchanged. A transfer that would take a balance negative returns 422 and the balance stays 100. One test per invariant per operation that touches it.
- **Behavioral constraint** ("never silently dropped"): inject a fault at the boundary and assert the system reports it.
- **Boundary precondition**: break it and assert a clean rejection, such as 401 without credentials.

These supplement scenario tests and don't replace them.

## Go

Tests live in `package xxx_test`, which makes unexported identifiers unreachable. Inject dependencies through an options struct and give servers `Start()` and `Shutdown()` so a test controls the lifecycle.

## Kotlin/Micronaut

For a Micronaut application, tests use `@MicronautTest` so the full context boots with real injection, and the request goes through `HttpClient`. `@MockBean` and `@Replaces` substitute external boundaries only. `@MockBean(UserService::class)` skips the real `UserService` and ties the test to how it is called, not to what the system does. Use Awaitility's `await().atMost()` where Go uses `require.Eventually`. For a Kotlin library, call the exported functions directly.
