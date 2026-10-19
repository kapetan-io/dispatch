# Config reference

dispatch reads `~/.config/dispatch/config.toml`. Every setting is optional, and a missing setting takes the default shown here. dispatch refuses to start a command on a file with a setting it doesn't know, and the error names the setting and the file, so a typo never falls back to a default unnoticed:

```
dispatch: while reading config /Users/you/.config/dispatch/config.toml: unknown setting max_in_progres
```

With several unknown settings, the error names the file once and lists each setting on its own line:

```
dispatch: while reading config /Users/you/.config/dispatch/config.toml: unknown settings:
  interval_minutes
  max_in_progress
```

`dispatch run`, `dispatch plan` and `dispatch rules` load the file this way. `dispatch run` reads the file once, at start, and the rules with it, so a change to either takes effect when you stop `dispatch run` and start it again. `dispatch ticket-hint` and `dispatch record-stop` run in every Claude Code session, so they print the error to stderr and exit 0 instead.

Every `toml` block in the docs is loaded by a test through the same loader, so these examples cannot drift from the code.

## Every setting at its default

```toml
# every setting at its default
[dispatch]
rules = "~/Development/dispatch/rules"
max_in_progress = 4
resync = "5m"

[history]
dir = "~/.local/state/dispatch/events"
rotate = "6h"
keep = "1d"

[plugins.linear]
api_key_command = ""
api_key_keychain = "dispatch-linear-api-key"
teams = ["ENG"]
ai_label = "ai"
repo_label_group = "repo"

[plugins.linear.statuses]
todo = "Todo"
in_progress = "In Progress"
in_review = "In Review"
needs_human = "Needs Human"
close_parent = "Done"
blocker_done = "Done"
finished = ["Done", "Canceled", "Duplicate"]
paused = ["Backlog", "Todo"]

[plugins.herdr]
plugin_root = "~/Development/dispatch"

[plugins.github]
[plugins.treehouse]
[plugins.notify]

[repos.dispatch]
path = "~/Development/dispatch"
open_pr_as = "open"
ignore_comment_authors = []
ignore_optional_checks = false
approval_checks = []
auto_merge = false
```

Repos have no default, so the block shows one repo with every setting at its default, and `path` is required. Without any `[repos.<label>]` table, every In Progress ticket escalates to Needs Human because its repo isn't configured. A leading `~` in `dispatch.rules`, `history.dir`, `plugins.herdr.plugin_root` and in every repo's `path` expands to your home directory.

## `[dispatch]`

| Setting | Default | What it does |
|---|---|---|
| `rules` | `~/Development/dispatch/rules` | The directory of TypeScript rules that `dispatch run`, `dispatch rules check` and `dispatch rules test` read. `dispatch rules init` writes the shipped rules there. |
| `max_in_progress` | `4` | The most workers running at once. Caps running workers, and In Review and Needs Human tickets don't count. A launch counts In Progress tickets that have a worker, plus those in a repo whose pool couldn't be read this cycle, and waits, which `dispatch plan` shows as `waiting` with `N of M in progress, 2nd in line`. Promoting a parent's children counts every In Progress ticket that isn't a parent, `design` tickets included, with or without a worker. A `design` ticket whose design worker has made children counts too, while its worker lives, and waits the same way. |
| `resync` | `5m` | Time between resyncs of `dispatch run`, such as `30s` or `5m`. A resync re-reads every source and runs a cycle, and it is the only clock rules wait on. `dispatch run` refuses a value of zero or less, and takes no flag for it. |

## `[history]`

| Setting | Default | What it does |
|---|---|---|
| `dir` | `~/.local/state/dispatch/events` | Where `dispatch run` keeps its history, one `events-<time>.jsonl` segment at a time. It is dispatch's only state, and `dispatch plan`, `dispatch trace` and `dispatch replay` read it. |
| `rotate` | `6h` | How old the current segment gets before `dispatch run` starts a new one, which opens with a snapshot of what dispatch knew and did. |
| `keep` | `1d` | How long old segments are kept before `dispatch run` deletes them. Days are written `7d`. |

