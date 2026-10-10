## In plain English

ENG-306 asks the loader for `config.toml` (the dispatcher's settings file) to reject any key it doesn't recognize, at any depth. The example config in the blueprint (the design document) contains keys the loader doesn't recognize, so the documented example would fail to load once the check exists. The ticket doesn't say whether the example, the loader, or the check should change. That decides what the config accepts, so it's your call.

The example from the blueprint, with what the loader's `Config` struct knows about each key.

```toml
[linear.statuses]
todo = "Todo"            # Config.Statuses has no todo field
[linear.needs_human]     # Config has no needs_human table
status = "Needs Human"
```

## Details

I'm stopping before I write code because every test I'd write depends on this answer. The ticket says nothing in the design changes, and I read that as the blueprint example being the source of truth.

**Option A, widen the loader.** Add `todo` under `[linear.statuses]` and a `[linear.needs_human]` table with `status`, then reject everything else at any depth. This is my recommendation. It keeps the ticket's "any depth" wording and leaves the design alone. The cost is two fields the dispatcher doesn't read yet, which I'd cover with load tests.

**Option B, fix the example.** Remove those two entries from the blueprint. This contradicts "nothing in the design changes", and it hides the question of whether the dispatcher should be configurable there.

**Option C, check top-level keys only.** The example loads unchanged, but typos like `[linear.statuss]` still pass, which is the failure the ticket exists to catch.

If you pick A, tell me whether the two new fields should have defaults or be required.

Which of A, B or C do you want?
