# Rules

Dispatch's decisions, such as when to start a worker, nudge one or escalate a ticket, are written as TypeScript rules in a directory you own. `dispatch rules` lints them, tests them and shows what each would do right now. Nothing acts on them yet. Today's `dispatch run` and `dispatch tick` still make every decision, and the rules are a readable copy of those decisions that you can change and try out before the loop runs them.

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

Add a rule by creating a file and importing it from `dispatch.ts`, which is the list of what runs. `extras.ts` and `commands.ts` are examples that `dispatch.ts` doesn't import.

## Files

| File | What it is |
|---|---|
| `dispatch.ts` | Imports every file that runs. |
| `workers.ts`, `trees.ts`, `parents.ts`, `escalations.ts`, `nudges.ts`, `notices.ts`, `merge.ts` | The 27 rules, grouped by job. |
| `shared.ts`, `texts.ts` | Helpers and the nudge texts the `dispatch:build` skill relies on word for word. |
| `types/dispatch.d.ts`, `types/testing.d.ts` | Generated from dispatch's Go types, and rewritten by `rules check`. They sit in `types/` because `tsc` ignores a `dispatch.d.ts` that has a `dispatch.ts` beside it. |
| `config.schema.json` | A JSON schema for `config.toml`, generated from the same structs the loader reads. |
