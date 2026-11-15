# dispatch

A general-purpose, event-driven reconcile loop for agents, packaged as a Claude Code plugin.

You approve a ticket by moving it to In Progress: a Linear ticket assigned to you that carries the `ai` label and a repo label, or a local ticket. Dispatch leases a tree for it (a git worktree from the repo's treehouse pool), starts a worker on that tree (a Claude Code session in its own terminal) and keeps the worker on the ticket until its pull request is ready, telling you when the ticket waits on you. After every event it reads from your ticket system, GitHub, the multiplexer (the program that runs the workers' terminals) and treehouse, TypeScript rules you can edit decide what it does next.

The Purpose section of [docs/CONTEXT.md](docs/CONTEXT.md#purpose) says what stays yours, the tradeoffs dispatch makes and what you can do with it.

## Running it

Clone this repo to `~/Development/dispatch`, where workers load the plugin from by default. A clone elsewhere needs `plugin_root` set to it in the table of the multiplexer you pick below, and `cd` to your clone in place of `~/Development/dispatch` in the commands here. Then:

```sh
cd ~/Development/dispatch && make install
dispatch run
```

`dispatch run` reads `~/.config/dispatch/config.toml`. Each repo dispatch works in needs a `[repos.<label>]` table, and the [config reference](docs/config.md) lists every setting.

Workers run in one of three multiplexers, and the config picks which.

| Multiplexer | Config | Section |
| --- | --- | --- |
| herdr terminal UI | `[plugins.herdr]` with `host = "tui"`, the default | [`[plugins.herdr]`](docs/config.md#pluginsherdr) |
| Herdr app, over herdr-core | `[plugins.herdr]` with `host = "app"` | [`[plugins.herdr]`](docs/config.md#pluginsherdr) |
| Rex | `[plugins.rex]`, even empty | [`[plugins.rex]`](docs/config.md#pluginsrex) |

Where `dispatch run` itself runs is a separate choice. Any terminal works, and so does its own Herdr app space:

```sh
herdr-app do space.new --name Dispatch --folder ~/Development/dispatch --tab dispatch --argv '["dispatch","run"]'
```

Tickets come from Linear, set up under [`[plugins.linear]`](docs/config.md#pluginslinear), and from dispatch's own local ticket database when you write [`[plugins.local]`](docs/config.md#pluginslocal). `dispatch ticket create linear` or `dispatch ticket create local` files one, and the `dispatch:ticket` skill writes it for you.

Under Rex, `dispatch rex restore` resumes your own Claude sessions after a Rex restart, and [docs/config.md](docs/config.md#having-rex-run-the-restore-when-its-server-starts) shows how to have Rex run it each time its server starts.

`dispatch` with no arguments lists every command.

## Docs

- [docs/config.md](docs/config.md) lists every setting in `config.toml`, with its default and what it does.
- [docs/rules.md](docs/rules.md) explains how to write, lint and test the rules, and how to switch a shipped rule off for a class of records.
- [docs/worker-states.md](docs/worker-states.md) lists every state a worker can be in, the emoji its label shows, and what dispatch does about it.
- [docs/CONTEXT.md](docs/CONTEXT.md) states what dispatch is for and defines the terms the code, tests and docs use.
- [docs/adr/](docs/adr/) holds the architecture decisions, one numbered file each.

## Layout

- `.claude-plugin/plugin.json` is the plugin manifest. Skills live in `skills/`, one folder each, and the `description` in each `SKILL.md` says what it does. `bin/` holds scripts on the session PATH, and `hooks/` holds the plugin's hooks.
- `cmd/` and `internal/` hold the `dispatch` Go CLI.
- `rules/` holds the shipped TypeScript rules. `evals/` holds the skill eval suites, one folder per skill.

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
| `make test-claude` | Worker scenarios against a real, logged-in Claude on the Herdr app host. Spends Haiku turns, and CI does not run it. |
| `make test-report` | The speed report of tests over budget, tagged slow tests included. |
| `make ci` | What CI runs: vet, the whole suite with the slow tests, and shellcheck. |
