# Config reference

dispatch reads `~/.config/dispatch/config.toml`. Every setting is optional, and a missing setting takes the default shown here. dispatch refuses to start a command on a file with a setting it doesn't know, and the error names the setting and the file, so a typo never falls back to a default unnoticed:

```
dispatch: while reading config /Users/you/.config/dispatch/config.toml: unknown setting max_in_progres
```

`dispatch tick`, `dispatch run` and `dispatch tell` all load the file this way. `dispatch ticket-hint` and `dispatch record-stop` run in every Claude Code session, so they print the error to stderr and exit 0 instead.

Every `toml` block in the docs is loaded by a test through the same loader, so these examples cannot drift from the code.

## Every setting at its default

```toml
# every setting at its default
interval_minutes = 5
max_in_progress = 4
launch_failure_limit = 2
nudge_limit = 2
blocked_renotify_ticks = 3
plugin_root = "~/Development/dispatch"
worker_model = "sonnet"
design_model = "opus"
auto_merge = []

[linear]
api_key_command = ""
api_key_keychain = "dispatch-linear-api-key"
teams = ["ENG"]
ai_label = "ai"
repo_label_group = "repo"

[linear.statuses]
todo = "Todo"
in_progress = "In Progress"
in_review = "In Review"
needs_human = "Needs Human"
close_parent = "Done"
blocker_done = "Done"
done = ["Done", "Canceled", "Duplicate"]
paused = ["Backlog", "Todo"]

[repos]
dispatch = "~/Development/dispatch"
```

`[repos]` has no default. Without it, every In Progress ticket escalates to Needs Human because its repo isn't configured. A leading `~` in `plugin_root` and in every `[repos]` path expands to your home directory.

## Top-level settings

