## In plain English

ENG-306 asks the config loader to reject any unknown key in `config.toml`, at every nesting level. The example config in the blueprint (the design document) contains keys the loader doesn't know. If I enforce the rule as written, the documented example fails to load. The ticket doesn't say whether the example, the loader, or the rule should give way, and that decides what the config accepts, so I'm asking you.

The two keys in the blueprint's example that the loader's `Config` struct doesn't have:

```toml
[linear.statuses]
todo = "Todo"            # Config.Statuses has no todo field
[linear.needs_human]     # Config has no needs_human table
status = "Needs Human"
```

## Details

**State.** The ticket is in Needs Human. I've read the blueprint and the loader and haven't changed the loader yet.

**Evidence.** A strict decode of the blueprint's example would report `linear.statuses.todo` and `linear.needs_human` as unknown. The ticket also says nothing in the design changes. Rejecting unknown keys while leaving the example and struct as they are can't satisfy both.

**Options**
- **Widen the struct.** Add a `todo` field to `Statuses` and a `needs_human` table to `Config`, so the loader matches the blueprint. The rule stays "any depth" and the design doc stays as written. The cost is two new settings the code must read or at least carry.
- **Edit the example.** Remove the two entries from the blueprint. This changes the design, which the ticket says it doesn't. If those settings were never meant to exist, it's the cleaner fix.
- **Check top-level keys only.** Both files stay as they are. This contradicts the ticket's "at any depth", and typos inside `[linear.statuses]` would still load silently.

**Recommendation.** Widen the struct. The blueprint is the design, and the ticket says the design doesn't change, so the loader is the side that's behind. If those two entries were never meant to be real settings, say so and I'll edit the example instead.

**Decision needed.** Which of the three should I build? I'll resume once you've answered here.
