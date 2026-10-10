## In plain English

ENG-306 asks me to make `config.toml` fail to load when it contains a key the program doesn't recognize, at any nesting depth, so a typo like `statsus` stops being silently ignored. The example config in the blueprint (the design document) already contains two keys the loader has no place to store, so the strict check would make the documented example fail. The ticket says nothing in the design changes and doesn't say which side to give, and the answer decides what `config.toml` accepts.

This is the blueprint's example config as written.

```toml
[linear.statuses]
todo = "Todo"            # Config.Statuses has no todo field
[linear.needs_human]     # Config has no needs_human table
status = "Needs Human"
```

## Details

**State.** Needs Human. I read the blueprint's example config and the config loader, and I have not changed the loader.

**Evidence.** The loader's `Config` struct has no `todo` field under `Statuses` and no `needs_human` table. With unknown keys rejected, loading the blueprint's own example returns an error naming `linear.statuses.todo`.

**Options**
1. **Widen the struct** to add `todo` and `needs_human`, so the example loads as written. This is my recommendation. The ticket says the design doesn't change, which makes the example the spec and the loader the part that is behind.
2. **Edit the documented example** to drop those two keys. This is right only if they were never meant to be real settings, and that would be a change to the design the ticket says is fixed.
3. **Check top-level keys only.** This is the smallest change, but it contradicts "at any depth" in the ticket and leaves typos under `[linear.statuses]` silently ignored.

**Decision needed.** Which of the three. If you pick 1, tell me what `todo` and `needs_human.status` should do at runtime, or whether they only need to load and be stored. I'll continue as soon as you answer.
