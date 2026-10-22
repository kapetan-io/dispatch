No, I'm not proposing new subcommands. `check`, `budget`, `golangci`, `pattern`, `changed` and `pin` already exist, and the command line you type stays exactly as it is. What changes is where each command's code lives. Today `runChanged` sits in `internal/cli` next to everything else. I'd move it into `internal/changed` as an exported function, and `cli.Run` would still look up the name and hand over the remaining arguments. The only difference is that a test can call `changed.Run(...)` directly and skip the lookup.

A `tiger run <name>` subcommand wouldn't fix the slow tests. The slowness comes from `internal/cli` importing all 58 packages. A test that calls `cli.Run([]string{"run", "changed"}, ...)` still goes through `internal/cli`, so it still rebuilds and reruns whenever any analyzer changes. It would also add a command to tiger's usage, which you ruled out for this ticket. I haven't checked how `tiger check` relates to the other commands, so I won't claim it runs all of them. Folding them together would be a behavior change in any case.

If `tiger run <name>` is something you want as a feature, I'd make it its own ticket, and it could sit on top of the per-domain functions later.

Do you want me to proceed with the per-domain entry functions and no new subcommand?
