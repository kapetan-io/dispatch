## In plain English

Ticket ENG-306 asks the loader to reject any key in `config.toml` that it doesn't recognize, so a typo like `statuss` fails loudly instead of being ignored. The blueprint's own example config contains two keys the loader doesn't recognize either, so with this check turned on, the example from the docs would fail to load.

```toml
[linear.statuses]
todo = "Todo"              # the loader has no field for this

[linear.needs_human]       # the loader has no table for this
status = "Needs Human"
```

The ticket doesn't say which side gives way, and the choice decides what the config accepts, so it's yours. I'm stopped until you pick.

I recommend widening the loader to accept both keys. The ticket says nothing in the design changes, and the example config is part of that design, so the loader is the thing that's behind. Editing the docs would change the design. A top-level-only check would drop the "at any depth" requirement, and typos nested under `[linear]` are the likeliest ones.

The cost is that `todo` and `[linear.needs_human]` would load into fields nothing reads yet. If they are meant to do nothing, I'd rather the docs lose them.

## Details

**State.** Nothing is implemented. I read the blueprint's example config and the config loader at commit `2700f6c`. I haven't checked whether other code reads `todo` or `needs_human` from somewhere other than the loader.

**Evidence.** `Config.Statuses` has no `todo` field. `Config` has no `needs_human` table. A strict decode of the example config fails on both.

**Options.**
- **Widen the loader (recommended).** Add `todo` and `[linear.needs_human]` to the config structs. Strict check at every depth. Design unchanged.
- **Edit the example config.** Remove the two keys from the blueprint. Strict check at every depth, but this changes the design and the ticket says it doesn't.
- **Top-level keys only.** The example loads untouched, but nested typos still pass silently, and the ticket asks for any depth.

**What I need.** Reply with the option you want. I'll build it, with a test that loads the blueprint's example config and a test that rejects a nested typo.
