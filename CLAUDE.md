# dispatch

## Scripts are Go, not shell

Write every new script, hook or helper as Go, either as a `dispatch` subcommand or as its own small CLI under `cmd/`. Test it through its command line, following the `dispatch:surface-testing` skill's Go CLI pattern: `main()` is a thin wrapper around `Run(ctx, args, opts)`, and tests call `Run` or run the built binary.

Shell lets parsing logic hide where no test reaches it. The ticket hint hook once read `config.toml` with a 20-line awk program that re-implemented TOML string escapes, while the Go code already loaded the same file with `internal/config`. It is now `dispatch ticket-hint`, which reuses that loader and gets surface tests for free.

A file in `bin/` may stay a one-line shell wrapper that execs the Go binary, since plugins put `bin/` on the session PATH. Existing shell scripts move to Go when they are next changed.

## Testing

`go test ./...` runs everything CI runs, against in-process fakes of the external tools (`internal/tooltest`). `go test -tags drift ./internal/...` runs the same contract scenarios against the installed treehouse in a temp repo and pool root, and checks the flags dispatch passes to gh against the installed gh's `--help`. It also runs the unseeded Linear scenarios against the workspace `LINEAR_API_KEY` belongs to, reading only and never changing a ticket (`DISPATCH_DRIFT_LINEAR_TEAM` picks the team and defaults to ENG). It fails when treehouse, gh or the Linear key is missing. The gh fake holds GitHub's answers per branch and refuses to merge a pull request whose head moved, but no test reaches GitHub, so only the flags are checked against the real tool. The Linear fake keeps tickets, statuses and comments in memory and `internal/linear/client_test.go` runs the real client against an HTTP test server to check the GraphQL wire format. CI never runs it, so run it after changing a fake or a real adapter.
