## In plain English
I'm adding a check that makes dispatch refuse to start when its settings file, `config.toml`, contains a key it doesn't recognize, so typos fail loudly instead of being silently ignored. The example config in the design document has two settings the loader doesn't recognize, so with the check on, copying the documented example would fail to load. The ticket doesn't say whether the example, the loader, or the check should give way, and each choice changes what users may write in their config. I need you to pick one.

```toml
[linear.statuses]
todo = "Todo"            # the loader has no field for this
[linear.needs_human]     # the loader has no table for this
status = "Needs Human"
```

## Details
**State.** I've changed no code. The ticket is in Needs Human.

**Evidence.** I read the blueprint's example config and the config loader at commit `2700f6c`. The example sets `todo` under `[linear.statuses]` and defines a `[linear.needs_human]` table. The loader's statuses struct has no `todo` field, and `Config` has no `needs_human` table. The example loads today only because unknown keys are ignored, so both settings do nothing.

**Options**
1. Remove the two keys from the example config. The loader stays as is.
2. Add `todo` and a `needs_human` table to the loader so the example stays valid.
3. Check top-level keys only. This contradicts the ticket's "at any depth", and both offending keys are nested, so typos inside tables would still pass silently.

**Recommendation.** Option 1. A key that loads and does nothing is the failure this ticket exists to stop, and option 2 would bless two settings that no code reads. If the design intends those settings to work, that is a separate ticket with real behavior behind it.

**Decision needed.** Which option, and if it's 2, what `todo` and `needs_human.status` should control. I'll record your answer here and continue.
