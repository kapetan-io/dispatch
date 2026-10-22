No, not new subcommands. The subcommands are the ones tiger has today (`check`, `budget`, `golangci`, `pattern`, `changed`, `pin`), and the command line stays exactly as it is. The change is inside the Go code. Each domain package would export a function, such as `changed.Run(args, streams)`, that does what `runChanged` does now. `cli.Run` would call it when it sees `changed`. Users see no difference, but a test can call `changed.Run` directly and link only `changed`, `callinventory` and `revision`. That narrow import list is what keeps the tests cached. A test going through any command in `internal/cli` links all 58 packages.

I wouldn't add `tiger run <name>` here, for two reasons:

- **It changes tiger's usage.** You ruled that out for this ticket, and it wouldn't speed up the tests anyway. A test calling `cli.Run(["run", "changed", ...])` still links every analyzer.
- **It mixes two kinds of thing.** I haven't confirmed which analyzers `tiger check` runs. But `changed`, `pin` and `pattern` are separate commands, not checks you'd pick by name, so a single `run <name>` would need a rule for which is which.

A way to run one check by name could still be useful as its own feature. I'd put it in a separate ticket so ENG-506 stays a pure restructure.

Do you want ENG-506 to keep the usage unchanged and give each domain package its own Go entry point, with `tiger run <name>` split off as a separate ticket if you want it?
