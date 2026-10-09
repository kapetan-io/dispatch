# dispatch

## Scripts are Go, not shell

Write every new script, hook or helper as Go, either as a `dispatch` subcommand or as its own small CLI under `cmd/`. Test it through its command line, following the surface-testing skill's Go CLI pattern: `main()` is a thin wrapper around `Run(ctx, args, opts)`, and tests call `Run` or run the built binary.

Shell lets parsing logic hide where no test reaches it. The ticket hint hook once read `config.toml` with a 20-line awk program that re-implemented TOML string escapes, while the Go code already loaded the same file with `internal/config`. It is now `dispatch ticket-hint`, which reuses that loader and gets surface tests for free.

A file in `bin/` may stay a one-line shell wrapper that execs the Go binary, since plugins put `bin/` on the session PATH. Existing shell scripts move to Go when they are next changed.
