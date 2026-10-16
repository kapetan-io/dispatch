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
nudge-stall        whenever  linear.ticket   wants nothing now
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
- Each response a `then` waited on is recorded in the cycle line of the history, so a replay can read it instead of calling the service. `dispatch replay` isn't built yet. `rules check` calls no service and runs no `token_command`, so a `get` reads as an unknown there and the rule is listed as held. Test HTTP rules with `rules test`.

Add a rule by creating a file and importing it from `dispatch.ts`, which is the list of what runs. `extras.ts` and `commands.ts` are examples that `dispatch.ts` doesn't import.

## Files

| File | What it is |
|---|---|
| `dispatch.ts` | Imports every file that runs. |
| `workers.ts`, `trees.ts`, `parents.ts`, `escalations.ts`, `nudges.ts`, `notices.ts`, `merge.ts` | The 27 rules, grouped by job. |
| `shared.ts`, `texts.ts` | Helpers and the nudge texts the `dispatch:build` skill relies on word for word. |
| `types/dispatch.d.ts`, `types/testing.d.ts` | Generated from dispatch's Go types, and rewritten by `rules check`. They sit in `types/` because `tsc` ignores a `dispatch.d.ts` that has a `dispatch.ts` beside it. |
| `config.schema.json` | A JSON schema for `config.toml`, generated from the same structs the loader reads. |
