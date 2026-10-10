## In plain English

I'm building the setting that limits how many workers dispatch runs at the same time. The ticket says to reject any value outside 1 to 8. The blueprint (the design document this ticket cites) gives the setting a name and a default but no range, so I can't tell whether 8 was a deliberate choice or a guess. The range decides what operators are allowed to configure, which is your call, so I've stopped before writing the check.

The ticket says:

```
The blueprint gives max_in_progress a valid range of 1 to 8. Reject values outside it.
```

The blueprint says only this:

```toml
[dispatch]
max_in_progress = 3
```

## Details

**State.** I have not written the range check. The ticket is in Needs Human.

**Evidence.** I searched the blueprint and the config package for any range, minimum or maximum on `max_in_progress`. The only place "1 to 8" appears is the ticket text. The blueprint's only facts about the setting are its name and the default of 3.

**Why this is a question.** Anything outside the range makes dispatch refuse to start, so the range is part of the public config contract. Operators will write configs against it.

**What I need.** Which range should dispatch accept for `max_in_progress`?

- **1 to 8, as the ticket says.** I recommend this once you confirm the 8 was intended. A cap is cheap to raise later because no existing config breaks. Lowering it later would make configs that used 9 or more fail to load.
- **Minimum 1, no maximum.** Pick this if 8 was a guess. Zero workers means nothing ever runs, so rejecting 0 and below is safe either way.

Once you answer, I'll write the range into the blueprint and the config check together, with tests for both edges, so the design and the code agree.
