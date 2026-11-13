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

`dispatch run`, `dispatch cycle` and `dispatch rules` load the file this way. `dispatch run` reads the file once, at start, and the rules with it, so a change to either takes effect when you stop `dispatch run` and start it again. `dispatch ticket-hint`, `dispatch record-stop` and `dispatch wake` run in every Claude Code session, so they print the error to stderr and exit 0 instead.

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
host = "tui"

[plugins.github]
[plugins.treehouse]
[plugins.notify]

# Writing this table turns local tickets on. Leave it out to keep them off.
[plugins.local]
path = ""

[repos.dispatch]
path = "~/Development/dispatch"
open_pr_as = "open"
ignore_comment_authors = []
ignore_optional_checks = false
approval_checks = []
auto_merge = false
github_remote = ""
worker = ""
design = ""
```

Repos have no default, so the block shows one repo with every setting at its default, and `path` is required. Without any `[repos.<label>]` table, every In Progress ticket escalates to Needs Human because its repo isn't configured. A leading `~` in `dispatch.rules`, `history.dir`, `plugins.herdr.plugin_root`, `plugins.rex.plugin_root`, `plugins.local.path` and in every repo's `path` expands to your home directory.

## `[dispatch]`

| Setting | Default | What it does |
|---|---|---|
| `rules` | `~/Development/dispatch/rules` | The directory of TypeScript rules that `dispatch run`, `dispatch rules check` and `dispatch rules test` read. `dispatch rules init` writes the shipped rules there. |
| `max_in_progress` | `4` | The most workers running at once. Caps running workers, and In Review and Needs Human tickets don't count. A launch counts In Progress tickets that have a worker, plus those in a repo whose pool couldn't be read this cycle, and waits, which `dispatch cycle` shows as `waiting` with `N of M in progress, 2nd in line`. Promoting a parent's children counts every In Progress ticket that isn't a parent, `design` tickets included, with or without a worker. A `design` ticket whose design worker has made children counts too, while its worker lives, and waits the same way. |
| `resync` | `5m` | Time between resyncs of `dispatch run`, such as `30s` or `5m`. A resync re-reads every source and runs a cycle, and it is the only clock rules wait on. `dispatch run` refuses a value of zero or less, and takes no flag for it. |

## `[history]`

| Setting | Default | What it does |
|---|---|---|
| `dir` | `~/.local/state/dispatch/events` | Where `dispatch run` keeps its history, one `events-<time>.jsonl` segment at a time. It is dispatch's only state, and `dispatch cycle`, `dispatch explain` and `dispatch replay` read it. |
| `rotate` | `6h` | How old the current segment gets before `dispatch run` starts a new one, which opens with a snapshot of what dispatch knew and did. |
| `keep` | `1d` | How long old segments are kept before `dispatch run` deletes them. Days are written `7d`. |

## `[rules]`

Your own table. Dispatch never interprets it, and a rule reads a key as `ctx.config.rules.<key>`. A rule that reads a key the file lacks fails `dispatch rules check`, naming the key. `dispatch rules init` writes the keys the shipped rules read, with today's values. `dispatch run` and `dispatch rules check` read them from the file when they start, so edit config.toml and then restart `dispatch run`. A key the file lacks is an error, not a default, for `launch_failure_limit`, `nudge_limit` and `blocked_renotify_minutes`: the rule that reads one throws, and `dispatch tick` prints the throw. The shipped rules fall back to `sonnet` and `opus` for a missing `worker_model` or `design_model`. The values below are what `init` writes.

| Key | Written by init | What it does |
|---|---|---|
| `worker_model` | `sonnet` | The model name passed to `claude --model` for every build worker. The shipped rules read it only when `worker` is unset, and use `sonnet` when the file lacks it. |
| `design_model` | `opus` | The model name passed to `claude --model` for the worker of a `design` or `design-review` ticket. The shipped rules read it only when `design` is unset, and use `opus` when the file lacks it. |
| `worker` | none | The `harness.<name>` table build workers launch on. A repo's `worker` wins. Unset (or `""`) means Claude Code on `worker_model`. `init` does not write it. |
| `design` | none | The same for `design` and `design-review` workers, falling back to `design_model`. |
| `launch_failure_limit` | `2` | Failed launches in a row before the ticket goes to Needs Human with the launcher's error. A full treehouse pool (`pool_exhausted`) never counts. A launch tries a treehouse lease and a `git switch` once, and a failure of either (`lease_failed`, `switch_failed`) never counts. The next resync launches again, and `dispatch run` prints the error at every resync it fails ([ADR 7](adr/0007-the-resync-is-the-only-retry.md)). A launch that fails after `dispatch run` was asked to stop (Ctrl-C or SIGTERM) never counts, and `dispatch explain` shows it as `interrupted`. Otherwise a failed pool read, a lease returned without a path, a wait for another launch that was cut off and a failed stack-record write still count. Closing a Claude whose launch died before its kickoff ran counts as one failure. |
| `nudge_limit` | `2` | PR nudges to an idle In Review worker with no push before the ticket goes to Needs Human. The count resets when a push moves the PR head, and it also caps relaunches of a gone worker that never pushes. In Progress tickets never read it, since an idle In Progress worker is never nudged. |
| `blocked_renotify_minutes` | `15` | Minutes between notifications for a worker that stays blocked on a question, a notification can only go out in a cycle, so it can come later than the period by up to one `resync`. The first notification comes on the first cycle that sees the worker blocked. `0` notifies in every cycle while it is blocked. |

```toml
[rules]
worker_model = "opus"
nudge_limit = 3
```

## `[harness.<name>]`

A harness table pairs the program that runs in a worker's session with a model. Claude Code is the only harness, so `kind` is `claude-code`. Both keys are required.

```toml
[harness.claude-sonnet]
kind = "claude-code"
model = "sonnet"

