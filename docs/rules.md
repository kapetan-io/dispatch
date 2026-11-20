# Rules

Dispatch's decisions, such as when to start a worker, nudge one or escalate a ticket, are written as TypeScript rules in a directory you own. `dispatch rules` lints them, tests them and shows what each would do right now. `dispatch run` loads them once at start and runs every one after each event, so they make every decision, and a change to a rule takes effect when you stop `dispatch run` and start it again. `dispatch rules check` and `dispatch rules test` let you try a change first, and `dispatch cycle` shows what the latest cycle of a run wanted.

## Get the rules

```sh
dispatch rules init            # writes to [dispatch].rules in config.toml
dispatch rules init ~/rules    # or to a directory you name
```

`init` writes the 28 shipped rules, their scenario tests, the generated types an editor reads, a `tsconfig.json`, a JSON schema for `config.toml`, and `config.example.toml`. It refuses to overwrite a file, so run it into an empty directory, and nothing is written when any file would collide.

The rules read these keys of the `[rules]` table in `config.toml`. Copy them from `config.example.toml`. `worker_model` and `design_model` are optional, since the shipped rules fall back to `sonnet` and `opus`, and `worker` / `design` can name a harness table instead (see `docs/config.md`):

```toml
[rules]
worker_model = "sonnet"
design_model = "opus"
launch_failure_limit = 2
nudge_limit = 2
blocked_renotify_minutes = 15
```

## Check them against the world

`dispatch rules check` lints the rules, reads Linear, the multiplexer, treehouse and GitHub once, runs one cycle, and prints what it found. With `[plugins.local]` on it reads the local tickets too, and the `ticket` line says how many of the tracked tickets are local and which statuses they use. It never changes the local ticket database or creates it: with `dispatch run` stopped it opens the file read-only, and while `dispatch run` holds the file it asks that process for the tickets.

```
ticket             37 tracked tickets (assigned to Ada Lovelace; teams ENG; statuses Todo, In Progress, In Review, Needs Human)
...
launch-worker      whenever  ticket  wants launch now for ENG-12, ENG-15
nudge-pr-activity  whenever  pr      wants nothing now
```

With local tickets on, the `ticket` line ends `; includes 2 local tickets (statuses Backlog, Todo, In Progress, In Review, Needs Human, Done, Canceled)`, and a rule an exemption stopped for a local ticket reads `exempt for LOC-1 by no-design-worker-on-local-ticket`.

A record whose rule read something that couldn't be read is listed as `held for ENG-9`. It exits 1 on any finding: a rule that fails to load, a rule that throws or runs out of time, a queued action with no once key, a rule that reads a `[rules]` key the file lacks, a rule that reads a fact the build doesn't produce (the rules may be newer than the build), and, when `tsc` is installed, a type error from `tsc --noEmit --strict`.

A check takes no action: it reads the world and rewrites `types/`. Every fact's `since` is the time of the read and the ledger is empty, so it shows what a fresh start would want.

## Test them

`dispatch rules test` runs the `*.test.ts` files in the rules directory in the same runtime a check uses. A scenario sets up a world, changes it step by step, and says what the cycle after each step wants:

```ts
import { scenario, ticket, worker, rulesFrom } from "dispatch/testing";

const rules = rulesFrom("./dispatch");

scenario(rules, "a crashed worker is relaunched once", s => {
  s.given(ticket("ENG-1", { status: "in_progress", optedIn: true, repo: "dispatch" }), worker("ENG-1", "working"));
  s.see(worker("ENG-1", "none"));
  s.expect([["launch-worker", "launch", "ENG-1"]]);
  s.land("launch", "ENG-1");
  s.expect([]);
});
```

`s.see` is a read that differs, `s.land` is an action's receipt, `s.unread` marks a source as failed, `s.expectHeld` names a rule held for a record, and `s.expectExempt` names a rule an exemption stopped for a record. `types/testing.d.ts` lists all of them.

`rulesFrom` takes several entry files and loads them as if one file imported each in order, so a scenario can test your rules with an example beside them: `rulesFrom("./dispatch", "./stacking")`.

## Writing a rule

```ts
import { whenever } from "dispatch";

whenever({
  name: "bugs-get-their-own-model",
  each: "ticket",
  if:   (t, ctx) => t.status === "in_progress" && t.optedIn && t.hasLabel("bug") && ctx.worker.of(t).state === "none",
  then: (t, ctx) => ctx.worker.queueLaunch(t, { model: ctx.config.rules.bug_model, once: ["launch-bug", ctx.worker.of(t).goneSince] }),
});
```

