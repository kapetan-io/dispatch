## In plain English

ENG-306 asks the loader to refuse a `config.toml` that contains a key it doesn't recognise, so a typo like `stauts` fails loudly instead of being silently ignored. The example config in the blueprint already contains two keys the loader doesn't recognise. If I turn the check on as written, the documented example stops loading.

```toml
[linear.statuses]
todo = "Todo"            # the loader has no field for this

[linear.needs_human]     # the loader has no table for this
status = "Needs Human"
```

The ticket doesn't say which side gives way, the documented config or the loader. That decides what users are allowed to put in their config, so I'm not choosing it for you.

**Decision needed.** Which of these should I do?

1. **Teach the loader the documented keys (my recommendation).** Add `todo` under `[linear.statuses]` and a `[linear.needs_human]` table, then reject everything else at any depth. The ticket says the design doesn't change, and the blueprint is the design, so the loader is what's out of date. Keys the loader lacks today are the ones I'd least want to find out about by having a user's config break.
2. **Edit the example config.** Remove `todo` and `[linear.needs_human]` from the blueprint example. This keeps the loader as is, but it changes a documented contract that the ticket says is fixed.
3. **Only check top-level keys.** The example would still load, but typos inside `[linear]` would pass unnoticed. That contradicts "at any depth" in the ticket, so I'd rule it out.

## Details

- **Status.** I've read the blueprint example config and the config loader at commit `2700f6c`. I have changed no code and run no tests.
- **Evidence.** `Config.Statuses` has no `todo` field, and `Config` has no `needs_human` table. Both appear in the blueprint example above.
- **Not yet checked.** If you pick option 1, I haven't confirmed whether anything reads `needs_human.status` today or whether the new fields would just be accepted and stored. I'll check that first and note what I find here.
- **Next.** Once you answer, I'll write the surface tests for an unknown key at the top level and at depth, plus a test that loads the documented example. Then I'll implement and continue the build.