[harness.claude-opus]
kind = "claude-code"
model = "opus"

[rules]
worker = "harness.claude-sonnet"
design = "harness.claude-opus"

[repos.dispatch]
path = "~/Development/dispatch"
worker = "harness.claude-opus"
```

The shipped rules launch a build worker on the table that `[repos.<label>].worker` names, else the one `[rules].worker` names, else Claude Code with `worker_model`. Design workers do the same with `design` and `design_model`. A rule can also pass `harness: "harness.<name>"` to `queueLaunch`, and `dispatch launch --harness harness.<name>` launches on a table. Rules never read the tables, only the names.

The load fails, naming the key or table, when a `worker` or `design` key isn't of the form `harness.<name>`, names a table the file lacks, or when a table has a missing or unknown `kind` or a missing `model`:

```
config: rules.worker names harness.claude-snnet, which has no [harness.claude-snnet] table
```

A `[rules]` table that already holds its own `worker` or `design` key fails the same way, since those keys now name a table.

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
| `plugin_root` | `~/Development/dispatch` | Your checkout of this repo. Workers get `scripts/shims` first on their PATH and load the plugin with `--plugin-dir`, both from it. Your own sessions load it the same way when you put `scripts/shims` first on your PATH, because the shim falls back to this setting when `DISPATCH_PLUGIN_ROOT` is unset. For a worker the shim runs the `dispatch` binary that launched it (the worker's `DISPATCH_BIN`), so a `dispatch` on PATH that is missing or older does not matter for launches. |
| `host` | `tui` | The herdr product that runs your workers. `tui` runs them in the herdr terminal UI. `app` runs each worker as a herdr-core terminal that the Herdr app shows, and keeps workers running when the terminal dispatch runs in closes. It needs `hc` on PATH. It also needs `herdr-app` on PATH and the app window open to name each worker's space `<emoji> <ticket>` and its tab `<ticket> <title>`, and to close a space left empty. With no window open, workers still run, the naming and closing are skipped without an error, and names catch up at the first resync after the window opens. A space left empty while the window was closed stays until you close it. Any other value stops dispatch at config load. Rules never see this setting in `ctx.config`. Before changing `host`, let running workers finish or close their windows. Dispatch drives only the configured host: a worker left running on the old host is invisible to dispatch, which launches a second one on the same tree. |

`[plugins.github]`, `[plugins.treehouse]` and `[plugins.notify]` take no settings yet. Write the empty table to say the plugin is on.

## `[plugins.rex]`

Writing the table, even empty, runs your workers in Rex instead of herdr. It cannot sit beside `[plugins.herdr]`, and a config with both stops dispatch at load, because workers run in one host.

```toml
[plugins.rex]
plugin_root = "~/Development/dispatch"
delete_empty_sessions = false
```

| Setting | Default | What it does |
|---|---|---|
| `plugin_root` | `~/Development/dispatch` | Your checkout of this repo, used as `[plugins.herdr]`'s `plugin_root` is. |
| `delete_empty_sessions` | `false` | When a worker's close leaves its repo session with no tab or block at all, delete the session. Off, the emptied session stays. A session that still holds a block of yours or another worker's, including one outside every window, is never deleted, and neither is one this close removed nothing from. Leave it off if your Rex server runs with `--exit-empty` and you don't keep the Rex app open: deleting the last session stops that server, and dispatch never starts it again. |

Rex has to be open. Dispatch never starts it, and a command that needs it exits saying "Rex is not running". It finds `rex` on PATH, then in `/Applications/Rex Beta.app`. Each repo gets a Rex session named for the folder of its main checkout, and each worker is a block in a window of that session. A close ends the worker and, unless `delete_empty_sessions` is on, leaves the session.

## `[plugins.local]`

Local tickets are tickets kept in dispatch's own database, beside Linear's. Writing the table, even empty, turns them on. Without it dispatch creates and opens no database, and `dispatch ticket` exits 1 saying local tickets are off.

```toml
[plugins.local]
# path = "~/.local/state/dispatch/tickets.db"
```

| Setting | Default | What it does |
|---|---|---|
| `path` | `tickets.db` in the state directory | The database file. A leading `~` is expanded. The first `dispatch run` creates it with mode 0600. Rules never see this setting in `ctx.config`. |

With `dispatch run` going, file a ticket for a configured repo and start it. `create` prints the new id, and a ticket starts in the status that does `todo`:

```
$ dispatch ticket create --repo dispatch --title "Retry webhooks on 502"
LOC-1
$ dispatch ticket show LOC-1
LOC-1 Retry webhooks on 502
status      Todo
repo        dispatch
branch      loc-1-retry-webhooks-on-502
filed by    you
labels      -
attributes  -
parent      -
blocked by  -
start PR    -
$ dispatch ticket move LOC-1 "In Progress"
LOC-1 In Progress
```

The shipped rules then launch a worker on that branch at the next resync. `dispatch ticket` reaches the database through the running `dispatch run`, so it exits 3 with `dispatch: not running` when none runs.

- There is no status setting. A new database is seeded once with Backlog, Todo, In Progress, In Review, Needs Human, Done and Canceled, each with the jobs `dispatch ticket statuses` lists, and dispatch never changes them afterwards. Each of the jobs `todo`, `in_progress`, `in_review`, `needs_human`, `close_parent` and `blocker_done` must belong to exactly one status, or `dispatch run` stops at start.
- Only the `dispatch run` that holds `dispatch.lock` opens the file, and a second `dispatch run` that points `path` at the same file stops after 5 seconds. Every other session reaches it through `dispatch ticket`. Stop `dispatch run` before copying the file.
- `dispatch run` also stops at start when the file's next ticket number is at or below a `LOC-<n>` that history or a tree's lease names, or when the file holds a ticket in a status it lacks, a link to a missing ticket or a parent loop.
- Linear ids that start with `LOC-` fail a Linear read while this table is on, so no Linear team may use the key `LOC`.
- A local ticket moved to In Progress gets a worker as a Linear ticket does, under the same `max_in_progress`, and the shipped rules nudge, escalate, promote, merge and tear down its worker the same way. The shipped rules launch no worker on a local ticket labeled `design` or `design-review`. A local ticket whose PR merged into the repo's default branch moves to the status that does `blocker_done` (Done in a new database) at the next resync, from any status outside a finished one, and its tree is returned and its workspace closed. A merge from a fork, into another branch, or from before the ticket entered its current status closes nothing. A local ticket with `--start-pr N` gets a tree on its own branch at the head of the repo's PR N, a fork's PR included, fetched from `origin`. An existing branch ignores it, and a PR that does not exist fails the launch. The PR is never the ticket's own: the ticket's PR is found by its branch.

## `[plugins.linear]`

| Setting | Default | What it does |
|---|---|---|
| `api_key_command` | none | A shell command that prints your Linear API key, such as `op read op://Work/linear/credential`. dispatch looks for the key in this order and stops at the first hit: the `LINEAR_API_KEY` environment variable, this command, then the keychain entry in `api_key_keychain`. With the command set, the keychain is never read. `dispatch run` runs the command once and keeps the key until Linear rejects it or the process exits, so a password manager that prompts to unlock asks once and a rotated key is picked up at the next read. Rules never see this setting in `ctx.config`. A command that fails or prints nothing stops `dispatch run` at start with an error naming the command and its exit status. One that runs longer than a minute, such as a password manager waiting on an unlock prompt, is killed and the error says it timed out. Start `dispatch run` again to run it again. |
| `api_key_keychain` | `dispatch-linear-api-key` | The macOS keychain service holding your Linear API key. Linux has no keychain lookup, so use `LINEAR_API_KEY` or `api_key_command` there. The `LINEAR_API_KEY` environment variable wins when set. Rules never see this setting in `ctx.config`. |
| `teams` | `["ENG"]` | Team keys dispatch manages. A ticket in another team is ignored, and a promoted child must be in one of these. |
| `ai_label` | `ai` | The label that opts a ticket in. dispatch touches only tickets assigned to the owner of the API key that carry it. |
| `repo_label_group` | `repo` | The Linear label group whose labels name a ticket's repo. A ticket's repo label must match a `[repos.<label>]` table, ignoring case. |

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
| `open_pr_as` | `"open"` | `"open"` or `"draft"`. With `"draft"`, workers open the PR as a draft and never mark it open, and a ready draft gets one "ENG-1's draft is ready for your review" notification per commit. Once you open it, the PR waits on its approvers (⏳) and sends nothing, then on whoever merges it (🔀). With `auto_merge` off, an approved PR gets one "ENG-1 is approved and ready to merge" notification per commit. Dispatch never merges a draft in any repo. |
| `ignore_comment_authors` | `[]` | GitHub logins, matched without regard to case, whose comments, reviews, approvals, change requests and review threads dispatch and the worker ignore. They draw no nudge, never keep a PR from being ready, and never count as an approval. A thread still counts when one of its last 20 comments is from someone else. |
| `ignore_optional_checks` | `false` | When `true`, only checks GitHub requires for merging count, for dispatch and the worker. A failing or never-finishing check GitHub doesn't require draws no nudge, doesn't hold back ready and doesn't mute a change-request nudge. A branch that requires no checks has no check that counts. |
| `approval_checks` | `[]` | Names of checks that pass only after a human approves the PR, such as `"Mergify Merge Protections"`, matched by full name without regard to case. They draw no nudge and never hold back ready, so a draft waiting for review is still reported ready, and a PR you opened stays at ⏳ until the check passes. A review's approval counts for nothing while one has not passed, so `auto_merge` waits for the check to pass. A failing or running build check still counts, even beside an approval check. Like `ignore_optional_checks`, it makes dispatch read each check of a PR that isn't passing, so such a PR with more than 100 checks fails to read. |
| `auto_merge` | `false` | Merge a ready PR once a review approves its head commit. See [Auto-merge](#auto-merge). |
| `github_remote` | none | The remote of this clone whose GitHub repo an inbox item must name for the item's `repo` to be this label. Unset (or `""`) means `origin`, so a clone with `origin` and `upstream` matches only `origin`'s repo. A fork clone whose inbox items come from the upstream repo sets `"upstream"`. A remote that is missing or not on github.com matches nothing. Dispatch reads it with `git config` only while a loaded rule runs over `inbox`. See [Reading the GitHub inbox](rules.md#reading-the-github-inbox). |
| `worker` | none | The harness table, written `harness.<name>`, that this repo's build workers launch on. Unset (or `""`) means the `[rules]` value. See [`[harness.<name>]`](#harnessname). `dispatch repo-settings` prints nothing for a key that is unset. |
| `design` | none | The same for the workers of `design` and `design-review` tickets. |

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

- A draft is never merged. In a repo with `open_pr_as = "draft"`, an approved PR you open merges in the next cycle when it is ready, with no ready notification.
- A check listed in `approval_checks` that hasn't passed holds the merge back, even when a review has approved the head, because a bot's approval doesn't clear a gate that waits on a person. The merge goes through in the cycle after the check passes.
- An approval of an earlier commit doesn't count, so a push after approval waits for a new one.
- A ready PR with no approval on its head gets the ready-for-review notification as before in a repo that opens PRs as open. In a repo that opens drafts it shows ⏳ and sends nothing. If the approval arrives later, the next cycle merges it.
- A merge GitHub refuses, such as branch protection refusing it, prints the error to stderr and sends one notification. Later cycles skip that head, and a push makes the PR eligible again. A merge that fails before it reaches GitHub, such as a Linear read that failed or a ticket that changed since the cycle, is tried again at the next resync.
- A merge GitHub refuses with "Base branch was modified" happens when another PR merged into the base a moment before. dispatch tries it again at the first resync a minute or more later, up to 5 more times, and sends the notification only if the last try is refused too. A retry needs the merge conditions to still hold, so a PR whose approval is withdrawn between tries gets no notification and no further try. Two PRs that become ready together both land without a push.
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
