## In plain English

I was adding a check that makes dispatch refuse to start when `config.toml` has a key it doesn't recognize, at any nesting depth. The blueprint (the design document) contains a sample config that uses keys the loader has no place for. With the check on, the file the docs tell people to copy would fail to load. The ticket doesn't say whether the docs, the loader, or the check should give way, and that decides what dispatch accepts in config, so I'm stopping to ask.

This is the sample from the blueprint. The comments are mine.

```toml
[linear.statuses]
todo = "Todo"            # the Statuses struct has no todo field
[linear.needs_human]     # the Config struct has no needs_human table
status = "Needs Human"
```

From reading the loader, strict checking would reject `linear.statuses.todo` and the whole `linear.needs_human` table. I haven't run it.

## Details

**State:** I've read the ticket, the blueprint and the config loader. I haven't written the check or changed any code.

**Choices**

- **A. Widen the struct.** Add `todo` and a `needs_human` table so the sample loads as written. This is right only if those settings are meant to exist. Nothing reads them today, so they would be settings that do nothing.
- **B. Fix the sample.** Delete the two unknown keys from the blueprint's example and enforce the check at every depth, as the ticket says. The documented config then matches what the loader accepts.
- **C. Check top-level keys only.** The sample loads unchanged, but a typo like `[linear.statuss]` passes silently, and that is the failure the ticket exists to stop. It also contradicts "at any depth".

**Recommendation:** B. The loader reflects what dispatch does today, and a key that nothing reads is the kind of mistake this ticket should catch. If `todo` and `needs_human` are planned features, pick A and tell me what they should do.

Reply with A, B or C and I'll continue from there.
