# dispatch user guide

dispatch runs Claude workers on your approved Linear tickets. You approve a ticket by moving it to In Progress, and a stateless dispatcher, ticking every few minutes in a herdr window you keep open, starts a worker in its own treehouse worktree. The worker carries the ticket to a merged pull request. Every setting is in the [config reference](config.md).

## Set it up

You need a Mac with these on your PATH. dispatch shells out to all of them.

| Tool | Why |
|---|---|
| `herdr` 0.9.0 or later | Hosts one workspace per worker, and reports whether each worker is working, idle or blocked. |
| `treehouse` v3.1.2 or later (`go install github.com/kunchenguid/treehouse/v3@latest`) | Leases a pooled git worktree to each worker. |
| `gh`, signed in with `gh auth login` | The dispatcher reads PR checks, reviews and conflicts, and merges approved PRs in `auto_merge` repos. Workers open PRs with it. |
| `claude` | The worker itself. |
| `git` | Used by `dispatch launch` to create the ticket's branch in its tree. |
| `go` | Builds the `dispatch` CLI. |

**1. Install the CLI** from your checkout of this repo, which is also the plugin workers load:

```sh
cd ~/Development/dispatch && go install ./cmd/dispatch
```

Put `$GOBIN` (default `~/go/bin`) on your PATH. `bin/dispatch-launch` runs `dispatch launch`, so it needs the binary there.

**2. Put your Linear API key in the keychain.** Create a personal API key in Linear under Settings, then Security & access. Store it under the service name `dispatch-linear-api-key`:

```sh
security add-generic-password -s dispatch-linear-api-key -a "$USER" -w
```

The command prompts for the key. Setting `LINEAR_API_KEY` in the environment overrides the keychain, and `api_key_keychain` in the config names a different service. dispatch manages only tickets assigned to the owner of this key.

**3. Prepare Linear.** dispatch finds your tickets by status and label, so each team in your config needs:

- the statuses Todo, In Progress, In Review, Done, Canceled, Duplicate and Backlog, which a default Linear team has, and a **Needs Human** status, which it doesn't. Create it in the team's workflow settings, in the Started category. dispatch reads only tickets whose status is of type Started, so a Needs Human status in any other category is never seen, and its tickets get no notification and no resume nudge. A name that differs from these goes in `[linear.statuses]`.
- an `ai` label. Only tickets assigned to you that carry it are managed.
- a label group named `repo` with one label per repository, such as `dispatch`. A ticket's repo label tells dispatch which clone to build in.

Linear's GitHub integration should move a ticket to Done when its PR merges. dispatch never moves a built ticket to Done itself, and the Done move is what triggers teardown. (It does move a parent to Done when its children finish.)

**4. Prepare each repo.** Workers run only on leased treehouse trees, so the repo's clone needs a `treehouse.toml` (`treehouse init` writes one). `max_trees` in it caps how many workers the repo runs at once. Turn on "Allow auto-merge" in the repo's GitHub settings. Workers enable auto-merge only when `main` requires an approving review, so protect `main` with the review and checks you want. To have dispatch merge approved PRs itself instead, list the repo in `auto_merge` in the config (step 5).

**5. Write the config.** Create `~/.config/dispatch/config.toml` with one `[repos]` entry per repo label, pointing at that repo's clone. Without `[repos]`, every In Progress ticket escalates to Needs Human with "its repo \"dispatch\" is not in the dispatcher's config" (naming the ticket's repo label):

```toml
[repos]
dispatch = "~/Development/dispatch"
```

The [config reference](config.md) lists every other setting and has examples for several repos and renamed statuses. dispatch fails on a setting it doesn't know.

**6. Start it.** Open a herdr window and run:

```sh
dispatch run
```

It ticks now and every `interval_minutes` after, until you press Ctrl-C. It does not survive a reboot or a herdr restart, so start it again after either.

## How a ticket flows

