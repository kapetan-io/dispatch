Not quite. No new subcommands are involved. `tiger changed`, `pin`, `pattern`, `budget`, `golangci` and `check` already exist, and `cli.Run` already switches on those names. What changes is where each command's code lives. `changed` would live in `internal/changed` instead of `internal/cli/changed.go`, as a Go function that tests can call directly. From the terminal, `tiger changed main` would behave exactly as it does today.

I'd leave `tiger run <name>` out of ENG-506, for two reasons:

- **It changes tiger's usage.** You ruled that out for this ticket, and a new subcommand means new help text, a new name to keep stable, and a decision about what happens to the existing subcommands. That's a design question of its own.
- **It wouldn't fix the slow tests.** Tests would still enter through `cli.Run`, which links every analyzer, so they'd rerun whenever any analyzer changes. The speedup comes from tests calling the domain package's function directly, not from how a user types the command.

I haven't read what `tiger check` does internally, so I can't confirm it runs every analyzer in one pass. I'll check that when I map which domain owns what.

`tiger run <name>` could be a good follow-up ticket once the packages are split. Should I write ENG-506 with the per-domain `Run` functions and no new subcommand, and note `tiger run` as a possible follow-up?
