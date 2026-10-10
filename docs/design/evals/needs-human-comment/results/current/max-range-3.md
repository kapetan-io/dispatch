Which values of `max_in_progress` may an operator set? That is a public contract call, so I'm stopping here and moving the ticket to Needs Human.

The ticket says the blueprint gives it a valid range of 1 to 8 and that values outside it should be rejected. I searched the blueprint and the config package, and the blueprint says no such thing. It gives the setting a name and a default of 3.

```toml
[dispatch]
max_in_progress = 3
```

The 1 to 8 range appears only in this ticket. If I enforce it, I'm deciding what operators can configure on the strength of one line, with no design backing it.

I'd go with 1 to 8 as written and add the range to the blueprint. A floor of 1 is needed anyway, since 0 would stop all work. A ceiling is safe to start with because loosening it later never breaks an existing config, while tightening it later would. If you'd rather have no ceiling, say so and I'll reject only values below 1.

**What I need from you**
- Confirm 1 to 8, or give me a different range.
- Say whether the blueprint should be updated in this PR. I'd update it so the contract has one source.

Once you answer, I'll write the answer here, add the validation with a surface test for each boundary (0, 1, 8, 9), and continue to the PR.
