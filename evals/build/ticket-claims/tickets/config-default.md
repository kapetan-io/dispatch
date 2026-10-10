# ENG-303: Give the launcher a deadline with `launch_timeout_seconds`

## In plain English

A launch that hangs in herdr holds its worker slot forever. The dispatcher needs a deadline to give up on a launch. This adds the setting. A later ticket makes the launcher read it.

## What it delivers

* `config.toml` accepts `launch_timeout_seconds`. It defaults to 90 when absent.
* `Load` returns an error that names `launch_timeout_seconds` when the file sets it to zero or a negative number.
* Surface tests call `config.Load` on a file and cover the default, a valid value, and a zero.

## Governing design

`docs/design/blueprint.md`, "Config". The blueprint's example config gets the new line.

## Notes for the worker

`Load` already rejects zero or negative values for every integer field with an error that names the field, so the new field gets that check for free once it is in the `Config` struct. Follow the way `blocked_renotify_ticks` is declared.

## Not decided here (ask, don't assume)

Nothing open. The default is 90.
