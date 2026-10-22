Not quite: nothing new for users. Tiger already has a subcommand for each concept (`check`, `budget`, `golangci`, `pattern`, `changed`, `pin`). My proposal is only about the Go code behind them. Each domain package would hold its own subcommand's code as a function like `changed.Run(args, streams)`, and `cli.Run` would just pick which one to call. The command line stays the same, so `tiger changed main` works exactly as it does today.

I haven't checked whether `tiger check` runs the other subcommands' work, so I wouldn't assume it does. I'll read `runCheck` before the design says anything about it.

I'd leave out `tiger run <name>`, for two reasons:

- **It doesn't fix the slow tests.** A test that goes through `cli.Run`, even to reach `run changed`, still links all 58 packages. The speedup comes from tests calling the domain package's function directly, and `tiger run` would sit on top of that without changing it.
- **It adds a new command.** You said tiger's usage shouldn't change in this ticket. If you want a way to run one check by name, it's a reasonable feature, but I'd file it as its own ticket and keep ENG-506 a pure restructure.

My recommendation is to keep the existing subcommands, give each domain package its own entry function, and have tests call that function. Do you want to go with that, and leave `tiger run <name>` for a separate ticket?
