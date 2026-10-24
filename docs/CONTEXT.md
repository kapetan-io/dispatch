# Context

Terms dispatch's tests and docs use for the tools it drives. herdr is the terminal workspace manager whose workspaces show each worker's tree, and treehouse keeps the pools of trees that tickets lease.

**Tool fake**:
An in-process Go state model of herdr, treehouse, gh, Linear or osascript that refuses what the real tool refuses.
_Avoid_: shell fake, mock, stub

**Port**:
An interface dispatch owns and names for a tool's role (`treehouse.Pool`, `herdr.Herdr`), implemented by a real adapter and a tool fake.
_Avoid_: domain interface (ADR 0001 and older designs use it for the same thing), client interface

**Real adapter**:
The production implementation of a tool's port, which invokes the installed tool.
_Avoid_: CLI wrapper, client

**Contract scenario**:
One situation run against both a tool fake and the real adapter, which must give the same outcome.
_Avoid_: shared test, conformance test

**Drift check**:
The contract scenarios run under `-tags drift` against the installed herdr and treehouse, in a throwaway `dispatch-drift-*` session and pool. Linear is the exception. It has no throwaway workspace, so its drift check runs only the unseeded scenarios, reading the workspace `LINEAR_API_KEY` belongs to and aiming moves and comments at a ticket that does not exist. It also runs the drift tests that have no fake to compare: the flags dispatch passes to gh against the installed gh's `--help`, two read-only queries to GitHub through gh, and the launcher's commands against the installed tools. One launcher drift test (internal/launcher/commands_drift_test.go) launches a worker on a real, logged-in Claude in a throwaway herdr session and checks that the kickoff and slash nudges record as sent by dispatch; it spends a few short Claude turns.
_Avoid_: integration test, live test

**Stand-in claude**:
A Go program the drift check starts in place of Claude, which shows the startup prompts and reports its state to herdr.
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

- Each port has one **real adapter** and one **tool fake**, and every **contract scenario** runs against both.
- The **drift check** runs the scenarios against the **real adapter** only.

**World**:
What the sources say right now, held in memory and updated by each read. Every value in it is a fact.
_Avoid_: state, store, snapshot (a snapshot is the world written at the head of a history segment)

**Fact**:
One value in the world, with the time it was first seen at that value (its `since`), or unknown.
_Avoid_: field, property

**Unknown**:
A fact whose read failed or came back cut short. A `whenever` rule that reads one holds for that record, and an `on` rule drops its event, so a failed read never counts as "no".
_Avoid_: missing, empty, null

**Cycle**:
One run of every rule over the world and the ledger, after an event. It gives what dispatch wants now, what waits for a slot, and what is held and why. `dispatch rules check` runs one against the world as it is now. `dispatch cycle` prints the latest recorded one.
_Avoid_: plan, pass

**Once key**:
The value a rule gives each action, naming what must change before the action runs again, such as a comment time or a PR head. The ledger runs an action once per key.
_Avoid_: dedupe key, idempotency key

**Event**:
Anything telling dispatch something changed: a source read that differs from what it knew, an action's result, the resync, `dispatch tick`. Each event runs a cycle.
_Avoid_: notification (a notification tells you), trigger