## `[rules]`

Your own table. Dispatch never interprets it, and a rule reads a key as `ctx.config.rules.<key>`. A rule that reads a key the file lacks fails `dispatch rules check`, naming the key. `dispatch rules init` writes the five keys the shipped rules read, with today's values. `dispatch run` and `dispatch rules check` read them from the file when they start.

| Key | Default | What it does |
|---|---|---|
| `worker_model` | `sonnet` | The model name passed to `claude --model` for every build worker. |
| `design_model` | `opus` | The model name passed to `claude --model` for the worker of a `design` or `design-review` ticket. |
| `launch_failure_limit` | `2` | Failed launches in a row before the ticket goes to Needs Human with the launcher's error. A full treehouse pool (`pool_exhausted`) never counts. Closing a Claude whose launch died before its kickoff ran counts as one failure. |
| `nudge_limit` | `2` | PR nudges to an idle In Review worker with no push before the ticket goes to Needs Human. The count resets when a push moves the PR head, and it also caps relaunches of a gone worker that never pushes. In Progress tickets never read it, since an idle In Progress worker is nudged once per Stop and never escalated. |
| `blocked_renotify_minutes` | `15` | Minutes between notifications for a worker that stays blocked on a question, a notification can only go out in a cycle, so it can come later than the period by up to one `resync`. The first notification comes on the first cycle that sees the worker blocked. `0` notifies in every cycle while it is blocked. |

```toml
[rules]
worker_model = "opus"
nudge_limit = 3
```

## `[http.<name>]`

A service a rule may call with `ctx.http`. Each table needs a `base_url`, an `http` or `https` URL with a host and no user, query or fragment. `token_command` prints the service's token, and is optional. Dispatch runs it the first time a rule calls the service and keeps the token in memory until the service answers 401, then runs it again on the next call.

```toml
[http.sentry]
base_url = "https://sentry.io/api/0"
token_command = "op read op://dev/sentry/token"
```

A rule names the service and a path, and dispatch sends `Authorization: Bearer <token>` itself. The token never reaches a rule, history or an error, and `ctx.config.http` leaves `token_command` out. The path must start with `/` and stay under the `base_url` path, so `/issues/` on the service above is `https://sentry.io/api/0/issues/`. A path such as `//other.host/x` or `/../admin` throws at the call, and so does a service this file doesn't name. A redirect is never followed.

## `[plugins.herdr]`

