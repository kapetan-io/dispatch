Not quite. I'm not proposing any new subcommands. The six that exist today (`check`, `budget`, `golangci`, `pattern`, `changed`, `pin`) stay exactly as they are. What changes is where the code behind each one lives. Today `runChanged` sits in `internal/cli/changed.go`. I'd move it into `internal/changed` as an exported `changed.Run`, and `cli.Run` would just call it when it sees `"changed"`. Users see no difference. Only tests can call `changed.Run` directly.

I'd skip `tiger run <name>`, for two reasons:

- **It changes tiger's usage.** You ruled that out for this ticket, and a new subcommand would need its own naming and compatibility decisions.
- **It doesn't fix the slow tests.** A test of `tiger run changed` would still enter through `cli.Run`, which links all 58 packages. Tests only stay cached when they call a function in a small package, like `changed.Run`.

I haven't checked whether `tiger check` runs the other commands' logic today. In the `cli.go` I read, they're separate top-level subcommands. If you want a "run everything" mode, that's a separate feature and I'd file it as its own ticket.

My recommendation is the per-package `Run` function with no new subcommand. Shall I write that into ENG-506 as the testing design?
