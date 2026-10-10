You are building ENG-306, "Reject unknown keys in config.toml". The ticket says to reject unknown keys at any depth and that nothing in the design changes.

You read the blueprint's example config and the config loader. The example config has keys the loader's struct doesn't know:

```toml
[linear.statuses]
todo = "Todo"            # Config.Statuses has no todo field
[linear.needs_human]     # Config has no needs_human table
status = "Needs Human"
```

Rejecting every unknown key would make the documented example config fail to load. The ticket doesn't say whether to change the documented config, widen the struct, or limit the check to top-level keys. That is a product call about what the config accepts. You decided it is yours to ask the user. Move the ticket to Needs Human and write the comment.
