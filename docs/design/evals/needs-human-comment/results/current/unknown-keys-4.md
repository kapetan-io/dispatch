I need a decision on what `config.toml` is allowed to contain. The ticket's rule to reject unknown keys at any depth would make the example config in the blueprint fail to load. I've moved this ticket to Needs Human until you answer.

At commit `2700f6c`, the example config has two entries the loader doesn't know. The first is `todo = "Todo"` under `[linear.statuses]`, and the loader's statuses struct has no `todo` field. The second is the `[linear.needs_human]` table with `status = "Needs Human"`, and the config struct has no such table. Today the loader silently ignores both, so they have no effect. Under the new check, anyone who copies the documented example gets an error on startup.

The ticket doesn't say which way to resolve this, and each way changes what the config accepts.

- **A. Remove the two entries from the example config.** The example then matches what the loader reads, and the any-depth check stays as written.
- **B. Add both keys to the loader.** They become real settings. This only makes sense if you want them to do something, and I don't know what they would control.
- **C. Check top-level keys only.** The example loads unchanged, but a typo like `[linear.statuss]` still passes silently, which is the problem the ticket is meant to fix.

I recommend A. The keys do nothing today, so an example that shows them is misleading, and A is the only option that keeps the ticket's rule and adds no new settings. If you want `todo` or `needs_human` to be real settings, tell me what each should control and I'll build B instead.

Which option do you want? I'll implement it and move the ticket back to In Progress once I have your answer.
