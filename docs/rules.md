# Rules

Dispatch's decisions, such as when to start a worker, nudge one or escalate a ticket, are written as TypeScript rules in a directory you own. `dispatch rules` lints them, tests them and shows what each would do right now. `dispatch run` loads them once at start and runs every one after each event, so they make every decision, and a change to a rule takes effect when you stop `dispatch run` and start it again. `dispatch rules check` and `dispatch rules test` let you try a change first, and `dispatch cycle` shows what the latest cycle of a run wanted.

## Get the rules

```sh
dispatch rules init            # writes to [dispatch].rules in config.toml
dispatch rules init ~/rules    # or to a directory you name
```

`init` writes the 27 shipped rules, their scenario tests, the generated types an editor reads, a `tsconfig.json`, a JSON schema for `config.toml`, and `config.example.toml`. It refuses to overwrite a file, so run it into an empty directory, and nothing is written when any file would collide.

The rules read five keys of the `[rules]` table in `config.toml`. Copy them from `config.example.toml`:

```toml
[rules]
worker_model = "sonnet"
design_model = "opus"
launch_failure_limit = 2
nudge_limit = 2
blocked_renotify_minutes = 15
```

## Check them against the world

`dispatch rules check` lints the rules, reads Linear, herdr, treehouse and GitHub once, runs one cycle, and prints what it found:

```
ticket             37 tracked tickets (assigned to Ada Lovelace; teams ENG; statuses Todo, In Progress, In Review, Needs Human)
...
launch-worker      whenever  ticket  wants launch now for ENG-12, ENG-15
nudge-pr-activity  whenever  pr      wants nothing now
```

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

`s.see` is a read that differs, `s.land` is an action's receipt, `s.unread` marks a source as failed, and `s.expectHeld` names a rule held for a record. `types/testing.d.ts` lists all of them.

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

- `whenever` wants `then` while `if` is true, for every record of the kind in every cycle. `on` runs once per new event of a type. `exempt` switches named `whenever` rules off for the records its `if` is true of (see below).
- Every queued action carries `once: [topic, ...values]`. The action runs once per key, so the key says what must change before it runs again.
- `ctx.notify.queueSend` takes a `reason` option, the words the notification's followers read after `dispatch event (`. With none, the reason is the once key's topic. Following sessions key on the words the shipped rules pass, such as `PR ready for review` (see `skills/dispatch/SKILL.md`), so rewording one changes what a following session does.
- `queueLaunch` takes `stackOn: pr` to start the ticket's branch at that PR's head and write a stack record, so the ticket builds before the PR's ticket merges. The launch receipt says `stacked on <ticket> at <short head> (<branch>)`. A relaunch, or a launch on a branch that exists, writes and changes no record. A worker launched with `/dispatch:build <id> --base <branch>` opens its PR against that branch.
- `t.stack` is the stack record of the ticket's branch, undefined for a branch with none and unknown when the clone's records can't be read or the record is partial. `t.branch` is the ticket's branch, `blocker.ticket` is the tracked ticket of a blocker, and `ctx.pr.of(t)` is the ticket's PR, undefined when dispatch read it and found none and unknown when it didn't read it this cycle.
- A rule keeps no state. Reading a fact whose read failed throws `Unknown`, and the rule is held for that record even if it catches the error. Wrap a read in `known(() => ...)` to ask whether it could be read.
- `Date` is pinned to `ctx.now`, `Math.random` throws, and `eval`, `Function`, `Promise`, async functions and generators aren't available.
- Each `if` has one second and each `then` thirty. A rule that throws or times out wants nothing for that record, and every other rule goes ahead.

## Switching off a rule for some records

A rule file can't edit another file's rule, and a second rule with the same name is a load error. `exempt` takes the named rules' wants away for the records its `if` is true of, from any file, so you don't edit a shipped rule or turn it off for every record.

`exempt` is the last tool to reach for when a rule acts when you don't want it to, not the first. It exists for one case, a shipped rule that is right for most records and wrong for a class of them you can name, such as the PR of a stacked ticket, which conflicts until its parent merges and has nothing for a nudge to fix. An exemption changes what a rule does without touching the rule, so a reader of the rule won't see it. Before you write one, ask in this order.

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

- `each` is the record kind, as in `whenever`. Every rule in `rules` must be a `whenever` rule of that kind. A rule no file defines, an `on` rule, a rule of another kind and a name a rule or exemption already has are load errors, so `rules check` fails and a run won't start.
- For a record `if` is true of, the named rules want nothing, and they act as before for every other record. The exemption has no `then`. It takes away what a rule wants and changes no setting, so behavior a rule doesn't already have is a new rule.
- An exemption whose `if` reads an unknown fact holds the rules it names for that record, so a guard that can't read its fact never lets a rule through. One that throws or runs out of time stops them too, and `rules check` reports the throw.
- Each exemption runs once per record of its kind in a cycle, before the named rules' own `if`. A shadow rule an exemption stops reports no would-be action for that record, and `rules check` lists it as exempt.
- `dispatch rules check` lists the exemption (`name  exempt  pr  exempts a, b`) and ends the rule's line with `exempt for ENG-4 by conflict-is-not-work`. `dispatch cycle` lists the stopped rule as `exempt`, and `dispatch explain` prints `exempt: stopped by conflict-is-not-work` for the ticket. A saved cycle with exemptions replays with no differences.
- `pr.conflict` is true when GitHub reports the PR's merge state as conflicting. `pr.needsWork` and `pr.problem` still count a conflict, so a rule that should leave a conflict out exempts the rules that read them, as above. A saved cycle from before `pr.conflict` reads it as false.

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

