## In plain English

The ticket asks me to make `config.toml` fail to load when it contains a key the program doesn't recognize, at any nesting depth. The example config in the design document (the blueprint) has two entries the loader doesn't recognize, so the strict check would make the documented example fail to load. The ticket doesn't say whether the example, the loader, or the strictness should give way, and that decides what a valid config file is. That is your call, not mine.

```toml
[linear.statuses]
todo = "Todo"            # the loader's statuses struct has no todo field

[linear.needs_human]     # the loader's config has no needs_human table
status = "Needs Human"
```

## Details

**State:** Needs Human. No code written yet. I read the blueprint's example config and the config loader at commit `2700f6c`.

**Evidence:** The loader reads these keys and no others. `todo` is missing from `Config.Statuses`, and there is no `needs_human` table in `Config`. With unknown keys rejected, loading the blueprint's own example returns an error naming `linear.statuses.todo`.

**Decision needed.** Which of these should hold?

1. **Fix the example.** Delete `todo` and `[linear.needs_human]` from the blueprint's config so it matches what the loader reads. This is a docs-only change and keeps the ticket's "any depth" rule intact. I recommend this one. Nothing in the loader reads those keys today, so the example currently promises settings that do nothing.
2. **Widen the loader.** Add a `todo` status field and a `needs_human` table to the config struct. This only makes sense if you want them as real settings, and it adds product behavior this ticket doesn't cover. It would also need a decision on what they do.
3. **Check top-level keys only.** The example then loads unchanged, but typos nested under `[linear.statuses]` would still pass silently, which contradicts the ticket's "any depth".

If you pick 1, I'll edit the example and ship the strict check as written. If those two entries are planned settings, tell me which option you want and I'll continue from there.
