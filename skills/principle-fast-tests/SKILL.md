---
name: principle-fast-tests
description: "Apply when writing, reviewing, speeding up or tagging slow tests. A test runs under 1 second and a service's suite under 5 minutes, and the speed comes from the test's shape, never from lower fidelity."
---

# Fast Tests

Budget: one test under 1 second, and a service's whole test run under 5 minutes of wall time. Measure without the race detector, since the budget is about the tests and CI doesn't run it either. Over budget is a problem to fix, not a build failure.

**Why:** Every merge waits on the suite, a production fix included. A suite that grows past the budget slows every ticket after it.

**Speed must not cost fidelity.** `dispatch:surface-testing` puts fidelity first, so swapping the real dependency for a fake to save time trades away what the test proves. Fixes that keep the fidelity:
- Move an injected clock instead of waiting out a timeout or retry delay.
- Run tests in parallel (`t.Parallel()` in Go). Give each test its own state so none shares a file, port or database row.
- Build expensive setup once per package, such as a booted server, a container or a compiled helper binary, and give each test its own data in it.
- Start a process once, not per call, since a fake of a command-line tool that spawns per call adds its startup to every test. Under `-race` that startup is 1 second, because every Go process sleeps that long before it exits. Setting `GORACE=atexit_sleep_ms=0` in the fake's environment took one call from 1.04s to 0.01s. It matters only when tests run with `-race`.

Skipping a test with `testing.Short` is not a speedup, because the behavior stops being tested everywhere. A build tag only moves a test into `test-slow` and `ci`.

**A test that stays over 1 second** after these fixes, and can't run in parallel, carries the `tests_slow` build tag (in Go, `//go:build tests_slow` in a file of its own). The `test-slow` and `ci` targets run it, so it is selected by tag, not dropped. Tag it last. The PR's Implementation section names the test, its time and why it can't be faster. A test that runs against both a fake and the real tool (see `dispatch:surface-testing`) is never tagged. Load `dispatch:repo-targets` for the targets.

**Check speed in a separate report, never inside the suite.** A time limit in the suite fails on a slow CI runner, and would block a production fix. In Go, `make test-report` (`go run ./cmd/slowtests -tags tests_slow`, so tagged tests are measured) lists tests over 1 second and packages over 5 minutes, slowest first. It exits 0 when it finds slow tests, so file what it finds as tickets by hand.
