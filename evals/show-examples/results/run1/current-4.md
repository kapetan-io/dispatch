Not quite. No new subcommands. The six that exist today (`check`, `budget`, `golangci`, `pattern`, `changed`, `pin`) stay exactly as they are, and tiger's usage doesn't change. What changes is where the code lives. Each concept's package exports a function that runs its command, and `cli.Run` calls it after matching the name. The user still types `tiger changed main`, but a test can call `changed.Run([]string{"main"}, streams)` directly.

I'd skip `tiger run <name of check>` for two reasons:
- **It changes the usage.** You ruled that out for this ticket. A new command could be its own ticket.
- **It doesn't fix the slow tests.** The 178s comes from tests importing `internal/cli`, which links all 58 packages. A test of `tiger run changed` would still go through `cli.Run`, so it would still rebuild whenever any analyzer changes. The speedup only comes from the test importing just the domain package.

I also haven't verified that `tiger check` runs every analyzer today. The dispatch I read lists it as one subcommand among six. If you know it fans out to the others, tell me. I'll read `runCheck` before the design relies on it.

Do you want the per-package `Run` with no new subcommand, and `tiger run <name>` kept out of ENG-506?
