I can't finish rejecting unknown keys in `config.toml` until you decide what the config is allowed to contain.

The ticket says to reject unknown keys at any depth. The example config in the blueprint (as of commit `2700f6c`) has two entries the loader doesn't know, so it would fail to load under that rule:

```toml
[linear.statuses]
todo = "Todo"            # Config.Statuses has no todo field
[linear.needs_human]     # Config has no needs_human table
status = "Needs Human"
```

Anyone who copies the documented config would get an error on first run. The ticket doesn't say which side is wrong, and the answer changes what users can put in their config, so I haven't picked one.

Options:

1. **Widen the loader.** Add `todo` and `needs_human.status` to the config struct. The documented config keeps working and the rest of the ticket stands.
2. **Fix the documentation.** Remove those two entries from the blueprint example. This is right if they were leftovers that were never meant to be settings.
3. **Check only top-level keys.** The example loads as written, but this contradicts "at any depth", and a typo like `[linear.statuss]` would still pass silently.

I lean toward 1, because both entries look like settings dispatch would want to read. You know whether they were meant to be configurable.

I've written no code yet. Reply with 1, 2 or 3 and I'll pick the build back up from there.
