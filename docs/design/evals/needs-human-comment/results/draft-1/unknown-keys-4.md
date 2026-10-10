Moving this to Needs Human. I need a decision on what `config.toml` accepts.

## In plain English

The ticket asks me to make the config loader refuse any key it doesn't recognize, at every nesting level. The sample `config.toml` in the design document (the blueprint) already contains keys the loader has no place for. If I make the check strict, the documented example stops loading. The ticket doesn't say whether the example, the loader, or the strictness should give way, and that choice sets what users can put in their config, so it's yours.

Here is the example from the blueprint. The two commented lines are the ones the loader doesn't know:

```toml
[linear.statuses]
todo = "Todo"            # Config.Statuses has no todo field

[linear.needs_human]     # Config has no needs_human table
status = "Needs Human"
```

## Details

**State:** I haven't changed the loader or the example config. The strict check is not written yet.

**Evidence:** The `Config` struct in the loader has no `todo` field under `Statuses` and no `needs_human` table. Any strict decode at any depth would reject both lines above.

**Options**
- **A. Widen the struct.** Add `todo` under statuses and a `needs_human` table to `Config`. The example stays valid and the check stays strict at every depth, as the ticket says. The cost is two new fields I haven't confirmed anything reads.
- **B. Fix the example.** Delete those keys from the blueprint. This edits the design, which the ticket says doesn't change, and it drops settings the design apparently wanted.
- **C. Check top-level keys only.** The example loads untouched, but typos under `[linear.statuses]` keep slipping through, which contradicts "at any depth".

**Recommendation:** A. It is the only option that satisfies both the ticket and the documented example.

**Decision needed:** reply A, B or C. If A, tell me whether `needs_human.status` should be a plain string field or part of a larger table. I'll finish the strict check and tests once I hear back.