- `whenever` wants `then` while `if` is true, for every record of the kind in every cycle. `on` runs once per new event of a type. `exempt` switches named `whenever` rules off for the records its `if` is true of, or takes named actions away for the tickets its `if` is true of (see below).
- Every queued action carries `once: [topic, ...values]`. The action runs once per key, so the key says what must change before it runs again.
- `ctx.notify.queueSend` takes a `reason` option, the words the notification's followers read after `dispatch event (`. With none, the reason is the once key's topic. Following sessions key on the words the shipped rules pass, such as `PR ready for review` (see `skills/dispatch/SKILL.md`), so rewording one changes what a following session does.
- `queueLaunch` takes `harness: "harness.<name>"` to launch on a `[harness.<name>]` table, or `model` to launch Claude Code with that model. Both together, or a `harness` that names no table, is refused with a detail naming it. With neither, the worker runs Claude Code on `worker_model`. The shipped `launchAs` (in `shared.ts`) returns the table a repo's or `[rules]`'s `worker` / `design` key names, else `worker_model` or `design_model` for the ticket's role, so a rule that spreads it gets the right one. A ticket whose `model` attribute is set wins over all of those: `launchAs` returns that model alone, so Claude Code runs it on the ticket's model, for a local ticket (`dispatch ticket create local --attr model=opus`) and for a Linear ticket (a `model: opus` line under `## Attributes`). `dispatch explain` prints the model or table a launch wanted.
- `queueLaunch` takes `stackOn: pr` to start the ticket's branch at that PR's head and write a stack record in its branch row, so the ticket builds before the PR's ticket merges. The launch receipt says `stacked on <ticket> at <short head> (<branch>)`. A relaunch, or a launch on a branch that exists, writes and changes no record. A worker launched with `/dispatch:build <id> --base <branch>` opens its PR against that branch.
- `t.stack` is the stack record in the ticket's branch row, undefined for a ticket with none and unknown when the branch rows can't be read or the repo's git config keys are not imported yet. `t.branch` is the branch its branch row names, else the ticket system's name for it, `blocker.ticket` is the tracked ticket of a blocker, and `ctx.pr.of(t)` is the ticket's PR, undefined when dispatch read it and found none and unknown when it didn't read it this cycle. Dispatch reads the open PR of an In Review build ticket while its worker is idle or gone. It also reads the PR of a Needs Human build ticket while its worker is idle, so a `pr` rule that should act only on In Review tests `pr.ticket.status`. The shipped `nudge-pr-activity` nudges the worker about each new `pr.lastActivity` in both statuses.
- `t.system` is `"linear"` or `"local"`, the ticket system the ticket comes from, and `t.attributes` is the ticket's key-value pairs (`{}` when it has none). A local ticket stores them. A Linear ticket writes them in its description, one `key: value` per line under a `## Attributes` heading (any case) that runs to the next heading, with both sides trimmed, a line without a colon skipped, and the last of a repeated key kept. Write plain lines, since a list marker stays in the key. Keys are case-sensitive, so `Model: opus` is not the `model` attribute. A rule can read them but not write them on a Linear ticket. `dispatch ticket edit <id> --attr key=value` writes them on either system. A local ticket is the same `ticket` record: `optedIn` is true, `repo` is its own repo, `url` is empty, and `status`, `finished`, `paused` and each blocker's `done` come from the jobs of its own database's statuses, not from `[plugins.linear.statuses]`. `statusSince` is when the ticket entered its current status, as the database recorded it.
- `pr.merged` is true for a local ticket's PR that merged into the repo's default branch, and `pr.mergedAt` is when, in milliseconds since the epoch (0 while unmerged). Dispatch reads only open PRs for a Linear ticket, whose merge Linear's GitHub integration records, so `pr.merged` is always false on one. For a local ticket it also looks, at each resync, for the newest merged PR of the ticket's branch that comes from the repo itself, so a fork's PR on the same branch name, or a PR merged into another branch such as a stack parent's, never counts. A merged PR is a `pr` record with `merged` and `closed` true and no work pending. Compare `pr.mergedAt` with `pr.ticket.statusSince` to count only a merge newer than the ticket's current status, as `finish-merged-local-ticket` does so that a ticket moved back out of Done is not closed again by its earlier PR. A ticket filed or moved since the last resync has not been looked at yet and reads as having no PR until the next resync. While the read fails, the `pr` record is unknown and the shipped rules hold, launches included; it prints `dispatch: while reading the merged PRs of the <repo> repo: <error>` once per failed read.
- Each ticket system is read or unknown on its own. A failed read of local tickets makes the local `ticket` records unknown and leaves Linear's alone, and the reverse, because workers, trees and PRs point at tickets of both systems. It prints `dispatch: while reading the local tickets: <error>` once per failed read. [ADR 0005](adr/0005-a-failed-read-of-a-source-nothing-references-keeps-its-last-good-read.md) explains why a source that other records point at is unknown rather than last-known-good.
- A rule keeps no state. Reading a fact whose read failed throws `Unknown`, and the rule is held for that record even if it catches the error. Wrap a read in `known(() => ...)` to ask whether it could be read.
- `Date` is pinned to `ctx.now`, `Math.random` throws, and `eval`, `Function`, `Promise`, async functions and generators aren't available.
- Each `if` has one second and each `then` thirty. A rule that throws or times out wants nothing for that record, and every other rule goes ahead.

