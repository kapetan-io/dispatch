Not quite. I'm not proposing new subcommands. `check`, `budget`, `golangci`, `pattern`, `changed` and `pin` already exist, and your earlier call was that tiger's usage doesn't change. What I proposed is a Go function in each domain package, which `cli.Run` calls after it matches the name:

```go
// cli.go: dispatch only
case "changed":
    return changed.Run(args[1:], streams)

// internal/changed: the command lives with its concept
func Run(args []string, streams cli.Streams) int
```

Users still type `tiger changed main`. Only the tests change, because they can call `changed.Run` directly and skip `cli.Run`, which links all 58 packages.

I haven't read `runCheck`, so I can't say whether `tiger check` runs every analyzer today. I'd confirm that before we design around it.

`tiger run <name of check>` would be a new subcommand, so it changes the usage you asked me to keep. It also wouldn't fix the test speed. A test going through `tiger run` still enters `cli.Run` and links every analyzer, so it reruns whenever any analyzer changes. It only pays off if tests bypass the CLI and call the package function, and then the Go function is doing the work, not the subcommand.

If you want `tiger run` for its own sake, as a way to run one analyzer, that's a separate feature. I'd put it in its own ticket and keep ENG-506 a restructure with no usage change.

One detail needs your call. `Streams` and the exit codes currently live in `internal/cli`, so a domain package calling them would import `cli`, and `cli` imports the domain packages. I'd move `Streams` and the exit codes into a small package that both sides import.

**Do you want the per-domain `Run` as a Go-level entry only, with no new subcommand and the usage unchanged?**
