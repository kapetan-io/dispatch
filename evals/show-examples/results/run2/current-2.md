Not quite. I'm not proposing new subcommands. The six that exist today (`check`, `budget`, `golangci`, `pattern`, `changed`, `pin`) stay exactly as they are. What changes is where each one's code lives. Today they all sit in `internal/cli`. In the proposal, each concept's package holds its own command function, and `cli.Run` just looks up the name and calls it. Users never see the difference, because `tiger changed main` still works the same way. Only tests would call `changed.Run` directly.

I also haven't confirmed that `tiger check` runs every analyzer, so I wouldn't build on that yet.

I'd leave `tiger run <name>` out of this ticket. It would add a new command, which cuts against your call that usage doesn't change. It also wouldn't fix the slow tests. The caching comes from the Go package boundary, not from the command line. A test that goes through `cli.Run`, even for `tiger run changed`, still links all 58 packages and reruns whenever any analyzer changes. If you want `tiger run` as a feature, it makes sense as its own ticket.

**Do you want to keep ENG-506 to the internal restructuring, with each package owning its command and no new subcommand?**
