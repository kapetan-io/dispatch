No, I'm not proposing new subcommands. `tiger changed`, `tiger pattern`, `tiger pin` and the rest already exist, and their usage stays exactly as it is. The change is inside the Go code: each domain package gets an exported function that the existing subcommand calls. Only where the code lives and how tests reach it changes.

```go
// today, in internal/cli/cli.go
case "changed":
	return runChanged(args[1:], streams)

// proposed: runChanged moves into internal/changed as Run
case "changed":
	return changed.Run(args[1:], streams)
```

A `tiger changed` test then calls `changed.Run([]string{"main"}, streams)`. That test links only `changed`, `callinventory` and `revision`, so Go's test cache skips it unless that code changes.

I don't think `tiger run <name>` solves the speed problem, and it conflicts with your "no change to usage" call. A test that calls `cli.Run([]string{"run", "changed"}, ...)` still goes through `internal/cli`, which imports all 58 packages. It would rerun whenever any analyzer changes, exactly as the 133s of `tiger changed` tests do today. The speedup comes from tests not importing `internal/cli`, and that doesn't depend on the command-line shape.

I also haven't verified your point that `tiger check` runs every check today. In `cli.go` it is one of six separate subcommands (`check`, `budget`, `golangci`, `pattern`, `changed`, `pin`). I haven't read what `runCheck` does, so I won't promise that behavior is preserved until I do. I can read it before the next step.

If you do want `tiger run <name>` as a feature in its own right, I'd keep it out of ENG-506 and file it as a separate ticket.

Does the per-domain `Run` entry point work for you, with the existing subcommands unchanged?
