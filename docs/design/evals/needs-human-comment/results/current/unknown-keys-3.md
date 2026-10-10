I need a decision on ENG-306 before I can finish, so I've moved it to Needs Human.

**The question**

The ticket says to reject unknown keys at any depth. The example config in the blueprint contains two keys the loader doesn't know, so a strict check would make the documented example fail to load. The ticket doesn't say which side should give. Choosing it sets what config.toml accepts, which is your call.

**Example**

Both files are as of commit `2700f6c`. This is the blueprint's example config.

```toml
[linear.statuses]
todo = "Todo"
[linear.needs_human]
status = "Needs Human"
```

`Config.Statuses` has no `todo` field, and `Config` has no `needs_human` table. Today the loader ignores both. With the new check, a user who copies the example gets an error naming `linear.statuses.todo`.

**Options**

- **A. Widen the struct.** Add `todo` and `needs_human` so the documented example stays valid. The ticket says nothing in the design changes, and this is the only option that fully honors that. If nothing reads those keys yet, they'd be accepted and do nothing.
- **B. Fix the documented example.** Remove the two keys from the blueprint. This is right if they were never meant to be real settings, and it edits the design the ticket says to leave alone.
- **C. Check top-level keys only.** The example loads, but a typo like `[linear.statuss]` still passes silently, and the ticket asks for any depth.

I recommend A. I haven't checked whether other code expects those keys, so tell me if B fits better.

Reply with A, B or C and I'll continue. I haven't written any code.
