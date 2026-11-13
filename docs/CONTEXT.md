# Context

Terms dispatch's tests and docs use for the tools it drives. herdr is the terminal workspace manager whose workspaces show each worker's tree, and treehouse keeps the pools of trees that tickets lease.

## Purpose

The user is a developer who owns the tickets, repos and rules dispatch runs. They approve a ticket by moving it to In Progress, and dispatch keeps a worker on it until its pull request is ready, telling the user when a ticket waits on them. The user keeps what gets built, the product questions, the rules and the merge.

### Tradeoffs

- **Worker time over the user's attention.** A worker reads the design, the code and the docs and tries things before it asks, so the user is asked only for product calls. The user pays for the extra worker turns.
- **Behavior in rules the user edits, not in Go.** Dispatch supplies facts and actions, and the rules decide what to do with them, so the user can change any behavior by editing a rule. The user keeps near-copies of shipped rules in step, and an edit can remove a guarantee.
- **A failed read is never an answer.** When a source that other records point at cannot be read, the decisions that depend on it are held, except a source nothing else references, such as the GitHub inbox, which keeps its last good read. During a Linear or GitHub outage, tickets stop moving.
- **A message goes out at most once.** A nudge, notification, comment or post whose outcome was lost counts as sent and is not repeated. The user can miss a notification that never arrived.
- **One slow clock, not retries.** A failed step or an expired wait is picked up at the next resync, 5 minutes by default. The user waits up to one resync for a retry.

### Capabilities

**Hand a ticket to a worker.** The user moves an `ai` ticket, or a local ticket made with `dispatch ticket create`, to In Progress. Dispatch leases a tree, starts a worker on it and moves the ticket on as the pull request progresses.

**Design a feature first.** A `design` ticket moved to In Progress starts a design conversation that ends in a blueprint and build tickets. Dispatch then starts the children in order and closes the parent when they finish.

**See who each ticket waits on.** Each worker's workspace label starts with an emoji that says whether the ticket waits on the worker, CI, the user, the pull request's approvers or whoever merges.

**Hear when a ticket needs them.** A session that runs `dispatch watch <ticket>` gets a message when the ticket is ready, needs a human or goes idle. `dispatch tell` sends a worker text, and `dispatch tick` makes dispatch act now.

**Keep pull requests moving.** Dispatch nudges an idle worker about review comments and failing checks and escalates when nudges don't help. In a repo with `auto_merge` on, it merges an approved, ready pull request.

**Change what dispatch decides.** `dispatch rules init`, `check` and `test` write, lint and test the TypeScript rules that make every decision, and `exempt` switches a shipped rule off for a class of records.

**Ask why it acted.** `dispatch cycle`, `dispatch explain <ticket>` and `dispatch replay` show what each cycle read, what it wanted and how each action came out.

**Tool fake**:
An in-process Go state model of herdr, treehouse, gh or Linear that refuses what the real tool refuses.
_Avoid_: shell fake, mock, stub

**Port**:
An interface dispatch owns and names for a tool's role (`treehouse.Pool`, `herdr.Herdr`), implemented by a real adapter and a tool fake.
_Avoid_: domain interface (ADR 0001 and older designs use it for the same thing), client interface

**Multiplexer**:
The product that runs the workers' terminals and shows them: the herdr TUI, herdr-core with the Herdr app as its window, or Rex. `[plugins.herdr] host` picks one of the first two, writing `[plugins.rex]` picks Rex, and dispatch drives only one.
_Avoid_: host (reads as a machine), backend

**Stream**:
A multiplexer's report, as it happens, of each pane whose agent may have changed. Dispatch reads the pane again to learn what changed. A change on the stream only asks for a pane read, and that read is the source read an Event names. A change about a pane that holds no worker reads nothing. Only Rex has one.
_Avoid_: event stream, watcher, subscription

**Pane read**:
A read of the panes a stream named, one `AgentAt` each, that rebuilds only the workers on them and lands like any other read.
_Avoid_: partial refresh, event apply

**Real adapter**:
The production implementation of a tool's port, which invokes the installed tool.
_Avoid_: CLI wrapper, client

