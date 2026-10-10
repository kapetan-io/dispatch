I need you to confirm the valid range for `max_in_progress` before I build the validation. Whatever range I pick decides which values operators are allowed to configure, and that is your call.

**Example.** If the range is 1 to 8, a config with `max_in_progress = 12` fails to load with an error. If the range is 1 to 16, the same config loads and runs 12 workers.

**What I found.** The ticket says the blueprint gives `max_in_progress` a valid range of 1 to 8. I searched the blueprint and the config package at commit `2700f6c`. They give it a name and a default of 4, and say nothing about a range.

```toml
[dispatch]
max_in_progress = 4
```

The 1 to 8 range appears only in this ticket.

**My recommendation.** Keep 1 to 8 and add it to the blueprint, so the ticket and the design agree. Starting narrow is cheap to undo. Widening the range later breaks no existing config, but narrowing it later rejects configs that already work.

**What I need from you.**
- Confirm 1 to 8, or give me the range you want.
- Tell me if you'd rather I not edit the blueprint, in which case the range lives only in the config validation.

Everything else on the ticket doesn't depend on this answer, and I'll resume as soon as you reply.
