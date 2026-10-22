Not quite. The subcommands already exist: `check`, `budget`, `golangci`, `pattern`, `changed` and `pin` are in the `switch` in `internal/cli/cli.go`. What I proposed adds no subcommands. Each domain package gets a Go function, like `changed.Run(args, streams)`, that holds the code `runChanged` holds today. `cli.Run` calls it, so `tiger changed main` behaves exactly as before. The only difference is that a test can call the function directly without linking the other 55 or so packages.

On `tiger run <name of check>`, I'd leave it out of this ticket, for two reasons:

- **It doesn't fix the slow tests.** The command would still be dispatched inside `cli.Run`, so a test that enters through it still links every analyzer and reruns whenever any analyzer changes. The speedup comes from tests calling the domain package's function, not from the command's name.
- **It changes tiger's usage.** You said usage stays the same for this ticket. A `tiger run` command is a new feature, and I'd file it separately. The per-domain functions would make it easy to add later, since `run` would only look up a name and call the function.

I haven't checked how `tiger check` assembles its analyzers today, so I can't yet say whether "runs them all" would carry over unchanged. I'd read that before the design commits to anything about it.

Is it OK to keep `tiger run` out of ENG-506 and go ahead with the per-domain function approach?
