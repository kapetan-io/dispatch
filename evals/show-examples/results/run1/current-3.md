No, I'm not proposing new subcommands. `changed`, `budget`, `pattern`, `pin`, `golangci` and `check` already exist in the dispatch switch in `internal/cli/cli.go`. My proposal is only about where the code behind each one lives. The `tiger changed` code would move into the `changed` package as a Go function, and `cli.Run` would call it. `tiger changed main` would behave exactly as it does today. Only the tests would call the function directly, so they'd skip the dispatch step and the 58 packages it links.

I haven't read `runCheck`, so I can't confirm that `tiger check` runs all the others today. If it doesn't, that's a separate fact to check before the design relies on it.

I'd leave `tiger run <name of check>` out of this ticket, for two reasons:
- **It changes tiger's usage.** You ruled that out for ENG-506. It could be its own ticket.
- **It wouldn't speed up the tests.** A test that runs `tiger run changed` still enters through `cli.Run`, so it still links every analyzer and reruns whenever any of them changes.

If `tiger run` is something you want anyway, the per-domain function is also what it would call, so doing this first doesn't block it.

**Question:** should the tests for each concept call that per-domain function directly (my recommendation, since that's what keeps them cached), or do you want them to keep going through `cli.Run` and accept the slow reruns?
