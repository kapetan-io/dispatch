No, I'm not proposing a new subcommand. The subcommands already exist (`check`, `budget`, `golangci`, `pattern`, `changed`, `pin`), and the command line stays exactly as it is. The change is in the Go code behind them. Today `runChanged` sits in `internal/cli`, so any test of it links all 58 packages. I'd move it into the `changed` package as an exported function:

```go
// in internal/changed
func Run(args []string, streams cli.Streams) int
```

`cli.Run` would still match `"changed"` and call `changed.Run(args[1:], streams)`. A user sees nothing different. Only the tests change: they call `changed.Run` directly and link three packages instead of 58.

I'd skip `tiger run <name>` for two reasons:

1. **It doesn't fix the slow tests.** A test of `tiger run changed` still enters through `cli.Run`, which links every analyzer, so the 133s of `tiger changed` tests would still rerun on unrelated changes. The speedup comes from the test importing only the domain package.
2. **It changes tiger's usage.** You ruled that out for ENG-506. If you want `tiger run <name>` as a feature, it fits better as its own ticket after the restructure.

One thing I haven't checked is whether `tiger check` runs every analyzer today or only some. I only read the dispatch switch, where `check` is one case among the others.

Do you want to go ahead with each domain package exporting its own `Run`, with no new subcommand, and `tiger run <name>` left for a separate ticket?
