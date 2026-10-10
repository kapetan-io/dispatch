## In plain English

The ticket asks me to make the settings file (`config.toml`) reject any key the loader doesn't recognize, at every nesting depth. The example settings file in the design already contains two entries the loader doesn't recognize, so strict checking would make that documented example fail to load. The ticket doesn't say whether the example, the loader, or the strictness should give way, and the answer decides what settings the product accepts. I need you to pick one.

```toml
[linear.statuses]
todo = "Todo"            # loader's statuses section has no "todo" field
[linear.needs_human]     # loader has no "needs_human" section at all
status = "Needs Human"
```

## Details

**State.** I have written no code and no tests. The ticket is in Needs Human because every option below changes what `config.toml` accepts.

**Evidence.** I read the design's example config and the config loader, both as of commit `2700f6c`. The loader's `Config.Statuses` struct has no `todo` field, and `Config` has no `needs_human` table. With unknown-key rejection on, loading the example config fails on both entries.

**Decision needed.** Which of these should the code do?

1. **Widen the loader** so it knows `todo` and `needs_human`. The ticket's "reject at any depth" and "nothing in the design changes" both stay true. This is my recommendation. I haven't checked whether any code reads those two settings, so they may be accepted and then ignored.
2. **Change the documented example** by deleting both entries. The loader stays as is, but the design then changes, which the ticket says it doesn't.
3. **Limit the check to top-level keys.** The example loads, but a typo like `[linear.statuss]` still passes silently, which is the bug the ticket exists to fix.

Once you answer, I'll implement it with surface tests that run the CLI against a good config and a config with an unknown key at each depth.
