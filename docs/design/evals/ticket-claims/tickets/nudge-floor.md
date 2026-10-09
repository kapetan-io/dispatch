# ENG-310: Reject a `nudge_limit` of zero in config.toml

## In plain English

`nudge_limit = 0` makes the dispatcher send no nudges and escalate an idle worker the first time it notices. The blueprint requires at least one nudge before escalation, but `Load` doesn't enforce it. This makes `Load` enforce it.

## What it delivers

* `config.Load` returns an error naming `nudge_limit` when the file sets it below 1.
* A file that leaves `nudge_limit` out still gets the default of 2.
* Surface tests call `config.Load` on a file and cover 0, -1, 1 and an absent key.

## Governing design

`docs/design/blueprint.md`, "Config" and "Nudges": every idle worker gets at least one nudge before the dispatcher escalates, so a limit below 1 is invalid. Nothing in the design changes.

## Not decided here (ask, don't assume)

Nothing open.
