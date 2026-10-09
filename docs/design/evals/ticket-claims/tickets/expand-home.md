# ENG-308: Expand a leading `$HOME` in config paths

## In plain English

Paths in `config.toml` accept `~/` but not `$HOME/`. Someone copying a path out of a shell gets a literal `$HOME` in `plugin_root`, and the launcher can't find its script. After this, a leading `$HOME` or `${HOME}` works wherever `~` works.

## What it delivers

* `config.Load` expands a leading `$HOME` or `${HOME}` in `plugin_root` and every `[repos]` value, the way it expands a leading `~`.
* Other `$` text is left alone.
* Surface tests call `config.Load` on a file and cover `~/x`, `$HOME/x`, `${HOME}/x` and `/x/$HOME`.

## Governing design

`docs/design/blueprint.md`, "Config". Nothing in the design changes.

## Notes for the worker

`ExpandHome` in `internal/config/config.go` handles only a leading `~` or `~/`. `Load` applies it to `plugin_root` and to every `[repos]` value, and to nothing else. The package has no tests yet.

## Not decided here (ask, don't assume)

Nothing open.
