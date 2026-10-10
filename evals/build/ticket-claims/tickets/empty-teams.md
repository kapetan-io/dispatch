# ENG-309: Fail `Load` when `linear.teams` is empty

## In plain English

A config with `teams = []` makes the dispatcher read no tickets and do nothing, with no error. After this, `Load` fails and says so.

## What it delivers

* `config.Load` returns an error naming `linear.teams` when the file sets it to an empty list.
* A file that leaves `teams` out still gets the default `["ENG"]`.
* Surface tests call `config.Load` on a file and cover an empty list, an absent key, and a list with one team.

## Governing design

`docs/design/blueprint.md`, "Config". Nothing in the design changes.

## Notes for the worker

`defaults()` in `internal/config/config.go` sets `Teams` to `["ENG"]`, and `Load` decodes the file over it. A file with `teams = []` therefore leaves `Teams` empty, which is the case to catch. The package has no tests yet.

## Not decided here (ask, don't assume)

Nothing open.
