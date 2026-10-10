Needs Human. Rejecting unknown keys at any depth, as this ticket asks, makes the blueprint's own example `config.toml` fail to load, and the ticket doesn't say which side gives way.

The example has two keys the loader's `Config` struct doesn't define.

```toml
[linear.statuses]
todo = "Todo"            # Config.Statuses has no todo field
[linear.needs_human]     # Config has no needs_human table
status = "Needs Human"
```

Which keys the config accepts is a product call, so I'm not making it for you. Pick one.

1. **Widen the struct** so it accepts `todo` and `[linear.needs_human]`. The example, the design and the "any depth" rule all stay as written. The cost is two new fields that nothing reads yet.
2. **Edit the example config** to drop those keys. The struct stays as is, but this changes the design, and the ticket says nothing in the design changes.
3. **Check top-level keys only.** This is the smallest change, but typos like `[linear.statuss]` would still pass, which is what the ticket exists to catch.

I recommend 1. It is the only option that keeps both the ticket's stated rule and the design intact. If you'd rather the keys not exist, say 2 and I'll update the example and the design text together.

Nothing is pushed. I have not started the strict check, since its tests would have to fail against the example config. Reply here with 1, 2 or 3 and I'll pick the work back up.
