## In plain English

I'm building the setting that caps how many dispatch workers run at once (`max_in_progress`). The ticket says the design document (the blueprint) allows values from 1 to 8 and that anything outside should be rejected. The blueprint gives the setting a name and a default of 3 but no range, so 1 to 8 comes only from the ticket. The range decides what operators can put in their config files, which is a public contract, so I need you to confirm it before I enforce it.

Here is the ticket text next to what the blueprint says.

Ticket ENG-312 says:

```
The blueprint gives max_in_progress a valid range of 1 to 8.
Reject values outside it.
```

The blueprint says:

```toml
[dispatch]
max_in_progress = 3
```

## Details

**State.** The ticket is in Needs Human. I have not written the range check, and I searched both the blueprint and the config package. Neither mentions a minimum, a maximum, or any validation for this setting.

**Why I'm asking.** Rejecting 9 or higher is a promise to operators that we never run more than 8 workers. If the 8 is a guess, someone who needs 10 hits a config error for no reason. Rejecting 0 and below is safe, since a cap of zero stops all work, but the upper bound is a product decision.

**Decision needed.** Is 1 to 8 the range you want?

- **Keep 1 to 8 (my recommendation).** The ticket author likely had a reason for the 8, and a hard error on bad config is easier to loosen later than to tighten. I'd add the range to the blueprint so the contract is written down in one place.
- **Reject only values below 1.** Choose this if 8 was a placeholder. Operators can then raise the cap as far as their machine allows.
- **A different range.** Give me the bounds.

Once you answer, I'll add the validation in the config package with surface tests for the lower bound, the upper bound, and the default of 3. I'll also record the answer on this ticket.
