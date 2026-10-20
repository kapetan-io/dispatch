---
name: principle-zero-value-settings
description: "Apply when designing or reviewing a setting, config key, option or struct field a user can leave unset. Name each so its unset (Go zero) value is either invalid or the default, never a third meaning, and so its name says what the code does with it."
---

# Zero-Value Settings

A setting left unset must mean one thing. `AllowELF: false` can read as "refuse ELF" or "nobody set this", and `MaxFileSize: 0` as "the default" or "nothing allowed". The ambiguity ships as a bug, because each implementation picks a reading.

**Why:** Marking "not set" afterwards takes a pointer or a magic value, which is a sign the setting is badly named. Fix the name, not the type.

- **Unset is invalid or the default.** A zero that is not a usable value (`MaxFileSize: 0` is no real limit) is invalid, and the server applies the default for it.
- **Name a default-on switch for turning it off.** `DisableRetries`, not `EnableRetries` defaulting to true, so the unset value is the default.
- **When zero is a real choice, add an explicit switch.** `EnableRetries: true, MaxRetries: 0`, not `MaxRetries: 0` silently meaning no retries, which implementations forget to handle. The count is read only when the switch is on.
- **No pointer or magic value to mark "not set".** If an "off" value is still needed, `Unlimited = math.MaxInt64` is safe in comparisons but overflows under arithmetic.
- **Read each setting from one place per run.** Env keys or Go struct fields, never a merge of both, because a merge needs a rule for which wins when one is unset, which is the same ambiguity.
- **Name what the code does with the value, not just what it holds.** `approval_checks` lists checks dispatch ignores, so `ignore_approval_checks`, like its sibling `ignore_optional_checks`.

The test for each setting is "what does an unset one mean, and is that the only thing it can mean?"
