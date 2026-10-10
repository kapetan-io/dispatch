# Commit message suite

Tests whether the commit-message skill's wording gives the right shape of message.

## Arms

- `arms/current.md` is the wording on main.

## Cases

`cases.json` lists six diffs and what a good message looks like for each.

## Graders, per run

- `subject`. The subject line is imperative and under 50 characters.
- `body`. A body is present exactly when `expect` says one is needed.
- `cause`. When a body is expected, it gives the reason and not only a description of the diff.
