# dispatch user guide

dispatch runs Claude workers on your approved Linear tickets. You approve a ticket by moving it to In Progress, and a dispatcher that runs in a herdr window you keep open and re-reads Linear, GitHub, herdr and treehouse every few minutes starts a worker in its own treehouse worktree. The worker carries the ticket to a merged pull request. Every setting is in the [config reference](config.md).

## Set it up

You need a Mac or a Linux machine with these on your PATH. dispatch shells out to all of them.

| Tool | Why |
|---|---|
| `herdr` 0.9.0 or later | Hosts one workspace per worker, and reports whether each worker is working, idle or blocked. |
| `treehouse` v3.1.2 or later (`go install github.com/kunchenguid/treehouse/v3@latest`) | Leases a pooled git worktree to each worker. |
| `gh`, signed in with `gh auth login` | The dispatcher reads PR checks, reviews and conflicts, and merges approved PRs in repos with `auto_merge = true`. Workers open PRs with it. |
| `claude` | The worker itself. |
| `git` | Used by `dispatch launch` to create the ticket's branch in its tree. |
| `go` | Builds the `dispatch` CLI. |

**1. Clone this repo and install the CLI.** The clone is also the plugin workers load. `~/Development/dispatch` is the default location. If you clone elsewhere, set `plugin_root` under `[plugins.herdr]` in step 5.

```sh
gh repo clone kapetan-io/dispatch ~/Development/dispatch
cd ~/Development/dispatch && go install ./cmd/dispatch
```

Put `$GOBIN` (default `~/go/bin`) on your PATH. `bin/dispatch-launch` runs `dispatch launch`, so it needs the binary there.

**2. Give dispatch your Linear API key.** Create a personal API key in Linear under Settings, then Security & access. On macOS, store it in the keychain under the service name `dispatch-linear-api-key`:

```sh
security add-generic-password -s dispatch-linear-api-key -a "$USER" -w
```

The command prompts for the key. On Linux there is no keychain lookup, so export `LINEAR_API_KEY` or set `api_key_command` (see below), for example `export LINEAR_API_KEY=lin_api_...` in the shell that runs `dispatch run`. Setting `LINEAR_API_KEY` in the environment overrides the keychain, and `api_key_keychain` in the config names a different service. If the keychain is off limits, set `api_key_command` in the `[plugins.linear]` config to a command that prints the key, such as `op read op://Work/linear/credential`, and skip the keychain. dispatch manages only tickets assigned to the owner of this key.

**3. Prepare Linear.** dispatch finds your tickets by status and label, so each team in your config needs:

- the statuses Todo, In Progress, In Review, Done, Canceled, Duplicate and Backlog, which a default Linear team has, and a **Needs Human** status, which it doesn't. Create it in the team's workflow settings, in the Started category. dispatch reads only tickets whose status is of type Started, so a Needs Human status in any other category is never seen, and its tickets get no notification and no resume nudge. A name that differs from these goes in `[plugins.linear.statuses]`.
- an `ai` label. Only tickets assigned to you that carry it are managed.
- a label group named `repo` with one label per repository, such as `dispatch`. A ticket's repo label tells dispatch which clone to build in.

Linear's GitHub integration should move a ticket to Done when its PR merges. dispatch never moves a built ticket to Done itself, and the Done move is what triggers teardown. (It does move a parent to Done when its children finish.)

**4. Prepare each repo.** Workers run only on leased treehouse trees, so the repo's clone needs a `treehouse.toml` (`treehouse init` writes one). `max_trees` in it caps how many workers the repo runs at once. Turn on "Allow auto-merge" in the repo's GitHub settings. Workers enable auto-merge only when `main` requires an approving review, so protect `main` with the review and checks you want. To have dispatch merge approved PRs itself instead, set `auto_merge = true` in the repo's table in the config (step 5).

**5. Write the config.** Create `~/.config/dispatch/config.toml` with one `[repos.<label>]` table per repo label, whose `path` is that repo's clone. Without one, every In Progress ticket escalates to Needs Human with "its repo \"dispatch\" is not in the dispatcher's config" (naming the ticket's repo label):

```toml
[repos.dispatch]
path = "~/Development/dispatch"
```

The [config reference](config.md) lists every other setting and has examples for several repos and renamed statuses. dispatch fails on a setting it doesn't know. The [rules guide](rules.md) covers `dispatch rules init`, `check` and `test`.

