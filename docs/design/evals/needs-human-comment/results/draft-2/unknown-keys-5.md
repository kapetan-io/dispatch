## In plain English

I'm making the config loader reject keys it doesn't recognise, so a typo like `statsus` fails at startup instead of being silently ignored. The design's own example config contains two settings the loader has no field for, so with the new check the documented example would fail to load. The ticket doesn't say whether the example, the loader, or the strictness should change, and that decides what `config.toml` accepts, so I'm asking you.

The two lines from the example config that the loader doesn't know:

```toml
[linear.statuses]
todo = "Todo"            # the loader's statuses section has no `todo` field
[linear.needs_human]     # the loader has no `needs_human` section at all
status = "Needs Human"
```

I recommend fixing the example config to match the loader and keeping the check strict at every depth, as the ticket asks. Nothing reads those two settings today, so they do nothing. Widening the loader would add settings nothing uses, and a top-level-only check would let typos through inside `[linear.statuses]`, where they are most likely.

Reply with one of these and I'll finish the ticket.
1. Fix the example config (my recommendation).
2. Add `todo` and `needs_human` to the loader, if they are meant to be real settings.
3. Check only top-level keys.

## Details

**State.** Moved to Needs Human. I have written no code yet, since all three options change what the tests assert.

**Evidence.**
- The ticket says to reject unknown keys at any depth and that nothing in the design changes.
- The example config has `[linear.statuses] todo` and a `[linear.needs_human]` table.
- The config struct has no field for either.
- A strict check at any depth makes the documented example fail to load, so the ticket and the design disagree.

**Decision needed.** Which of the three options above to take. I'll note the answer on this ticket and build to it.
