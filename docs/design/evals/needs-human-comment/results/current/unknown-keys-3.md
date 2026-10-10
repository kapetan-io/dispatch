Moving this to Needs Human. Rejecting unknown keys at any depth would make the blueprint's own example config fail to load, and the ticket doesn't say which side should give way.

The example config has two entries the loader's struct doesn't know.

```toml
[linear.statuses]
todo = "Todo"            # Config.Statuses has no todo field
[linear.needs_human]     # Config has no needs_human table
status = "Needs Human"
```

Anyone who copies the documented config would get a load error. This decides what config.toml accepts, so I'm leaving it to you. I haven't changed any code.

**Options**

1. **Widen the struct.** Add `todo` and a `needs_human` table to the loader, and keep rejecting unknown keys at every depth. The documented config loads as written, and the ticket's "nothing in the design changes" holds.
2. **Trim the example config.** Remove those two entries from the blueprint and keep strict checking at every depth. This changes the design, which the ticket says it doesn't.
3. **Check top-level keys only.** The example loads unchanged. A typo like `tood = "Todo"` under `[linear.statuses]` still passes silently, which is the mistake this ticket exists to catch.

I'd take option 1. It's the only one that matches both the ticket and the blueprint. If you pick it, tell me whether `todo` and `needs_human` should drive any behavior now or just be accepted and stored.

Reply with 1, 2 or 3 and I'll resume the build.
