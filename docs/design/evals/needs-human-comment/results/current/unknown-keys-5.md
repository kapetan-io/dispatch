Moving this to Needs Human. I need a decision on what `config.toml` accepts before I can reject unknown keys.

The ticket says to reject unknown keys at any depth. The blueprint's example config (as of commit `2700f6c`) has two entries the loader's struct doesn't know, so a strict check makes the documented example fail to load.

```toml
[linear.statuses]
todo = "Todo"            # Config.Statuses has no todo field
[linear.needs_human]     # Config has no needs_human table
status = "Needs Human"
```

The ticket doesn't say which side gives way. That is a call about what the config accepts, so I'm not making it for you.

**Options**
1. **Fix the example.** Remove `todo` and the `[linear.needs_human]` table from the documented config. The check stays as the ticket describes.
2. **Widen the struct.** Add a `todo` status and a `needs_human` table to the loader, so the example stays valid and the keys start to mean something. This needs a decision on what they do.
3. **Check top-level keys only.** The example loads, but a typo like `[linear.statusses]` still passes silently, which is the problem the ticket exists to fix.

I'd take option 1. The loader ignores both entries today, so nothing can depend on them, and a strict check was always going to flag them. If the design intends them to work, option 2 is right, and I need to know what they should do.

Which one do you want? I'll start on it as soon as you answer here.
