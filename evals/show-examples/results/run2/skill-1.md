No, I'm not proposing a new subcommand. The subcommands already exist (`check`, `budget`, `golangci`, `pattern`, `changed`, `pin`), and `tiger`'s usage stays exactly as it is. What I proposed is a change to where the Go code for each one lives.

Today `runChanged` sits in `internal/cli/changed.go`, so any test of `tiger changed` goes through `cli.Run`, which links all 58 packages. After the change, `runChanged` moves into `internal/changed` as an exported `Run`, and `cli.Run` just forwards to it:

```go
case "changed":
    return changed.Run(args[1:], streams)
```

A `tiger changed` test then calls `changed.Run([]string{"main"}, streams)` and links only `changed`, `callinventory` and `revision`.

I'd keep `tiger run <check>` out of this. It wouldn't fix the test speed, because a test that enters through `cli.Run` still links every analyzer, however the subcommand is named. It would also add to tiger's usage, which you ruled out for ENG-506. It could be a good feature on its own, and I can file it as a separate ticket if you want.

I haven't checked whether `tiger check` runs every analyzer today. I'll confirm that before the design relies on it.

**Question: is it OK for each domain package to export its own `Run` for its subcommand, with `cli.Run` only doing the name lookup and usage text?**