**6. Load the plugin in your own Claude sessions.** Workers get the plugin from `[plugins.herdr].plugin_root` on their own. Your session, the one that files tickets and supervises workers, needs it too, for `/dispatch:dispatch` and `dispatch:ticket`. The repo is its own marketplace, so install it once from your clone. Use your clone's path where the commands show `~/Development/dispatch`:

```sh
claude plugin marketplace add ~/Development/dispatch
claude plugin install dispatch@dispatch
```

Every Claude session you start after that has the dispatch skills, and `claude plugin list` shows `dispatch@dispatch` enabled. The install is a snapshot of the clone's commit. After a `git pull`, run `claude plugin update dispatch@dispatch` and restart your session. For a single session without installing, run `claude --plugin-dir ~/Development/dispatch`.

**7. Check the setup.** Run these in order. Each must pass before you start the dispatcher, and a failure points at the step to redo.

```sh
herdr --version && treehouse --version && gh auth status
dispatch linear-viewer
dispatch rules check
claude plugin list
```

- The first line passes when `herdr` is 0.9.0 or later, `treehouse` is v3.1.2 or later and `gh` reports you logged in. A "command not found" or a logged-out `gh` means the tools table above is incomplete.
- `dispatch linear-viewer` prints `{"id":"...","name":"..."}` for the owner of your Linear key. `no Linear API key` means step 2 is incomplete. An error naming a command is `api_key_command` failing. `Linear rejected the API key` means the key was revoked or mistyped. The `name` should be you, because dispatch manages only tickets assigned to the key's owner.
- `dispatch rules check` lints the rules, reads every source once and prints what each rule wants now. It takes no action. Move a test ticket that has the `ai` label and a `repo` label to In Progress first, and expect a `launch-worker` line that says `wants launch now for ENG-n`. `unknown setting` means a typo in the config (step 5). A ticket listed as `held for ENG-n` has a source that could not be read. No ticket in the output means none matches: check its assignee, label and status.
- `claude plugin list` shows `dispatch@dispatch` enabled (step 6). Without it, `/dispatch:dispatch` is not found in your session.

If every check passes, the setup is complete. Troubleshooting at the end of this guide covers the rest.

**8. Start it.** Open a herdr window and run:

```sh
dispatch run
```

It reads every source and runs a cycle at start, then again every `resync` period, until you press Ctrl-C. It does not survive a reboot or a herdr restart, so start it again after either.

## How a ticket flows

