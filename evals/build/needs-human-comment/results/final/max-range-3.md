## In plain English

I can't build the validation for ENG-312 yet, because the only place the allowed range for `max_in_progress` is written down is this ticket. Operators can set this value to cap how many workers run at once. If I pick the wrong limits, a setting that works today could be rejected, or a bad one accepted, and that is hard to take back once people depend on it.

Here is what an operator would see under the ticket's range of 1 to 8:

```toml
[dispatch]
max_in_progress = 9   # rejected, outside 1 to 8
max_in_progress = 0   # rejected, outside 1 to 8
max_in_progress = 4   # accepted (the default)
```

Do you want the range to be 1 to 8? I think yes, and that the blueprint should be updated to say so. Reply with the range you want, or tell me the ticket's range is final, and I'll build it.

## Details

- **State:** nothing is implemented. The branch is untouched for this ticket and the ticket is moving to Needs Human.
- **Evidence:** I searched the blueprint and the config package at commit `2700f6c`. Both name `max_in_progress` and give it a default of 4. Neither mentions a minimum, a maximum, or any rejection behavior.
- **Why this is yours to decide:** the range sets what operators may configure, which is a public contract call. The ticket is the only source for 1 to 8.
- **Recommendation:** keep 1 to 8, reject anything outside it with an error that names the setting and the allowed range, and add the range to the blueprint in the same PR so the config and the design agree. The default of 4 is already inside the range.
- **Open edge:** I'd reject `0` and negatives rather than treat `0` as "no cap", since the ticket's purpose is to always cap workers. Tell me if you want `0` to mean unlimited.
