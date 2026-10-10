**Needs your decision: should the example config in the design keep loading once unknown keys are rejected?**

Right now it can't. Rejecting unknown keys at every depth, as the ticket asks, makes the design's own example `config.toml` fail to load. Two of its entries have nothing in the loader to land on.

```toml
[linear.statuses]
todo = "Todo"            # Config.Statuses has no todo field
[linear.needs_human]     # Config has no needs_human table
status = "Needs Human"
```

The ticket doesn't say which side gives way. That decides what `config.toml` accepts, so I'm not picking it for you. I've stopped work and moved this to Needs Human.

**Options**
- **Add `todo` and a `needs_human` table to the loader's struct.** The documented config loads unchanged, the check stays at every depth, and the design doesn't change. The catch is that two keys get accepted that I haven't confirmed anything reads.
- **Edit the example config in the design to drop those keys.** The loader stays as it is. This contradicts the ticket's "nothing in the design changes".
- **Reject unknown keys at the top level only.** Nothing breaks, but `[linear.statuses]` typos still pass silently, which is the case the ticket exists to catch.

**My recommendation** is to add the two fields. It's the only option that keeps both the ticket's "any depth" and its "no design change". If nothing will ever read `needs_human`, the second option is cleaner, and you'd know that better than I do.

Reply with which one and I'll build it. Everything else on the ticket is ready to go.
