## In plain English

ENG-306 asks the config loader to refuse any key in `config.toml` that it doesn't recognize, at every nesting level. The example config in the design blueprint contains two entries the loader has no place for, so with the check on, the documented example would fail to load. The ticket doesn't say whether the example, the loader, or the strictness should give way, and that decides what users are allowed to put in their config, so I'm asking before I pick.

The blueprint's example config, with the two entries the loader doesn't know:

```toml
[linear.statuses]
todo = "Todo"            # Config.Statuses has no todo field

[linear.needs_human]     # Config has no needs_human table
status = "Needs Human"
```

## Details

**State.** Nothing is committed for this ticket. The ticket is moving to Needs Human because I'm blocked on the decision below.

**Evidence.** I read the blueprint's example config and the struct in `internal/config`. `Config.Statuses` has no `todo` field, and `Config` has no `needs_human` table. Today the loader silently drops both. With "reject unknown keys at any depth" in place, loading the blueprint's own example returns an error on the first of them.

**Options.**

1. Keep the check strict at any depth and delete the two entries from the example config. This matches the ticket's wording and its purpose, which is to catch typos like `statuss`. It is only right if those two entries are stale docs rather than planned settings.
2. Keep the check strict and add `todo` and a `needs_human` table to the struct. This is only right if someone intends those settings to work, and it adds config surface the ticket never asked for.
3. Limit the check to top-level keys. The example then loads untouched, but a typo inside `[linear.statuses]` still passes silently, which is the failure the ticket is meant to close.

**Recommendation.** Option 1. I haven't found any code that reads either entry, so I believe they are leftovers. Option 3 contradicts "at any depth" in the ticket.

**What I need from you.** Tell me which option, and if it's option 2, what `todo` and `needs_human.status` should do. Once I have the answer I'll finish the loader change and its surface tests.