| Setting | Default | What it does |
|---|---|---|
| `plugin_root` | `~/Development/dispatch` | Your checkout of this repo. Workers get `scripts/shims` first on their PATH and load the plugin with `--plugin-dir`, both from it. The shim runs the `dispatch` binary that launched the worker (the worker's `DISPATCH_BIN`), so a `dispatch` on PATH that is missing or older does not matter for launches. |

`[plugins.github]`, `[plugins.treehouse]` and `[plugins.notify]` take no settings yet. Write the empty table to say the plugin is on.

## `[plugins.linear]`

| Setting | Default | What it does |
|---|---|---|
| `api_key_command` | none | A shell command that prints your Linear API key, such as `op read op://Work/linear/credential`. dispatch looks for the key in this order and stops at the first hit: the `LINEAR_API_KEY` environment variable, this command, then the keychain entry in `api_key_keychain`. With the command set, the keychain is never read. `dispatch run` runs the command once and keeps the key until Linear rejects it or the process exits, so a password manager that prompts to unlock asks once and a rotated key is picked up at the next read. A command that fails or prints nothing stops `dispatch run` at start with an error naming the command and its exit status. One that runs longer than a minute, such as a password manager waiting on an unlock prompt, is killed and the error says it timed out. Start `dispatch run` again to run it again. |
| `api_key_keychain` | `dispatch-linear-api-key` | The macOS keychain service holding your Linear API key. Linux has no keychain lookup, so use `LINEAR_API_KEY` or `api_key_command` there. The `LINEAR_API_KEY` environment variable wins when set. |
| `teams` | `["ENG"]` | Team keys dispatch manages. A ticket in another team is ignored, and a promoted child must be in one of these. |
| `ai_label` | `ai` | The label that opts a ticket in. dispatch touches only tickets assigned to the owner of the API key that carry it. |
| `repo_label_group` | `repo` | The Linear label group whose labels name a ticket's repo. A ticket's repo label must match a `[repos.<label>]` table. |

## `[plugins.linear.statuses]`

Each value is the exact name of a workflow status in your Linear team. dispatch writes only `in_progress` (promoting a child), `needs_human` (escalating) and `close_parent`. The rest it reads.

| Setting | Default | What it does |
|---|---|---|
| `todo` | `Todo` | The status of a child waiting for promotion under an In Progress parent. |
| `in_progress` | `In Progress` | A worker should be running. Moving a ticket here is how you approve it. |
| `in_review` | `In Review` | The worker opened a PR and waits on it. |
| `needs_human` | `Needs Human` | The worker or dispatcher needs you. Every team in `teams` must have this status in the Started category, because dispatch looks it up by name when it escalates and reads only tickets in Started statuses. |
| `close_parent` | `Done` | What a parent becomes when all its children are finished. |
| `blocker_done` | `Done` | What a blocker must reach before its child is promoted. A Canceled or Duplicate blocker moves its child to Needs Human instead. |
| `finished` | `["Done", "Canceled", "Duplicate"]` | Statuses of a finished ticket. Its worker is stopped and its tree returned, unless the tree holds modified or staged tracked files, or unpushed commits. Untracked files are deleted first. A parent closes when every child is in one of these. |
| `paused` | `["Backlog", "Todo"]` | Statuses of a ticket you parked. Its worker is stopped and its tree kept. |

## `[repos.<label>]`

One table per repo. The label is the repo's name inside the `repo_label_group` label group, and the table holds the repo's settings. A repo with only `path` behaves as dispatch always has.

```toml
[repos.dispatch]
path = "~/Development/dispatch"
auto_merge = true
```

| Setting | Default | What it does |
|---|---|---|
| `path` | required | The local clone dispatch launches workers from. The clone needs a `treehouse.toml`. Two repos can't share a path. |
| `open_pr_as` | `"open"` | `"open"` or `"draft"`. With `"draft"`, workers open the PR as a draft and never mark it open, and a ready PR gets one notification per commit in each phase, "ENG-1's draft is ready for your review" while it is a draft and "ENG-1's reviews are handled" once you open it. Dispatch never merges a draft in any repo. |
| `ignore_comment_authors` | `[]` | GitHub logins, matched without regard to case, whose comments, reviews, approvals, change requests and review threads dispatch and the worker ignore. They draw no nudge, never keep a PR from being ready, and never count as an approval. A thread still counts when one of its last 20 comments is from someone else. |
| `ignore_optional_checks` | `false` | When `true`, only checks GitHub requires for merging count, for dispatch and the worker. A failing or never-finishing check GitHub doesn't require draws no nudge, doesn't hold back ready and doesn't mute a change-request nudge. A branch that requires no checks has no check that counts. |
| `approval_checks` | `[]` | Names of checks that pass only after a human approves the PR, such as `"Mergify Merge Protections"`, matched by full name without regard to case. They draw no nudge and never hold back ready, so a draft waiting for review is still reported ready. A review's approval counts for nothing while one has not passed, so `auto_merge` waits for the check to pass. A failing or running build check still counts, even beside an approval check. Like `ignore_optional_checks`, it makes dispatch read each check of a PR that isn't passing, so such a PR with more than 100 checks fails to read. |
| `auto_merge` | `false` | Merge a ready PR once a review approves its head commit. See [Auto-merge](#auto-merge). |

The load fails, naming the repo and the fix, on a `label = "path"` entry under `[repos]`, a table without `path`, two tables with one `path`, an unknown key in a table, an `open_pr_as` other than `"open"` or `"draft"`, and a top-level `auto_merge`.

Inside a configured repo's clone or a worktree of it, `dispatch repo-settings` prints that repo's table with every setting at its effective value, in a form the config accepts:

```
$ dispatch repo-settings
[repos.dispatch]
path = "/Users/you/Development/dispatch"
open_pr_as = "open"
ignore_comment_authors = []
ignore_optional_checks = false
approval_checks = []
auto_merge = true
```

Anywhere else it fails with `dispatch: /tmp is not inside a repo under [repos] in /Users/you/.config/dispatch/config.toml`.

## Auto-merge

In a repo with `auto_merge = true`, a cycle that finds an In Review ticket's PR ready (the same test as the ready-for-review notification, with the worker idle) and approved by a review on its head commit runs `gh pr merge <PR URL> --merge --match-head-commit <head>`. The run window prints `merge ENG-1 (auto-merge)` when the merge starts and `merge ENG-1 done` when it ends, and dispatch sends a notification, `ENG-1 was approved and merged: <PR URL>`, in place of the ready-for-review one. Any reviewer's approval counts, which today means CodeRabbit's, unless another reviewer's latest review requests changes. The merge only goes through for the commit that was approved.

- A draft is never merged. In a repo with `open_pr_as = "draft"`, an approved PR you open merges in the next cycle when it is ready, without the "reviews are handled" notification.
- A check listed in `approval_checks` that hasn't passed holds the merge back, even when a review has approved the head, because a bot's approval doesn't clear a gate that waits on a person. The merge goes through in the cycle after the check passes.
- An approval of an earlier commit doesn't count, so a push after approval waits for a new one.
- A ready PR with no approval on its head gets the ready-for-review notification as before. If the approval arrives later, the next cycle merges it.
- A merge GitHub refuses, such as branch protection refusing it, prints the error to stderr and sends one notification. Later cycles skip that head, and a push makes the PR eligible again. A merge that fails before it reaches GitHub, such as a Linear read that failed or a ticket that changed since the cycle, is tried again at the next resync.
- `dispatch rules check` shows whether the `merge` action is wanted now and takes no action.
- dispatch reads the config once, when `dispatch run` starts, so a change to a repo's `auto_merge` takes effect when you stop `dispatch run` and start it again.

The PR is found through the repo `gh` resolves for the clone. A clone with two remotes (`origin` plus a fork as `upstream`) can resolve to the wrong one. Run `gh repo set-default owner/name` in that clone.

## Examples

### One repo with defaults

The smallest useful file. Linear has an `ENG` team, an `ai` label, a `repo` label group with a `dispatch` label, and a `Needs Human` status.

```toml
[repos.dispatch]
path = "~/Development/dispatch"
```

### Several repos and teams

Two teams share one dispatcher. The cap is raised to 6, and resyncs run every 10 minutes.

```toml
[dispatch]
max_in_progress = 6
resync = "10m"

[plugins.linear]
teams = ["ENG", "OPS"]

[repos.dispatch]
path = "~/Development/dispatch"

[repos.webapp]
path = "~/src/webapp"
auto_merge = true

[repos.infra]
path = "/work/infra"
```

Each of `dispatch`, `webapp` and `infra` must exist as a label in the `repo` label group, and each clone needs a `treehouse.toml`.

### A workspace with different status names

A workspace that says "Backlog" for parked work, "Doing" for work in progress, "Blocked on me" for questions, and "Shipped" or "Won't do" for finished work. The label group is `project`, the opt-in label is `bot`, and the key lives in a different keychain entry.

```toml
[rules]
worker_model = "opus"

[plugins.linear]
api_key_keychain = "acme-linear-key"
ai_label = "bot"
repo_label_group = "project"

[plugins.linear.statuses]
todo = "Ready"
in_progress = "Doing"
in_review = "In Review"
needs_human = "Blocked on me"
close_parent = "Shipped"
blocker_done = "Shipped"
finished = ["Shipped", "Won't do"]
paused = ["Backlog", "Ready"]

[repos.platform]
path = "~/src/platform"
```

Keep every status you park tickets in inside `paused`. A ticket moved to a status listed in neither `paused` nor `finished` keeps its running worker.
