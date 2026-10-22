Not quite. I'm not proposing any new subcommands. The six that exist today (`check`, `budget`, `golangci`, `pattern`, `changed`, `pin`) stay as they are. What moves is the code behind each one. The `tiger changed` code would leave `internal/cli` and live in `internal/changed`, and `cli.Run` would still map the word `changed` to it. Users see no difference, but a test can call `changed.Run` directly without linking the other analyzers.

I'd keep `tiger run <name of check>` out of this ticket, for two reasons:
- **Usage change:** it adds a new command, and you said tiger's usage shouldn't change.
- **No test speedup:** `tiger run <name>` has to look a check up by name, so something has to import every check. Tests going through it would link all the analyzers, which is the same slowness as `cli.Run` today.

It could still be a good feature on its own, so I'd file it as a separate ticket.

I also haven't verified that `tiger check` runs every analyzer. I've only seen `cli.go` hand `check` to `runCheck`, and I haven't read `runCheck`. I'll read it before the design relies on that.

**Question: should ENG-506 only move each existing command's code into its domain package, with no new subcommand, and should I file `tiger run <name>` as a separate ticket?** I'd say yes.
