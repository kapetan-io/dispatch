## In plain English

The ticket tells me to reject any worker cap outside 1 to 8, but the design never states a range. The range decides which values operators may put in their config file, so I think it's your call and not mine.

An operator would write this in their config:

```toml
[dispatch]
max_in_progress = 12
```

Under the ticket's rule, dispatch rejects that config as out of range. With no rule, it runs 12 workers at once. I need you to confirm that 1 to 8 is the range you want.

## Details

**State.** I stopped before writing code and moved the ticket to Needs Human.

**Evidence.** The blueprint and the config package, both at commit `2700f6c`, name `max_in_progress` and default it to 4. Neither mentions a minimum or maximum. The 1 to 8 range appears only in the ticket.

**Options.**
- Accept 1 to 8 as the ticket says, and I add the range to the blueprint so the design and the code agree.
- Enforce a minimum of 1 and no maximum.

I recommend 1 to 8. A typo like `80` would start 80 workers, each holding its own worktree, and a ceiling catches that at startup. Raising a ceiling later breaks no existing config, but lowering one does.

Reply with "1 to 8" or a different range and I'll continue.
