Strict loading as the ticket describes would reject the example config in the blueprint, so it can't ship until you pick which side moves. This is your call because it sets what `config.toml` accepts.

The example config has two things the loader doesn't know about.

```toml
[linear.statuses]
todo = "Todo"            # Config.Statuses has no todo field
[linear.needs_human]     # Config has no needs_human table
status = "Needs Human"
```

Today the loader ignores both silently. With unknown keys rejected at any depth, anyone who copies the documented example gets a load error. The ticket doesn't cover this, and it says nothing in the design changes.

**Options**
1. **Fix the example.** Remove `todo` and `[linear.needs_human]` from the blueprint so it matches what the loader reads. Nothing is read from those keys today, so no behavior changes. This is my recommendation.
2. **Widen the struct.** Add `todo` and a `needs_human` table to `Config` so the example stays valid. This adds config surface that nothing consumes yet, and it needs its own design.
3. **Check top-level keys only.** The example loads unchanged, but typos under `[linear.*]` still pass silently. That is the failure the ticket exists to stop.

If those two keys were meant to do something, tell me and I'll take option 2, with a follow-up ticket for the behavior. Otherwise I'll take option 1. I'm holding the ticket in Needs Human until you answer, and I haven't started implementation.
