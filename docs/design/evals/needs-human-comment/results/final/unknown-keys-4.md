## In plain English

I was making the program's settings file (`config.toml`) reject any key it doesn't recognize, so a typo like `statuss` fails loudly instead of being ignored. The example settings file in the design document contains two keys the loader doesn't recognize. With the new check, that documented example would fail to load. The ticket doesn't say which side should give way, and that decides what the config accepts, so it's your call.

The two keys, as they appear in the design's example config:

```toml
[linear.statuses]
todo = "Todo"            # the loader's statuses section has no "todo" field

[linear.needs_human]     # the loader has no "needs_human" table at all
status = "Needs Human"
```

## Details

**State.** Ticket moved to Needs Human. No code written yet. I read the design's example config and the config loader, both as of commit `2700f6c`.

**Evidence.**
- The ticket says to reject unknown keys at any depth and that nothing in the design changes.
- The loader's `Config.Statuses` has no `todo` field, and `Config` has no `needs_human` table.
- Strict checking at any depth would make the documented example fail on both keys.

**Options.**
1. Add `todo` and a `needs_human` table to the loader, so the documented config loads as written.
2. Remove those two keys from the design's example config.
3. Check only top-level keys. This lets `todo` through but still rejects `[linear.needs_human]`, and it drops the "any depth" requirement.

**Recommendation.** Option 1. The ticket says the design doesn't change, and the design is where users learn what to write, so the loader should accept what it documents. Option 2 edits the design the ticket says to leave alone. Option 3 still breaks the example and weakens the check.

**What I need.** Which option you want. If you pick 1, I'd also like to know whether these keys should do anything now or only be accepted and ignored until a later ticket wires them up. I'll resume as soon as you answer here.