## Switching off a rule for some records

A rule file can't edit another file's rule, and a second rule with the same name is a load error. `exempt` takes away the wants of the rules or the actions it names for the records its `if` is true of, from any file, so you don't edit a shipped rule or turn it off for every record.

An `exempt` that names rules is the last tool to reach for when a rule acts when you don't want it to, not the first. It exists for one case, a shipped rule that is right for most records and wrong for a class of them you can name, such as the PR of a stacked ticket, which conflicts until its parent merges and has nothing for a nudge to fix. An exemption changes what a rule does without touching the rule, so a reader of the rule won't see it. Before you write one, ask in this order.

- Is the rule wrong for everyone? Fix it in the file that defines it.
- Do you want different behavior and not less? Write a new rule.
- Is the rule your own? Put the condition in its `if`.

```ts
import { exempt } from "dispatch";

exempt({
  name: "conflict-is-not-work",
  each: "pr",
  rules: ["nudge-pr-needs-work", "relaunch-for-pr"],
  if:   (pr) => pr.conflict && pr.ticket.hasLabel("stacked"),
});
```

- With `rules`, `each` is the record kind, as in `whenever`. Every rule in `rules` must be a `whenever` rule of that kind. A rule no file defines, an `on` rule, a rule of another kind and a name a rule or exemption already has are load errors, so `rules check` fails and a run won't start.
- For a record `if` is true of, the named rules want nothing, and they act as before for every other record. The exemption has no `then`. It takes away what a rule wants and changes no setting, so behavior a rule doesn't already have is a new rule.
- An exemption whose `if` reads an unknown fact holds the rules it names for that record, so a guard that can't read its fact never lets a rule through. One that throws or runs out of time stops them too, and `rules check` reports the throw.
- An exemption that names rules runs once per record of its kind in a cycle, before the named rules' own `if`. A shadow rule an exemption stops reports no would-be action for that record, and `rules check` lists it as exempt.
- `dispatch rules check` lists the exemption (`name  exempt  pr  exempts a, b`) and ends the rule's line with `exempt for ENG-4 by conflict-is-not-work`. `dispatch cycle` lists the stopped rule as `exempt`, and `dispatch explain` prints `exempt: stopped by conflict-is-not-work` for the ticket. A saved cycle with exemptions replays with no differences.
- `pr.conflict` is true when GitHub reports the PR's merge state as conflicting. `pr.needsWork` and `pr.problem` still count a conflict, so a rule that should leave a conflict out exempts the rules that read them, as above. A saved cycle from before `pr.conflict` reads it as false.

### Taking an action away for some tickets

An exemption that names rules stops only those rules, so a rule written later, shipped or your own, acts on the tickets you meant to leave alone. To take a kind of action away for a class of tickets whichever rule asks for it, name actions in place of rules. This is the way to say "no nudge and no merge for these tickets", and a shipped rule file may use it to guard every rule, including ones not yet written.

```ts
import { exempt } from "dispatch";

exempt({
  name:    "leave-on-call-alone",
  each:    "ticket",
  actions: ["nudge", "merge", { kind: "escalate", topic: "pr-ignored" }],
  if:      (t) => t.hasLabel("on-call"),
});
```

- For a ticket its `if` is true of, no rule's want of a listed action about that ticket goes through, whichever rule queued it: a `whenever` or an `on` rule, from any file. A want about a PR, a worker or a tree counts as about its ticket. Every later step of the same `then` that waits on a want it took away goes too, so a rule that merges and then notifies loses both, and neither is reported as waiting.
- An entry is an action's receipt name (`nudge`, `merge`, `escalate`, `notify` and the rest), or `{ kind, topic }`, the shape `ctx.ledger.receipts` filters by, where `topic` is the first element of the want's once key.
- Give exactly one of `rules` and `actions`. Neither, both, an empty `actions`, an action no rule can queue, an entry of another shape, and `actions` with an `each` other than `"ticket"` are load errors that name the exemption.
- It runs after the rules' `then`, on what they queued, so the rules' reads show in `dispatch explain` and an HTTP call a `then` makes still goes out. It runs only for a ticket some rule wants a listed action about, at most once per ticket in a cycle. A shadow rule's want it takes away is reported as exempt, not as a would-be action.
- An `if` that reads an unknown fact holds the wants (`rules check` reports them held), or for an `on` rule drops them, as its own unknown read does. One that throws takes them away, as an exemption that names rules does for its rules.
- `dispatch rules check` lists it as `leave-on-call-alone  exempt  ticket  exempts nudge, merge, escalate (pr-ignored)` and ends the line of each rule it took a want from with `exempt for ENG-4 by leave-on-call-alone`. `dispatch cycle` lists each want taken away as `exempt` with its action and once key, `dispatch explain` prints `exempt: stopped by leave-on-call-alone, nudge once [poke ENG-4]`, and a scenario asserts one with `s.expectExempt(rule, subject, by, receipt?)`, where `receipt` names the action and may be left out.

