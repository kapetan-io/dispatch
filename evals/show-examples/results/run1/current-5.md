Not quite. I'm not proposing new subcommands. `tiger changed`, `tiger pin`, `tiger pattern` and the others already exist and keep working exactly as they do now. The change is inside the Go code: each concept's package gets an exported function (`changed.Run(args, streams)`) that holds what `runChanged` holds today. `cli.Run` still reads `changed` from the command line and hands the remaining arguments to that function. Users see no difference, which matches your call that usage doesn't change.

I'd leave `tiger run <name>` out of this ticket, for two reasons:

1. **It wouldn't fix the slow tests.** A test of `tiger run changed main` would still enter through `cli.Run`, which links all 58 packages, so it would rerun whenever any analyzer changes. The caching gain comes from tests calling the domain package directly, and that doesn't depend on any command-line shape.
2. **It changes the usage.** Adding a command is a user-facing feature with its own design questions, such as whether `tiger check` becomes shorthand for running every named check. That deserves its own ticket.

I haven't checked how `tiger check` is built today, so I can't say whether it already runs every analyzer. I can look if you want to pursue `tiger run` separately.

Do you want to go ahead with per-domain Go entry points and no change to the command line, with `tiger run` filed as a follow-up ticket?
