# dispatch

An autonomous build workflow for Claude Code, packaged as a Claude Code plugin.

You design a feature with Claude in an interactive session. An autonomous review then checks the design and splits it into small Linear build tickets. Once you approve, a stateless dispatcher, running every 5 minutes in a herdr window you keep open, starts a Claude worker for each ticket in its own worktree, and each worker carries its ticket to a merged pull request.

## Status

Under construction. The design is in `docs/design/`. Start with `decision-log.md`, then `blueprint.md` (the dispatcher) and the handoff. The workflow is being built one piece at a time. The first piece is the dispatcher launching a build worker for a ticket you approve.

## Layout

- `.claude-plugin/plugin.json` is the plugin manifest. Skills live in `skills/` (`build` runs a ticket, `ticket` writes one), scripts on the session PATH in `bin/`, and the plugin's hooks in `hooks/`. `hooks/hooks.json` runs `dispatch ticket-hint` at session start (silently skipped when `dispatch` is not installed), which points an agent in a dispatch-managed repo at `dispatch:ticket` because a long skill list can show a skill by name only.
- `cmd/` and `internal/` hold the `dispatch` Go CLI.
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

It ticks now and every `interval_minutes` after. Each tick prints one line per action, then a summary such as `16:05:07 tick: 0 tickets, 0 actions, Linear 1 call in 240ms (2499 of 2500 requests left this hour), took 405ms`. Ctrl-C stops it. To check what a tick would do without acting, run `dispatch tick --dry-run`.

## Nudges, notifications and `tell`

While `dispatch run` ticks, an In Progress ticket whose worker is idle or finished gets the stall nudge typed into its session. After `nudge_limit` nudges (default 2) with no status change, the ticket goes to Needs Human with one comment. A ticket you move from Needs Human back to In Progress gets the resume nudge, with no comment.

An In Review ticket whose worker is idle gets the PR nudge when its PR has a failed check, changes requested, an unresolved review thread or a merge conflict. The nudge names the problem and tells the worker to handle it as its build skill's step 7 says. A worker that is gone is relaunched on its existing tree, then nudged on the tick after it goes idle. After `nudge_limit` PR nudges with no push, the ticket goes to Needs Human with one comment, and a push that moves the PR head resets the count. The dispatcher reads PRs through `gh`, so it must be authenticated.

A worker blocked on a question (AskUserQuestion), on an In Progress or In Review ticket, fires a macOS notification (through `osascript`) on its first blocked tick and again every `blocked_renotify_ticks` ticks (default 3) while it stays blocked. The dispatcher never answers the question and never sets Needs Human for it. A ticket entering Needs Human fires one notification, a parent included.

## Parents

Move a parent ticket to In Progress and the dispatcher starts its children for you. Each tick it reads every child of an In Progress parent and acts in this order.

- **Close.** A parent whose children are all Done, Canceled or Duplicate is moved to `close_parent` (default Done). A parent whose children couldn't be read is never closed.
- **Promote.** A child in `todo` (default Todo) that is assigned to you, carries the `ai` label and has no unfinished blocker moves to In Progress, and the next tick launches its worker. A child waits until every blocker reaches `blocker_done` (default Done), so a Canceled blocker holds it. A child that holds a treehouse lease was paused with its tree kept, so it stays in Todo until you move it to In Progress yourself. A child whose blocker list ran past one page waits too.
- **Cap.** Promotion stops when the In Progress tickets other than parents reach `max_in_progress`. A child waiting for a slot is promoted on a later tick, and the tick prints `wait <child>` each time.

A parent never gets a worker, whatever its status. Parents labeled `design` or `design-review` are left alone until the grill and review skills exist. If the children can't be read, the tick prints the error and skips promotion and closing, and everything else runs as usual. Promotion is the only way the dispatcher moves a ticket to In Progress.

## Teardown and pausing

Each tick also looks at every tree held by a ticket assigned to you with the `ai` label, whatever the ticket's status. Statuses come from `[linear.statuses]`: `done` (default Done, Canceled, Duplicate) and `paused` (default Backlog, Todo).

- A finished ticket whose tree is clean and fully pushed has its herdr workspace closed and its tree returned with `treehouse return --force`, so `treehouse status` shows the slot available.
- A finished ticket whose tree has uncommitted changes, or commits that no remote has, keeps its tree. Its worker's workspace is closed, and the ticket gets one comment saying what the tree holds. Push or discard the work and the next tick returns the tree.
- A ticket moved back to Backlog or Todo has its worker's workspace closed and keeps its tree. Moving it to In Progress again relaunches a worker on that tree.

"Pushed" means every commit reachable from the tree's HEAD is also reachable from a remote-tracking ref. A branch squash-merged and then pruned from the remote counts as unpushed, so its tree stays leased with the comment until you discard it. A pause or a kept tree closes only a workspace with a Claude agent in it, so a workspace you opened by hand there stays open. A clean tree is returned whatever workspace is open on it, because a worker whose Claude exited leaves an agentless workspace behind and the slot has to come back.

Trees of `design` and `design-review` tickets are left alone until the grill and review skills exist. A tree whose git state can't be read, or whose ticket has more labels than the dispatcher reads, stays leased, and the tick prints why. If the lookup of tickets holding trees fails, the tick prints the error and skips teardown but still launches and nudges.

To type into a worker yourself:

```sh
dispatch tell ENG-228 "use the v2 endpoint"
```

It fails when the ticket's worker is blocked on a question or absent.
