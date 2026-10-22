No, no new subcommands. The six that exist (`check`, `budget`, `golangci`, `pattern`, `changed`, `pin`) stay exactly as they are, and what you type at the terminal doesn't change. What I'm proposing is a Go-level change. Each command's code moves into its domain's package, and `cli.Run` becomes a thin lookup that forwards to it:

```go
case "changed":
    return changed.Run(args[1:], streams)
```

Tests can then call `changed.Run` directly and skip the other packages. I haven't checked how `tiger check` relates to the other commands, so I won't claim it "runs them all" yet.

I'd steer away from `tiger run <name>`, for two reasons:

1. It adds a command to tiger's usage, which you ruled out for this ticket.
2. It wouldn't fix the slow tests. A test of `tiger run changed` would still go through `cli.Run`, which links all 58 packages, so the 133s of `tiger changed` tests would still rerun whenever any analyzer changes.

If you want `tiger run <name>` as a feature, it fits better as its own ticket after this restructure, since the per-domain entry points would make it easy to add.

So, do you want the per-domain `Run` functions with no usage change in ENG-506, and `tiger run <name>` split off as a separate ticket? I recommend that.
