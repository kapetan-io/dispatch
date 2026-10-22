# Rules

Dispatch's decisions, such as when to start a worker, nudge one or escalate a ticket, are written as TypeScript rules in a directory you own. `dispatch rules` lints them, tests them and shows what each would do right now. `dispatch run` loads them once at start and runs every one after each event, so they make every decision, and a change to a rule takes effect when you stop `dispatch run` and start it again. `dispatch rules check` and `dispatch rules test` let you try a change first, and `dispatch plan` shows what the latest cycle of a run wanted.

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
linear.ticket      37 tracked tickets (assigned to Ada Lovelace; teams ENG; statuses Todo, In Progress, In Review, Needs Human)
...
launch-worker      whenever  linear.ticket   wants launch now for ENG-12, ENG-15
nudge-resume       whenever  linear.ticket   wants nothing now
```

A record whose rule read something that couldn't be read is listed as `held for ENG-9`. It exits 1 on any finding: a rule that fails to load, a rule that throws or runs out of time, a queued action with no once key, a rule that reads a `[rules]` key the file lacks, and, when `tsc` is installed, a type error from `tsc --noEmit --strict`.

A check takes no action: it reads the world and rewrites `types/`. Every fact's `since` is the time of the read and the ledger is empty, so it shows what a fresh start would want.

## Test them

`dispatch rules test` runs the `*.test.ts` files in the rules directory in the same runtime a check uses. A scenario sets up a world, changes it step by step, and says what the cycle after each step wants:

```ts
import { scenario, ticket, worker, rulesFrom } from "dispatch/testing";

const rules = rulesFrom("./dispatch");

scenario(rules, "a crashed worker is relaunched once", s => {
  s.given(ticket("ENG-1", { status: "in_progress", optedIn: true, repo: "dispatch" }), worker("ENG-1", "working"));
  s.see(worker("ENG-1", "none"));
  s.expect([["launch-worker", "herdr.launch", "ENG-1"]]);
  s.land("herdr.launch", "ENG-1");
  s.expect([]);
});
```

`s.see` is a read that differs, `s.land` is an action's receipt, `s.unread` marks a source as failed, and `s.expectHeld` names a rule held for a record. `types/testing.d.ts` lists all of them.

## Writing a rule

```ts
import { whenever } from "dispatch";

