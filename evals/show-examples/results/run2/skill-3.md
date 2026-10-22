Not quite. I'm not proposing new subcommands. `check`, `budget`, `golangci`, `pattern`, `changed` and `pin` already exist in `cli.Run`'s switch, and the command line stays exactly as it is. The change is internal: each command's code moves out of `internal/cli` into the package for its concept, as a Go function.

```go
// today: internal/cli/changed.go
func runChanged(args []string, streams Streams) int

// proposed: internal/changed
func Run(args []string, streams Streams) int
```

`cli.Run` would keep `case "changed": return changed.Run(args[1:], streams)`. A user typing `tiger changed main` sees no difference. The only gain is that a test can call `changed.Run` directly and link three packages instead of 58.

I'd skip `tiger run <name of check>`, for two reasons:
- It adds a new command, and you said usage doesn't change.
- It doesn't help the tests. They would still enter through `cli.Run`, which links every analyzer, so the 133s would still rerun on unrelated changes.

I haven't read `runCheck` yet, so I can't confirm that `tiger check` runs every analyzer. If it does, a `tiger run <name>` could be a useful feature for users, but that would be a separate ticket from this restructure.

Does the per-domain `Run` function work for you, with the command line unchanged?