| Setting | Default | What it does |
|---|---|---|
| `interval_minutes` | `5` | Minutes between ticks of `dispatch run`. `dispatch run --interval 30s` overrides it. It must be above zero. |
| `max_in_progress` | `4` | The most workers running at once. Caps running workers, and In Review and Needs Human tickets don't count. A launch counts In Progress tickets that have a worker, plus those in a repo whose pool couldn't be read this tick, and waits with `wait <ticket>: N of M workers already running`. Promoting a parent's children counts every In Progress ticket that isn't a parent, `design` tickets included, with or without a worker. A `design` ticket whose design worker has made children counts too, while its worker lives, and waits with `wait <ticket>: N of M In Progress tickets already`. |
| `launch_failure_limit` | `2` | Failed launches in a row before the ticket goes to Needs Human with the launcher's error. A full treehouse pool (`pool_exhausted`) never counts. |
| `nudge_limit` | `2` | PR nudges to an idle In Review worker with no push before the ticket goes to Needs Human. The count resets when a push moves the PR head, and it also caps relaunches of a gone worker that never pushes. In Progress tickets never read it, since an idle In Progress worker is nudged once per Stop and never escalated. |
| `blocked_renotify_ticks` | `3` | Ticks between macOS notifications for a worker that stays blocked on a question. The first notification comes on the first blocked tick. `0` notifies on every blocked tick. |
| `plugin_root` | `~/Development/dispatch` | Your checkout of this repo. Workers get `scripts/shims` first on their PATH and load the plugin with `--plugin-dir`, both from it. The shim runs the `dispatch` binary that launched the worker (the worker's `DISPATCH_BIN`), so a `dispatch` on PATH that is missing or older does not matter for launches. |
| `worker_model` | `sonnet` | The model name passed to `claude --model` for every build worker. |
| `design_model` | `opus` | The model name passed to `claude --model` for the worker of a `design` or `design-review` ticket. |
| `auto_merge` | `[]` | GitHub repos, as `owner/name`, where dispatch merges a ready PR itself once a review approves its head commit. See [Auto-merge](#auto-merge). An entry that isn't `owner/name` fails the config load. |

## Auto-merge

`auto_merge` names repos by their GitHub `owner/name`, not by a `[repos]` label or a path on disk, because the same repo can be cloned anywhere. Matching ignores case.

```toml
auto_merge = ["kapetan-io/dispatch"]
```

In a listed repo, a tick that finds an In Review ticket's PR ready (the same test as the ready-for-review notification, with the worker idle) and approved by a review on its head commit runs `gh pr merge <PR URL> --merge --match-head-commit <head>`. It prints `merge ENG-1: PR approved` and sends a macOS notification, `ENG-1 was approved and merged: <PR URL>`, in place of the ready-for-review one. Any reviewer's approval counts, which today means CodeRabbit's, unless another reviewer's latest review requests changes. The merge only goes through for the commit that was approved.

- An approval of an earlier commit doesn't count, so a push after approval waits for a new one.
- A ready PR with no approval on its head gets the ready-for-review notification as before. If the approval arrives later, the next tick merges it.
- A failed merge, such as branch protection refusing it, prints the error to stderr and sends one notification. The next ticks skip that head, and a push makes the PR eligible again.
- `dispatch tick --dry-run` prints the `merge` line without merging.
- dispatch reads the file on every tick, so adding or removing a repo takes effect on the next tick of a running `dispatch run`.
- An entry that matches no repo under `[repos]` is reported once per `dispatch run` as a warning on stderr. It is not an error.

The PR's repo is read from the PR's own URL, but the PR is found through the repo `gh` resolves for the clone. A clone with two remotes (`origin` plus a fork as `upstream`) can resolve to the wrong one, and `gh repo view` then names the fork. Run `gh repo set-default owner/name` in that clone.

## `[linear]`

| Setting | Default | What it does |
|---|---|---|
| `api_key_command` | none | A shell command that prints your Linear API key, such as `op read op://Work/linear/credential`. dispatch looks for the key in this order and stops at the first hit: the `LINEAR_API_KEY` environment variable, this command, then the keychain entry in `api_key_keychain`. With the command set, the keychain is never read. `dispatch run` runs the command once and keeps the key until it exits, so a password manager that prompts to unlock asks once. A command that fails or prints nothing stops the tick with an error naming the command and its exit status. |
| `api_key_keychain` | `dispatch-linear-api-key` | The macOS keychain service holding your Linear API key. The `LINEAR_API_KEY` environment variable wins when set. |
| `teams` | `["ENG"]` | Team keys dispatch manages. A ticket in another team is ignored, and a promoted child must be in one of these. |
| `ai_label` | `ai` | The label that opts a ticket in. dispatch touches only tickets assigned to the owner of the API key that carry it. |
| `repo_label_group` | `repo` | The Linear label group whose labels name a ticket's repo. A ticket's repo label must match a key in `[repos]`. |

## `[linear.statuses]`

Each value is the exact name of a workflow status in your Linear team. dispatch writes only `in_progress` (promoting a child), `needs_human` (escalating) and `close_parent`. The rest it reads.

| Setting | Default | What it does |
|---|---|---|
| `todo` | `Todo` | The status of a child waiting for promotion under an In Progress parent. |
| `in_progress` | `In Progress` | A worker should be running. Moving a ticket here is how you approve it. |
| `in_review` | `In Review` | The worker opened a PR and waits on it. |
| `needs_human` | `Needs Human` | The worker or dispatcher needs you. Every team in `teams` must have this status in the Started category, because dispatch looks it up by name when it escalates and reads only tickets in Started statuses. |
| `close_parent` | `Done` | What a parent becomes when all its children are finished. |
| `blocker_done` | `Done` | What a blocker must reach before its child is promoted. A Canceled or Duplicate blocker moves its child to Needs Human instead. |
| `done` | `["Done", "Canceled", "Duplicate"]` | Statuses of a finished ticket. Its worker is stopped and its tree returned, unless the tree holds modified or staged tracked files, or unpushed commits. Untracked files are deleted first. A parent closes when every child is in one of these. |
| `paused` | `["Backlog", "Todo"]` | Statuses of a ticket you parked. Its worker is stopped and its tree kept. |

## `[repos]`

Maps a repo label to the local clone dispatch launches workers from. The clone needs a `treehouse.toml`. The key is the label's name inside the `repo_label_group` label group.

```toml
[repos]
dispatch = "~/Development/dispatch"
```

## Examples

### One repo with defaults

The smallest useful file. Linear has an `ENG` team, an `ai` label, a `repo` label group with a `dispatch` label, and a `Needs Human` status.

```toml
[repos]
dispatch = "~/Development/dispatch"
```

### Several repos and teams

Two teams share one dispatcher. The cap is raised to 6, and ticks run every 10 minutes.

```toml
interval_minutes = 10
max_in_progress = 6

[linear]
teams = ["ENG", "OPS"]

[repos]
dispatch = "~/Development/dispatch"
webapp = "~/src/webapp"
infra = "/work/infra"
```

Each of `dispatch`, `webapp` and `infra` must exist as a label in the `repo` label group, and each clone needs a `treehouse.toml`.

### A workspace with different status names

A workspace that says "Backlog" for parked work, "Doing" for work in progress, "Blocked on me" for questions, and "Shipped" or "Won't do" for finished work. The label group is `project`, the opt-in label is `bot`, and the key lives in a different keychain entry.

```toml
worker_model = "opus"

[linear]
api_key_keychain = "acme-linear-key"
ai_label = "bot"
repo_label_group = "project"

[linear.statuses]
todo = "Ready"
in_progress = "Doing"
in_review = "In Review"
needs_human = "Blocked on me"
close_parent = "Shipped"
blocker_done = "Shipped"
done = ["Shipped", "Won't do"]
paused = ["Backlog", "Ready"]

[repos]
platform = "~/src/platform"
```

Keep every status you park tickets in inside `paused`. A ticket moved to a status listed in neither `paused` nor `done` keeps its running worker.