whenever({
  name: "bugs-get-their-own-model",
  each: "linear.ticket",
  if:   (t, ctx) => t.status === "in_progress" && t.optedIn && t.hasLabel("bug") && ctx.herdr.worker(t).state === "none",
  then: (t, ctx) => ctx.herdr.queueLaunch(t, { model: ctx.config.rules.bug_model, once: ["launch-bug", ctx.herdr.worker(t).goneSince] }),
});
```

- `whenever` wants `then` while `if` is true, for every record of the kind in every cycle. `on` runs once per new event of a type.
- Every queued action carries `once: [topic, ...values]`. The action runs once per key, so the key says what must change before it runs again.
- A rule keeps no state. Reading a fact whose read failed throws `Unknown`, and the rule is held for that record even if it catches the error. Wrap a read in `known(() => ...)` to ask whether it could be read.
- `Date` is pinned to `ctx.now`, `Math.random` throws, and `eval`, `Function`, `Promise`, async functions and generators aren't available.
- Each `if` has one second and each `then` thirty. A rule that throws or times out wants nothing for that record, and every other rule goes ahead.

## Reading a worker's turns

`ctx.herdr.worker(t).turns` lists every turn the worker's current Claude session has ended, oldest first, each as `{ at, running }`. `running` says some background task of the session (a shell, subagent or monitor) was still running as the turn ended, so a rule can tell a worker looping on a background job from one that stalled:

```ts
whenever({
  name: "long-loop-in-review",
  each: "linear.ticket",
  if:   (t, ctx) => {
    if (t.status !== "in_review") return false;
    const turns = ctx.herdr.worker(t).turns;
    return turns.length >= 20 && turns.every((turn) => turn.running);
  },
  then: (t, ctx) => ctx.linear.queueComment(t, "this worker has run in the background for 20 turns", { once: ["long-loop", t.id] }),
});
```

- Check the ticket's status before reading it, as the example does, because a read that throws `Unknown` holds the rule for every other ticket. It is read for a worker in any state on an In Progress or In Review ticket. It is empty for a session that has ended no turns, and unknown when herdr reports no session, the session's record can't be read, or the ticket is in another status.
- A dispatch killed in the middle of writing a turn leaves a half line, and the next turn starts a new line after it. From then on `turns` is unknown for the rest of that session, because a list with a hole in it would pass for a shorter one. A rule that reads it without `known(...)` is held for that worker until the session ends or the worker is relaunched.
- A relaunched worker's new Claude session reads an empty list, even on the same tree. The lists survive a restart and `dispatch state reset`, and turns ended while `dispatch run` was down are there after it starts, at the next read of local sources.
- `dispatch trace` prints the turns a decision read, and `dispatch replay` reproduces it from the recorded read. In `rules test`, `worker("ENG-1", "idle", { turns: [{ at: 1000, running: true }] })` sets them, and `sessionUnread: "reason"` makes them unknown.

## Reading a worker's commands

`ctx.herdr.worker(t).commands` lists every slash command the worker's current Claude session has taken, oldest first, each as `{ name, args, cut, at, by }`. A person can take a worker over by typing a command such as a repo's own merge command into its pane, and dispatch cannot see that happen any other way, so a rule reads `commands` to tell a worker a person is driving from one that went quiet:

```ts
whenever({
  name: "person-took-over",
  each: "linear.ticket",
  if:   (t, ctx) => {
    if (t.status !== "in_progress") return false;
    return ctx.herdr.worker(t).commands.some((c) => c.by === "person" && c.name === "merge-it");
  },
  then: (t, ctx) => ctx.linear.queueComment(t, "a person ran /merge-it in this worker, so dispatch is leaving it alone", { once: ["took-over", t.id] }),
});
```

- A message is a command when it starts with `/` and a name of letters, digits and `: _ . -`, and then white space or its end. `/merge-it 42` is `merge-it` with `42`. `when should I run /merge-it?`, `/Users/x` and ` /x` are not commands, and a skill the model loads by itself never is. `args` is the rest of the message, trimmed and cut at 1KB with `cut` set when it was longer. It is whatever the sender typed, so a rule that forwards it should treat it as untrusted text.
- `by` says who typed it. Dispatch's kickoff (`/dispatch:build ENG-457`) and every nudge that starts with a slash, `dispatch tell` included, are `"dispatch"`. The same text typed by a person afterward is `"person"`, because each send matches one command. Dispatch remembers what it sent in memory, so a send whose text never reaches Claude without herdr refusing it, or one sent before a restart, can make a later identical command read wrongly.
- `at` is when Claude took the message. A message typed while Claude is working waits for the turn to end, so its `at` follows that turn's end.
- Check the ticket's status before reading it, as the example does, for the reason given under turns above. It is empty for a session that has taken no commands, and unknown when herdr reports no session, the session's record can't be read, or the ticket is in another status. A half line in the session's record makes `commands` and `turns` unknown for the rest of that session.
- A relaunched worker's new Claude session, or one after `/clear`, reads an empty list. The list survives a restart and `dispatch state reset`, but a command taken while dispatch was not running is missing from it. `dispatch trace` and `dispatch replay` show and reproduce what a decision read. In `rules test`, `worker("ENG-1", "idle", { commands: [{ name: "merge-it", args: "42", at: 1000, by: "person" }] })` sets them.
- The plugin's `UserPromptSubmit` hook runs `dispatch wake`, which reports a command to the running dispatch without waiting for it, and never blocks, delays, adds text to or fails a prompt, whatever goes wrong. With no dispatch running, or one that is stuck, the report is dropped and the command never appears.

## Calling a service

A rule can call the services `config.toml` names under `[http.<name>]`, with dispatch adding the token:

```ts
whenever({
  name: "note-open-errors",
  each: "linear.ticket",
  if:   (t) => t.status === "in_progress",
  then: (t, ctx) => {
    const issues = ctx.http.get("sentry", "/issues/?query=is:unresolved").body as any[];
    if (issues.length > 0) return ctx.linear.queueComment(t, `${issues.length} unresolved Sentry issues`, { once: ["open-errors", t.id] });
  },
});
```

- `ctx.http.get(service, path)` works only in `then`. It waits for the response, within the `then` budget of thirty seconds, and returns `{ status, body }` with `body` parsed as JSON, or the text when it isn't JSON. A `get` in `if` throws.
- A request that fails, times out or answers outside 2xx reads as an unknown, so the rule is held for that record and tried again in the next cycle. A slow service delays the rest of its cycle, because a cycle runs every rule one after another.
- `ctx.http.last(service, path)` reads the response a `get` kept, from `if` or `then`, without a request. It lasts until the next resync, and reads as an unknown when nothing is kept. Rule order matters inside a cycle, so register the rule that gets before the rules that read.
- `ctx.http.queuePost(service, path, body, { once })` queues a POST of `body` as JSON. It runs like any other action, so the key decides whether it runs again. A POST that fails is tried again at the next resync, but one that times out counts as sent and is not repeated, because it may have landed.
- `rules test` has no network. A scenario names the service with `s.config({ http: { sentry: { baseUrl: "https://sentry.test/api/0" } } })` and sets its answers with `s.http("sentry", "/issues/?query=is:unresolved", [])`.
- Each response a `then` waited on is recorded in the cycle line of the history, with the rule and ticket that asked. `dispatch replay` reads those answers instead of calling the service, and keeps them until the same resync, tick or start that cleared them live. It reports a difference when a cycle asks for a service or path the live cycle didn't, or skips one it made, and under `--rules` a get with no recorded answer is a difference too. A path that contains `..;` throws at the call, like any other path that leaves the service. `rules check` calls no service and runs no `token_command`, so a `get` reads as an unknown there and the rule is listed as held. Test HTTP rules with `rules test`.

Add a rule by creating a file and importing it from `dispatch.ts`, which is the list of what runs. `extras.ts` and `commands.ts` are examples that `dispatch.ts` doesn't import.

## Files

| File | What it is |
|---|---|
| `dispatch.ts` | Imports every file that runs. |
| `workers.ts`, `trees.ts`, `parents.ts`, `escalations.ts`, `nudges.ts`, `notices.ts`, `merge.ts` | The 25 rules, grouped by job. [Worker states](worker-states.md) lists when they nudge, notify or escalate a worker in each state, so read it before changing one. |
| `shared.ts`, `texts.ts` | Helpers and the nudge texts the `dispatch:build` skill relies on word for word. |
| `types/dispatch.d.ts`, `types/testing.d.ts` | Generated from dispatch's Go types, and rewritten by `rules check`. They sit in `types/` because `tsc` ignores a `dispatch.d.ts` that has a `dispatch.ts` beside it. |
| `config.schema.json` | A JSON schema for `config.toml`, generated from the same structs the loader reads. |