## Reading a worker's turns

`ctx.worker.of(t).turns` lists every turn the worker's current Claude session has ended, oldest first, each as `{ at, running }`. `running` says some background task of the session (a shell, subagent or monitor) was still running as the turn ended, so a rule can tell a worker looping on a background job from one that stalled:

```ts
whenever({
  name: "long-loop-in-review",
  each: "ticket",
  if:   (t, ctx) => {
    if (t.status !== "in_review") return false;
    const turns = ctx.worker.of(t).turns;
    return turns.length >= 20 && turns.every((turn) => turn.running);
  },
  then: (t, ctx) => ctx.ticket.queueComment(t, "this worker has run in the background for 20 turns", { once: ["long-loop", t.id] }),
});
```

- Check the ticket's status before reading it, as the example does, because a read that throws `Unknown` holds the rule for every other ticket. It is read for a worker in any state on an In Progress, In Review or Needs Human ticket. It is empty for a session that has ended no turns, and unknown when the multiplexer reports no session, the session's record can't be read, or the ticket is in another status.
- A dispatch killed in the middle of writing a turn leaves a half line, and the next turn starts a new line after it. From then on `turns` is unknown for the rest of that session, because a list with a hole in it would pass for a shorter one. A rule that reads it without `known(...)` is held for that worker until the session ends or the worker is relaunched.
- A relaunched worker's new Claude session reads an empty list, even on the same tree. A worker the app host resumed after a herdr-core restart, or a worker dispatch restarted with `claude --resume` after a Rex restart, keeps its session, so it keeps its list. The lists survive a restart and `dispatch state reset`, and turns ended while `dispatch run` was down are there after it starts, at the next read of local sources.
- `dispatch explain` prints the turns a decision read, and `dispatch replay` reproduces it from the recorded read. In `rules test`, `worker("ENG-1", "idle", { turns: [{ at: 1000, running: true }] })` sets them, and `sessionUnread: "reason"` makes them unknown.

## Reading a worker's commands

`ctx.worker.of(t).commands` lists every slash command the worker's current Claude session has taken, oldest first, each as `{ name, args, cut, at, by }`. A person can take a worker over by typing a command such as a repo's own merge command into its pane, and dispatch cannot see that happen any other way, so a rule reads `commands` to tell a worker a person is driving from one that went quiet:

```ts
whenever({
  name: "person-took-over",
  each: "ticket",
  if:   (t, ctx) => {
    if (t.status !== "in_progress") return false;
    return ctx.worker.of(t).commands.some((c) => c.by === "person" && c.name === "merge-it");
  },
  then: (t, ctx) => ctx.ticket.queueComment(t, "a person ran /merge-it in this worker, so dispatch is leaving it alone", { once: ["took-over", t.id] }),
});
```

- A message is a command when it starts with `/` and a name of letters, digits and `: _ . -`, and then white space or its end. `/merge-it 42` is `merge-it` with `42`. `when should I run /merge-it?`, `/Users/x` and ` /x` are not commands, and a skill the model loads by itself never is. `args` is the rest of the message, trimmed and cut at 1KB with `cut` set when it was longer. It is whatever the sender typed, so a rule that forwards it should treat it as untrusted text.
- `by` says who sent it. Dispatch's kickoff (`/dispatch:build ENG-457`) and every nudge that starts with a slash, `dispatch tell` included, are `"dispatch"`. The same text typed by a person afterward is `"person"`, because each send matches one command. Dispatch remembers what it sent in memory, so a send whose text never reaches Claude without dispatch seeing an error, or one sent before a restart, can make a later identical command read wrongly.
- `at` is when Claude took the message. A message typed while Claude is working waits for the turn to end, so its `at` follows that turn's end.
- Check the ticket's status before reading it, as the example does, for the reason given under turns above. It is empty for a session that has taken no commands, and unknown when the multiplexer reports no session, the session's record can't be read, or the ticket is in another status. A half line in the session's record makes `commands` and `turns` unknown for the rest of that session.
- A relaunched worker's new Claude session, or one after `/clear`, reads an empty list. A worker the app host resumed after a herdr-core restart, or a worker dispatch restarted with `claude --resume` after a Rex restart, keeps its session, so it keeps its list. The list survives a restart and `dispatch state reset`, but a command taken while dispatch was not running is missing from it. `dispatch explain` and `dispatch replay` show and reproduce what a decision read. In `rules test`, `worker("ENG-1", "idle", { commands: [{ name: "merge-it", args: "42", at: 1000, by: "person" }] })` sets them.
- The plugin's `UserPromptSubmit` hook runs `dispatch wake`, which reports every non-empty prompt, a command or a plain message, to the running dispatch without waiting for it, and never blocks, delays, adds text to or fails a prompt, whatever goes wrong. With no dispatch running, or one that is stuck, the report is dropped and the command never appears. A plain message is kept in the session log as its time and whether it is a task notice, and `messageAt` reads the latest one (see [Reading whether a worker asked](#reading-whether-a-worker-asked)). It also ends the worker's pause: `waiting` reads false for a worker whose session took a command or a message after its latest Stop.

