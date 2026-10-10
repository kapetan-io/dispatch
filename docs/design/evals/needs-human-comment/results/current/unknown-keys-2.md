I need a decision on what `config.toml` accepts. The ticket says to reject unknown keys at any depth, but the blueprint's own example config has keys the loader doesn't know. The ticket doesn't say whether the blueprint, the loader, or the check gives way.

**Example.** The blueprint's example config (as of commit `2700f6c`) contains these lines.

```toml
[linear.statuses]
todo = "Todo"
[linear.needs_human]
status = "Needs Human"
```

The loader's `Config` struct has no `todo` field under statuses and no `needs_human` table. With the strict check, anyone who copies the documented config gets an error on both keys.

**Options**
1. **Widen the loader.** Add `todo` and `needs_human` to the struct so the documented config loads and typos still fail. This is my pick. The ticket says nothing in the design changes, so the blueprint stays as written and the code catches up.
2. **Change the blueprint.** Delete those two keys from the example. This only works if they were never meant to be settings.
3. **Check top-level keys only.** The example loads unchanged, but a typo like `[linear.statuss]` still passes silently. That is the mistake this ticket exists to catch.

Are `todo` and `needs_human` real settings? If yes, I'll take option 1 and start on it as soon as you answer. The ticket is in Needs Human until then.
