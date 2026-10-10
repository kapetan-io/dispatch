# ENG-307: Bound `max_in_progress` in config.toml

## In plain English

`max_in_progress = 400` would let the dispatcher start 400 workers. The blueprint puts a ceiling on the setting, but `Load` doesn't enforce it. This makes `Load` enforce it.

## What it delivers

* `config.Load` returns an error naming `max_in_progress` when the file sets it outside the range the blueprint gives.
* Surface tests call `config.Load` on a file and cover both ends of the range and one value past each end.

## Governing design

`docs/design/blueprint.md`, "Config": `max_in_progress` must be between 1 and 8, and `Load` fails on a value outside that range. Nothing in the design changes.

## Not decided here (ask, don't assume)

Nothing open.
