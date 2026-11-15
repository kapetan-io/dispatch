# dispatch

A general-purpose, event-driven reconcile loop for agents, packaged as a Claude Code plugin.

Create a ticket and move it to In Progress. Dispatch notices, starts a worker on it in its own git worktree, and tells you when the pull request is ready for your review. While the worker runs, dispatch nudges it about review comments and failing checks, and it calls you only when the ticket needs a decision that is yours. You keep the product calls and the merge.

The Purpose section of [docs/CONTEXT.md](docs/CONTEXT.md#purpose) says what stays yours, the tradeoffs dispatch makes and what you can do with it.

## Supported

| Multiplexers, where workers run | Ticket systems |
| --- | --- |
| [herdr](docs/config.md#pluginsherdr) terminal UI | [Linear](docs/config.md#pluginslinear) |
| [Herdr app](docs/config.md#pluginsherdr) | [Local tickets](docs/config.md#pluginslocal), kept in dispatch's own database |
| [Rex](docs/config.md#pluginsrex) | |

## Make it yours

Every decision dispatch makes, from when to start a worker to when to nudge it, notify you or escalate, lives in a TypeScript rule you can read and edit. Dispatch supplies the facts and the actions, and the rules decide what to do with them, so changing how dispatch behaves means editing a rule, not forking Go. `dispatch rules check` runs your rules once against what your sources say right now, `dispatch rules test` runs the tests you write for them, and `dispatch explain <ticket>` shows which rule did what and why.

Each tool dispatch talks to is a plugin you turn on in `config.toml`: the ticket systems, the multiplexer, GitHub, treehouse and notifications. Rules can also call any HTTP service you name in the config, so your workflow can reach whatever else your team uses.

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
