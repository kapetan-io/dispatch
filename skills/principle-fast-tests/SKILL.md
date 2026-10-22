---
name: principle-fast-tests
description: "Apply when writing, reviewing or speeding up tests. A test runs under 1 second and a service's suite under 5 minutes, and the speed comes from the test's shape, never from lower fidelity."
---

# Fast Tests

Budget: one test under 1 second, and a service's whole test run under 5 minutes of wall time. Measure without the race detector, since the budget is about the tests and CI's `-race` run is slower. Over budget is a problem to fix, not a build failure.

**Why:** Every merge waits on the suite, a production fix included. A suite that grows past the budget slows every ticket after it.

**Speed must not cost fidelity.** `dispatch:surface-testing` puts fidelity first, so swapping the real dependency for a fake to save time trades away what the test proves. Fixes that keep the fidelity:
- Move an injected clock instead of waiting out a timeout or retry delay.
- Run tests in parallel (`t.Parallel()` in Go). Give each test its own state so none shares a file, port or database row.
- Build expensive setup once per package, such as a booted server, a container or a compiled helper binary, and give each test its own data in it.
- Start a process once, not per call. A stand-in tool that spawns a new process on every call adds that startup to every test.

Skipping slow tests (`testing.Short`, build tags) is not a speedup, because the behavior stops being tested.

**Check speed in a separate report, never inside the suite.** A time limit in the suite fails on a slow CI runner, and would block a production fix. In Go, `go run ./cmd/slowtests` lists tests over 1 second and packages over 5 minutes, slowest first. It exits 0 when it finds slow tests, so file what it finds as tickets by hand.
