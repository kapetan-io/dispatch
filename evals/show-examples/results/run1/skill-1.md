Not quite. I'm not proposing new subcommands. `tiger check`, `changed`, `pin` and the others already exist, and your earlier call was that tiger's usage doesn't change. The proposal is internal: each domain package gets a Go function that runs its existing subcommand, and `cli.Run` calls it. Users see nothing different. Only the tests change, because they call the domain's function directly instead of going through `cli.Run`.

I don't think `tiger run <name>` fixes the slow tests, for two reasons:
- **It changes usage.** It adds a new way to invoke tiger, which you ruled out for this ticket.
- **It keeps the dependency.** The dispatch for `tiger run` would still live in `cli.Run`, which imports all 58 packages. A test going through it would still rebuild and rerun whenever any analyzer changes. The caching only improves if the test avoids `cli.Run` entirely.

If you want a `tiger run <name>` command for its own sake, I'd file it as a separate ticket.

One consequence: the exit code constants (`ExitClean` and the others) live in `internal/cli` today. Domain packages can't import `cli` without pulling it back in, so the constants would move to a small shared package.

Do you want to go with the per-domain Go entry and no change to the command line?
