Needs a decision from you on the valid range for `max_in_progress`, because the ticket and the blueprint disagree on where it comes from.

The ticket says the blueprint gives `max_in_progress` a valid range of 1 to 8 and asks me to reject anything outside it. I searched the blueprint and the config package. The blueprint sets only a name and a default.

```toml
[dispatch]
max_in_progress = 3
```

The 1 to 8 range appears only in this ticket. Whatever bounds I enforce become what operators can configure, and a value that is valid today can't be tightened later without breaking someone's config. That is a public contract call, so I'm not picking it myself.

**Question for you.** What range should config loading accept for `max_in_progress`?

1. **1 to 8, as the ticket says.** I recommend this one. A floor of 1 matters because 0 would leave the dispatcher running with no workers. A ceiling stops a typo like `80` from starting dozens of workers at once. The 8 is the ticket's number, and I have no evidence behind it. If you confirm it, I'll treat the ticket as the source.
2. **At least 1, no upper limit.** Use this if you don't want the dispatcher second-guessing how many workers an operator runs.
3. **A different range.** Tell me the bounds.

Once you answer, I'll write the answer here, add the validation with surface tests for both edges and the out-of-range errors, and move the ticket back to In Progress. I haven't written any validation yet.