1. **Todo.** You or the `dispatch:ticket` skill write the ticket with the `ai` label, a repo label, and yourself as assignee. It waits in Todo. dispatch ignores it.
2. **Approval.** You move it to In Progress. This is the only approval step. Nothing but you, or the promotion of a child below, moves a ticket there.
3. **Launch.** On the next tick, `dispatch launch` leases a treehouse tree held by the ticket id, creates the ticket's branch in it, opens a herdr workspace, and starts Claude with `/dispatch:build <ticket>`.
4. **Build.** The worker follows the `dispatch:build` skill. It reads the ticket and its comments, implements with surface tests, has fresh sub-agents review the tests and the code, rebases onto `main`, runs the tests and the app, and opens a PR that ends with `Fixes <ticket>`. It reports only through the ticket's status and comments.
5. **In Review.** The worker moves the ticket to In Review once the PR is linked. It then waits. Each tick, the dispatcher checks an In Review ticket with an idle worker, and nudges it when the PR has a failed check, requested changes or a conflict.
6. **Needs Human.** A worker that needs a decision outside its terminal moves the ticket to Needs Human with a comment saying what it needs. dispatch fires a macOS notification. Answer on the ticket and move it back to In Progress, and the worker gets the resume nudge. The dispatcher also sets Needs Human itself, with one comment, after `nudge_limit` fruitless PR nudges, `nudge_limit` relaunches of an In Review worker that never pushed, or `launch_failure_limit` failed launches. It does the same at once for a ticket with no repo label, a repo label missing from `[repos]`, or more labels than it reads.
7. **Merge.** The PR merges, either you or dispatch (when its repo is in [`auto_merge`](config.md#auto-merge) and a review has approved the head commit), and Linear moves the ticket to Done.
8. **Teardown.** The next tick closes the worker's workspace and returns the tree to the pool, unless the tree holds modified or staged tracked files, or unpushed commits. Untracked files are deleted first.

A question the worker asks in its terminal shows in herdr as blocked. dispatch notifies you, never answers for you, and never sets Needs Human for it.

## Nudges and notifications

While `dispatch run` ticks, an In Progress ticket whose worker is idle or finished gets the stall nudge (a `design` ticket never does, see [Design tickets](#design-tickets)) typed into its session, unless the worker is waiting on its own background work. The plugin's Stop hook runs `dispatch record-stop` each time a worker ends a turn, which records the time and the background tasks (Monitors, background shells and subagents) still running. A worker whose latest Stop is under 60 minutes old and lists running tasks is left alone. Any other idle worker gets one stall nudge per Stop, so a worker that stops again is reminded again. A worker with no record, or a record without the task list, is treated as stopped when the dispatcher first saw it idle. A stalled worker is never moved to Needs Human, because workers may legitimately run for hours. You get a macOS notification instead, with no change to the ticket, on the 6th stall nudge with no status change and every 6th after, and once when a worker has not stopped again 60 minutes after a stall nudge. A ticket you move from Needs Human back to In Progress gets the resume nudge on the next tick, whatever the Stop record says, with no comment.

An In Review ticket whose worker is idle gets the PR nudge when its PR has a failed check, changes requested or a merge conflict. The nudge names the problem and tells the worker to handle it as its build skill's step 7 says. A worker that is gone is relaunched on its existing tree, then nudged on the tick after it goes idle. After `nudge_limit` PR nudges with no push, the ticket goes to Needs Human with one comment, and a push that moves the PR head resets the count. The dispatcher reads PRs through `gh`, so it must be authenticated.

An idle In Review worker also gets the activity nudge when its PR gains a review, a conversation comment or a comment on an open review thread newer than the newest one dispatch has put to it. This covers a review with only a written body, which opens no thread and sets no `reviewDecision`, so none of the reasons above catches it. An approval with no body, a dismissal and a draft review don't count. An open review thread is not nudged about on every tick, only when it gets a new comment, and it still keeps the PR from being ready. The nudge names the review or comment and fires once per new item. A worker that is busy gets it once it is idle. A PR with no worker is relaunched only for a comment on an open review thread, and that relaunch does not count toward `nudge_limit`.

The activity nudge counts toward neither the stall nor the PR limit, and it goes ahead of the ready notification and auto-merge, so a review nobody has read is never merged past. A PR that needs work gets the PR nudge first, and the activity nudge follows once those problems clear. When a ticket enters In Review, its worker is nudged about any review or comment already on the PR. A deleted cache looks the same as a new ticket, so it draws that nudge once for each In Review ticket, unlike the comment nudge, which records old comments without nudging. As with comments, dispatch can't tell your reply from the worker's own, so a worker's reply draws one more nudge, which tells it to say nothing if there is nothing to act on.

Any In Progress or In Review ticket other than a `design` ticket whose worker is working or idle gets the comment nudge when it gains a comment newer than the newest one dispatch has seen. It fires once per comment, reaches a busy worker mid-turn, and counts toward neither the stall nor the PR limit. A blocked worker is not nudged, and gets the nudge once it is unblocked. A comment posted while the ticket was out of the managed set still reaches a worker that kept its tree. A comment that arrives while the worker is gone, while a resume nudge is pending, or on a design-review ticket is not nudged later. dispatch can't tell your comment from the worker's own, so a worker nudged about its own comment rereads it and carries on. The first tick that sees a ticket only records its newest comment, so starting `dispatch run` nudges nobody for old comments.

A worker blocked on a question (AskUserQuestion), on an In Progress or In Review ticket, fires a macOS notification (through `osascript`) on its first blocked tick and again every `blocked_renotify_ticks` ticks (default 3) while it stays blocked. The dispatcher never answers the question and never sets Needs Human for it. A ticket entering Needs Human fires one notification, a parent included.

A PR is ready for review when its ticket is In Review, its worker is idle, every check has finished and passed (a PR with no checks counts as passed), no review thread is unresolved, `reviewDecision` is not `CHANGES_REQUESTED`, and GitHub reports the PR as mergeable. A check still running, such as CodeRabbit's while its review is in progress, or a merge state GitHub has not computed yet, keeps the PR from being ready. The dispatcher fires one notification naming the ticket and its PR, once per PR head commit. A push that leaves the PR ready again notifies again, and no tick in between repeats it.

## Events in your session

Every notification above, including merge and Needs Human ones, can also reach a Claude Code session you steer from, as a message that wakes it if it is idle. Run `/dispatch:converse` in that session, which runs `dispatch subscribe`. The command records the session's inbox socket (from `CLAUDE_CODE_MESSAGING_SOCKET`) in the state directory, and several sessions can subscribe. It fails outside a Claude Code session.

From then on each notification a tick raises is also sent to every subscribed session, after the macOS alert. The message names the ticket and the event, and carries the PR URL when the event has one and always the ticket URL. A socket that no longer accepts connections, because its session exited, is dropped from the list the first time a tick tries it, and the tick prints one line on stderr for each dropped socket. A failed macOS alert holds the messages back until its retry shows, so a broken alert never wakes your session repeatedly. The merge notices are the exception, because nothing retries them, so they reach your session even when the alert fails. `dispatch tick --dry-run` prints the messages it would send, one `would message` line per session. Worker nudges are unchanged and still type into the worker's pane.

Add this to your Claude Code user settings (`~/.claude/settings.json`):

```json
{ "crossSessionInbound": "accept" }
```

Without it, a session that skips permission prompts holds each message behind an approval dialog ("the sender did not attest its permission mode"), and the event waits until you open that session and answer. dispatch cannot see a held message, because the socket sends no reply.

## Workspace labels

After every tick, each worker's herdr workspace is labeled `<emoji> <ticket>`.

| Emoji | Meaning |
|---|---|
| 🟢 | In Progress |
| 🟡 | In Review, PR not ready |
| 👀 | In Review, ready for review (the same condition as the notification) |
| 🔴 | Needs Human |
| ⏸️ | Paused (Backlog or Todo) with the tree kept and a workspace still open |

A workspace is renamed only when its label differs from the one its ticket's status calls for. The dispatcher finds a worker by its tree path, so a renamed workspace is still found.

## Design tickets

A ticket labeled `design` (with the `ai` and repo labels) waits in Backlog until you design it. Move it to In Progress and the next tick launches a worker typed `/dispatch:grill <ticket>` on `grill_model` (default `opus`), where builds use `worker_model`. `dispatch tick --dry-run` shows `launch ENG-N` for it. A `design` ticket that already has sub-issues and no live worker is skipped instead, so a grill that exits after creating sub-issues is not relaunched.

The grill's worker is never nudged about a stall, a comment or a PR, never escalated, and stopped only when you move the ticket out of In Progress, because a design waiting on you is normal. It keeps running after the grill creates a child ticket under it. A Backlog or Todo child is left alone, because a `design` parent's children are never promoted. A child you move to In Progress yourself launches as a build. A `design` ticket in In Progress holds a tree, so it counts toward `max_in_progress`.

## Parent tickets

Move a parent ticket to In Progress and the dispatcher starts its children for you. Each tick it reads every child of an In Progress parent and acts in this order.

- **Close.** A parent whose children are all Done, Canceled or Duplicate is moved to `close_parent` (default Done). A parent whose children couldn't be read is never closed.
- **Promote.** A child in `todo` (default Todo) that is assigned to you, carries the `ai` label, belongs to a team in `[linear].teams`, is not a `design` or `design-review` ticket and has no unfinished blocker moves to In Progress, and the next tick launches its worker. A child waits until every blocker reaches `blocker_done` (default Done), A blocker that is Canceled or Duplicate (any status of Linear's canceled type) never gets there, so the child moves to Needs Human with one comment naming the blocker. It is escalated again if you move it back to Todo with that blocker still attached. A child that holds a treehouse lease was paused with its tree kept, so it stays in Todo until you move it to In Progress yourself. A child whose blocker list ran past one page waits too.
- **Cap.** Promotion stops when the In Progress tickets other than parents (a `design` parent with a live grill counts) reach `max_in_progress`. A child waiting for a slot is promoted on a later tick, and the tick prints `wait <child>` each time.

A parent never gets a worker, whatever its status. A parent labeled `design` keeps the grill worker it already has, which is never nudged or escalated. A parent labeled `design-review` is left alone until the review skill exists, and neither kind has its children promoted or the parent closed. If the children can't be read, the tick prints the error and skips promotion and closing, and everything else runs as usual. Promotion is the only way the dispatcher moves a ticket to In Progress.

## Teardown and pausing

Each tick also looks at every tree held by a ticket assigned to you with the `ai` label, whatever the ticket's status. Statuses come from `[linear.statuses]`: `done` (default Done, Canceled, Duplicate) and `paused` (default Backlog, Todo).

- A finished ticket whose tree has no tracked changes and is fully pushed has its herdr workspace closed, its untracked files deleted (`git clean -f -d`, which leaves ignored files alone), and its tree returned with `treehouse return --force`, so `treehouse status` shows the slot available.
- A finished ticket whose tree has modified or staged tracked files, or commits that no remote has, keeps its tree. Its worker's workspace is closed, and the ticket gets one comment saying what the tree holds. Every tick, including `tick --dry-run`, prints a `keep` line for each held tree, so the log says why the pool is short a tree. Push or discard the work and the next tick returns the tree.
- A ticket moved back to Backlog or Todo has its worker's workspace closed and keeps its tree. Moving it to In Progress again relaunches a worker on that tree.

"Pushed" means every commit reachable from the tree's HEAD is also reachable from a remote-tracking ref. A branch squash-merged and then pruned from the remote counts as unpushed, so its tree stays leased with the comment until you discard it. A pause or a kept tree closes only a workspace with a Claude agent in it, so a workspace you opened by hand there stays open. A tree with nothing to keep is returned whatever workspace is open on it, because a worker whose Claude exited leaves an agentless workspace behind and the slot has to come back.

A `design` ticket is torn down and paused like a build, since its grill holds a tree. The tree of a `design-review` ticket is left alone until the review skill exists. A tree whose git state can't be read, or whose ticket has more labels than the dispatcher reads, stays leased, and the tick prints why. If the lookup of tickets holding trees fails, the tick prints the error and skips teardown but still launches and nudges.

## Running it

`dispatch run` prints one line per action, then a summary. The summary reads like this:

```
16:05:07 tick: 0 tickets, 0 actions, Linear 1 call in 240ms (2499 of 2500 requests left this hour), took 405ms
```

It says how many managed tickets the tick read, how many actions it took, how many Linear API calls it made with the time they took and the hourly request allowance left, and the whole tick's duration. An action line starts with its kind, then the ticket and a reason, such as `wait ENG-9: 4 of 4 workers already running` or `escalate ENG-4: launch failed 2 times`. `tick skipped: another tick is running` means a one-off `dispatch tick` held the lock.

To see what a tick would do without doing it:

```sh
dispatch tick --dry-run
```

**Pause** a ticket by moving it to Backlog or Todo. Its worker stops and its tree stays leased. Move it to In Progress to relaunch a worker on that tree. **Cancel** it, and the worker stops and the tree returns, unless it holds unpushed work.

To type into a worker yourself:

```sh
dispatch tell ENG-228 "use the v2 endpoint"
```

It fails when the ticket's worker is blocked on a question or absent.

## Troubleshooting

**A tick fails with `no Linear API key`.** `LINEAR_API_KEY` is unset and the keychain has no entry under the configured service. Redo step 2, and check the entry exists with `security find-generic-password -s dispatch-linear-api-key >/dev/null && echo found`, which reports the entry without printing the key. `dispatch run` goes on ticking after a failed tick, so fix the cause and the next tick recovers.

**A tick fails with `unknown setting`.** The config has a key dispatch doesn't know, and the message names it and the file. The usual causes are a typo and an example copied from an old design document. See the [config reference](config.md).

**The tick prints `while escalating ENG-4: team ENG has no "Needs Human" status` every tick.** The tick carries on, but the escalation fails and the ticket keeps its old status. The team lacks that status or calls it something else. Create it in Linear (Started category) or set `needs_human` under `[linear.statuses]`. A missing `in_progress` or `close_parent` status fails the same way.

**An In Progress ticket never launches.** Run `dispatch tick --dry-run`. The ticket is skipped or escalated when it has no `repo` label, when its label isn't a key in `[repos]`, when it has sub-issues (a parent never gets a worker), or when it carries a `design-review` label. A `design` ticket launches too, as a grill rather than a build. A ticket that isn't assigned to you or lacks the `ai` label doesn't appear at all. `wait` means the cap (`max_in_progress`) is reached or the repo's pool couldn't be read. A full treehouse pool shows only on a live tick, as `launch ENG-1: pool_exhausted: all 4/4 pool trees are taken: ...` naming who holds every tree. A `not_pooled` launch means the repo has no `treehouse.toml`.

**A worker sits in a state of working.** dispatch nudges a working worker only about a new ticket comment, so a worker that keeps working is never nudged for stalling. Open its herdr workspace and look. To redirect it, run `dispatch tell <ticket> "<text>"`. To restart it, move the ticket to Backlog and back to In Progress. Its tree and branch stay. Wait for a tick to see Backlog before moving it back, because a round trip inside one interval changes nothing.

**A tree is kept after the ticket finished.** The ticket got a comment saying what the tree holds, either modified or staged tracked files or commits no remote has. Untracked scratch files never keep a tree, since the tick deletes them. Push or discard the work and the next tick returns the tree. A branch that was squash-merged and then deleted from the remote counts as unpushed, so discard it yourself.

**A change to dispatch doesn't take effect after merging.** `dispatch run` is the binary you installed, not your checkout. Run `go install ./cmd/dispatch` in the checkout, then stop `dispatch run` with Ctrl-C and start it again. Workers pick up skill changes from `plugin_root` without a reinstall. The launcher is part of the binary, so a launcher change needs the reinstall too.
