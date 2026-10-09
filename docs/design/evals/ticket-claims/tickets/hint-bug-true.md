# ENG-305: Ticket hint doesn't fire for repos whose path contains a double quote

## In plain English

The hook that tells Claude to use `dispatch:ticket` says nothing in a clone whose path has a double quote in it. Claude then files tickets with the wrong skill in that repo.

## What happened

`config.toml` lists the clone as `dispatch = "/Users/me/say \"hi\"/dispatch"`, which is valid TOML. A session started in that clone gets no hint. Clones with ordinary paths get it.

## Cause

In `bin/dispatch-ticket-hint`, the awk rule for a `[repos]` value strips everything from the first quote character with `sub(/["\x27].*$/, "")`. An escaped quote counts, so `/Users/me/say \"hi\"/dispatch` is cut to `/Users/me/say \`, no clone matches, and the hook exits without printing.

## What it delivers

* A session in a clone whose configured path contains an escaped double quote gets the hint.
* A surface test in `ticket_hint_test.go` covers it.

## Governing design

`docs/design/blueprint.md`, "Config". Nothing in the design changes.

## Not decided here (ask, don't assume)

Nothing open.
