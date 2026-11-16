# dispatch

A general-purpose, event-driven reconcile loop for agents, packaged as a Claude Code plugin.

Move a ticket to In Progress and get back to your day. Dispatch puts an agent on it and keeps that agent working until the pull request is ready for your review. It chases the review comments and failing checks so you don't have to, and it interrupts you only for the calls that are yours to make. You decide what ships. You stop babysitting agents.

The Purpose section of [docs/CONTEXT.md](docs/CONTEXT.md#purpose) says what stays yours, the tradeoffs dispatch makes and what you can do with it.

## Supported

| Multiplexers, where workers run | Ticket systems |
| --- | --- |
| [herdr](docs/config.md#pluginsherdr) terminal UI | [Linear](docs/config.md#pluginslinear) |
| [Herdr app](docs/config.md#pluginsherdr) | [Local tickets](docs/config.md#pluginslocal), kept in dispatch's own database |
| [Rex](docs/config.md#pluginsrex) | |

## Customizable

Dispatch is event-driven. Every change it sees, a ticket moving, a check failing, an agent going quiet, runs your rules, and your rules orchestrate the workflow. Your workflow is not a dispatch feature request.

### Rules in TypeScript

The workflow is TypeScript you own. Change when agents start, when they get nudged, when you get pulled in, all without touching Go. Lint your rules and test them against the live world before they run, and ask dispatch why any ticket moved the way it did.

### Plugins

Ticket systems, multiplexers, GitHub and notifications are plugins, and you switch each one on in config. Each sits behind a small interface of its own, so supporting a new ticket system or multiplexer means writing one adapter, not rewriting dispatch.

### Any HTTP service

Rules can call any HTTP service you name in config, with dispatch holding the token. Your workflow can reach Sentry, your deploy system or anything else your team runs.

### One config file

A single `config.toml` holds your repos, your plugins and the knobs your rules read, and dispatch refuses a typo instead of guessing.

### It tells you

When a ticket is ready for review, needs you, or goes idle, dispatch messages the Claude sessions following it. You hear about it where you already work.

## Docs

- [docs/config.md](docs/config.md) lists every setting in `config.toml`, with its default and what it does.
- [docs/rules.md](docs/rules.md) explains how to write, lint and test the rules, and how to switch a shipped rule off for a class of records.
- [docs/startup-screen.md](docs/startup-screen.md) shows what `dispatch run` prints while it starts, and when that output is plain text.
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
