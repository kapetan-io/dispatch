# dispatch

An autonomous build workflow for Claude Code, packaged as a Claude Code plugin.

You design a feature with Claude in an interactive session. An autonomous review then checks the design and splits it into small Linear build tickets. Once you approve, a stateless dispatcher, running every 5 minutes in a herdr window you keep open, starts a Claude worker for each ticket in its own worktree, and each worker carries its ticket to a merged pull request.

## Status

Under construction. The design is in `docs/design/`. Start with `decision-log.md`, then `blueprint.md` (the dispatcher) and the handoff. The workflow is being built one piece at a time. The first piece is the dispatcher launching a build worker for a ticket you approve.

## Layout

- `.claude-plugin/plugin.json` is the plugin manifest. Skills will live in `skills/`, scripts on the session PATH in `bin/`, and eval suites in `evals/`.
- `cmd/` and `internal/` will hold the `dispatch` Go CLI.
- `docs/design/` holds the design: the decision log, the dispatcher blueprint, the handoff, and the size-check eval suite.

## Development

Load the plugin in place for one session:

```sh
claude --plugin-dir ~/Development/dispatch
```

## Running the dispatcher

Install the CLI from your checkout, which is also the plugin root that workers load:

```sh
cd ~/Development/dispatch && go install ./cmd/dispatch
```

The dispatcher reads `~/.config/dispatch/config.toml` and the Linear API key from the keychain entry it names. It shells out to `herdr`, `treehouse`, `git`, `jq` and `claude`, so run it from a shell that has them on PATH.

Open a herdr window and run:

```sh
dispatch run
```

It ticks now and every `interval_minutes` after, printing a timestamped line per tick and one line per action. Ctrl-C stops it. To check what a tick would do without acting, run `dispatch tick --dry-run`.
