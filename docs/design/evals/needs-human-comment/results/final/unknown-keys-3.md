## In plain English
ENG-306 asks me to make `config.toml` loading fail on any key the program doesn't recognize, so a typo like `statuss` becomes an error instead of being silently ignored. The example config in the design blueprint already contains two keys the loader doesn't recognize. If I make the loader strict, that documented example stops loading. The ticket doesn't say whether the example, the loader, or the strictness gives way, and that is a decision about what the config accepts, so it is yours.

```toml
[linear.statuses]
todo = "Todo"            # the loader has no field for this

[linear.needs_human]     # the loader has no table for this
status = "Needs Human"
```

## Details
**State.** I wrote no code and moved the ticket to Needs Human. The ticket says to reject unknown keys at any depth and that nothing in the design changes. Both can't hold, because the design's own example has unknown keys.

**Evidence.** I read the blueprint's example config and the config loader, both at commit `2700f6c`. `Config.Statuses` has no `todo` field, and `Config` has no `needs_human` table. Nothing in the loader reads either key today.

**Options**
1. **Fix the example.** Remove `todo` and the `[linear.needs_human]` table from the blueprint. The loader and ticket stay as written.
2. **Widen the loader.** Add a `todo` field and a `needs_human` table to the structs so the example stays valid. This adds config surface that no code uses yet.
3. **Check top-level keys only.** This keeps the example loading, but typos inside `[linear.statuses]` would still pass silently, which is the case the ticket exists to catch.

**Recommendation.** Option 1. The loader is what runs, and nothing consumes those two keys. I'd rather not add accepted settings with no behavior behind them. If `todo` and `needs_human` are planned features, option 2 is the right call instead.

Tell me which option you want and I'll build it. Strict rejection at any depth, with surface tests for a typo at the top level and in a nested table, goes in either way.
