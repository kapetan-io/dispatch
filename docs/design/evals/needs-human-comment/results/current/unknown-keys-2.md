Rejecting unknown keys at every depth makes the blueprint's own example config fail to load, so I need you to choose what `config.toml` accepts before I build ENG-306.

The example has two entries the loader doesn't know. `todo = "Todo"` under `[linear.statuses]` has no matching field on `Config.Statuses`. The `[linear.needs_human]` table with `status = "Needs Human"` has no matching table on `Config`. The ticket says to reject unknown keys at any depth and that nothing in the design changes. It doesn't say which side gives, the documented example or the loader. I found this by reading both. I haven't written any code yet.

**Options**

- **A. Fix the example.** Delete those two entries from the blueprint's example config and keep the check strict at every depth. Typos like `statuss` or `todo` under the wrong table fail at load.
- **B. Widen the loader.** Add a `todo` field and a `needs_human` table to the config struct so the example loads as written. This adds two settings that nothing reads today.
- **C. Check top-level keys only.** The example loads untouched, but a typo inside `[linear.statuses]` passes silently. That contradicts "any depth" in the ticket.

I recommend A. The loader is what runs, so the documentation should match it, and A is the only option that delivers what the ticket asks for without adding settings. If `todo` and `needs_human` are planned settings, pick B and tell me where they should take effect.

Reply with A, B or C. I'll pick the ticket back up from your answer.