**Resync**:
The 5-minute event that re-reads every source (Linear, GitHub, herdr, treehouse and Stop records) and runs a cycle. It is the only clock rules wait on, so a wait ends up to 5 minutes after its deadline.
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
A message to a worker that starts with `/name`, as Claude took it, recorded with its arguments (cut at 1KB), the time and whether a person or dispatch sent it.
_Avoid_: prompt, message (both include text that isn't a command), skill (a skill the model loads by itself is not a command)

**Turn**:
One reply a worker's Claude session finished, ending in a Stop, with whether a background task was still running.
_Avoid_: stop (a Stop record keeps only the latest turn)

**Session log**:
The file of one worker session's commands and turns, appended by the running dispatch from the hooks' reports and read by the world.
_Avoid_: transcript (Claude's own file), history (dispatch's segments)

**Send record**:
The text of a slash command dispatch is about to type into a worker's pane, kept in the running dispatch process and claimed by the command it matches, so the session log marks that command as sent by dispatch.
_Avoid_: ledger entry, receipt (both are in history)

**Workspace emoji**:
The first word of a worker's herdr workspace label. It says who the ticket is waiting on: the worker (🟢), CI (🟡), you (👀, 💤 or 🔴), a PR's approvers (⏳), whoever merges the PR (🔀, you when `auto_merge` is off), or nobody (⏸️). In a repo with `open_pr_as = "draft"`, a ready draft is 👀, and once you open it the PR is ⏳ until its head is approved and 🔀 after. `worker-states.md` lists every state, its emoji, and what dispatch does in it.
_Avoid_: status icon, status

**Worker**:
The ticket's place on a tree it holds: the tree, the herdr workspace showing that tree, and the Claude session running there now, if any. Claude sessions come and go inside it: a relaunch keeps the same worker with a new session, and the worker lasts as long as the ticket holds the tree. A ticket that holds trees in two repos has a worker on each. The worker's state is its Claude session's: `working`, `idle`, `blocked`, or `none` when no Claude session runs there. Looking up a ticket that holds no tree also answers `none`, though there is no worker. Some messages use "worker" for the Claude session: "has no worker" and "gone" mean the state `none`, and "a worker is running" means a Claude session runs there.
_Avoid_: agent (herdr's word for any terminal agent, kept inside the herdr adapter), runner, executor, session (a session is one Claude run inside the worker)

**Tree**:
A git worktree in a repo's treehouse pool, leased or not; a rule reads which from its `leased` fact. A ticket leases one and holds it under the ticket id until it is returned.
_Avoid_: worktree and checkout (the tools' words, used inside the tool adapters and for a plain git worktree of a repo), lease (the lease is the holding, not the tree), slot (room in the pool)

**Launch**:
A launch leases a tree for the ticket, creates the ticket's branch, opens a herdr workspace, starts Claude and types the kickoff. A relaunch is a launch on a tree the ticket already holds. The launcher is the code that does it, run by the executor or by `dispatch launch`.
_Avoid_: spawn, start (starting Claude is one step of a launch)

**Kickoff**:
The first command a launch types into the worker's new Claude session, `/dispatch:build <ticket>` unless the rule names another. A launch keeps a kickoff record in the state directory's `kickoffs` until the launch finishes. A kickoff record with no Stop from the worker since means the kickoff has not run, and the worker is unconfirmed.
_Avoid_: initial prompt, first message

**Nudge**:
Text dispatch types into a worker's pane, such as the PR nudge, a resume or a `dispatch tell`. dispatch never types into a blocked Claude session, since the text would answer its question; the shipped rules nudge only an idle one. A nudge that starts with /name is also a command, and dispatch keeps a send record of it before typing.
_Avoid_: notification (that goes to you), prompt

**Close**:
Closing a worker's herdr workspace, which ends its Claude session and keeps its tree, so a relaunch can start a new session in the same worker.
_Avoid_: kill, stop (a Stop is a turn ending)

**Return**:
Giving a tree back to its treehouse pool. dispatch closes the herdr workspace on the tree, deletes its untracked files, and returns it only when it has no uncommitted changes, no commits that no remote has, and nothing untracked left after the deletion (git clean skips a nested repository, which a return would discard). Otherwise it keeps the tree, and the refusal's detail says why. After the return it deletes the tree's Stop records (keeping the time of the return, so a Stop that ended by then is ignored even when it is reported later), session logs and kickoff record.
_Avoid_: free, delete (the untracked files are deleted, the tree is not)

**Action**:
One thing a cycle wants done, such as a launch, nudge, move or merge, named by the name its receipts carry, in rules and rule scenarios too, and carried out by the executor. A nudge, notify, comment or post is not safe to retry: when its outcome is lost it may already have gone out, so it counts as sent and never runs again.
_Avoid_: task, job, and the tool-named spelling such as `herdr.launch` (from before ADR 0003; a receipt's `kind` field holds the action's name and keeps its field name)

**Notification**:
Telling you, and the sessions following the ticket, that something happened on it, such as Needs Human or a ready PR. It shows as an alert, then goes to each follower.
_Avoid_: notice, event (an event tells dispatch; a follower's message still starts with `dispatch event`, which sessions act on), nudge (a nudge goes to the worker)

**Alert**:
The pop-up that shows you a notification, through osascript on macOS or through herdr elsewhere.
_Avoid_: toast, banner

**Follower**:
A Claude Code session that ran `dispatch watch` on a ticket and gets its notifications as messages. Followers live in the running dispatch process and end at a restart. The loop's stderr calls a follower's socket a watcher socket.
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
One thing a rule reads: a ticket, PR, worker, tree, escalation or the dispatch record, built from the world and the ledger each cycle. Elsewhere "record" keeps its plain sense, as in a Stop record, a send record, a kickoff record, and a receipt (the ledger's record of one action).
_Avoid_: object, entity, fact (a fact is one value in the world, such as a ticket's status)

**Replay**:
Running the cycles in a stretch of history again, over the world and the ledger each one ran over, and comparing each with what the live run decided (`dispatch replay`).
_Avoid_: rerun, simulation

**Explain**:
`dispatch explain <ticket>` prints, from saved history, each cycle that changed what the rules decided for the ticket, what they read, and each receipt. With `--json` it prints each of those lines as one JSON object.
_Avoid_: trace (it follows nothing live), log

**Escalation**:
A ticket dispatch moved to Needs Human, with the reason and how the move and its comment came out, read from the ledger's escalate receipts.
_Avoid_: Needs Human (that is the status an escalation moves the ticket to), alert

**Rule scenario**:
A case in a `*.test.ts` file that `dispatch rules test` runs. It sets up a world, changes it step by step, and says what the cycle after each step wants.
_Avoid_: contract scenario (a tool fake against its adapter), unit test

**Stop record**:
The latest Stop of each worker session, from the Stop hook through `dispatch record-stop`, saying whether background tasks were still running. A Stop that ended at or before its tree's last return is ignored.
_Avoid_: stop file, turn (the session log keeps every turn)
