## In plain English

ENG-306 says `config.toml` should refuse keys it doesn't recognize, so a typo like `statsuses` fails loudly instead of being ignored. The example config in the blueprint already contains keys the loader doesn't recognize. If I add the check as written, the documented example stops loading.

The blueprint example at commit `2700f6c` has these two entries:

```toml
[linear.statuses]
todo = "Todo"            # the loader has no field for this

[linear.needs_human]     # the loader has no table for this
status = "Needs Human"
```

The ticket doesn't say whether the config or the check should give way. That decides what users are allowed to write in their config, so I'm not picking for you. I've stopped work on this ticket until you answer.

Which of these do you want?

1. Keep the check strict at every depth and delete the two stale entries from the example. I recommend this. The loader has never read them, so nobody's config can depend on them.
2. Keep the check strict and add `todo` and `needs_human` to the loader, so the example stays as written. This only makes sense if you meant them to be real settings.
3. Check top-level keys only. Typos inside `[linear.statuses]` would still pass silently, which is the case the ticket is trying to catch.

## Details

**State:** Nothing is implemented yet. The ticket is in Needs Human.

**Evidence:**
- I read the blueprint's example config and the config loader, both at `2700f6c`. The loader's `Config.Statuses` struct has no `todo` field, and `Config` has no `needs_human` table.
- Today these keys load without error because unknown keys are ignored.
- I haven't checked whether anything outside the loader reads `todo` or `needs_human`. If you pick option 1, I'll grep for that first and come back if I find a reader.

**Decision needed:** option 1, 2 or 3. Reply on the ticket and I'll resume from the answer.