## Reading whether a worker asked

A worker that needs the user runs `dispatch ask`, says why in chat and ends its turn. `ctx.worker.of(t).asking` is true from the first read that sees it idle after that until its session takes a message, typed or sent by dispatch, or ends. `ctx.worker.of(t).askedAt` is when the session last ran `dispatch ask`, in milliseconds since the epoch. `rules/asks.ts` holds the shipped rules about an ask:

```ts
whenever({
  name: "notify-asked",
  each: "ticket",
  if:   (t, ctx) => (t.status === "in_progress" || t.status === "in_review") && !t.hasChildren
           && ctx.worker.of(t).asking,
  then: (t, ctx) => ctx.notify.queueSend(t, `${t.id}'s worker asked a question in its workspace: ${t.title}`,
           { once: ["asked", ctx.worker.of(t).askedAt!], reason: "worker asked a question" }),
});
```

- `state` is unchanged, so an asking worker reads `idle`, and a rule that acts on `state === "idle"` acts on it too. Dispatch reports the fact and rewrites none, so a rule that must leave an asking worker alone reads `asking`, or an [exemption](#taking-an-action-away-for-some-tickets) takes its action away. `an-asking-worker-hears-one-notice` in `asks.ts` takes away the notifications whose once key starts with `idle`, `ready`, `draft-ready` or `approved-ready`, so followers hear of the ask and not that the worker is idle or its PR is ready.
- A worker that ran `dispatch ask` and is still in its turn is not asking yet. Neither is one dispatch just nudged, nor one whose launch is unconfirmed. While `asking` is true the workspace shows 🔴.
- `askedAt` keeps its value after the ask ends and across a restart, so a once key on it acts once per ask. A second ask after an answer has a new time. It is undefined for a session that never asked.
- Both are read for a worker on an In Progress, In Review or Needs Human ticket. While the session's record can't be read, or the multiplexer reports no session, `asking` is false and `askedAt` undefined, never unknown. A relaunched worker's new session has asked nothing.
- No ticket moves for an ask. The worker's ticket keeps its status, and the worker keeps its place under `max_in_progress`.
- A task notice, the message Claude Code hands the session when one of its own background tasks finishes (its text opens with `<task-notification>`), ends no ask. The session log marks it, and the worker that handles it is asking again once it stops.
- `ctx.worker.of(t).messageAt` is when the session took its latest command or other message, typed or sent by dispatch, in milliseconds since the epoch. A task notice does not count. It is undefined for a session that took none, and, like `askedAt`, undefined rather than unknown while the session's record can't be read.
- In `rules test`, `worker("ENG-1", "idle", { asked: -1000 })` gives the session an ask a second before the scenario starts, `messages: [{ at: 1000 }]` a plain message after it, which ends the ask, and `messages: [{ at: 1000, task: true }]` a task notice, which does not.

Two exemptions take the same actions away, the list `HELD_ACTIONS` in `rules/shared.ts`: every nudge, every merge, and the `pr-ignored` escalation for ignoring PR nudges, whichever rule queued them.

```ts
// rules/asks.ts
exempt({ name: "an-asking-worker-is-left-alone", each: "ticket", actions: HELD_ACTIONS,
         if: (t, ctx) => ctx.worker.of(t).asking && t.status !== "needs_human" });

// rules/nudges.ts
exempt({ name: "a-worker-finishes-its-message-first", each: "ticket", actions: HELD_ACTIONS,
         if: (t, ctx) => onLastMessage(t, ctx) });
