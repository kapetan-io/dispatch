Not quite. No new subcommands, and the command line stays exactly as it is. `check`, `budget`, `golangci`, `pattern`, `changed` and `pin` already exist. What I'm proposing is a change inside the Go code: each domain package gets a function that does what its subcommand does. `cli.Run` still reads `changed` from the command line and calls `changed.Run(args, streams)`, and the `tiger changed` tests call that function directly. Nobody running tiger would see a difference.

I'd keep `tiger run <name>` out of ENG-506, for two reasons:

- **It changes usage.** You ruled that out for this ticket.
- **It wouldn't fix the slow tests.** Any test that goes through the `tiger` command line goes through `cli.Run`, which links all 58 packages. The caching comes from tests calling the domain package directly, whatever the command is named.

I also haven't confirmed that `tiger check` runs every analyzer today. I'd read `runCheck` before building on that.

**Do you want to keep ENG-506 to the Go-level change, with `tiger run <name>` as a separate ticket if you still want it?** I recommend yes.