**Contract scenario**:
One situation run against both a tool fake and the real adapter, which must give the same outcome. Linear's scenarios run against the tool fake only, because the drift check never touches a Linear account.
_Avoid_: shared test, conformance test

**Drift check**:
The contract scenarios run under `-tags drift` against the installed herdr and treehouse, in a throwaway `dispatch-drift-*` session and pool, and against the installed `hc` on a scratch core (own `HC_SESSION`, scratch XDG paths, a `/tmp` socket), never the user's core. It never touches a GitHub or Linear account. It also runs the drift tests that have no fake to compare: the flags dispatch passes to gh against the installed gh's `--help`, and the launcher's commands against the installed tools. Two launcher drift tests (internal/launcher/commands_drift_test.go) launch a worker on a real, logged-in Claude and check that the kickoff and slash nudges record as sent by dispatch, one in a throwaway herdr session and one with `[plugins.herdr] host = "app"` on a scratch core. The app run also checks that the Worker label `dispatch.session` is the session id in Claude's transcript and that the worker's Stop reached its Stop record; a hook that cannot find its terminal through `HC_TERMINAL_ID` shows up as a timeout waiting for the worker's first turn. It also checks that the hooks' environment names the scratch core. Each spends a few short Claude turns.
_Avoid_: integration test, live test

**Stand-in claude**:
A Go program the drift check starts in place of Claude, which shows the startup prompts and reports its state to whichever multiplexer runs it.
_Avoid_: fake claude (that name belongs to the `scripts/shims/claude` test)

**Repo settings**:
A `[repos.<label>]` table in `config.toml`, holding a repo's `path` and every per-repo setting. A table with only `path` behaves as dispatch did before per-repo settings.
_Avoid_: repo config, repo entry

**Listed author**:
A GitHub login in a repo's `ignore_comment_authors`. Dispatch and the worker act as if nothing it posts exists.
_Avoid_: bot, ignored bot (some bots, such as CodeRabbit, are not listed and still count)

**Optional check**:
A check GitHub does not mark required for merging the PR. It never counts in a repo with `ignore_optional_checks = true`.
_Avoid_: non-blocking check, flaky check

**Approval check**:
A check that passes only after a human approves the PR, such as "Mergify Merge Protections". A repo names them in `approval_checks`, and they count for nothing, so a draft waiting for review is still ready.
_Avoid_: gate, blocking check