```

- The first holds them while the worker asks, except on a Needs Human ticket, so its worker hears of reviews and comments on its PR. The shipped nudge and merge rules (`nudge-pr-activity`, `nudge-pr-needs-work`, `nudge-restack` and `nudge-stacked-activity` in `stacking.ts`, `auto-merge`) test `state === "idle"` and not `asking`, and a comment above each names the exemptions that hold it. A rule of your own that nudges or merges is held the same way without knowing of asks, and `dispatch rules check` ends its line with `exempt for ENG-4 by an-asking-worker-is-left-alone`.
- The second holds them for any idle worker still on the last message it took, whether or not it asked. `onLastMessage` in `shared.ts` is true while `state` is `idle`, `messageAt` is under `MESSAGE_HOLD` (15 minutes) old, and no turn in `turns` ended after `messageAt`. So the hold ends when the worker ends its turn, or 15 minutes after the message when that turn end is lost. Edit `MESSAGE_HOLD` to change the limit.
- Neither reads anything that is unknown while the session's record can't be read, so then neither holds anything and nudges and merges go on as for any idle worker.
- `dispatch tell` reads no exemption, so it always delivers.

## Creating and editing local tickets

A rule that sees work arrive, such as a review request, files a local ticket for it, so a worker starts on it. Search first, so the rule files one ticket and not one per cycle:

```ts
whenever({
  name: "review-requested",
  each: "inbox",
  if:   (item) => item.reason === "review_requested" && item.repo !== undefined,
  then: (item, ctx) => {
    const pr = ctx.http.get("github", item.apiUrl!.replace("https://api.github.com", ""));
    const body = pr.body as { head: { sha: string }; number: number };
    const head = body.head.sha;
    if (ctx.ticket.search({ attributes: { pr: item.apiUrl!, commit: head } }).length > 0) return;
    ctx.ticket.queueCreate("local", {
      title: `Review ${item.title} at ${head.slice(0, 7)}`,
      repo: item.repo!,
      labels: ["review"],
      attributes: { pr: item.apiUrl!, commit: head },
      startPR: body.number,
      status: "in_progress",
    }, { once: ["review", item.apiUrl!, head] });
  },
});
```

- `ctx.ticket.search(filter)` works only in `then` and throws in `if`. It returns every local ticket the filter matches, finished ones included, as `{ id, title, status, finished, labels, attributes }`. Every field given must match. `status` is a job (`todo`, `in_progress`, `in_review`, `needs_human`, `close_parent`, `blocker_done`, `finished` or `paused`) and matches the tickets whose status does it. `labels` match ignoring case, `attributes` match exactly, and `open: true` leaves out the tickets in a finished status. It throws `local tickets are off` without a `[plugins.local]` table. A search the database cannot answer makes the rule's read unknown, so the rule is held and tried again.
- A found ticket is a plain value, not a `ticket` record, so a rule cannot pass it to an action. To change one, run the rule over `each: "ticket"` and edit the record there.
- `ctx.ticket.queueCreate("local", draft, { once })` files a ticket. `draft` takes `title` and `repo` (a `[repos.<label>]` table), and optionally `description`, `labels`, `attributes`, `parent` and `blockedBy` (`LOC-<n>` ids), `startPR` (a PR in `repo`) and `status` (a job key, as `queueMove` takes; the default is `todo`). The ticket's filed-by is the rule's name, which `dispatch ticket show` prints. The subject of a create is the system `"local"`, so the once key is what keeps it from running again. It throws for any other system, and while local tickets are off.
- `ctx.ticket.queueEdit(t, changes, { once })` edits a local ticket and throws for a Linear one. `changes` takes `title`, `description`, `repo`, `addLabels`, `removeLabels`, `setAttributes`, `unsetAttributes`, `parent` (`null` clears), `blockedBy` (replaces; `[]` clears) and `startPR` (`0` clears). An edit changes neither status nor comments.
- A create or edit the database refuses, such as an unknown repo, a link to a missing ticket or a parent cycle, ends its receipt Refused with the refusal as the detail, and changes nothing. Any other error ends it Failed.
- A create is not safe to retry, because when its outcome is lost the ticket may already exist. It counts as sent and does not run again until history rotates and drops its receipt, so a rule that must survive a rotation searches first, as above. An edit is safe to retry.
- Each search is recorded in the cycle line of the history with what it found, and `dispatch replay` answers searches from those records without opening the database. A search the live cycle didn't run, or one it ran that replay skips, is a difference.
- `dispatch state reset --quiet` leaves a create to run in the first cycle, and answers searches from the database it reads. `rules check` answers them from the same read.
- `rules test` always has local tickets on, and answers a search from the scenario's `LOC-` tickets, finished ones included.

## Calling a service

A rule can call the services `config.toml` names under `[http.<name>]`, with dispatch adding the token:

```ts
whenever({
  name: "note-open-errors",
  each: "ticket",
  if:   (t) => t.status === "in_progress",
  then: (t, ctx) => {
    const issues = ctx.http.get("sentry", "/issues/?query=is:unresolved").body as any[];
    if (issues.length > 0) return ctx.ticket.queueComment(t, `${issues.length} unresolved Sentry issues`, { once: ["open-errors", t.id] });
  },
});
```

- `ctx.http.get(service, path)` works only in `then`. It waits for the response, within the `then` budget of thirty seconds, and returns `{ status, body }` with `body` parsed as JSON, or the text when it isn't JSON. A `get` in `if` throws.
- A request that fails, times out or answers outside 2xx reads as an unknown, so the rule is held for that record and tried again in the next cycle. A slow service delays the rest of its cycle, because a cycle runs every rule one after another.
- `ctx.http.last(service, path)` reads the response a `get` kept, from `if` or `then`, without a request. It lasts until the next resync, and reads as an unknown when nothing is kept. Rule order matters inside a cycle, so register the rule that gets before the rules that read.
- `ctx.http.queuePost(service, path, body, { once })` queues a POST of `body` as JSON. It runs like any other action, so the key decides whether it runs again. A POST that fails is tried again at the next resync, but one that times out counts as sent and is not repeated, because it may have landed.
- `rules test` has no network. A scenario names the service with `s.config({ http: { sentry: { baseUrl: "https://sentry.test/api/0" } } })` and sets its answers with `s.http("sentry", "/issues/?query=is:unresolved", [])`.
- Each response a `then` waited on is recorded in the cycle line of the history, with the rule and ticket that asked. `dispatch replay` reads those answers instead of calling the service, and keeps them until the same resync, tick or start that cleared them live. It reports a difference when a cycle asks for a service or path the live cycle didn't, or skips one it made, and under `--rules` a get with no recorded answer is a difference too. A path that contains `..;` throws at the call, like any other path that leaves the service. `rules check` calls no service and runs no `token_command`, so a `get` reads as an unknown there and the rule is listed as held. Test HTTP rules with `rules test`.

Add a rule by creating a file and importing it from `dispatch.ts`, which is the list of what runs. `extras.ts`, `commands.ts` and `stacking.ts` are examples that `dispatch.ts` doesn't import.

## Reading the GitHub inbox

A rule with `each: "inbox"` runs once per item in your GitHub notifications inbox, such as a review request or a mention. While any loaded rule runs over `inbox`, dispatch asks `gh` for the newest 50 items, read or unread, from any repo, on every resync and `dispatch tick`. With no such rule it makes no call.

```ts
whenever({
  name: "tell-me-about-review-requests",
  each: "inbox",
  if:   (item) => item.unread && item.reason === "review_requested" && item.subjectType === "PullRequest",
  then: (item, ctx) => ctx.http.queuePost("chat", "/review", { title: item.title, repo: item.repoName }, { once: ["review", item.id, item.updatedAt] }),
});
```

- An item has `id` (GitHub's thread id, the same as notifications arrive on the thread), `reason`, `unread`, `updatedAt` (RFC 3339 as GitHub wrote it), `repoName` (`owner/name`), `subjectType` (`PullRequest`, `Issue`, `Release`, ...), `title` and `apiUrl` (the subject's API URL, undefined when GitHub gives none). Dispatch copies them and corrects none. `repo` is the label of the `[repos.<label>]` whose clone has a remote naming `repoName`, and is undefined for any other repo.
- A clone has one chosen remote, `origin` unless `[repos.<label>].github_remote` names another. `repo` is the label whose chosen remote's GitHub `owner/name` equals `repoName`, ignoring case. A remote that is missing or not on github.com matches nothing. When two labels' clones name the same repo, `repo` is unknown and a rule that reads it is held, so read it after the cheaper checks. A failed `git config` read keeps that clone's last good value, prints `dispatch: while reading the <remote> remote of the <label> clone: <error>` to stderr, and makes `dispatch state reset --quiet` refuse.
- GitHub replaces a thread's `reason` with the newest one, so a review request followed by a mention reads `mention`. A rule that must not miss a review request checks the PR itself.
- An action like `ctx.http.queuePost` has the service as its subject, not the item, so put `item.id` in the once key. `[topic, item.id]` acts once per item and `[topic, item.id, item.updatedAt]` once per update.
- The first time an inbox rule loads, every item in the 50 is new to the ledger, so the rule acts on all of them. `unread` is a weak cutoff, because an item you opened before the next resync already reads `false`. Use a date (`item.updatedAt`) when that matters.
- To read the PR, add an `[http.github]` service with `base_url = "https://api.github.com"` and `token_command = "gh auth token"`, and call `ctx.http.get("github", path)` with `apiUrl` minus the base. A `get` runs only in `then` and sends a request every cycle the rule's `if` holds, so put the cheap checks (`subjectType`, `unread`) in `if`.
- Your GitHub login is not in the world, because dispatch's viewer is your Linear name. A rule reads `/user` from the service, or a key in `[rules]`.
- `ctx.dispatch.inboxReadAt` is the time, in milliseconds since the epoch, of the last good inbox read, and is undefined until the first. Use it to judge staleness (`ctx.now - ctx.dispatch.inboxReadAt`), not newness: rules run right after the read, so a once key on `[item.id, item.updatedAt]` is how a rule finds new and changed items.
- When a read fails, dispatch prints `dispatch: while reading the inbox: <gh error>` to stderr and keeps the items and `inboxReadAt` of the last good read. Rules over inbox items run on those items, and rules over tickets, PRs, workers and trees run as usual. After a restart there are no items until a read succeeds. [ADR 0005](adr/0005-a-failed-read-of-a-source-nothing-references-keeps-its-last-good-read.md) has the reasoning.
- GitHub's notifications endpoints accept only a classic personal access token with the `notifications` or `repo` scope. A fine-grained `gh` login fails every read.
- `rules check` prints `inbox  N inbox items (newest 50 of the GitHub inbox), U unread, C in configured repos`, `inbox  not read (no loaded rule runs over inbox)`, or `inbox  not read: <gh error>` when the read failed. `dispatch state reset --quiet` refuses while the inbox can't be read, because it would record no receipts and the next run would act on every item.
- `rules test` gives a scenario items with `inbox("t1", { reason: "review_requested", unread: true, updatedAt: "2026-10-31T09:00:00Z", repoName: "acme/widgets", subjectType: "PullRequest", title: "Fix", apiUrl: "..." })`. Giving an id again changes only the fields it names. `pool("dispatch", { github: "acme/widgets" })` names the GitHub repo the clone's chosen remote points at, and `s.unread("remotes:dispatch")` fails that clone's later remote reads: the repo given before it stays. `s.unread("inbox")` fails every later read: the items given before it stay, and items given after it don't appear. Test a recovery or a restart with `dispatch run`, since `s.unread` has no undo.

## Stacking tickets

People split a feature into tickets that build on each other, and by default each waits for the one before it to merge. Add `import "./stacking";` to `dispatch.ts` and a ticket launches on its blocker's unmerged PR instead:

- A ticket whose only open blocker, in the same repo, is In Review with a ready PR gets a worker on that PR's branch, and its PR opens against that branch. Two open blockers, a blocker without a ready PR, or a blocker in another repo do not stack. A feature piece is promoted on the same condition. The bar is `stackablePR` in `stacking.ts`, so edit it there.
- When the stack parent reaches Done (Linear sets that when its PR merges), the stacked ticket's worker is told once per stretch in its status to restack, meaning to move its own commits onto the default branch, point its PR there, force-push, end the stack record with `dispatch ticket branch <ticket> --unstack` and follow the repo's own after-restack steps. A worker that finds dispatch not running moves the ticket to Needs Human, since the record is still there. A stacked ticket with no worker is relaunched with the same instructions. No other rule launches or nudges it until the record is gone.
- `auto-merge` never merges a stacked PR while its record exists, and an approved one in an `auto_merge` repo gets a ready notice. A stacked PR's conflict with its base starts no needs-work nudge, relaunch or escalation, and review activity on it still reaches an idle worker.
- A stacked ticket whose stack parent is Canceled or Duplicate moves to Needs Human.
- If the repo's branch rows can't be read, or its git config keys are not imported yet, `auto-merge` is held for that repo's PRs.

The shipped rules stay untouched. `stacking.ts` keeps them off stacked tickets with [exemptions](#switching-off-a-rule-for-some-records). The guards cover only what the rules do. The `dispatch:autobots` skill merges feature children itself, and `dispatch:build` arms GitHub auto-merge when a base branch requires approval, so either can still merge a stacked PR into its stack parent's branch. `notify-stacked-ready` and `nudge-stacked-activity` copy their shipped rules' conditions, so an edit to `notify-ready-for-review` or `nudge-pr-activity` has to be carried over, and the pinning scenarios in `stacking.test.ts` fail until it is.

## Files

| File | What it is |
|---|---|
| `dispatch.ts` | Imports every file that runs. |
| `workers.ts`, `trees.ts`, `parents.ts`, `escalations.ts`, `nudges.ts`, `notices.ts`, `asks.ts`, `merge.ts` | The 28 rules, grouped by job. [Worker states](worker-states.md) lists when they nudge, notify or escalate a worker in each state, so read it before changing one. |
| `shared.ts`, `texts.ts` | Helpers and the nudge texts. The PR nudges start with `/dispatch:pr-respond`, the skill a worker follows on its PR. |
| `types/dispatch.d.ts`, `types/testing.d.ts` | Generated from dispatch's Go types, and rewritten by `rules check`. They sit in `types/` because `tsc` ignores a `dispatch.d.ts` that has a `dispatch.ts` beside it. |
| `config.schema.json` | A JSON schema for `config.toml`, generated from the same structs the loader reads. |
