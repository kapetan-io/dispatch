# dispatch

An autonomous build workflow for Claude Code, packaged as a Claude Code plugin.

You design a feature with Claude in an interactive session. An autonomous review then checks the design and splits it into small Linear build tickets. Once you approve, a dispatcher, running in a herdr window you keep open and re-reading its sources every 5 minutes, starts a Claude worker for each ticket in its own worktree, and each worker carries its ticket to a merged pull request.

## Status

The dispatcher works end to end today. `dispatch run` is a reconcile loop: it keeps what it knows of Linear, GitHub, herdr and treehouse in memory, runs the TypeScript [rules](docs/rules.md) after each event, and records what it did in a ledger kept in history files. It launches a worker for each ticket you move to In Progress, nudges idle workers (once per Stop, never escalating them) and stalled PRs, escalates to Needs Human with a notification, starts a parent ticket's children in order and closes the parent, and tears down finished trees. The `dispatch:build` skill carries a ticket to an open PR, and `dispatch:ticket` writes tickets the dispatcher picks up.

Start with "Set it up" in the [user guide](docs/guide.md), which takes a fresh Mac to a working dispatcher and supervisor session and ends with a check you run to confirm it. The [config reference](docs/config.md) lists every setting, and [rules](docs/rules.md) covers `dispatch rules`, which lints and tests the TypeScript rules that make its decisions. The design is in `docs/design/`, starting with `decision-log.md`.

## Layout

- `.claude-plugin/plugin.json` is the plugin manifest. Skills live in `skills/` (`build` runs a ticket, `ticket` writes one, `arena` runs independent attempts at a question and merges the best, `surface-testing` holds how to test through a system's surface, `converse` holds how a session talks with the user and fits any session, `bro` restates the last message in plain words and an agent loads it when the user asks for plainer wording, `ketchup` catches the user up on what changed in the session since their last message and is started by the user with `/dispatch:ketchup`, `principles` indexes the `principle-*` skills that hold the design rules a session can be told to apply by name, and `dispatch` drives dispatch from a session and loads `converse`, started by the user with `/dispatch:dispatch`), scripts on the session PATH in `bin/`, and the plugin's hooks in `hooks/`. `hooks/hooks.json` runs `dispatch ticket-hint` at session start (silently skipped when `dispatch` is not installed), which points an agent in a dispatch-managed repo at `dispatch:ticket` because a long skill list can show a skill by name only.
- `cmd/` and `internal/` hold the `dispatch` Go CLI.
- `docs/` holds the [user guide](docs/guide.md), the [config reference](docs/config.md) and the [rules guide](docs/rules.md). `rules/` holds the shipped TypeScript rules. `docs/design/` holds the design: the decision log, the dispatcher blueprint and the handoff. `evals/` holds the skill eval suites, one folder per skill.

## Development

Load the plugin in place for one session (the [user guide](docs/guide.md) installs it for good):

```sh
claude --plugin-dir ~/Development/dispatch
```

To start your supervising session with the plugin loaded and cross-session messages accepted (how dispatch events reach it), put this fish function in `~/.config/fish/functions/claude-dispatch.fish`:

```fish
function claude-dispatch --wraps claude --description 'claude with cross-session inbound and the local dispatch plugin'
    claude --settings '{"crossSessionInbound":"accept"}' --plugin-dir ~/Development/dispatch $argv
end
```

## Running the dispatcher

```sh
cd ~/Development/dispatch && go install ./cmd/dispatch
dispatch run
```

The [user guide](docs/guide.md) covers the setup this needs (herdr, treehouse, `gh`, the Linear API key and statuses), how a ticket flows, pausing, `dispatch tick`, `dispatch plan`, `dispatch trace`, `dispatch replay`, `dispatch state reset`, `dispatch tell`, and what to do when something breaks.
