## In plain English

ENG-306 asks the config loader to refuse any key in `config.toml` that it doesn't recognize, at every level of nesting. I checked the documented example config against the loader and it already contains two settings the loader never reads. Turning on the strict check as written would make the example in our own docs fail to load, and the ticket doesn't say whether the docs, the loader, or the strictness should give way. That choice decides what the config file accepts, so I'm asking you instead of picking.

The example config from the blueprint, with what the loader does with each part:

```toml
[linear.statuses]
todo = "Todo"            # loader's Statuses struct has no `todo` field, so this is ignored today
[linear.needs_human]     # loader has no `needs_human` table, so the whole table is ignored today
status = "Needs Human"
```

With strict loading, both blocks produce an unknown-key error at startup.

## Details

**State.** I haven't changed the loader yet. Nothing is committed for the strict check. The branch holds only the unrelated work already in the tree.

**Evidence.** The blueprint's example config and `internal/config` disagree on `linear.statuses.todo` and on the `linear.needs_human` table. The ticket says "reject unknown keys at any depth" and "nothing in the design changes", and those two statements can't both hold while the example stays as written.

**Options**

1. **Reject at any depth and trim the example.** Remove `todo` and `[linear.needs_human]` from the documented config. The loader keeps its current fields, and the docs change is the only edit outside the loader. This is my recommendation. Today a user who edits `needs_human.status` gets no effect and no error, which is the failure this ticket exists to catch. Adding fields is a feature and belongs in its own ticket.
2. **Reject at any depth and widen the struct.** Add `todo` and `needs_human` to the config so the example loads. This only makes sense if the dispatcher is meant to read those settings, and I can't tell that from the code.
3. **Reject only top-level unknown keys.** The example loads unchanged, but a typo like `[linear.statuss]` or a misspelled key inside a table still passes silently. That contradicts "at any depth".

**Decision I need.** Pick 1, 2, or 3. If 2, tell me what the dispatcher should do with `todo` and `needs_human.status`. Once you answer, I'll add surface tests for nested unknown keys and finish the ticket.