- Check the ticket's status before reading it, as the example does, because a read that throws `Unknown` holds the rule for every other ticket. It is read for a worker in any state on an In Progress or In Review ticket. It is empty for a session that has ended no turns, and unknown when herdr reports no session, the session's record can't be read, or the ticket is in another status.
- A dispatch killed in the middle of writing a turn leaves a half line, and the next turn starts a new line after it. From then on `turns` is unknown for the rest of that session, because a list with a hole in it would pass for a shorter one. A rule that reads it without `known(...)` is held for that worker until the session ends or the worker is relaunched.
- A relaunched worker's new Claude session reads an empty list, even on the same tree. The lists survive a restart and `dispatch state reset`, and turns ended while `dispatch run` was down are there after it starts, at the next read of local sources.
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
- `by` says who typed it. Dispatch's kickoff (`/dispatch:build ENG-457`) and every nudge that starts with a slash, `dispatch tell` included, are `"dispatch"`. The same text typed by a person afterward is `"person"`, because each send matches one command. Dispatch remembers what it sent in memory, so a send whose text never reaches Claude without herdr refusing it, or one sent before a restart, can make a later identical command read wrongly.
- `at` is when Claude took the message. A message typed while Claude is working waits for the turn to end, so its `at` follows that turn's end.
- Check the ticket's status before reading it, as the example does, for the reason given under turns above. It is empty for a session that has taken no commands, and unknown when herdr reports no session, the session's record can't be read, or the ticket is in another status. A half line in the session's record makes `commands` and `turns` unknown for the rest of that session.
- A relaunched worker's new Claude session, or one after `/clear`, reads an empty list. The list survives a restart and `dispatch state reset`, but a command taken while dispatch was not running is missing from it. `dispatch explain` and `dispatch replay` show and reproduce what a decision read. In `rules test`, `worker("ENG-1", "idle", { commands: [{ name: "merge-it", args: "42", at: 1000, by: "person" }] })` sets them.
- The plugin's `UserPromptSubmit` hook runs `dispatch wake`, which reports a command to the running dispatch without waiting for it, and never blocks, delays, adds text to or fails a prompt, whatever goes wrong. With no dispatch running, or one that is stuck, the report is dropped and the command never appears.

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

## Stacking tickets

People split a feature into tickets that build on each other, and by default each waits for the one before it to merge. Add `import "./stacking";` to `dispatch.ts` and a ticket launches on its blocker's unmerged PR instead:

- A ticket whose only open blocker, in the same repo, is In Review with a ready PR gets a worker on that PR's branch, and its PR opens against that branch. Two open blockers, a blocker without a ready PR, or a blocker in another repo do not stack. A feature piece is promoted on the same condition. The bar is `stackablePR` in `stacking.ts`, so edit it there.
- When the stack parent reaches Done (Linear sets that when its PR merges), the stacked ticket's worker is told once per stretch in its status to restack, meaning to move its own commits onto the default branch, point its PR there, force-push, remove the stack record and follow the repo's own after-restack steps. A stacked ticket with no worker is relaunched with the same instructions. No other rule launches or nudges it until the record is gone.
- `auto-merge` never merges a stacked PR while its record exists, and an approved one in an `auto_merge` repo gets a ready notice. A stacked PR's conflict with its base starts no needs-work nudge, relaunch or escalation, and review activity on it still reaches an idle worker.
- A stacked ticket whose stack parent is Canceled or Duplicate moves to Needs Human.
- If the clone's stack records can't be read, `auto-merge` is held for that repo's PRs.

The shipped rules stay untouched. `stacking.ts` keeps them off stacked tickets with [exemptions](#switching-off-a-rule-for-some-records). The guards cover only what the rules do. The `dispatch:autobots` skill merges feature children itself, and `dispatch:build` arms GitHub auto-merge when a base branch requires approval, so either can still merge a stacked PR into its stack parent's branch. `notify-stacked-ready` and `nudge-stacked-activity` copy their shipped rules' conditions, so an edit to `notify-ready-for-review` or `nudge-pr-activity` has to be carried over, and the pinning scenarios in `stacking.test.ts` fail until it is.

## Files

| File | What it is |
|---|---|
| `dispatch.ts` | Imports every file that runs. |
| `workers.ts`, `trees.ts`, `parents.ts`, `escalations.ts`, `nudges.ts`, `notices.ts`, `merge.ts` | The 24 rules, grouped by job. [Worker states](worker-states.md) lists when they nudge, notify or escalate a worker in each state, so read it before changing one. |
| `shared.ts`, `texts.ts` | Helpers and the nudge texts. The PR nudges start with `/dispatch:pr-respond`, the skill a worker follows on its PR. |
| `types/dispatch.d.ts`, `types/testing.d.ts` | Generated from dispatch's Go types, and rewritten by `rules check`. They sit in `types/` because `tsc` ignores a `dispatch.d.ts` that has a `dispatch.ts` beside it. |
| `config.schema.json` | A JSON schema for `config.toml`, generated from the same structs the loader reads. |
