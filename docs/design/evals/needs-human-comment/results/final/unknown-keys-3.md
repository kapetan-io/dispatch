## In plain English

You asked for `config.toml` to be rejected when it contains a key the loader doesn't recognize. The example config in the design contains two such keys. With the check on, the config the design documents would fail to load. I need you to pick which side gives, because the ticket doesn't say.

Here is the documented example, with the two keys the loader doesn't know:

```toml
[linear.statuses]
todo = "Todo"            # loader has no field for this

[linear.needs_human]     # loader has no table for this
status = "Needs Human"
```

Both keys would produce an unknown-key error today.

**Options**

- **A. Widen the loader to accept the documented keys.** The design stays as written and the check still applies at every depth. I recommend this one, since the ticket says nothing in the design changes.
- **B. Edit the example config to drop the two keys.** This is the smallest code change, but it changes the design's documented config.
- **C. Check top-level keys only.** The two keys would load, but this contradicts "at any depth" in the ticket, and a typo inside `[linear.statuses]` would still pass silently.

I stopped before writing code because each option changes what the config accepts.

## Details

- **State:** Nothing is implemented yet. The ticket is in Needs Human.
- **Evidence:** I compared the blueprint's example config with the config loader, both as of commit `2700f6c`.
  - `Config.Statuses` has no `todo` field.
  - `Config` has no `needs_human` table, so `status = "Needs Human"` has nowhere to land.
- **Open question under A:** I haven't checked whether anything should read these two values. If nothing does, A adds fields that are accepted but unused. If something should read them, that is more scope than this ticket.
- **Next step:** Reply with A, B or C. On A, I'll add the fields and the strict check, then add a surface test that loads the documented example unchanged and one that rejects a typo at depth 3.
