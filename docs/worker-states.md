# Worker states

Each worker's herdr workspace is labeled `<emoji> <ticket>`, and the emoji says who the ticket is waiting on. This page is the one place that lists every state a worker can be in, the emoji it shows, and what dispatch does about it: whether it nudges the worker, notifies you or escalates the ticket. Read it when you use dispatch, and before you change a rule that nudges, notifies or escalates.

## The emoji

| Emoji | Waiting on | Meaning |
|---|---|---|
| 🟢 | The worker, or dispatch | The worker is busy, dispatch just nudged it, or dispatch is about to relaunch it or close its workspace |
| 🟡 | CI | In Review, the worker is idle, gone or unconfirmed, the PR isn't ready and its checks are still running |
| 👀 | You | In Review, the PR is ready for your review (the notification also needs an idle worker) |
| 💤 | You | The worker is idle with no background task running and nothing else pending. Open its window |
| 🔴 | You | Needs Human, or the worker is blocked on a question popup |
| ⏸️ | Nobody | Paused (Backlog or Todo) with the tree kept and a workspace still open |

💤 changes the label and nothing else. It sends no notification and no reminder, and it goes back to 🟢 within seconds of you typing into the window. That depends on herdr already reporting the worker as working when the hook fires. Otherwise 💤 stays until the next Notification or resync.

## How the label is chosen

The first line that fits wins.

1. The ticket is Needs Human: 🔴.
2. The ticket is paused and its worker's Claude session is gone: ⏸️.
3. The worker is blocked on a question popup: 🔴.
4. The worker is working, or dispatch typed into it since the last resync: 🟢.
5. The PR is ready and the worker is idle or gone: 👀.
6. The worker's Claude session is gone, or its launch isn't confirmed: 🟡 if the PR's checks are running, otherwise 🟢.
7. The worker is idle and its last Stop (the Claude Code hook that records when a turn ended) lists a background task still running, and that Stop is under 60 minutes old: 🟢.
8. The PR's checks are running: 🟡.
9. The worker has no Stop record, or its Stop didn't say what was running: 🟢. Nothing proves it is waiting.
10. Otherwise: 💤.

A PR that dispatch could not read leaves a 🟢, 👀, 🟡 or 💤 label as it is until the next read.

The Stop record comes from the plugin's Stop hook (`dispatch record-stop`). The plugin's Notification and UserPromptSubmit hooks run `dispatch wake`, which asks the running loop to read herdr and the pools again and run a cycle. That is not a resync, so nothing that failed is retried and no PR is read again unless a worker just went idle. Claude Code raises an `idle_prompt` Notification about 60 seconds after a turn ends with nobody typing, so 💤 shows within about a minute of the worker going idle. Without a running loop the label moves at the next resync. A worker dispatch just typed into shows 🟢 until the next resync, even if it has already gone idle again.

## Every combination

"Gone" means the workspace exists and the worker's Claude session is gone (the state `none`). "Unconfirmed" means the launch's first instruction hasn't run yet. A ticket with no workspace has nothing to label. The tests in `labels_test.go` cover each row.

### In Progress

| Worker | Stop record | Emoji | What dispatch does |
|---|---|---|---|
| Working | any | 🟢 | Nothing |
| Idle | none, or without the task list | 🟢 | Nothing |
| Idle | a task still running, under 60 minutes old | 🟢 | Nothing. After 60 minutes it counts as stopped, so 💤 |
| Idle | stopped, nothing running | 💤 | Nothing. No nudge and no notification |
| Idle, unconfirmed | any | 🟢 | After 5 minutes, closes the workspace and relaunches the worker. The closes count toward `launch_failure_limit` |
| Blocked on a popup | any | 🔴 | Notifies on the first cycle and every `blocked_renotify_minutes`. Never nudges, never moves the ticket |
| Gone | n/a | 🟢 | Relaunches the worker |
| Idle, ticket just moved back from Needs Human | any | 🟢 once nudged | Sends the resume nudge once per move, after the worker has stopped since its last nudge |

### In Review

dispatch reads the PR only when the worker is idle or gone.

| Worker | Stop record | PR | Emoji | What dispatch does |
|---|---|---|---|---|
| Working | any | not read | 🟢 | Nothing |
| Blocked on a popup | any | not read | 🔴 | Notifies as in In Progress |
| Idle | none | ready | 👀 | Notifies once per head commit |
| Idle | none | checks running | 🟡 | Nothing about the label. Nudges for a failed check, changes requested or a merge conflict |
| Idle | none | none, or not ready with no check running | 🟢 | Nothing about the label. Nudges for a failed check, changes requested or a merge conflict |
| Idle | stopped | ready | 👀 | Notifies once per head commit |
| Idle | stopped | checks running | 🟡 | Nothing |
| Idle | stopped | none, unresolved thread or mergeability not computed | 💤 | Nothing |
| Idle | stopped | failed check, changes requested or merge conflict | 💤, then 🟢 once nudged | PR nudge every 5 minutes. After `nudge_limit` nudges with no push, moves the ticket to Needs Human |
| Idle | a task running | ready | 👀 | Notifies once per head commit |
| Idle | a task running | anything else | 🟢 | Nudges for a PR that needs work, as above. The label stays 🟢 |
| Idle | without the task list | checks running | 🟡 | Nothing |
| Idle | without the task list | anything else | 🟢 | Nudges for a PR that needs work, as above. The label stays 🟢 |
| Idle, unconfirmed | any | ready | 👀 | Closes the workspace after 5 minutes |
| Idle, unconfirmed | any | checks running | 🟡 | Closes the workspace after 5 minutes |
| Idle, unconfirmed | any | anything else | 🟢 | Closes the workspace after 5 minutes |
| Gone | n/a | ready | 👀 | Nothing |
| Gone | n/a | checks running | 🟡 | Nothing |
| Gone | n/a | anything else | 🟢 | Relaunches the worker for a PR that needs work or an unseen review thread |

An idle In Review worker whose PR gains a review or comment gets the activity nudge once per new item.

### Other statuses

| Ticket | Worker | Emoji | What dispatch does |
|---|---|---|---|
| Needs Human | any | 🔴 | Notifies once on entering the status. A `design` ticket doesn't notify |
| Paused (Backlog or Todo) | gone, tree kept | ⏸️ | Nothing |
| Paused | live | unchanged | Closes the workspace, then ⏸️ |

A `design` or `design-review` worker is never nudged, and a `design` ticket sends no notification for a blocked popup or for Needs Human. Its label follows the same lines as a build worker's, and 💤 is how you see that a design is waiting on you.

## What dispatch no longer does

Dispatch used to type a stall nudge into an idle In Progress worker, and to notify you on every 6th stall nudge and once when a nudge went unanswered for an hour. Both are gone. The stall nudge could land on top of a question the worker had just asked and read as the answer. Without it, nothing prompts a worker that stopped In Progress without moving its ticket, and 💤 is how you see it.

A build worker asks you its questions in chat, not through the question popup. A popup it still opens is handled as before: herdr reports the worker blocked, you get the notification, and the label is 🔴.