**PR phase**:
Draft or open, read from GitHub's `isDraft` on every resync.
_Avoid_: PR state (GitHub's `state` is OPEN for a draft too)

**Review question**:
A review or comment that asks rather than claims. The worker answers a question about what the code does, and sends a question needing a product decision to the user.
_Avoid_: review claim (a claim asserts something to check)

## Relationships

- Each port has one or more **real adapters** (the herdr port has two, one per **multiplexer**) and one **tool fake**, and every **contract scenario** runs against both, except Linear's, which run against the fake only.
- The **drift check** runs the scenarios against the **real adapter** only, and does not run Linear's.
- Every ticket belongs to exactly one **Ticket system**, and its id's prefix says which.
- A **Local ticket** has one status, and each status carries any number of **Jobs**. `todo`, `in_progress`, `in_review`, `needs_human`, `close_parent` and `blocker_done` each belong to exactly one status, `finished` and `paused` to any number.
- A **Local ticket** has any number of **Attributes** and at most one **Start PR**.
- A **Start PR** is never its ticket's PR and writes no **Stack record**.
- The local ticket store is not a **Port**: it has no **Tool fake**, and tests run it on a temp file.
- A **Stacked ticket** has exactly one **Stack parent** and one **Stack record** until a **Restack** removes the record.

**World**:
What the sources say right now, held in memory and updated by each read. Every value in it is a fact.
_Avoid_: state, store, snapshot (a snapshot is the world written at the head of a history segment)

**Fact**:
One value in the world, with the time it was first seen at that value (its `since`), or unknown.
_Avoid_: field, property

**Unknown**:
A fact whose read failed or came back cut short. A `whenever` rule that reads one holds for that record, and an `on` rule drops its event, so a failed read never counts as "no". A failed inbox read is the exception: it keeps the last good items known, because nothing else points at an inbox item. A failed read of a clone's remote follows it and keeps that clone's last good repo. A failed read of local tickets makes local ticket records unknown and leaves Linear's alone, and the reverse, because worker, tree and PR records point at tickets of both systems.
_Avoid_: missing, empty, null

**Cycle**:
One run of every rule over the world and the ledger, after an event. It gives what dispatch wants now, what waits for a slot, and what is held and why. `dispatch rules check` runs one against the world as it is now. `dispatch cycle` prints the latest recorded one.
_Avoid_: plan, pass

**Once key**:
The value a rule gives each action, naming what must change before the action runs again, such as a comment time or a PR head. The ledger runs an action once per key.
_Avoid_: dedupe key, idempotency key

**Event**:
Anything telling dispatch something changed: a source read that differs from what it knew, an action's result, the resync, `dispatch tick`. Each event runs a cycle.
_Avoid_: notification (a notification tells a follower), trigger

**Resync**:
The 5-minute event that re-reads every source (Linear, local tickets, GitHub, the GitHub inbox while a rule runs over it, herdr, treehouse and Stop records) and runs a cycle. It is the only clock rules wait on, so a wait ends up to 5 minutes after its deadline.
_Avoid_: tick (`dispatch tick` forces one early), poll interval

**Ledger**:
What dispatch did, one receipt per action, keyed by the action's once key. A rule asks it whether an action already happened.
_Avoid_: log, history (history is the segment files the ledger is written to)

**Receipt**:
The ledger's record of one action: key, rule, outcome (done, refused, failed; absent while attempted), detail, times.
_Avoid_: result, audit entry

**Executor**:
Starts each action a cycle wants whose once key the ledger doesn't cover, off the loop, and reports how it ended. The loop writes the receipt.
_Avoid_: runner, worker (a worker is the ticket's place on its tree, where its Claude sessions run)

**Command**:
A message to a worker that starts with `/name`, as the session took it, recorded with its arguments (cut at 1KB), the time and whether a person or dispatch sent it.
_Avoid_: prompt, message (both include text that isn't a command), skill (a skill the model loads by itself is not a command)

**Turn**:
One reply a worker's session finished, ending in a Stop, with whether a background task was still running.
_Avoid_: stop (a Stop record keeps only the latest turn)

**Session log**:
The file of one worker session's commands and turns, appended by the running dispatch from the hooks' reports and read by the world.
_Avoid_: transcript (Claude's own file), history (dispatch's segments)

**Send record**:
The text of a slash command dispatch is about to send to a worker's session (a nudge) or type into its pane (a kickoff), kept so the session log can tell dispatch's commands from a person's.
_Avoid_: ledger entry, receipt (both are in history)

**Workspace emoji**:
The first word of a worker's workspace label (under Rex, of its window label, which reads `<emoji> <ticket> <title>`). It says who the ticket is waiting on: the worker (🟢), CI (🟡), you (👀, 💤 or 🔴), a PR's approvers (⏳), whoever merges the PR (🔀, you when `auto_merge` is off), or nobody (⏸️). In a repo with `open_pr_as = "draft"`, a ready draft is 👀, and once you open it the PR is ⏳ until its head is approved and 🔀 after. `worker-states.md` lists every state, its emoji, and what dispatch does in it.
_Avoid_: status icon, status

**Workspace**:
The multiplexer's place showing a worker's tree: a herdr TUI workspace, a Herdr app space whose id dispatch derives from the tree, or under Rex the window showing the tree in its repo session, with an id built from the repo session's label and the tree. Code and docs say workspace, and the word window stays inside the Rex adapter, as the app's word space stays inside the app adapter. The first word of its name is the workspace emoji.
_Avoid_: space (the app's word, kept inside the app adapter), window

**Worker label**:
The multiplexer's record of whose worker a terminal is. Under the app host it is `dispatch.*` key=value pairs on the terminal (`dispatch.tree`, `.session`). Rex has no key-value labels, so the block's label holds `dispatch <claude session id or -> <first 8 characters of the server's instance id> <tree>`, at most 128 characters, which limits a tree to 73. It is distinct from the workspace and tab labels the loop renames (the workspace emoji), and from **Unknown**, which is a fact whose read failed, not the agent status `unknown`.
_Avoid_: tag, metadata

**Repo session**:
The Rex session dispatch runs a repo's workers in, labeled with the folder name of the repo's main checkout. A launch creates it when no session has that label. A close leaves it, unless `[plugins.rex] delete_empty_sessions` is on and the close removed a worker and left nothing else in it (no block of the user's or another worker's, and none outside every window), which deletes it.
_Avoid_: workspace, project, Rex session (any session, the user's included). Elsewhere session means one harness run inside a worker, so say repo session in full for the Rex object.

**Structure lock**:
The file lock `rex.lock` in dispatch's state directory, held while dispatch changes Rex sessions, windows or worker blocks, so two launches on one tree leave one worker.
_Avoid_: start lock, mutex

**Worker**:
The ticket's place on a tree it holds: the tree, the workspace showing that tree, and the harness session running there now, if any. Only a session of a harness dispatch has an adapter for counts; an agent of another kind a person opened in the workspace, such as a Pi, is ignored. Sessions come and go inside it: a relaunch keeps the same worker with a new session, and the worker lasts as long as the ticket holds the tree. A ticket that holds trees in two repos has a worker on each. The worker's state is its session's: `working`, `idle`, `blocked`, or `none` when no session runs there. A rule that looks up a ticket that holds no tree gets a worker record with state `none`, though there is no worker. Some messages use "worker" for the session: "has no worker" and "gone" mean the state `none`, and "a worker is running" means a session runs there.
_Avoid_: agent (herdr's word for any terminal agent, kept inside the herdr adapter; dispatch's word is harness), runner, executor, session (a session is one harness run inside the worker)

**Tree**:
A git worktree in a repo's treehouse pool, leased or not; a rule reads which from its `leased` fact. A ticket leases one and holds it under the ticket id until it is returned.
_Avoid_: worktree and checkout (the tools' words, used inside the tool adapters and for a plain git worktree of a repo), lease (the lease is the holding, not the tree), slot (room in the pool)

**Harness**:
The program that runs in a worker's session, such as Claude Code, which is the only one dispatch drives today. Every harness receives follower messages, so an adapter that cannot is not a harness dispatch drives. A harness table pairs a harness kind with a model. A kind is the name `config.toml` gives the program, such as `claude-code`; herdr detects the same program under its own agent kind, such as `claude`.
_Avoid_: agent (herdr's word, kept inside the herdr adapter), tool (the tools are herdr, treehouse, gh, Linear), model (a harness runs a model)

**Harness table**:
A `[harness.<name>]` table in `config.toml`, named everywhere as `harness.<name>`. A rule passes that name to `queueLaunch`; `worker` and `design` in `[rules]` or `[repos.<label>]` name the table the shipped rules use for build and design workers.
_Avoid_: profile, role (a role is only which of `worker` / `design` a ticket's launch reads)

**Program** (herdr):
What herdr runs in a worker's new tab, as a harness describes it: a name for messages, the herdr agent kind, arguments, directories put ahead of PATH, tab environment, and the startup screens (dialogs to answer, the unrun launch command, the output that shows it ran, the ready input box) and the session id the launch chose, which each adapter passes with the harness's session flag (Claude: `--session-id`), and the flag that resumes an earlier session (Claude: `--resume`), which Core uses after a herdr-core restart.
_Avoid_: command line, launch spec

**Launch**:
A launch leases a tree for the ticket, creates the ticket's branch (at another ticket's PR head for a stacked launch, or at a local ticket's start PR head), opens a workspace, starts the harness and types the kickoff. A relaunch is a launch on a tree the ticket already holds. The launcher is the code that does it, run by the executor or by `dispatch launch`.
_Avoid_: spawn, start (starting the harness is one step of a launch)

**Kickoff**:
The first command a launch types into the worker's new session, `/dispatch:build <ticket>` unless the rule names another. A launch keeps a kickoff record in the state directory's `kickoffs` until the launch finishes. A kickoff record with no Stop from the worker since means the kickoff has not run, and the worker is unconfirmed.
_Avoid_: initial prompt, first message

**Stacked ticket**:
A ticket whose branch the launcher started at another ticket's PR head instead of the default branch, so it is built and reviewed before that ticket merges.
_Avoid_: dependent ticket, child (a child is a piece of a Linear parent)

**Stack parent**:
The ticket whose PR head a stacked ticket's branch started from.
_Avoid_: parent (a Linear parent ticket), base ticket

**Stack record**:
Two keys in the repo clone's git config under the stacked ticket's branch naming the stack parent and the fork point. The launcher writes it once on a brand-new branch, and dispatch never changes it once the branch exists. Rules read it as `t.stack`.
_Avoid_: stack state, stack metadata

**Ticket branch**:
The branch a ticket's work is on. It is named by the ticket branch key, the git config variable `branch.<name>.dispatchTicket` in the repo clone, whose value is the ticket's id. The launcher writes the key when it puts a worker on a branch, and at start dispatch writes it for a tracked ticket whose one local branch starts with its id. git moves the key when a branch is renamed and drops it when the branch is deleted. The ticket system's name for the branch is only the first guess, since Linear writes the id followed by a title that changes or gets cut short.
_Avoid_: launched branch

**Fork point**:
The commit a stacked ticket's branch started at, the stack parent's PR head at launch.
_Avoid_: merge base (git computes that, and it moves)

**Restack**:
Moving a stacked ticket's own commits onto the default branch after its stack parent merged, and pointing its PR there. A worker does it when a rule tells it to.
_Avoid_: rebase (the git step), retarget (one step of it)

**Nudge**:
Text dispatch delivers to a worker's session as a message through its harness, never typed into the pane. A nudge that fails sent nothing. A rule's nudge is wanted again at the next cycle, and a failed `dispatch tell` is reported to its caller. Dispatch sends no nudge to a blocked session, to one whose status herdr reports as unknown, or to a worker whose session id herdr does not report yet.
_Avoid_: notification (that goes to you), prompt

**Close**:
Closing a worker's workspace, which ends its session and keeps its tree, so a relaunch can start a new session in the same worker.
_Avoid_: kill, stop (a Stop is a turn ending)

**Return**:
Giving a tree back to its treehouse pool. dispatch closes the workspace on the tree, deletes its untracked files, and returns it only when it has no uncommitted changes, no commits that no remote has, and nothing untracked left after the deletion (git clean skips a nested repository, which a return would discard). Otherwise it keeps the tree, and the refusal's detail says why. After the return it deletes the tree's Stop records (keeping the time of the return, so a Stop that ended by then is ignored even when it is reported later), session logs and kickoff record.
_Avoid_: free, delete (the untracked files are deleted, the tree is not)

**Action**:
One thing a cycle wants done, such as a launch, nudge, move or merge, named by the name its receipts carry, in rules and rule scenarios too, and carried out by the executor. A nudge, notify, comment, post or create is not safe to retry: when its outcome is lost it may already have gone out, so it counts as sent and never runs again.
_Avoid_: task, job, and the tool-named spelling such as `herdr.launch` (from before ADR 0003; a receipt's `kind` field holds the action's name and keeps its field name)

**Notification**:
Telling the sessions following a ticket that something happened on it, such as Needs Human, a ready PR or a worker going idle (💤). It goes to each follower as a message. A ticket nobody follows gets no notification, only its Linear status. A message about dispatch itself (Linear has more tickets than one page, GitHub rate-limits the PR reads, a rule threw or ran out of time, dispatch stopped) goes to every following session, once each.
_Avoid_: notice, event (an event tells dispatch; a follower's message still starts with `dispatch event`, which sessions act on), nudge (a nudge goes to the worker)

**Inbox item**:
One entry in the dispatch owner's GitHub notifications inbox, as GitHub lists it: why GitHub sent it (`review_requested`, `mention`, ...), the repo, and the PR or issue it's about. While a loaded rule runs over them, the world holds the newest 50, read on every resync. Not the loop's `inbox` channel or a session's inbox socket.
_Avoid_: notification (that means dispatch telling a follower)

**Follower**:
A session of a harness dispatch can deliver to that ran `dispatch watch` on a ticket and gets its notifications as messages through that harness. The address dispatch delivers to is the follower's session id. Followers live in the running dispatch process and end at a restart. The loop's warnings say "session", not "watcher socket".
_Avoid_: subscriber (`dispatch subscribe` is gone)

**Loop**:
The reconcile loop `dispatch run` keeps going: one goroutine that takes one event at a time and runs a cycle after each. Reads and actions run off it.
_Avoid_: supervisor, daemon, dispatcher (the whole program)

**Memory**:
What the world remembers across a restart (`world.Memory`): each fact's value and its `since`, keyed by record kind, id and field, and each ticket's previous status, waiting time, pool returns and children. Each cycle carries it forward, and a fact keeps its `since` only while the same key holds the same value.
_Avoid_: state, cache, "held in memory" (the World entry's sense, meaning the running process)

**History**:
The segment files dispatch writes under `[history].dir`. A segment is one `events-<time>.jsonl` file: a header with the config and rules it ran, a snapshot of the memory and ledger before it, then every event, read, change, cycle and receipt. `dispatch run` loads the newest segment at start.
_Avoid_: log, journal (the tool fakes' record of calls)

**Record**:
One thing a rule reads: a ticket, PR, worker, tree, escalation, inbox item or the dispatch record, built from the world and the ledger each cycle. Elsewhere "record" keeps its plain sense, as in a Stop record, a send record, a kickoff record, and a receipt (the ledger's record of one action).
_Avoid_: object, entity, fact (a fact is one value in the world, such as a ticket's status)

**Replay**:
Running the cycles in a stretch of history again, over the world and the ledger each one ran over, and comparing each with what the live run decided (`dispatch replay`).
_Avoid_: rerun, simulation

**Explain**:
`dispatch explain <ticket>` prints, from saved history, each cycle that changed what the rules decided for the ticket, what they read, and each receipt. With `--json` it prints each of those lines as one JSON object.
_Avoid_: trace (it follows nothing live), log

**Escalation**:
A ticket dispatch moved to Needs Human, with the reason and how the move and its comment came out, read from the ledger's escalate receipts.
_Avoid_: Needs Human (that is the status an escalation moves the ticket to)

**Rule scenario**:
A case in a `*.test.ts` file that `dispatch rules test` runs. It sets up a world, changes it step by step, and says what the cycle after each step wants.
_Avoid_: contract scenario (a tool fake against its adapter), unit test

**Stop record**:
The latest turn end of each worker session, from the harness's report through `dispatch record-stop`, which finds the harness from the terminal the hook runs in (`HC_TERMINAL_ID` under the Herdr app, `HERDR_PANE_ID` under the herdr TUI, `REX_SESSION` with `REX_BLOCK` under Rex), saying whether background tasks were still running. A turn end at or before its tree's last return is ignored.
_Avoid_: stop file, turn (the session log keeps every turn)

**Ticket system**:
Where tickets come from and are written back: Linear, or dispatch's own local tickets. A ticket's id says which: `LOC-` starts a local ticket's.
_Avoid_: source (what the world reads), tracker

**Local ticket**:
A ticket in dispatch's own database, id `LOC-<n>`, which only the holder of `dispatch.lock` opens for writing. `dispatch ticket` reaches it through the running `dispatch run`; `dispatch rules check` and `dispatch state reset` read it without writing, under the lock or by asking the running `dispatch run`. Numbers count up and are never reused, and nothing deletes a ticket.
_Avoid_: internal ticket, offline ticket

**Attribute**:
A key-value pair on a local ticket, beside its labels, that `dispatch ticket list --attr` matches.
_Avoid_: field, property (both name a fact), metadata, tag

**Start PR**:
A PR a local ticket names, whose head the ticket's own branch starts at.
_Avoid_: base PR, stack parent (a ticket a branch is stacked on)

**Job**:
What dispatch does with a status: `todo`, `in_progress`, `in_review`, `needs_human`, `close_parent`, `blocker_done`, `finished` or `paused`. A local ticket database keeps each status with its jobs.
_Avoid_: status key (the rules' `status` value), role, and "job" for an action (an action is what a cycle wants done)
