# ENG-306: Reject unknown keys in config.toml

## In plain English

A typo such as `nudge_limt = 3` is silently ignored, and the dispatcher runs with the default. The user finds out when nudges don't change. After this, `Load` fails and names the key.

## What it delivers

* `config.Load` returns an error naming the key when the file has a key the `Config` struct doesn't know, at any depth. The error also names the file.
* A file with only known keys loads as before.
* Surface tests call `config.Load` on a file and cover a misspelled top-level key, a misspelled key under `[linear]`, and a valid file.

## Governing design

`docs/design/blueprint.md`, "Config". Nothing in the design changes.

## Notes for the worker

To check this, copy `internal/config/testdata/config.toml`, add a misspelled key, and run `go test ./internal/config`. The testdata file and the `loadFixture` helper in that package already exist, so a new test is a few lines.

## Not decided here (ask, don't assume)

Nothing open.
