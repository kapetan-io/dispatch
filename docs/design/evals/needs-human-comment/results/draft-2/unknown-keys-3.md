## In plain English

This ticket makes dispatch refuse to start when `config.toml` contains a setting the loader doesn't recognize. The example config in the design document has two such settings, so with the strict check on, the documented example would fail to load. Fixing that means choosing what the config file accepts, which is a product decision, so I've stopped and need you to make it.

The example config's `[linear.statuses]` and `[linear.needs_human]` sections currently load fine, because the loader ignores keys it doesn't know:

```toml
[linear.statuses]
todo = "Todo"            # the loader's statuses struct has no todo field
[linear.needs_human]     # the loader's config has no needs_human table
status = "Needs Human"
```

## Details

**State:** Nothing is implemented. Ticket moved to Needs Human.

**Evidence:** I read the example config in the blueprint and the config loader. `Config.Statuses` has no `todo` field, and `Config` has no `needs_human` table. I did not search the rest of the codebase for other readers of these two keys, so I can't say whether anything depends on them.

**Why it's yours:** The ticket says to reject unknown keys at any depth and that nothing in the design changes. Both can't hold, because the design's own example breaks under the check.

**Options**
1. **Fix the example config.** Remove `todo` and the `needs_human` table from the design. The strict check then applies at any depth as the ticket says.
2. **Widen the loader.** Add a `todo` status and a `needs_human` table to the config struct. The example stays valid, and you get two new settings to define and support.
3. **Check top-level keys only.** The example loads unchanged, but typos inside `[linear.statuses]` still pass silently, and the ticket asked for any depth.

**My recommendation:** Option 1, unless you plan to use those two settings soon. Strict loading is the point of the ticket, and option 3 gives up the nested typo case it exists to catch. If you pick option 2, tell me what `todo` and `needs_human.status` should do, since the loader currently reads neither.

Reply with 1, 2 or 3 and I'll resume the build.
