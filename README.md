# dispatch

An autonomous build workflow for Claude Code, packaged as a Claude Code plugin.

You design a feature with Claude in an interactive session. An autonomous review then checks the design and splits it into small Linear build tickets. Once you approve, a stateless dispatcher, running every 5 minutes in a herdr window you keep open, starts a Claude worker for each ticket in its own worktree, and each worker carries its ticket to a merged pull request.

## Status

The dispatcher works end to end today. `dispatch run` launches a worker for each ticket you move to In Progress, nudges idle workers and stalled PRs, escalates to Needs Human with a macOS notification, starts a parent ticket's children in order and closes the parent, and tears down finished trees. The `dispatch:build` skill carries a ticket to an open PR, and `dispatch:ticket` writes tickets the dispatcher picks up. Not built yet are the design-review pipeline (tickets labeled `design` or `design-review` are left alone) and the herdr workspace status emoji the blueprint describes.

Start with the [user guide](docs/guide.md) to set it up and for troubleshooting, and the [config reference](docs/config.md) for every setting. The design is in `docs/design/`, starting with `decision-log.md`.

## Layout

- `.claude-plugin/plugin.json` is the plugin manifest. Skills live in `skills/` (`build` runs a ticket, `ticket` writes one), scripts on the session PATH in `bin/`, and the plugin's hooks in `hooks/`. `hooks/hooks.json` runs `dispatch ticket-hint` at session start (silently skipped when `dispatch` is not installed), which points an agent in a dispatch-managed repo at `dispatch:ticket` because a long skill list can show a skill by name only.
- `cmd/` and `internal/` hold the `dispatch` Go CLI.
- `docs/` holds the [user guide](docs/guide.md) and the [config reference](docs/config.md). `docs/design/` holds the design: the decision log, the dispatcher blueprint, the handoff, and the size-check eval suite.

## Development

Load the plugin in place for one session:

```sh
claude --plugin-dir ~/Development/dispatch
```

## Running the dispatcher

```sh
cd ~/Development/dispatch && go install ./cmd/dispatch
dispatch run
```

The [user guide](docs/guide.md) covers the setup this needs (herdr, treehouse, `gh`, the Linear API key and statuses), how a ticket flows, pausing, `dispatch tell`, and what to do when something breaks.