1. **Todo.** You or the `dispatch:ticket` skill write the ticket with the `ai` label, a repo label, and yourself as assignee. It waits in Todo. dispatch ignores it.
2. **Approval.** You move it to In Progress. This is the only approval step. Nothing but you, or the promotion of a child below, moves a ticket there.
3. **Launch.** At the next resync, `dispatch launch` leases a treehouse tree held by the ticket id, creates the ticket's branch in it, opens a herdr workspace, and starts Claude with `/dispatch:build <ticket>`.
4. **Build.** The worker follows the `dispatch:build` skill. It reads the ticket and its comments, implements with surface tests, has fresh sub-agents review the tests and the code, rebases onto `main`, runs the tests and the app, and opens a PR that ends with `Fixes <ticket>`. It reports only through the ticket's status and comments.
5. **In Review.** The worker moves the ticket to In Review once the PR is linked. It then waits. Each cycle, the dispatcher checks an In Review ticket with an idle worker, and nudges it when the PR has a failed check, requested changes or a conflict.
6. **Needs Human.** A worker that needs a decision outside its terminal moves the ticket to Needs Human with a comment saying what it needs. dispatch fires a notification. Answer on the ticket and move it back to In Progress, and the worker gets the resume nudge. The dispatcher also sets Needs Human itself, with one comment, after `nudge_limit` fruitless PR nudges, `nudge_limit` relaunches of an In Review worker that never pushed, or `launch_failure_limit` failed launches. It does the same at once for a ticket with no repo label, a repo label with no `[repos.<label>]` table, or more labels than it reads. A launch killed after Claude started and before its kickoff ran leaves an idle Claude with no ticket. Five minutes later dispatch closes that workspace and relaunches, and each such close counts toward `launch_failure_limit`.
7. **Merge.** The PR merges, either you or dispatch (when its repo sets [`auto_merge = true`](config.md#auto-merge) and a review has approved the head commit), and Linear moves the ticket to Done.
8. **Teardown.** The next resync closes the worker's workspace and returns the tree to the pool, unless the tree holds modified or staged tracked files, or unpushed commits. Untracked files are deleted first.

A worker asks its questions in chat, and its window shows 💤 while it waits. A question popup shows in herdr as blocked and the window shows 🔴. dispatch notifies you of a popup, never answers for you, and never sets Needs Human for it.

## Nudges and notifications

While `dispatch run` is running, an idle In Progress worker is not nudged to keep going. An idle worker is either waiting on you or stopped early, and the workspace label tells you which to look at ([Worker states](worker-states.md) lists every state, its emoji, and what dispatch does in it). The plugin's Stop hook runs `dispatch record-stop` each time a worker ends a turn, which reports the time and the background tasks (Monitors, background shells and subagents) still running to the running dispatch over its socket. Dispatch stores the Stop and adds the turn to the session's log that rules read as `worker.turns` (see [rules](rules.md#reading-a-workers-turns)). With no dispatch running the report is dropped. Its Notification and UserPromptSubmit hooks run `dispatch wake`, which asks the running loop to read herdr again and run a cycle, so the label follows a prompt within seconds and an idle worker about a minute after its turn ends. When the prompt starts with a slash command, `dispatch wake` also reports it, and dispatch stores the command and whether a person or dispatch typed it in the same log, read as `worker.commands` (see [rules](rules.md#reading-a-workers-commands)). A ticket you move from Needs Human back to In Progress gets the resume nudge at the next resync, whatever the Stop record says, with no comment.

An In Review ticket whose worker is idle gets the PR nudge when its PR has a failed check, changes requested on its current head or a merge conflict. A request on a commit the worker has since pushed past, or one made while a check on the head is still running (a review bot re-reviewing), does not nudge or escalate. The nudge names the problem and tells the worker to handle it as its build skill's step 7 says. A worker that is gone is relaunched on its existing tree, then nudged once a later read shows it idle. After `nudge_limit` PR nudges with no push, the ticket goes to Needs Human with one comment, and a push that moves the PR head resets the count. The dispatcher reads PRs through `gh`, so it must be authenticated.

An idle In Review worker also gets the activity nudge when its PR gains a review, a conversation comment or a comment on an open review thread newer than the newest one dispatch has put to it. This covers a review with only a written body, which opens no thread and sets no `reviewDecision`, so none of the reasons above catches it. An approval with no body, a dismissal and a draft review don't count. An open review thread is not nudged about on every cycle, only when it gets a new comment, and it still keeps the PR from being ready. The nudge names the review or comment and fires once per new item. A worker that is busy gets it once it is idle. A PR with no worker is relaunched only for a comment on an open review thread, and that relaunch does not count toward `nudge_limit`.

The activity nudge doesn't count toward the PR limit, and it goes ahead of the ready notification and auto-merge, so a review nobody has read is never merged past. A PR that needs work gets the PR nudge first, and the activity nudge follows once those problems clear. When a ticket enters In Review, its worker is nudged about any review or comment already on the PR. dispatch can't tell your reply on the PR from the worker's own, so a worker's reply draws one more nudge, which tells it to say nothing if there is nothing to act on.

A worker blocked on a question popup (AskUserQuestion), on an In Progress or In Review ticket, fires a notification (a macOS notification through `osascript` on macOS, a herdr notification through `herdr notification show` elsewhere) on its first blocked cycle and again every `blocked_renotify_minutes` minutes (default 15) while it stays blocked. The dispatcher never answers the question and never sets Needs Human for it. A ticket entering Needs Human fires one notification, a parent included.

A PR is ready for review when its ticket is In Review, its worker is idle, every check has finished and passed (a PR with no checks counts as passed), no review thread is unresolved, no reviewer's latest review requests changes on the head, and GitHub reports the PR as mergeable. A check still running, such as CodeRabbit's while its review is in progress, or a merge state GitHub has not computed yet, keeps the PR from being ready. The dispatcher fires one notification naming the ticket and its PR, once per PR head commit. A push that leaves the PR ready again notifies again, and no cycle in between repeats it.

## Events in your session

Every notification above, including merge and Needs Human ones, can also reach the Claude Code sessions that follow the ticket, as a message that wakes a session if it is idle. In a session, run `dispatch watch ENG-123` to follow a ticket and `dispatch unwatch ENG-123` to stop. A session can follow several tickets, and a ticket can have several followers. The command asks the running dispatch to follow the session's inbox socket (from `CLAUDE_CODE_MESSAGING_SOCKET`), and fails outside a Claude Code session or when no dispatch is running. Dispatch keeps followers in memory, so after a restart a session runs `dispatch watch` again. A session follows a ticket only when you ask it to. After it moves a ticket to In Progress or takes over a ticket whose worker is running, it may ask whether to follow that ticket, and runs `dispatch watch` only on a yes. A session never follows a ticket you move to In Progress in Linear yourself, a build piece the dispatcher promotes, or a review a design worker starts, so tell a session to follow a ticket when you want to hear about it. A session hears nothing about tickets it does not follow. `dispatch subscribe` is gone, and sessions that ran it are not carried over, so run `dispatch watch` for each ticket you still want to hear about.

Each notification a cycle raises is sent to the sessions following its ticket, after the alert, and the alert fires whether or not anyone follows the ticket. The message names the ticket and the event, and carries the PR URL when the event has one and always the ticket URL. A socket that no longer accepts connections, because its session exited, is dropped from every ticket it followed the first time dispatch tries it, and dispatch prints one line on stderr for each dropped socket. A failed alert holds the messages back until its retry shows, so a broken alert never wakes your session repeatedly. The merge notices are the exception, because nothing retries them, so they reach your session even when the alert fails. Worker nudges are unchanged and still type into the worker's pane.

Add this to your Claude Code user settings (`~/.claude/settings.json`):

```json
{ "crossSessionInbound": "accept" }
```

If you use fish, the `claude-dispatch` function in the [README](../README.md#development) starts a session with this setting and the plugin loaded, so you don't need to edit your user settings.

Without it, a session that skips permission prompts holds each message behind an approval dialog ("the sender did not attest its permission mode"), and the event waits until you open that session and answer. dispatch cannot see a held message, because the socket sends no reply.

## Workspace labels

After every cycle, each worker's herdr workspace is labeled `<emoji> <ticket>`, and the emoji says who the ticket is waiting on.

| Emoji | Meaning |
|---|---|
| 🟢 | The worker is busy, or dispatch just nudged it |
| 🟡 | In Review, the worker is idle, gone or unconfirmed, the PR isn't ready and its checks are still running |
| 👀 | In Review, ready for review (the notification also needs an idle worker) |
| 💤 | The worker is idle with nothing running and nothing else pending. It needs you. Goes back to 🟢 within seconds of you typing into it |
| 🔴 | Needs Human, or the worker is blocked on a question popup |
| ⏸️ | Paused (Backlog or Todo) with the tree kept and a workspace still open |

[Worker states](worker-states.md) lists every combination of ticket status, worker, background work and PR with its emoji and what dispatch does.

A workspace is renamed only when its label differs from the one its ticket's status calls for. The dispatcher finds a worker by its tree path, so a renamed workspace is still found.

## From design to build tickets

A feature that needs a design goes through four steps, and you decide two of them.

1. **Design.** A ticket labeled `design` (with the `ai` and repo labels) waits in Backlog. Move it to In Progress and the next resync launches a worker typed `/dispatch:design <ticket>` on `[rules].design_model` (`dispatch rules init` writes `opus`), where builds use `[rules].worker_model`. The worker designs with you and saves the blueprint as a Linear document on the feature ticket. It is never nudged about a comment or a PR, never escalated, and stopped only when you move the ticket out of In Progress, because a design waiting on you is normal. It holds a tree, so it counts toward `max_in_progress`.
2. **Review, or not.** At the end the design worker asks whether the design gets a review. Choosing review files a `design-review` child of the feature ticket, moves it to In Progress and runs `dispatch tick`. The loop then launches a worker typed `/dispatch:design-review <ticket>` on `design_model`. It reviews the blueprint with you in its window, audits it against the repo's ADRs, then runs `dispatch:breakdown`. A review that is still failing after 5 rounds comments its blockers, moves to Needs Human and files nothing. Like a design worker it holds a tree, counts toward `max_in_progress` and is never nudged. It ends Done whether you start the work or hold it, and the next resync returns its tree like a build's. Choosing no review makes the design worker break the design down itself. A design that ends with nothing filed leaves its feature ticket in Backlog, so no new design worker starts.
3. **Break down.** `dispatch:breakdown` sizes the design. A design that fits one PR gets one build ticket, and a larger one gets one per piece. Each is a Todo child of the feature ticket with the `ai` and repo labels, blocked by the first piece and by the pieces it builds on, and its body cites the blueprint instead of copying it. A rerun files no second ticket for a piece. You can ask any session or worker to break down a design or a ticket. A worker breaking down its own ticket keeps the first piece and its branch, and files the other pieces as new tickets blocked by it.
4. **Start or hold.** Whichever step files the tickets asks whether to start the work now or hold it. The feature ticket keeps `design` until the work starts, and the dispatcher never promotes the children of a `design` parent, so nothing starts early. Starting removes `design` from an In Progress feature ticket. It is then an ordinary [parent](#parent-tickets), whose first piece is promoted at the next resync. Held work starts when you tell any session "start ENG-N's work".

## Parent tickets

Move a parent ticket to In Progress and the dispatcher starts its children for you. Each cycle it reads every child of an In Progress parent and acts in this order.

- **Close.** A parent whose children are all Done, Canceled or Duplicate is moved to `close_parent` (default Done). A parent whose children couldn't be read is never closed.
- **Promote.** A child in `todo` (default Todo) that is assigned to you, carries the `ai` label, belongs to a team in `[plugins.linear].teams`, is not a `design` or `design-review` ticket and has no unfinished blocker moves to In Progress, and the next cycle launches its worker. A child waits until every blocker reaches `blocker_done` (default Done), A blocker that is Canceled or Duplicate (any status of Linear's canceled type) never gets there, so the child moves to Needs Human with one comment naming the blocker. It is escalated again if you move it back to Todo with that blocker still attached. A child that holds a treehouse lease was paused with its tree kept, so it stays in Todo until you move it to In Progress yourself. A child whose blocker list ran past one page waits too.
- **Cap.** Promotion stops when the In Progress tickets other than parents (a `design` parent with a live design worker counts) reach `max_in_progress`. A child waiting for a slot is promoted on a later cycle, and `dispatch plan` lists it as `waiting` meanwhile.

A parent never gets a worker, whatever its status. A parent labeled `design` keeps the design worker it already has, which is never nudged or escalated. A parent labeled `design-review` is left alone too, and neither kind has its children promoted or the parent closed. If the children can't be read, the parent shows as `held` in `dispatch plan`, nothing is promoted or closed, and everything else runs as usual. Promotion is the only way the dispatcher moves a ticket to In Progress.

## Teardown and pausing

Each cycle also looks at every tree held by a ticket assigned to you with the `ai` label, whatever the ticket's status. Statuses come from `[plugins.linear.statuses]`: `finished` (default Done, Canceled, Duplicate) and `paused` (default Backlog, Todo).

- A finished ticket whose tree has no tracked changes and is fully pushed has its herdr workspace closed, its untracked files deleted (`git clean -f -d`, which leaves ignored files alone), and its tree returned with `treehouse return --force`, so `treehouse status` shows the slot available.
- A finished ticket whose tree has modified or staged tracked files, or commits that no remote has, keeps its tree. Its worker's workspace is closed, and the ticket gets one comment saying what the tree holds. Push or discard the work and the next resync returns the tree.
- A ticket moved back to Backlog or Todo has its worker's workspace closed and keeps its tree. Moving it to In Progress again relaunches a worker on that tree.

"Pushed" means every commit reachable from the tree's HEAD is also reachable from a remote-tracking ref. A branch squash-merged and then pruned from the remote counts as unpushed, so its tree stays leased with the comment until you discard it. A pause or a kept tree closes only a workspace with a Claude agent in it, so a workspace you opened by hand there stays open. A tree with nothing to keep is returned whatever workspace is open on it, because a worker whose Claude exited leaves an agentless workspace behind and the slot has to come back.

A `design` ticket is torn down and paused like a build, since its design worker holds a tree. A `design-review` ticket's tree is returned like a build's. A tree whose git state can't be read stays leased and shows as `held` in `dispatch plan`.

## Running it

`dispatch run` prints a start line, then one line when an action starts and one when it ends, and a resync line after each resync. They read like this:

```
16:05:07 start: 12 tracked tickets, 25 rules, ledger 31 receipts from events-20261015T160507Z.jsonl
16:05:08 launch ENG-9 (launch-worker)
16:05:31 launch ENG-9 ok: workspace w3
16:05:32 launch ENG-4 (launch-worker)
16:05:33 launch ENG-4 refused: pool_exhausted: all 4/4 pool trees are taken: ...
16:10:07 resync: 12 tickets, 2 actions since 16:05:07, Linear 3 calls (2497 of 2500 left this hour)
```

The start line says how many tickets and rules it loaded, and which history segment its ledger came from. An action's start line names its kind, the ticket and the rule that wanted it. Its end line says what a successful action did, such as `ok: workspace w3`, or `done` when there is nothing more to say, or `refused` or `failed` with the reason. A resync line says how many tickets it read, how many actions started since the last resync line, and how many Linear API calls it made with the hourly request allowance left. Errors go to stderr, such as `dispatch: while reading: ...` when a source can't be read.

The resync is the only clock the rules wait on, so a wait ends up to `resync` after its deadline. To re-read now instead of waiting:

```sh
dispatch tick [ticket] [--timeout DURATION]
```

It asks the running loop to re-read every source and waits, for up to 2 minutes unless `--timeout` says otherwise, until the loop has settled. It prints one line per ticket (only the named ticket's, if you name one): what ended since you asked, such as `ENG-9 launched`, `ENG-4 launch refused: pool_exhausted`, what is queued, such as `ENG-7 queued: launch, <reason>`, what is held, such as `ENG-3 held: <reason>`, and a rule that threw, such as `ENG-3 launch-worker threw: Error: config.toml has no [rules] key "design_model"...`. A throw doesn't change the exit code. It exits 0 once things settle, 1 when an action failed or was refused (a full pool doesn't count) or when it timed out, and 3 with `dispatch: not running` when no `dispatch run` is going. On a timeout it lists what is still pending, and that keeps going.

To see what the latest cycle wants without asking the loop:

```sh
dispatch plan
```

It reads the newest history segment, so it works while `dispatch run` is stopped, and fails with `there is no history to read` before the first run. It prints one row per ticket and action: `act` for an action the cycle wants now, `waiting` for one that waits for a worker slot or an earlier step, `held` for a ticket a rule can't decide because a source it reads couldn't be read, and `covered` for an action the ledger already has a receipt for. The columns are the state, the ticket, the action, the rule and a detail, which is the once key, the reason it waits or is held, or the receipt that covers it.

To see why dispatch acted on a ticket, or didn't:

```sh
dispatch trace ENG-301
```

```
12:00:00 cycle #1 (start)  launch-worker  wants launch once [launch - 3]  read status=in_progress worker=none
12:00:11 receipt  launch ENG-301 done once [launch - 3]: ok: workspace w3
12:00:11 cycle #2 (event)  launch-worker  wants launch once [launch - 3], covered: done 12:00
```

`trace` reads every kept segment and prints a cycle only when what the rules decided for the ticket changed since the cycle before, and again after `dispatch run` starts or `state reset`. The number after `cycle #` is the cycle line's position in this printout, which moves when old segments are deleted. Each line names the rule, what it wanted, waited for, was held on, threw or dropped, and what it read. An `http <rule> GET <service> <path> <status body>` line (or `no answer: <reason>`) shows each response a rule waited on for the ticket. It prints with a changed decision, and also when an answer changed while the decision didn't. History written before exchanges named a ticket shows none. Receipts print as they were written, first as `attempted` and then with their outcome, and those from segments already deleted print from the snapshot that carried them. It exits 1 when kept history has no record of the ticket.

To script against a trace, add `--json`. It prints every line the text form prints as one JSON object, so the lines above come out as:

```sh
dispatch trace ENG-301 --json
```

```
{"type":"cycle","time":"2026-10-09T12:00:00Z","cycle":1,"why":"start","rule":"launch-worker","decision":"wants","kind":"launch","subject":"ENG-301","once":"[launch - 3]","read":["status=in_progress","worker=none"]}
{"type":"receipt","time":"2026-10-09T12:00:11Z","rule":"launch-worker","kind":"launch","subject":"ENG-301","state":"attempted","once":"[launch - 3]"}
{"type":"receipt","time":"2026-10-09T12:00:11Z","rule":"launch-worker","kind":"launch","subject":"ENG-301","state":"done","once":"[launch - 3]","detail":"ok: workspace w3"}
{"type":"cycle","time":"2026-10-09T12:00:11Z","cycle":2,"why":"event","rule":"launch-worker","decision":"covered","kind":"launch","subject":"ENG-301","once":"[launch - 3]","detail":"done 12:00"}
```

Every line has a `type` of `cycle`, `http` or `receipt`, and a `time` in UTC. The text form prints the local time of day. A field that doesn't apply to a line, or is empty, is left out.

* A `cycle` line has the `cycle` number, the same position in this printout that the text form shows (it moves when old segments are deleted, so it is not an id), `why`, `rule`, `subject`, and a `decision` of `wants`, `waits`, `held`, `dropped`, `covered`, `threw`, `timeout` or `nothing` (a ticket history knows but no rule has wanted anything for, which has no `rule`). A `wants` or `covered` line has the `kind` and `once` key, a `waits` line has its `kind`, and `waits`, `held`, `dropped`, `covered`, `threw` and `timeout` lines put the reason or error in `detail`. `read` lists what the rule read.
* An `http` line has the `cycle`, `why`, `rule`, `subject`, `service`, `path`, and either the `status` of the response and its `body`, the JSON the service answered with (left out when the response had none), or `unknown`, the reason no answer is known.
* A `receipt` line has the `rule`, `kind`, `subject`, `state`, `once` key and `detail`. `time` is when the receipt started for `attempted` and when it ended otherwise. `reset` is true for a receipt `state reset --quiet` recorded for something never sent.

A receipt prints as `attempted` and again with its outcome, so a script that wants one result per action keeps the last line for each `kind`, `subject` and `once`.

To check that the rules still decide what they decided, or to try a changed rule on what really happened:

```sh
dispatch replay --from "2026-10-15 09:00" --to 10:30
dispatch replay --rules ~/rules-next
```

`replay` runs the cycles in that stretch again over the world and ledger each one ran over, and compares them with what the live run decided. It prints `42 cycles, 42 match` and exits 0, or prints the first difference and exits 1. `--from` and `--to` take RFC 3339, `2006-01-02 15:04` or a time of day today. With `--rules`, it runs the rules in that directory instead, stops at the first action that differs from the live run, whether the new rules want one the live run didn't or no longer want one it did, prints both sides and exits 0. Only cycles that wrote a line are run again, so a changed rule is not tried on a cycle the live run found identical to the one before, and `on` rules see no events, because the loop passes none. A get is answered from the exchanges the live cycle recorded and never reaches a service, so a get the live cycle didn't make, or one it made that replay skips, is a difference. A cycle with a rule call that timed out live isn't compared on its gets. A segment written before history recorded the world cannot be replayed. Replay skips it, says so on stderr, and exits 2 only when nothing else was left to replay. It exits 1 before the first run, when there is no history.

To start from an empty ledger, so dispatch acts on every ticket as if for the first time:

```sh
dispatch state reset
```

To do that without repeating the nudges, notices and comments it already sent:

```sh
dispatch state reset --quiet
```

It starts a new history segment with an empty ledger and keeps what the world remembers, so no wait starts over. With `--quiet` it first reads everything once and records the nudges, notices and comments the first cycle wants as already sent, flagged `reset`, so the next `dispatch run` doesn't send them again. It refuses when a source can't be read, because a rule holds back every message for what it can't read and nothing would be recorded. It may make live GETs through the `[http.<name>]` services a rule calls, and refuses when one of them comes back unknown for the same reason. It exits 1 while `dispatch run` is going.

dispatch keeps its state in history. History is a series of segments named `events-<time>.jsonl` under `[history].dir`. Each starts with a snapshot of what dispatch knew and did before it, and then appends every event, change, cycle and receipt, and the world each cycle read, which is what `replay` runs again. A cycle line's `http` entries carry the `rule` and `ticket` that asked, which older history omits. The line format is public, and every line has a `line` field naming its type. The field names are those of the types in `internal/history`, except the world inside a `read` line, which `worldRecord` in `internal/world/json.go` names.

| `line` | What it holds |
| -- | -- |
| `header` | why the segment began (`start`, `rotate` or `reset`), the config text, the bundled rules, and when the run began |
| `snapshot` | the world's memory, the ledger, the resync count and the last `read`, from before the segment |
| `event` | something that told dispatch to look again |
| `read` | the world a cycle ran over, with each error as its message, and the tickets with a launch in flight. Written only when it differs from the one before |
| `world` | the changes to what dispatch remembers, with `at` naming the cycle that found them |
| `cycle` | what the rules decided: wants, waiting, held (`dropped` for an `on` rule's lost event), covered and throws, with what each rule read. Written only when it differs from the cycle before |
| `receipt` | an action attempted, and again with its outcome |
| `stop` | a clean end. A segment without one ended in a crash |

`dispatch run` loads the newest segment at start, so a restart picks up where it stopped. A new segment starts every `[history].rotate`, and a segment older than `[history].keep` is deleted. `dispatch run` reads the config and the rules once, at start, so a change to either needs a restart.

**Pause** a ticket by moving it to Backlog or Todo. Its worker stops and its tree stays leased. Move it to In Progress to relaunch a worker on that tree. **Cancel** it, and the worker stops and the tree returns, unless it holds unpushed work.

To type into a worker yourself:

```sh
dispatch tell ENG-228 "use the v2 endpoint"
```

It needs the running loop, and exits 3 with `dispatch: not running` without one. It fails when the ticket's worker is blocked on a question popup or absent.

## Troubleshooting

**`dispatch run` stops at start with `no Linear API key`.** `LINEAR_API_KEY` is unset, `api_key_command` is not set, and the keychain has no entry under the configured service (Linux has no keychain lookup, so set one of the first two). When `api_key_command` is set, the error instead names that command and its exit status, so run the command yourself. Otherwise redo step 2. On macOS, check the entry exists with `security find-generic-password -s dispatch-linear-api-key >/dev/null && echo found`, which reports the entry without printing the key. Fix the cause and start `dispatch run` again.

**`dispatch run` stops at start with `unknown setting`.** The config has a key dispatch doesn't know, and the message names it and the file. The usual causes are a typo and an example copied from an old design document. See the [config reference](config.md).

**A source can't be read.** The loop prints `dispatch: while reading: ...` and goes on. A ticket whose rule needs the unreadable fact is held, not decided on a guess, and shows as `held` in `dispatch plan` with the reason. The next resync reads again, and the ticket is decided as soon as the read works.

**The run window prints `escalate ENG-4 failed: team ENG has no "Needs Human" status`.** The escalation fails and the ticket keeps its old status. The team lacks that status or calls it something else. Create it in Linear (Started category) or set `needs_human` under `[plugins.linear.statuses]`, then restart `dispatch run`, since it reads the config once. A missing `in_progress` or `close_parent` status fails the same way. A failed action is tried again at the next resync.

**The run window prints `escalate ENG-4 failed: team ENG not found`.** Dispatch found the ticket but Linear's team list has no team under its key. dispatch only reads tickets from the teams in `[plugins.linear].teams`, so a config typo can't cause this. The likely cause is a team archived or deleted after dispatch read the ticket. Check in Linear that the team is still active and that the API key's user can see it. The failed action is tried again at the next resync, so no restart is needed once the team is back.

**An In Progress ticket never launches.** Run `dispatch plan`. The ticket is skipped or escalated when it has no `repo` label, when its label has no `[repos.<label>]` table, or when it has sub-issues (a parent never gets a worker). A `design` ticket launches too, as a design session rather than a build, and a `design-review` ticket as a review session. A ticket that isn't assigned to you or lacks the `ai` label doesn't appear at all. `waiting` means the cap (`max_in_progress`) is reached, and `held` that the repo's pool couldn't be read. A full treehouse pool shows only when the launch runs, as `launch ENG-1 refused: pool_exhausted: all 4/4 pool trees are taken: ...` in the run window and in `dispatch tick`, naming who holds every tree. A `not_pooled` launch means the repo has no `treehouse.toml`.

**A worker sits in a state of working.** dispatch never nudges a working worker. Open its herdr workspace and look. To redirect it, run `dispatch tell <ticket> "<text>"`. To restart it, move the ticket to Backlog and back to In Progress. Its tree and branch stay. Wait for a resync to see Backlog before moving it back, because a round trip inside one `resync` period changes nothing.

**A tree is kept after the ticket finished.** The ticket got a comment saying what the tree holds, either modified or staged tracked files or commits no remote has. Untracked scratch files never keep a tree, since teardown deletes them. Push or discard the work and the next resync returns the tree. A branch that was squash-merged and then deleted from the remote counts as unpushed, so discard it yourself.

**A change to dispatch doesn't take effect after merging.** `dispatch run` is the binary you installed, not your checkout. Run `go install ./cmd/dispatch` in the checkout, then stop `dispatch run` with Ctrl-C and start it again. Workers pick up skill changes from `plugin_root` without a reinstall. The launcher and the claude shim's logic are part of the binary, so a change to either needs the reinstall too.
