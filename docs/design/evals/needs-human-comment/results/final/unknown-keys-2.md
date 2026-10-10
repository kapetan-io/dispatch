## In plain English

I can't finish "reject unknown keys in config.toml" without breaking the example config in the blueprint. That example sets two keys the loader has never read, so a strict loader would refuse to start on the file we tell people to copy.

```toml
[linear.statuses]
todo = "Todo"            # the loader has no field for this
[linear.needs_human]     # the loader has no table for this
status = "Needs Human"
```

Today the loader ignores both lines. Someone who sets `needs_human.status = "Triage Hold"` gets no error and no effect. That silent failure is what this ticket is meant to end. But once unknown keys are rejected, the documented config fails to load.

I need you to pick one of three ways out.

1. **Remove both entries from the example config.** The check stays strict at every depth, as the ticket says.
2. **Add `todo` and `[linear.needs_human]` to the loader.** The keys become real, but that adds new configuration behavior the ticket never asked for.
3. **Check only top-level keys.** Nothing breaks, but the typo `[linear.statuses] in_reveiw = "..."` still passes silently.

I recommend option 1. The loader has never read these keys, so the example documents behavior that doesn't exist. Option 2 invents a feature, and option 3 skips the nested typos this ticket targets. If you do want `needs_human` to be configurable, it should be its own ticket.

## Details

**State:** no code changed yet. The ticket is in Needs Human.

**Evidence** (blueprint example config and config loader, both at `2700f6c`)
- `Config.Statuses` has no `todo` field.
- `Config` has no `needs_human` table.
- A strict decode at any depth would fail on both entries, so the example config would stop loading.

**Why this is yours:** what the config accepts is product scope. The ticket says "reject unknown keys at any depth" and "nothing in the design changes", and I can't satisfy both with the example as written.

**Decision needed:** option 1, 2 or 3. On option 1 I'll edit the example config and add surface tests for a typo at the top level and at depth. Reply on this ticket and I'll pick it up on the next nudge.
