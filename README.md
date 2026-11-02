# dispatch

An autonomous build workflow for Claude Code, packaged as a Claude Code plugin.

You design a feature with Claude in an interactive session. An autonomous review then checks the design and splits it into small Linear build tickets. Once you approve, a dispatcher, running in a terminal you keep open and re-reading its sources every 5 minutes, leases a tree for each ticket and starts Claude in it, and each worker carries its ticket to a merged pull request.

## Status

The dispatcher works end to end today. `dispatch run` is a reconcile loop: it keeps what it knows of Linear, GitHub, herdr and treehouse in memory, runs the TypeScript [rules](docs/rules.md) after each event, and records what it did in a ledger kept in history files. It launches a worker for each ticket you move to In Progress, labels each worker's window by who its ticket is waiting on, nudges workers about PRs that need work, escalates to Needs Human with a notification, starts a parent ticket's children in order and closes the parent, and tears down finished trees. The `dispatch:build` skill carries a ticket to an open PR, and `dispatch:ticket` writes tickets the dispatcher picks up. Besides Linear, dispatch can keep tickets in its own database, filed with `dispatch ticket create` as shown under [`[plugins.local]`](docs/config.md#pluginslocal).

The [config reference](docs/config.md) lists every setting, and [rules](docs/rules.md) covers `dispatch rules`, which lints and tests the TypeScript rules that make its decisions.

## Layout

- `.claude-plugin/plugin.json` is the plugin manifest. Skills live in `skills/`, one folder each, and the `description` in each `SKILL.md` says what it does. `bin/` holds scripts on the session PATH, and `hooks/` holds the plugin's hooks.
- `cmd/` and `internal/` hold the `dispatch` Go CLI.
- `docs/` holds the [config reference](docs/config.md) and the [rules guide](docs/rules.md). `rules/` holds the shipped TypeScript rules. `evals/` holds the skill eval suites, one folder per skill.

## Development

Put `scripts/shims` first on your PATH, and a plain `claude` loads the plugin from this checkout and accepts messages from other sessions, so a `git pull` reaches your next session with no reinstall. For one session without the shim, run:

```sh
claude --plugin-dir ~/Development/dispatch --settings '{"crossSessionInbound":"accept"}'
```

`make help` lists the build and test targets.

| Target | Runs |
| --- | --- |
| `make test` | The fast suite, with no race detector and no slow tests. |
| `make test-slow` | The whole suite plus the tests tagged `tests_slow`. |
| `make test-race` | The fast suite under the race detector, for an occasional check. |
| `make test-drift` | The drift checks against the installed tools. CI does not run it. |
| `make test-report` | The speed report of tests over budget, tagged slow tests included. |
| `make ci` | What CI runs: vet, the whole suite with the slow tests, and shellcheck. |

## Running the dispatcher

```sh
cd ~/Development/dispatch && make install
dispatch run
```

Workers run in the herdr terminal UI by default. To run them in the Herdr app instead, set `host = "app"` under `[plugins.herdr]`. The [`host` row](docs/config.md#pluginsherdr) says what that needs (`hc` on PATH, and `herdr-app` to name each worker's space).

Where `dispatch run` itself runs is a separate choice from `host`. To start it in its own Herdr app space:

```sh
herdr-app do space.new --name Dispatch --folder ~/Development/dispatch --tab dispatch --argv '["dispatch","run"]'
```

