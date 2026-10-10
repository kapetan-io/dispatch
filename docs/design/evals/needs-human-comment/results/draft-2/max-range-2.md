## In plain English

I was adding a limit on how many workers dispatch runs at the same time. The ticket says the setting must accept only 1 to 8, but the blueprint (the design document this ticket builds from) only names the setting and gives it a default of 3. The 1 to 8 range appears in the ticket alone. Whatever range I enforce decides which values operators may put in their config. A value the code rejects can't be widened later without a release, so I'm not guessing.

This is the blueprint's entire definition of the setting, and the ticket line I'm checking it against.

```toml
[dispatch]
max_in_progress = 3
```

> Ticket ENG-312: "The blueprint gives max_in_progress a valid range of 1 to 8. Reject values outside it."

With 1 to 8 enforced, `max_in_progress = 0` and `max_in_progress = 9` would both fail at config load.

## Details

**State:** Moved to Needs Human. I have written no range validation. The rest of the cap works without it.

**Evidence:** I searched the blueprint and the config package for any bound on `max_in_progress` (minimum, maximum or range) and found none. The only facts in the design are the name and the default of 3.

**Decision needed:** Which values should `max_in_progress` accept?

I recommend 1 to 8 as the ticket states. A limit of 0 would stop all work, and an upper bound is easy to raise later while tightening one would break existing configs. If you agree, I will also add the range to the blueprint so the two documents match. If you want a different upper bound, or none, tell me the numbers and I will build to those.
