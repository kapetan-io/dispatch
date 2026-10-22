Not quite. No new subcommands. The six that exist today (`check`, `budget`, `golangci`, `pattern`, `changed`, `pin`) stay exactly as they are, and what you type at the terminal doesn't change. What I'm proposing is internal: each domain package gets a Go function that runs its command. Today `runChanged` lives in `internal/cli`, which drags in all 58 packages. Moved into `internal/changed` as `changed.Run`, the tests can call it directly and only link the three packages it needs.

`tiger run <name>` would be a new way to invoke checks, which changes tiger's usage. You ruled that out for this ticket, and it isn't needed for the testing goal, since tests call the Go function and never go through the command line. If you want it, it fits better as its own ticket after the restructure, when each concept already has its own entry point.

I haven't checked whether `tiger check` runs the other commands' work today, so I won't claim that. I'd leave `check` as it is and look at that separately if it matters.

My recommendation: each domain package owns its command as `Run(args, streams) int`, `cli.Run` keeps only the name lookup and usage text, and `tiger run` is deferred. Do you want to go with that?
