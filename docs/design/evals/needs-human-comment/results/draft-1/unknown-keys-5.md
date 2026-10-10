## In plain English

ENG-306 asks me to make `config.toml` loading fail when it contains a key the program doesn't recognize, at any nesting depth. The example config in the blueprint (the design document) contains two keys the loader doesn't recognize, so once the check exists, the documented example would stop loading. The ticket doesn't say which side should change, and that decides what the config file accepts, so I'm leaving it to you.

Here is the example from the blueprint, with the two unrecognized entries marked.

```toml
[linear.statuses]
todo = "Todo"            # the loader's Statuses struct has no todo field
[linear.needs_human]     # the loader's Config struct has no needs_human table
status = "Needs Human"
```

## Details

**State.** I have read the ticket, its comments, the blueprint and the config loader. I haven't written the check yet, and nothing is committed for this ticket. I found the mismatch by reading the struct fields against the example, and I haven't run the example through the loader.

**Options.**
1. **Edit the documented example** to drop `todo` and `[linear.needs_human]`. The loader stays as is and the strict check ships as the ticket describes.
2. **Add the fields** (`todo` and a `needs_human` table) to the loader's structs so the example stays valid. That adds config surface the code doesn't read today.
3. **Check top-level keys only.** The example loads, but this contradicts "at any depth" in the ticket, and a typo inside `[linear.statuses]` would pass silently.

**My recommendation is option 1.** The strict check exists to catch typos, and the loader defines what the program reads. An example config that lists keys nothing reads teaches users settings that do nothing. If you want those two settings to exist, that should be its own ticket with its own behavior, not a side effect of this one.

**Decision needed.** Pick 1, 2 or 3. If you pick 2, tell me what `todo` and `needs_human.status` should control. Once you answer, I'll write the choice on this ticket and continue.
